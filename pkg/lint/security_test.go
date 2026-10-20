package lint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A script that reads a credential and a token the way SAP's samples do.
const secretScript = `import com.sap.gateway.ip.core.customdev.util.Message
import com.sap.it.api.ITApiFactory
import com.sap.it.api.securestore.SecureStoreService

def Message processData(Message message) {
    def service = ITApiFactory.getService(SecureStoreService.class, null)
    def cred = service.getUserCredential("Finance_Basic")
    String user = cred.getUsername()
    String pwd = new String(cred.getPassword())
    def basic = "${user}:${pwd}".bytes.encodeBase64().toString()
    message.setHeader("Authorization", "Basic " + basic)  // allowed
    message.setHeader("X-Debug-User", user)                // the user name is not secret
    message.setHeader("X-Password", pwd)                   // not allowed
    message.setProperty("secretFinance", pwd)             // allowed
    message.setProperty("financeCred",
        cred)                                              // the credential object, not allowed
    def messageLog = messageLogFactory.getMessageLog(message)
    messageLog.setStringProperty("user", user)
    messageLog.addAttachmentAsString("auth", basic, "text/plain")
    // message.setHeader("Commented", pwd)
    def auth = message.getHeaders().get("Authorization")
    if (!auth) {
        throw new IllegalStateException("no auth " + auth)
    }
    message.setBody("grant_type=client_credentials&client_secret=" + pwd)
    return message
}
`

func TestSecretUses(t *testing.T) {
	uses := secretUses("script/auth.groovy", secretScript, defaultSecurity())
	var got []string
	for _, u := range uses {
		got = append(got, u.sink+":"+u.name)
	}
	assert.Equal(t, []string{"header:Authorization", "header:X-Password", "property:secretFinance", "property:financeCred",
		"log:addAttachmentAsString", "log:throw new IllegalStateException", "body:setBody"}, got)
	assert.Equal(t, 13, uses[1].line)
	assert.Equal(t, 15, uses[3].line, "a call spanning lines counts at its first line")
}

func TestSecretUsesKeystoreAndToken(t *testing.T) {
	src := `def ks = ITApiFactory.getService(KeystoreService.class, null)
def key = ks.getKey("signing", null)
def token = service.getAccesTokenForOauth2AuthorizationCodeCredential("Graph")
message.setProperty("signingKey", key)
message.setHeader("Authorization", "Bearer " + token)
for (e in map.entrySet()) { message.setHeader(e.getKey(), e.getValue()) }
log.info("token {}", token)
def json = new JsonSlurper().parseText(response)
def bearer = json.access_token
message.setHeader("X-Bearer", bearer)
messageLog.setStringProperty("secretName", config.getSecretName())
`
	var got []string
	for _, u := range secretUses("s.groovy", src, defaultSecurity()) {
		got = append(got, u.sink+":"+u.name)
	}
	assert.Equal(t, []string{"property:signingKey", "header:Authorization", "log:log.info", "header:X-Bearer"}, got)
}

func modifierTable(rows ...[3]string) string {
	out := ""
	for _, r := range rows { // name, type, value
		out += "<row><cell id='Action'>Create</cell><cell id='Type'>" + r[1] + "</cell><cell id='Value'>" + r[2] +
			"</cell><cell id='Default'></cell><cell id='Name'>" + r[0] + "</cell><cell id='Datatype'>java.lang.String</cell></row>"
	}
	return out
}

func TestSecretRules(t *testing.T) {
	cm := func(id string, headers, body string) tEl {
		el := modifier(id, body)
		el.props["headerTable"] = headers
		return el
	}
	httpReceiver := func(list string) map[string]string {
		return map[string]string{"ComponentType": "HTTP", "direction": "Receiver", "httpAddressWithoutQuery": "{{URL}}",
			"allowedRequestHeaders": list, "allowedResponseHeaders": "*",
			"cmdVariantUri": "ctype::AdapterVariant/cname::sap:HTTP/tp::HTTP/mp::None/direction::Receiver/version::1.16.0"}
	}
	processDirect := map[string]string{"ComponentType": "ProcessDirect", "direction": "Receiver", "address": "/next",
		"cmdVariantUri": "ctype::AdapterVariant/cname::ProcessDirect/vendor::SAP/tp::Not Applicable/mp::Not Applicable/direction::Receiver/version::1.1.2"}

	flows := map[string]*sapModel{}
	// copies a secret property into a header not named for it, sends it with
	// a wildcard and to another flow
	leaky := &sapModel{props: map[string]string{"allowedHeaderList": "SAPCorrelationId|authorization|X-API-*"}}
	leaky.channel("Partner", "Call", httpReceiver("*"))
	leaky.channel("Next", "End", processDirect)
	leaky.process("Process_1", "IntegrationProcess", start("Start"),
		cm("Copy", modifierTable([3]string{"X-Token", "property", "secretToken"}, [3]string{"Authorization", "expression", "Bearer ${property.secretToken}"}),
			"<token>${header.Authorization}</token>"),
		call("Call", "Call partner"), end("End"))
	flows["Leaky"] = leaky
	// names the header for the one receiver it is meant for and removes it
	// before the next flow
	clean := &sapModel{props: map[string]string{"allowedHeaderList": "*"}}
	clean.channel("Partner", "Call", httpReceiver("Content-Type|Authorization"))
	clean.channel("Next", "End", processDirect)
	remove := modifier("Remove", "")
	remove.props["headerTable"] = "<row><cell id='Action'>Delete</cell><cell id='Type'></cell><cell id='Value'></cell><cell id='Name'>Authorization</cell></row>"
	clean.process("Process_1", "IntegrationProcess", start("Start"),
		cm("Auth", modifierTable([3]string{"Authorization", "expression", "Bearer ${property.secretToken}"}), ""),
		call("Call", "Call partner"), remove, end("End"))
	flows["Clean"] = clean

	res := lintSAPFlows(t, flows, nil)
	assert.Empty(t, rulesOf(res, "Clean", "secret-"))

	byRule := map[string][]string{}
	for _, f := range res.Findings {
		if f.Artifact == "Leaky" {
			byRule[f.Rule] = append(byRule[f.Rule], f.Message)
		}
	}
	require.Len(t, byRule["secret-target-name"], 1)
	assert.Contains(t, byRule["secret-target-name"][0], `copies security material into the header X-Token`)
	assert.Len(t, byRule["secret-in-body"], 1)
	assert.ElementsMatch(t, []string{
		`the HTTP receiver "Partner" sends the headers "*" (allowedRequestHeaders), which includes authorization, x-token`,
		`the ProcessDirect receiver "Next" passes every header to the called flow, including authorization, x-token, which the flow sets and does not remove`,
	}, byRule["secret-header-sent"])
	assert.ElementsMatch(t, []string{
		`Allowed Header(s) of the flow accept Authorization from senders (entry "authorization")`,
		`Allowed Header(s) of the flow accept X-API-Key from senders (entry "X-API-*")`,
	}, byRule["secret-header-inbound"])
}

func TestSecurityConfig(t *testing.T) {
	assert.True(t, globMatch("secret*", "secrettoken"))
	assert.True(t, globMatch("x-*-key", "x-api-key"))
	assert.False(t, globMatch("x-*-key", "x-api-keys"))
	assert.True(t, defaultSecurity().secretHeader("authorization"))
	assert.False(t, defaultSecurity().secretProperty("token"))
}
