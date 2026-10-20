package lint

import (
	"context"
	"os"
	"path/filepath"
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

// A project's own authentication header: undeclared, lint asks to declare
// it; declared in .cpi/lint.yaml (added to the defaults), the secret-*
// rules protect it like Authorization.
func TestCustomAuthHeader(t *testing.T) {
	script := `def cred = service.getUserCredential("Partner")
message.setHeader("X-Partner-Token", new String(cred.getPassword()))
message.setHeader("X-Correlation", "abc")
`
	jms := map[string]string{"ComponentType": "JMS", "direction": "Receiver", "QueueName_outbound": "Q.Out",
		"cmdVariantUri": "ctype::AdapterVariant/cname::sap:JMS/tp::Not Applicable/mp::Not Applicable/direction::Receiver/version::1.6.3"}
	run := func(lintYAML string) *Result {
		root := t.TempDir()
		m := &sapModel{}
		m.channel("Queue", "End", jms)
		m.process("Process_1", "IntegrationProcess", start("Start"), end("End"))
		dir := filepath.Join(root, "content")
		files := map[string]string{
			"META-INF/MANIFEST.MF": "Manifest-Version: 1.0\nBundle-SymbolicName: Partner_Out; singleton:=true\nBundle-Version: 1.0.0\nSAP-BundleType: IntegrationFlow\n",
			"src/main/resources/scenarioflows/integrationflow/Partner_Out.iflw": m.String(),
			"src/main/resources/script/token.groovy":                            script,
		}
		for name, data := range files {
			p := filepath.Join(dir, "P", "Partner_Out", filepath.FromSlash(name))
			require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
			require.NoError(t, os.WriteFile(p, []byte(data), 0o644))
		}
		cfgFile := filepath.Join(root, ".cpi", "lint.yaml")
		require.NoError(t, os.MkdirAll(filepath.Dir(cfgFile), 0o755))
		require.NoError(t, os.WriteFile(cfgFile, []byte(lintYAML), 0o644))
		cfg, err := LoadConfig(cfgFile)
		require.NoError(t, err)
		res, err := Run(context.Background(), Options{Dir: dir, Config: cfg})
		require.NoError(t, err)
		return res
	}

	res := run("")
	assert.ElementsMatch(t, []string{"secret-header-sent", "secret-target-name", "undeclared-auth-header"}, rulesOf(res, "Partner_Out", "secret-", "undeclared-"))
	f := findingsOf(res, "secret-target-name", "Partner_Out")[0]
	assert.Contains(t, f.Suggestion, "add it to security.secretHeaders in .cpi/lint.yaml")
	assert.Contains(t, findingsOf(res, "undeclared-auth-header", "Partner_Out")[0].Message, "X-Partner-Token (script token.groovy)")

	res = run("security:\n  secretHeaders: [X-Partner-Token]\n")
	assert.Equal(t, []string{"secret-header-sent"}, rulesOf(res, "Partner_Out", "secret-", "undeclared-"),
		"declared: a valid place for the token, still not for the JMS queue")

	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.yaml"))
	require.NoError(t, err)
	assert.Contains(t, cfg.Security.SecretHeaders, "Authorization")
	assert.True(t, defaultSecurity().withDefaults().secretHeader("x-api-key"))
	assert.Len(t, SecurityConfig{SecretHeaders: []string{"authorization", "X-Own"}}.withDefaults().SecretHeaders, 5, "added to the defaults, no duplicates")
}
