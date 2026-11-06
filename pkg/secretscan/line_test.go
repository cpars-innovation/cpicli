package secretscan

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestLineSecret(t *testing.T) {
	const jwt = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"
	for _, c := range []struct{ path, line, kind string }{
		// true positives
		{"a.groovy", `def key = "-----BEGIN RSA PRIVATE KEY-----\nMIIE"`, KindPrivateKey},
		{"a.groovy", `-----BEGIN PRIVATE KEY-----`, KindPrivateKey},
		{"a.js", `var pem = '-----BEGIN EC PRIVATE KEY-----';`, KindPrivateKey},
		{"a.groovy", `def url = "https://admin:S3cret99@erp.example.com/api"`, KindURL},
		{"a.js", `fetch('https://user:pa55word@host/path');`, KindURL},
		{"a.groovy", `def u = "https://acct.blob.core.windows.net/c/f?sv=2022&sig=Ab12Cd34Ef56Gh78%2B"`, KindURL},
		{"a.js", `const u = "https://h/x?a=1&signature=9f8e7d6c5b4a";`, KindURL},
		{"a.groovy", `def u = "https://h/x?api_key=k3y12345678"`, KindURL},
		{"a.groovy", `def u = "https://h/x?apikey=k3y12345678"`, KindURL},
		{"a.java", `String u = "https://h/x?access_token=at9876543210";`, KindURL},
		{"a.js", `const u = "https://maps.example.com/api?key=q8Zr2LmX7vT4pK9wN3bY";`, KindURL},
		{"a.groovy", `def u = "https://s3.amazonaws.com/b/o?X-Amz-Signature=0a1b2c3d4e5f60718293"`, KindURL},
		{"a.groovy", `def spec = new SecretKeySpec("mySecretKey".getBytes("UTF-8"), "HmacSHA256")`, KindKeyMaterial},
		{"a.java", `Key k = new SecretKeySpec(Base64.getDecoder().decode("c2VjcmV0"), "AES");`, KindKeyMaterial},
		{"a.java", `PBEKeySpec spec = new PBEKeySpec("hunter2".toCharArray(), salt, 65536, 256);`, KindKeyMaterial},
		{"a.js", `const h = crypto.createHmac('sha256', 'sharedsecret').update(body);`, KindKeyMaterial},
		{"a.groovy", `conn.setRequestProperty("X", "Bearer abc123def456")`, KindToken},
		{"a.js", `headers.Authorization = 'Basic dXNlcjpwYXNz';`, KindCredentials},
		{"a.groovy", `def t = "` + jwt + `"`, KindToken},
		{"a.js", "const t = `" + jwt + "`;", KindToken},
		{"a.groovy", `def aws = "AKIAIOSFODNN7EXAMPLE"`, KindToken},
		{"a.js", `const gh = 'ghp_abcdefghijklmnopqrstuvwxyz0123456789';`, KindToken},
		{"a.groovy", `def password = "S3cr3t!"`, KindNamed},
		{"a.java", `String apiKey = 'abcd1234';`, KindNamed},
		{"a.js", `var client_secret = "clientS3cret";`, KindNamed},
		{"a.java", `private static final String PASSWORD = "Welcome1";`, KindNamed},
		{"a.js", `const cfg = { accessToken: "tok12345" };`, KindNamed},
		{"a.js", `headers['Authorization'] = 'tok12345';`, KindNamed},
		{"a.groovy", `message.setHeader("Authorization", "Basic dXNlcjpwYXNz")`, KindCredentials},
		{"a.groovy", `message.setProperty("password", "hunter22")`, KindNamed},
		{"a.groovy", `def pwd = "hunter22" // test only`, KindNamed},
		{"a.xsl", `<xsl:variable name="password" select="'hunter2x'"/>`, KindNamed},
		{"a.xslt", `<xsl:param name="apiKey">abc123xyz</xsl:param>`, KindNamed},
		{"a.xsl", `<xsl:with-param name="client_secret" select="'s3cr3tv4lue'"/>`, KindNamed},
		{"a.xsl", `<password>hunter22</password>`, KindNamed},
		{"a.xsl", `<xsl:value-of select="'Bearer abc123def456'"/>`, KindToken},
		{"a.xsl", `<xsl:variable name="k" select="'q8Zr2LmX7vT4pK9wN3bY'"/>`, KindRandomLiteral},
		{"a.groovy", `def x = foo("q8Zr2LmX7vT4pK9wN3bY")`, KindRandomLiteral},
		{"a.js", `call(arg, 'Zm9vYmFyYmF6cXV4K/9+aGVsbG8gd29ybGQ=');`, KindRandomLiteral},
		{"a.txt", `token = "q8Zr2LmX7vT4pK9wN3bY"`, KindNamed},
	} {
		kind, hit := LineSecret(c.path, c.line)
		if assert.True(t, hit, "%s: %s", c.path, c.line) {
			assert.Equal(t, c.kind, kind, "%s: %s", c.path, c.line)
		}
	}
	for _, c := range [][2]string{
		// runtime credential code
		{"a.groovy", `def cred = secureStore.getUserCredential("ERP_User"); def pwd = new String(cred.getPassword())`},
		{"a.groovy", `def p = cred.getPassword()`},
		{"a.groovy", `message.setHeader("Authorization", "Bearer " + accessToken)`},
		{"a.js", `headers.Authorization = "Basic " + encoded;`},
		{"a.groovy", `def t = "${token}"`},
		{"a.groovy", `def h = "Bearer ${token}"`},
		{"a.groovy", `def h = "Bearer $token"`},
		{"a.js", "const h = `Bearer ${token}`;"},
		{"a.groovy", `password = props.get("pwd")`},
		{"a.groovy", `def token = message.getProperty("token")`},
		{"a.java", `String apiKey = System.getenv("API_KEY");`},
		{"a.groovy", `def url = "https://${user}:${pwd}@host/api"`},
		{"a.groovy", `def u = "https://h/x?sig=" + sig`},
		{"a.java", `SecretKeySpec spec = new SecretKeySpec(key.getBytes(), "HmacSHA256");`},
		{"a.groovy", `def password = "abc" + suffix`},
		{"a.groovy", `if (password == "Welcome1") { fail() }`},
		// identifiers and class names
		{"a.groovy", `def c = new OAuth2ClientCredentials()`},
		{"a.groovy", `import com.sap.it.api.securestore.UserCredential`},
		{"a.groovy", `def service = ITApiFactory.getService(SecureStoreService.class, null)`},
		{"a.groovy", `def name = "OAuth2ClientCredentials"`},
		{"a.groovy", `def type = "SecureStoreServiceImplementation2"`},
		// UUIDs and digests
		{"a.groovy", `def id = "3f2b1c4e-9a7d-4e21-8b3a-0c5d6e7f8a9b"`},
		{"a.js", `const h = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855";`},
		{"a.js", `const h = "d41d8cd98f00b204e9800998ecf8427e";`},
		{"a.js", `const h = "DA39A3EE5E6B4B0D3255BFEF95601890AFD80709";`},
		{"a.groovy", `def h = "cf83e1357eefb8bdf1542850d66d8007d620e4050b5715dc83f4a921d36ce9ce47d0d13c5d85f2b0ff8318d2877eec2f63b931bd47417a81a538327af927da3e"`},
		// words, identifiers, paths, ordinary URLs
		{"a.groovy", `messageLog.addAttachmentAsString("Log", "Customer master data replicated successfully", "text/plain")`},
		{"a.groovy", `message.setHeader("Content-Type", "application/json")`},
		{"a.groovy", `def status = "PasswordResetRequired"`},
		{"a.groovy", `if (err == "token_expired") return`},
		{"a.js", `const e = "token_expired";`},
		{"a.groovy", `def p = "Message-Processing-Log-2024"`},
		{"a.groovy", `def f = "src/main/resources/script/Script1.groovy"`},
		{"a.groovy", `def u = "https://api.example.com/v1/orders?page=2"`},
		{"a.groovy", `def u = "https://login.example.com/oauth/token?grant_type=client_credentials"`},
		{"a.groovy", `def tokenUrl = "https://login.example.com/oauth/token"`},
		{"a.groovy", `def credentialName = "ERP_User"`},
		{"a.groovy", `def secret = "SAP_ERP_PASSWORD"`},
		{"a.groovy", `def password = "Enter your password"`},
		{"a.java", `String passwordField = "pwd_field_name";`},
		// placeholders
		{"a.groovy", `def password = ""`},
		{"a.groovy", `def password = "changeme"`},
		{"a.groovy", `def password = "***"`},
		{"a.groovy", `def password = "<password>"`},
		{"a.groovy", `def password = "{{password}}"`},
		{"a.groovy", `def password = '${password}'`},
		{"a.groovy", `def password = "xxx"`},
		{"a.groovy", `def password = "dummy"`},
		{"a.js", `const auth = "Bearer <token>";`},
		{"a.js", `const auth = "Bearer token";`},
		// comments that mention the words
		{"a.groovy", `// read the password from the secure store`},
		{"a.groovy", `def x = 1 // don't hard-code the user's password`},
		{"a.js", `/* the token is read at runtime */ const t = getToken();`},
		// XSLT
		{"a.xsl", `<xsl:variable name="password" select="$pwd"/>`},
		{"a.xsl", `<xsl:param name="apiKey"/>`},
		{"a.xsl", `<xsl:value-of select="concat('Bearer ', $token)"/>`},
		{"a.xsl", `<xsl:text>Customer master data replicated successfully</xsl:text>`},
		{"a.xsl", `<!-- the password comes from the header --> <xsl:copy-of select="."/>`},
		{"a.xsl", `<xsl:if test="$status = 'token_expired'">`},
	} {
		kind, hit := LineSecret(c[0], c[1])
		assert.False(t, hit, "%s: %s (%s)", c[0], c[1], kind)
	}
}

