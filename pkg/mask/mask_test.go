package mask

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Each detector: what it masks, what it leaves (checksum rejects too).
func TestDetectors(t *testing.T) {
	m := Default()
	for _, c := range []struct {
		name, in, secret, kind string
	}{
		{"iban", "pay to DE89 3704 0044 0532 0130 00 today", "3704 0044 0532", DetectIBAN},
		{"iban compact", "IBAN=GB82WEST12345698765432", "WEST12345698", DetectIBAN},
		{"email", "contact jane.doe+cpi@example.co.uk please", "jane.doe", DetectEmail},
		{"phone intl", "call +49 30 1234 5678", "1234 5678", DetectPhone},
		{"phone 00", "Tel 0044 20 7946 0958", "7946 0958", DetectPhone},
		{"card", "card 4111 1111 1111 1111 exp", "4111 1111 1111", DetectCard},
		{"card dashes", "5500-0000-0000-0004", "5500-0000-0000", DetectCard},
		{"jwt", "token eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U", "dozjgNryP4J3", DetectJWT},
		{"bearer", "Authorization: Bearer 8f3kQ2pL9xZ0vB7n", "8f3kQ2pL9xZ0vB7n", DetectAuthorization},
		{"basic", `"Basic dXNlcjpwYXNzd29yZA=="`, "dXNlcjpwYXNzd29yZA", DetectAuthorization},
		{"url credentials", "sftp://erpuser:S3cret!@sftp.example.com/in", "S3cret!", DetectURLCredentials},
		{"url signature", "https://acct.blob.core.windows.net/c/f?sv=2022-11-02&sig=Ab12Cd34Ef%2Bg%3D", "Ab12Cd34Ef", DetectURLSecret},
		{"url api key", "https://api.example.com/v1?api_key=k_live_abc123&page=2", "k_live_abc123", DetectURLSecret},
		{"private key", "-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQEA0Z3VS5JJcds3xfn\n-----END RSA PRIVATE KEY-----", "MIIEpAIBAAKCAQEA", DetectPrivateKey},
	} {
		t.Run(c.name, func(t *testing.T) {
			out, st := m.Text(c.in)
			assert.NotContains(t, out, c.secret)
			assert.Contains(t, out, Dots)
			assert.Positive(t, st[c.kind], "%v", st)
		})
	}
	for _, in := range []string{
		"DE89 3704 1144 0532 0130 00",        // IBAN with a wrong check digit
		"4111 1111 1111 1112",                // card failing Luhn
		"order 1234567890123 shipped",        // digit run, not a card
		"Basic configuration of the adapter", // a word after Basic
		"bearer of the message",
		"https://api.example.com/v1/orders?page=2&key=customer", // key=word
		"https://api.example.com/v1/orders?code=DE",
		"-----BEGIN CERTIFICATE-----\nMIIDdzCCAl+gAwIBAgIE\n-----END CERTIFICATE-----", // certificates are public
		"version 1.0.3, 2026-11-06T18:51:00Z, 3f2b1c4e-9a7d-4e21-8b3a-0c5d6e7f8a9b",
		"call me at 030 1234",
	} {
		out, st := m.Text(in)
		assert.Equal(t, in, out, "%v", st)
		assert.Zero(t, st.Total())
	}
}

// Values under sensitive names are masked whole, in every form.
func TestNames(t *testing.T) {
	m, err := New(Config{Names: []string{`^x-tenant-key$`}})
	require.NoError(t, err)
	out, st := m.Field("ERP_Password", "hunter22")
	assert.Equal(t, Dots, out)
	assert.Equal(t, 1, st[CountName])
	out, _ = m.Field("X-Tenant-Key", "abc")
	assert.Equal(t, Dots, out, "a caller's name")
	out, _ = m.Field("Content-Type", "application/json")
	assert.Equal(t, "application/json", out)

	out, _ = m.Text(`<Login><user>erp</user><password>hunter22</password><apiKey a="1">k1</apiKey></Login>`)
	assert.Equal(t, `<Login><user>erp</user><password>••••</password><apiKey a="1">••••</apiKey></Login>`, out)
	out, _ = m.Text(`<conn user="erp" password="hunter22" secret='x'/>`)
	assert.Equal(t, `<conn user="erp" password="••••" secret='••••'/>`, out)
}

