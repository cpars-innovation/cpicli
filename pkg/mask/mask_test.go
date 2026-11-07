package mask

import (
	"crypto/sha256"
	"encoding/hex"
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
	for _, in := range []string{"Flow4000000000000002", "ORD_4111111111111111", "Basic configuration of the adapter", "pwd = new String(cred.getPassword())"} {
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

// The second verification of 0.38.0 (cpicli#75).
func TestVerificationFindings2(t *testing.T) {
	m := Default()
	for _, c := range []struct{ in, secret string }{
		{"PAN-4111111111111111", "4111111111"},
		{"card:-4111111111111111", "4111111111"},
		{"Order ORD-1234 4111 1111 1111 1111", "4111 1111 1111"},
		{"qty 12 4111111111111111", "4111111111"},
		{`Password="p@ss w0rd";`, "p@ss w0rd"},
		{`Password='S3cret!';`, "S3cret!"},
		{"PWD=(hunter2)", "hunter2"},
		{"Password=Pa(ss)word", "Pa(ss)word"},
		{"PWD={p;ss}", "ss}"},
		{"https://u:p@ss/word@host.example.com/x", "ss/word"},
		{"Authorization: Basic dTpw", "dTpw"},
	} {
		out, _ := m.Text(c.in)
		assert.NotContains(t, out, c.secret, c.in)
	}
	for _, in := range []string{"Password=getPassword()", "Password={{ERP_Password}}"} {
		out, _ := m.Text(in)
		assert.Equal(t, in, out)
	}

	// pairs: any name key, every value key, any value type
	out, _, err := m.JSON([]byte(`[{"Name":"x","name":"password","value":"hunter2"},{"id":"Password","name":"Label","value":"hunter2"},` +
		`{"key":"Password","value":123456},{"name":"Password","value":{"inner":"hunter2"}},{"password":{"v":"hunter2"}},{"password":123456},` +
		`{"key":"Password","Value":"a","value":"b"}]`))
	require.NoError(t, err)
	for _, leak := range []string{"hunter2", "123456", `"a"`, `"b"`} {
		assert.NotContains(t, string(out), leak)
	}

	// hashes and IDs keep their value: no IBAN inside them
	for _, v := range []string{"ac1266f54e0d1c3a9f6b2e8d7c4a5b3f2e1d0c9b8a7f6e5d4c3b2a1f0e9d8c7b", "AGrSWKfjlBddQU_blYCHKdnThF-G"} {
		out, _, err := m.JSON([]byte(`{"sha256":"` + v + `","messageGuid":"` + v + `"}`))
		require.NoError(t, err)
		assert.Equal(t, `{"sha256":"`+v+`","messageGuid":"`+v+`"}`, string(out))
	}
	out, _, err = m.JSON([]byte(`{"customerId":"DE89370400440532013000"}`))
	require.NoError(t, err)
	assert.NotContains(t, string(out), "3704004405320130", "a whole IBAN in an ID field is still masked")
}

// About 1 in 97 random strings passes mod-97; with the country lengths and
// whole-value matching on identifiers, hashes keep their value.
func TestIBANFalsePositives(t *testing.T) {
	m := Default()
	changed := 0
	for i := range 20000 {
		h := sha256.Sum256([]byte(fmt.Sprint(i)))
		v := hex.EncodeToString(h[:])
		out, _, err := m.JSON([]byte(`{"sha256":"` + v + `"}`))
		require.NoError(t, err)
		if string(out) != `{"sha256":"`+v+`"}` {
			changed++
		}
	}
	assert.Zero(t, changed, "identifier fields change only when the whole value is an IBAN")
	changed = 0
	for i := range 20000 {
		h := sha256.Sum256([]byte(fmt.Sprint(i)))
		v := strings.ToUpper(hex.EncodeToString(h[:]))
		if out, _ := m.Text("ref " + v); out != "ref "+v {
			changed++
		}
	}
	assert.Less(t, changed, 20, "free text: rare (was about 1 percent)")
}

// A long run of digit groups costs linear time.
func TestCardRunIsLinear(t *testing.T) {
	assertLinear(t, "digit groups", 25000, func(n int) string { return strings.Repeat("1 ", n) }, func(s string) { Default().Text(s) })
}

// Escapes in connection-string values (cpicli#75, round 3).
func TestConnectionStringEscapes(t *testing.T) {
	m := Default()
	for _, c := range []struct{ in, secret string }{
		{"PWD={p}}w;d};Server=x", "w;d"},
		{`Password="a""bcd";Server=x`, "bcd"},
		{`Password="abc`, "abc"},
		{`Password='a''b'`, "a''b"},
	} {
		out, _ := m.Text(c.in)
		assert.NotContains(t, out, c.secret, c.in)
	}
	out, _ := m.Text("PWD={p}}w;d};Server=x")
	assert.Equal(t, "PWD={••••};Server=x", out, "the rest of the string stays")
}

// Round-3 nits: lists of years or ports are no cards; an IBAN word inside
// an identifier is masked.
func TestRound3Nits(t *testing.T) {
	m := Default()
	for _, in := range []string{"2021 2022 2023 2024 2025 2026 2027 2028", "ports 5000 5001 5002 5003 5004 5005"} {
		out, _ := m.Text(in)
		assert.Equal(t, in, out)
	}
	out, _, err := m.JSON([]byte(`{"accountId":"x DE89370400440532013000","f":false,"cap":{"name":"Password","value":false}}`))
	require.NoError(t, err)
	assert.NotContains(t, string(out), "3704004405320130")
	assert.Contains(t, string(out), `"f":false`)
	assert.Contains(t, string(out), `"value":"••••"`)
}

// Cards of every network mask (cpicli#75, round 3: Maestro, Mir, RuPay,
// UnionPay), years do not.
func TestCardNetworks(t *testing.T) {
	m := Default()
	for _, card := range []string{"4111111111111111", "5555555555554444", "2223003122003222", "378282246310005", "36227206271667",
		"6011111111111117", "3530111333300000", "6759649826438453", "5018000000000009", "2200000000000004", "6070000000000002",
		"6200000000000005", "8171999927660000", "6759649826438453"} {
		out, _ := m.Text("pay " + card)
		assert.NotContains(t, out, card[:8], card)
	}
	out, _ := m.Text("2021 2022 2023 2024 2025 2026 2027 2028")
	assert.Equal(t, "2021 2022 2023 2024 2025 2026 2027 2028", out)
}

// cpicli#76: no over-masking of secretEnv, port lists or hex GUIDs; real
// cards still mask.
func TestOverMasking(t *testing.T) {
	m := Default()
	out, _, err := m.JSON([]byte(`{"credentials":[{"name":"SFTP","secretEnv":"CPI_SECRET_USER_SFTP","password":"hunter2"}]}`))
	require.NoError(t, err)
	assert.Contains(t, string(out), `"secretEnv":"CPI_SECRET_USER_SFTP"`)
	assert.NotContains(t, string(out), "hunter2")
	for _, in := range []string{"id 3c1ae2bd-6978-4339-1512-40b9ce02be89", "ports 5000 5001 5002 5003 5004 5005"} {
		got, _ := m.Text(in)
		assert.Equal(t, in, got)
	}
	for _, in := range []string{"card 4111 1111 1111 1111", "card 5555-5555-5555-4444", "x 4111111111111111 y"} {
		got, _ := m.Text(in)
		assert.NotEqual(t, in, got, in)
	}
}

// Random hex GUIDs in free text keep their value (the verifier found 2 in
// 20,000 masked).
func TestGUIDsKeepTheirValue(t *testing.T) {
	m := Default()
	changed := 0
	for i := range 20000 {
		h := sha256.Sum256([]byte(fmt.Sprint("guid", i)))
		x := hex.EncodeToString(h[:16])
		g := x[:8] + "-" + x[8:12] + "-" + x[12:16] + "-" + x[16:20] + "-" + x[20:32]
		if out, _ := m.Text("id " + g); out != "id "+g {
			changed++
		}
	}
	assert.Zero(t, changed)
}

// The verification of 0.38.1 (cpicli#76): what the over-masking fixes must
// not let through.
func TestOverMaskingFixesKeepMasking(t *testing.T) {
	m := Default()
	for _, in := range []string{
		"4111111111111111x", "PAN:4111111111111111abc", "4111 1111 1111 1111EUR", "4111-1111-1111-1111_x",
		"4111111111111111JOHN SMITH 2027",
		"call+4915123456789", "Mobil+49 151 2345678", "+4915123456789SMITH", "+49 151 2345678x",
		"+49-151-2345678abc", "tel-+49 151 2345678",
	} {
		out, _ := m.Text(in)
		assert.NotEqual(t, in, out, in)
	}
	for _, in := range []string{"id 3c1ae2bd-6978-4339-1512-40b9ce02be89", "id 3cd90e4d-0049-1771-53ff-05307223ef0d",
		"sha " + strings.Repeat("0049151234567890", 4)} {
		out, _ := m.Text(in)
		assert.Equal(t, in, out, in)
	}

	// the caller's names apply to credential metadata too
	named, err := New(Config{Names: []string{"^user$", "^description$"}})
	require.NoError(t, err)
	out, _, err := named.JSON([]byte(`{"userCredentials":[{"name":"ERP","user":"jdoe","description":"internal note"}]}`))
	require.NoError(t, err)
	assert.NotContains(t, string(out), "jdoe")
	assert.NotContains(t, string(out), "internal note")
	assert.Contains(t, string(out), `"ERP"`)

	// secretEnv only as a variable name
	out, _, err = m.JSON([]byte(`{"credentials":[{"secretEnv":"hunter2"},{"secretEnv":"CPI_SECRET_USER_SFTP"}]}`))
	require.NoError(t, err)
	assert.NotContains(t, string(out), "hunter2")
	assert.Contains(t, string(out), "CPI_SECRET_USER_SFTP")
}

// Round 6 of #76: the hex-ID check stays linear; a card glued to more hex
// is masked; secretEnv only in cpictl's format.
func TestHexIDLinearAndStrict(t *testing.T) {
	m := Default()
	for _, unit := range []string{"4111111111111111-a-", "x-0049151234567-"} {
		assertLinear(t, unit, 3000, func(n int) string { return strings.Repeat(unit, n) }, func(s string) { m.Text(s) })
	}
	out, _ := m.Text("pan 4111111111111111" + strings.Repeat("ab", 16))
	assert.NotContains(t, out, "4111111111111111")
	h := sha256.Sum256([]byte("x"))
	hash := hex.EncodeToString(h[:])
	out, _ = m.Text("sha " + hash)
	assert.Equal(t, "sha "+hash, out)

	got, _, err := m.JSON([]byte(`{"credentials":[{"secretEnv":"HUNTER2"},{"secretEnv":"CPI_SECRET_OAUTH2_GRAPH"}]}`))
	require.NoError(t, err)
	assert.NotContains(t, string(got), "HUNTER2")
	assert.Contains(t, string(got), "CPI_SECRET_OAUTH2_GRAPH")
}

// JSON that is not one document still masks values by key (cpicli#77).
func TestTextJSONFallback(t *testing.T) {
	m, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct{ in, want string }{
		"bom":       {"\xef\xbb\xbf{\"password\":\"hunter2\"}", "\xef\xbb\xbf{\"password\":\"" + Dots + "\"}"},
		"ndjson":    {"{\"password\":\"hunter2\"}\n{\"token\":\"abcdef12\"}\n", "{\"password\":\"" + Dots + "\"}\n{\"token\":\"" + Dots + "\"}\n"},
		"trailing":  {`{"password":"hunter2"} trailing`, `{"password":"` + Dots + `"} trailing`},
		"truncated": {`{"user":"a","password":"hunter2","more":[1,`, `{"user":"a","password":"` + Dots + `","more":[1,`},
		"cut value": {`{"a":1,"password":"hunt`, `{"a":1,"password":"` + Dots},
		"log line":  {`POST failed: body={"client_secret":"s3cr3tvalue"}`, `POST failed: body={"client_secret":"` + Dots + `"}`},
		"pretty":    {"{\n  \"password\": \"hunter2\",\n  \"n\": 1\n", "{\n  \"password\": \"" + Dots + "\",\n  \"n\": 1\n"},
	}
	for name, c := range cases {
		got, st := m.Text(c.in)
		if got != c.want || strings.Contains(got, "hunt") || strings.Contains(got, "s3cr3t") {
			t.Errorf("%s: got %q, want %q", name, got, c.want)
		}
		if st.Total() == 0 {
			t.Errorf("%s: no count", name)
		}
	}
	// a plain value next to a harmless one is unchanged
	if got, _ := m.Text(`{"a":"b"} {"c":"d"}`); got != `{"a":"b"} {"c":"d"}` {
		t.Errorf("unchanged sequence: %q", got)
	}
}

func TestTextJSONFallbackLinear(t *testing.T) {
	m, _ := New(Config{})
	build := func(n int) string { return `{"password":"` + strings.Repeat(`a\"b `, n) }
	if got, _ := m.Text(build(1000)); strings.Contains(got, "a\\\"b") {
		t.Error("cut-off value not masked")
	}
	assertLinear(t, "cut-off value", 25000, build, func(s string) { m.Text(s) })
}

// The fallback counts what it masks, once, and masks values that are no
// strings under sensitive names (cpicli#77, verifier round 1).
func TestTextJSONFallbackCounts(t *testing.T) {
	m, _ := New(Config{})
	cases := map[string]struct {
		in, want string
		n        int
	}{
		"string":       {`{"password":"hunter2"}x`, `{"password":"` + Dots + `"}x`, 1},
		"list":         {`{"password":["hunter2"]}x`, `{"password":"` + Dots + `"}x`, 1},
		"number":       {`{"password":12345}x`, `{"password":"` + Dots + `"}x`, 1},
		"bool":         {`{"a":1,"apiKey":true} x`, `{"a":1,"apiKey":"` + Dots + `"} x`, 1},
		"object":       {`{"secret":{"a":"b]}"},"n":1`, `{"secret":"` + Dots + `","n":1`, 1},
		"open list":    {`{"token":["abc","de`, `{"token":"` + Dots + `"`, 1},
		"log number":   {`failed: {"secret": 123456}`, `failed: {"secret": "` + Dots + `"}`, 1},
		"null":         {`{"password":null}x`, `{"password":null}x`, 0},
		"credentials":  {`{"userCredentials":[{"name":"svc"}]}x`, `{"userCredentials":[{"name":"svc"}]}x`, 0},
		"reference":    {`{"credentialName":"svc-user"}x`, `{"credentialName":"svc-user"}x`, 0},
		"ref in text":  {`body={"passwordType":"plain"}`, `body={"passwordType":"plain"}`, 0},
		"seq then bad": {"{\"password\":\"a1\"}\n{\"password\":\"b2\"}\nx", "{\"password\":\"" + Dots + "\"}\n{\"password\":\"" + Dots + "\"}\nx", 2},
	}
	for name, c := range cases {
		got, st := m.Text(c.in)
		if got != c.want || st[CountName] != c.n || st.Total() != c.n {
			t.Errorf("%s: got %q %v, want %q name %d", name, got, st, c.want, c.n)
		}
	}
}

func TestTextOtherValuesLinear(t *testing.T) {
	m, _ := New(Config{})
	if got, _ := m.Text("[" + strings.Repeat(`{"x":1,"secret":2},`, 100)); strings.Contains(got, `"secret":2`) {
		t.Error("value not masked")
	}
	for _, unit := range []string{`{"secret":[`, `{"x":1,"secret":2},`} {
		assertLinear(t, unit, 12500, func(n int) string { return "[" + strings.Repeat(unit, n) }, func(s string) { m.Text(s) })
	}
}

// Values under names that are not masked are not scanned (verifier round 2:
// every unclosed "a": [ scanned to the end of the text).
func TestTextOtherValuesUnclosedLinear(t *testing.T) {
	m, _ := New(Config{})
	for _, unit := range []string{`"a":[`, `"a":{`, `"a":[1,`, `"secret":[`, `"a":[{"secret":[1]},`} {
		for _, prefix := range []string{"[", "x "} { // the JSON fallback and quoted text
			assertLinear(t, prefix+unit, 50000/len(unit), func(n int) string { return prefix + strings.Repeat(unit, n) }, func(s string) { m.Text(s) })
		}
	}
}

func TestReferenceKeyFlags(t *testing.T) {
	m, _ := New(Config{})
	for _, end := range []string{"", "x"} { // JSON and the fallback
		in := `{"tokenExpiresIn":3600,"passwordRequired":true,"password":"x1"}` + end
		if got, _ := m.Text(in); got != `{"tokenExpiresIn":3600,"passwordRequired":true,"password":"`+Dots+`"}`+end {
			t.Errorf("got %q", got)
		}
		// a string under them is no flag (cpicli#78)
		in = `{"passwordRequired":"hunter2","tokenExpiresIn":"opaque-token-abcdef"}` + end
		if got, _ := m.Text(in); got != `{"passwordRequired":"`+Dots+`","tokenExpiresIn":"`+Dots+`"}`+end {
			t.Errorf("got %q", got)
		}
	}
}