func TestLineSecretLongLine(t *testing.T) {
	lines := []string{
		strings.Repeat("a", 1<<20),
		strings.Repeat(`"ab", `, (1<<20)/6),
		strings.Repeat(`x = "a" + `, (1<<20)/10),
		`"` + strings.Repeat("q8Zr2LmX7vT4", (1<<20)/12) + `"`,
		strings.Repeat(`<a b='c'>`, (1<<20)/9),
		strings.Repeat(`setHeader("password", `, (1<<20)/22),
	}
	for _, path := range []string{"a.groovy", "a.js", "a.xsl"} {
		for _, l := range lines {
			start := time.Now()
			LineSecret(path, l)
			assert.Less(t, time.Since(start), raceSlowdown*time.Second, path)
		}
	}
}

func TestMaskLines(t *testing.T) {
	in := "def cred = secureStore.getUserCredential(\"ERP\")\r\n    def password = \"S3cr3t!x9\"\r\ndef auth = \"Bearer \" + token\n"
	out, n := MaskLines("script/a.groovy", in, "••••")
	if n != 1 || out != "def cred = secureStore.getUserCredential(\"ERP\")\r\n    ••••\r\ndef auth = \"Bearer \" + token\n" {
		t.Fatalf("got %d %q", n, out)
	}
	if out, n := MaskLines("README.md", in, "••••"); n != 0 || out != in {
		t.Fatal("only scripts")
	}
}

// A private key block is masked whole, not only its BEGIN line (cpicli#75).
func TestMaskLinesKeyBlock(t *testing.T) {
	in := "def key = \"\"\"-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASC\n  BKcwggSjAgEAAoIBAQC7\n-----END PRIVATE KEY-----\"\"\"\nreturn key\n"
	out, n := MaskLines("s.groovy", in, "••••")
	if n != 4 || out != "••••\n••••\n  ••••\n••••\nreturn key\n" {
		t.Fatalf("got %d %q", n, out)
	}
}