// JSON stays valid, keeps key order and numbers exactly; sensitive keys and
// path rules mask everything below them.
func TestJSON(t *testing.T) {
	m, err := New(Config{Rules: []Rule{{Kind: "path", Pattern: "//Customer/Name"}, {Kind: "regex", Pattern: `ORD-\d+`}}})
	require.NoError(t, err)
	in := `{"z":1,"amount":12345678901234567890.10,"ratio":1.0,"Customer":{"Name":"Jane Doe","Mail":"jane@example.com"},` +
		`"auth":{"token":"abc","clientSecret":42},"ref":"ORD-991","list":[1e2,"+49 30 1234 5678"]}`
	out, st, err := m.JSON([]byte(in))
	require.NoError(t, err)
	assert.True(t, json.Valid(out), "%s", out)
	assert.Equal(t, `{"z":1,"amount":12345678901234567890.10,"ratio":1.0,"Customer":{"Name":"••••","Mail":"••••@••••"},`+
		`"auth":{"token":"••••","clientSecret":"••••"},"ref":"••••","list":[1e2,"+•• •••• ••••"]}`, string(out))
	assert.Equal(t, Stats{CountPath: 1, DetectEmail: 1, CountName: 2, CountRegex: 1, DetectPhone: 1}, st)

	same, st, err := m.JSON([]byte(`{"a": 1.50}`))
	require.NoError(t, err)
	assert.Equal(t, `{"a": 1.50}`, string(same), "nothing masked: unchanged bytes")
	assert.Zero(t, st.Total())

	text, _ := m.Text("{\n  \"Customer\": {\"Name\": \"Jane\"}\n}")
	assert.True(t, json.Valid([]byte(text)))
	assert.NotContains(t, text, "Jane")
}

// InPlace keeps the line count and every unmasked line byte for byte.
func TestInPlace(t *testing.T) {
	m := Default()
	in := "# settings\r\n" +
		"ERP_Host=erp.example.com\n" +
		"ERP_Password=hunter22\n" +
		"  password: \"s3cret\"\n" +
		"def token = message.getProperty(\"token\")\n" +
		"def pwd = new String(cred.getPassword())\n" +
		"message.setHeader(\"Authorization\", \"Bearer \" + accessToken)\n" +
		"// contact jane@example.com\n" +
		"{\"apiKey\": \"k1\", \"name\": \"x\"}\n" +
		"<password>p1</password>\n" +
		"-----BEGIN PRIVATE KEY-----\n" +
		"MIIEvQIBADANBgkqhkiG9w0BAQEFAASC\n" +
		"  BKcwggSjAgEAAoIBAQC7\n" +
		"-----END PRIVATE KEY-----\n" +
		"last"
	out, st := m.InPlace(in)
	inLines, outLines := strings.Split(in, "\n"), strings.Split(out, "\n")
	require.Len(t, outLines, len(inLines))
	want := map[int]string{
		2:  "ERP_Password=••••",
		3:  "  password: \"••••\"",
		7:  "// contact ••••@••••",
		8:  "{\"apiKey\": \"••••\", \"name\": \"x\"}",
		9:  "<password>••••</password>",
		11: "••••",
		12: "  ••••",
	}
	for i := range inLines {
		if w, ok := want[i]; ok {
			assert.Equal(t, w, outLines[i], "line %d", i+1)
		} else {
			assert.Equal(t, inLines[i], outLines[i], "line %d unchanged", i+1)
		}
	}
	assert.Equal(t, 7, st.Total(), "%v", st)
	same, st := m.InPlace("nothing\nhere\n")
	assert.Equal(t, "nothing\nhere\n", same)
	assert.Zero(t, st.Total())
}

// No masked value appears in the output or the stats, and invalid rules
// are errors, not silently ignored.
func TestNoValueLeaks(t *testing.T) {
	m := Default()
	secrets := []string{"hunter22", "DE89370400440532013000", "jane@example.com", "4111111111111111"}
	in := `{"password":"hunter22","iban":"DE89370400440532013000","mail":"jane@example.com","card":"4111111111111111"}`
	out, st, err := m.JSON([]byte(in))
	require.NoError(t, err)
	for _, s := range secrets {
		assert.NotContains(t, string(out), s)
		assert.NotContains(t, st.String(), s)
	}
	for _, c := range []Config{
		{BuiltIns: []string{"ssn"}},
		{Rules: []Rule{{Kind: "regex", Pattern: "("}}},
		{Rules: []Rule{{Kind: "regex", Pattern: "a*"}}},
		{Rules: []Rule{{Kind: "path", Pattern: "Customer/Name"}}},
		{Rules: []Rule{{Kind: "xpath", Pattern: "//a"}}},
		{Names: []string{"["}},
	} {
		_, err := New(c)
		assert.Error(t, err, "%+v", c)
	}
	none, err := New(Config{BuiltIns: []string{}})
	require.NoError(t, err)
	out2, _ := none.Text("jane@example.com")
	assert.Equal(t, "jane@example.com", out2, "no detectors chosen")
}

// The writer masks whole lines, keeps terminal escape sequences intact and
// passes everything through while off.
func TestWriter(t *testing.T) {
	var b strings.Builder
	w := NewWriter(&b, Default())
	_, _ = w.Write([]byte("off jane@example.com\n"))
	w.Enable(true)
	_, _ = w.Write([]byte("\x1b[1mjane@example.com\x1b[0m and pass"))
	_, _ = w.Write([]byte("word=hunter22\n-----BEGIN PRIVATE KEY-----\nMIIEvQ\n-----END PRIVATE KEY-----\ntail"))
	require.NoError(t, w.Close())
	assert.Equal(t, "off jane@example.com\n\x1b[1m••••@••••\x1b[0m and password=••••\n-----BEGIN PRIVATE KEY-----\n••••\n-----END PRIVATE KEY-----\ntail", b.String())
	assert.Equal(t, 3, w.Stats().Total())
}

func BenchmarkTextNothingToMask(b *testing.B) {
	m := Default()
	s := "Orders_Inbound_Replication_2026 COMPLETED AGrSWKfjlBddQU_blYCHKdnThF-G 2026-11-06T18:51:00.123Z"
	for b.Loop() {
		m.Text(s)
	}
}

// XML: comments, CDATA, processing instructions, quoted '>' in attributes
// and namespaced elements.
func TestXMLScanner(t *testing.T) {
	m := Default()
	in := `<?xml version="1.0"?><!-- <password>x</password> --><a:Login note="a>b" a:password='p1'>` +
		`<a:password><![CDATA[s3cr3t]]></a:password><user>erp</user></a:Login> 3 < 4`
	out, _ := m.Text(in)
	assert.Equal(t, `<?xml version="1.0"?><!-- <password>x</password> --><a:Login note="a>b" a:password='••••'>`+
		`<a:password><![CDATA[••••]]></a:password><user>erp</user></a:Login> 3 < 4`, out)
}

func BenchmarkTextLargeXML(b *testing.B) {
	m := Default()
	var sb strings.Builder
	sb.WriteString("<definitions>")
	for i := range 2000 {
		fmt.Fprintf(&sb, `<bpmn2:serviceTask id="Task_%d" name="Map %d"><ifl:property><key>scriptFunction</key><value>processData</value></ifl:property></bpmn2:serviceTask>`, i, i)
	}
	sb.WriteString("</definitions>")
	s := sb.String()
	for b.Loop() {
		m.Text(s)
	}
}

// Digit groups that are not card-shaped are no cards, whatever Luhn says.
func TestCardShape(t *testing.T) {
	m := Default()
	for _, in := range []string{"20261106-193216-20061", "1234-567890-12345678", "7992739871300001", "4111-11111111-1111"} {
		out, _ := m.Text(in)
		assert.Equal(t, in, out)
	}
	for _, in := range []string{"4111111111111111", "4111 1111 1111 1111", "3782-822463-10005", "6011 0009 9013 9424"} {
		out, _ := m.Text(in)
		assert.NotEqual(t, in, out, in)
	}
}

func BenchmarkTextRegexRule(b *testing.B) {
	m, _ := New(Config{Rules: []Rule{{Kind: "regex", Pattern: `ORD-\d{6}`}, {Kind: "regex", Pattern: `CUST/[A-Z]{3}\d+`}}})
	s := "Orders_Inbound_Replication_2026 COMPLETED AGrSWKfjlBddQU_blYCHKdnThF-G 2026-11-06T18:51:00.123Z"
	for b.Loop() {
		m.Text(s)
	}
}

// The verification of 0.38.0 (cpicli#75): overlong matches, gaps, pairs,
// identifiers.
func TestVerificationFindings(t *testing.T) {
	m := Default()
	for _, c := range []struct{ in, secret string }{
		{"IBAN DE89370400440532013000 EUR", "3704004405320130"},
		{"iban de89 3704 0044 0532 0130 00", "3704 0044 0532"},
		{"card 4111 1111 1111 1111 12/27", "4111 1111 1111 1111"},
		{"4111 1111 1111 1111 123", "4111 1111 1111"},
		{"Endpoint=sb://ns.servicebus.windows.net/;SharedAccessKeyName=Root;SharedAccessKey=AbC123xyz=", "AbC123xyz"},
		{"DefaultEndpointsProtocol=https;AccountName=acc;AccountKey=Zm9vYmFy+/==;EndpointSuffix=core.windows.net", "Zm9vYmFy"},
		{"Authorization: Basic dXNlcjpwYXNz", "dXNlcjpwYXNz"},
		{"bearer:abcdef123456", "abcdef123456"},
		{"mail info@bäckerei-schmidt.de now", "bäckerei"},
		{"https://u:ab/cd@host.example.com/x", "ab/cd"},
		{"sftp://user:p@ss@host.example.com", "p@ss"},
		{"-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASC", "MIIEvQIBADAN"},
	} {
		out, _ := m.Text(c.in)
		assert.NotContains(t, out, c.secret, c.in)
	}
	for _, in := range []string{"Flow-4000000000000002", "ORD_4111111111111111", "Basic configuration of the adapter", "pwd = new String(cred.getPassword())"} {
		out, _ := m.Text(in)
		assert.Equal(t, in, out)
	}

	out, _, err := m.JSON([]byte(`[{"key":"SFTP_Password","value":"hunter2"},{"id":"ClientSecret","value":"s3cr3t"},{"Name":"Password","Value":"x1"},{"key":"Host","value":"erp"}]`))
	require.NoError(t, err)
	assert.Equal(t, `[{"key":"SFTP_Password","value":"••••"},{"id":"ClientSecret","value":"••••"},{"Name":"Password","Value":"••••"},{"key":"Host","value":"erp"}]`, string(out))

	out, _, err = m.JSON([]byte(`{"tokenId":"t1","customerId":"max@example.com","correlationId":"Bearer abcdef123456789","messageGuid":"AGrSWKfj4111111111111111","credentialName":"ERP_User","passwordType":"basic"}`))
	require.NoError(t, err)
	assert.Equal(t, `{"tokenId":"••••","customerId":"••••@••••","correlationId":"Bearer ••••","messageGuid":"AGrSWKfj4111111111111111","credentialName":"ERP_User","passwordType":"basic"}`, string(out))
}
