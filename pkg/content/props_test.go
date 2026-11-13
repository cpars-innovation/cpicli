package content

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeProperties(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"date line dropped, keys sorted":                      {"#Tue Oct 07 10:00:00 UTC 2026\nB=2\nA=1\n", "A=1\nB=2\n"},
		"single-digit day":                                    {"#Wed Oct  8 09:05:01 CEST 2026\nb=1\na=2\n", "a=2\nb=1\n"},
		"continuation kept together":                          {"Z=1\nM=a\\\n  b\\\n  c\nA=x\n", "A=x\nM=a\\\n  b\\\n  c\nZ=1\n"},
		"escaped separators in the key":                       {"b\\=x=1\na\\:y=2\n", "a\\:y=2\nb\\=x=1\n"},
		"escaped backslash is no continuation":                {"B=c:\\\\\nA=1\n", "A=1\nB=c:\\\\\n"},
		"duplicates keep their order":                         {"K=2\nA=0\nK=1\n", "A=0\nK=2\nK=1\n"},
		"other comments kept on top, comments with : dropped": {"#Tue Oct 07 10:00:00 UTC 2026\nB=2\n#\nA=1\n# see: wiki\n", "#\nA=1\nB=2\n"},
		"CRLF becomes LF":                                     {"#Tue Oct 07 10:00:00 UTC 2026\r\nB=2\r\nA=1\r\n", "A=1\nB=2\n"},
		"empty":                                               {"#Tue Oct 07 10:00:00 UTC 2026\n", ""},
		"colon and space separators":                          {"b : 1\na 2\n", "a 2\nb : 1\n"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got := string(NormalizeProperties([]byte(c.in)))
			assert.Equal(t, c.want, got)
			assert.Equal(t, got, string(NormalizeProperties([]byte(got))), "idempotent")
		})
	}
}

func TestPropertiesEqual(t *testing.T) {
	assert.True(t, PropertiesEqual([]byte("#Tue Oct 07 10:00:00 UTC 2026\nB=2\nA=1\n"), []byte("A=1\nB=2\n")))
	assert.True(t, PropertiesEqual([]byte("A = 1\r\n"), []byte("A=1\n")))
	assert.False(t, PropertiesEqual([]byte("A=1\n"), []byte("A=2\n")))
	assert.False(t, PropertiesEqual([]byte("A=1\n"), []byte("A=1\nB=2\n")))
}

func TestRestoreProperties(t *testing.T) {
	tenant := []byte("#Thu Oct 08 14:11:02 UTC 2026\nHost=herrenberg.example\nPort=443\nUser=t\n")
	local := []byte("Port=443\nHost=base.example\nUser=l\n")
	got := RestoreProperties(tenant, local, map[string]bool{"Host": true})
	assert.Equal(t, "Host=base.example\nPort=443\nUser=t\n", string(got), "only the override keys come from the repository")
	got = RestoreProperties(tenant, nil, map[string]bool{"Host": true})
	assert.Equal(t, "Host=herrenberg.example\nPort=443\nUser=t\n", string(got), "no local value: the tenant's")
}

// The tenant writes metainfo.prop with a comment line and ':' escaped: the
// same description as an unescaped local file.
func TestPropLinesUnescape(t *testing.T) {
	tenant := []byte("#Store metainfo properties\r\ndescription=cpictl test harness\\: forwards \\u00e4 \\\\ x\r\n")
	local := []byte("description=cpictl test harness: forwards ä \\\\ x\n")
	assert.Equal(t, propLines(tenant), propLines(local))
	assert.NotEqual(t, propLines(local), propLines([]byte("description=other\n")))
	assert.Equal(t, propLines([]byte("a = one \\\n    two\n")), propLines([]byte("a=one two\n")))
}

// The flow's propdef is the one entry at its path (cpicli#91): "./" and
// backslashes resolve, "../…" is outside the archive, and two entries for
// it are refused rather than one chosen.
func TestPropdefNamesFromZip(t *testing.T) {
	real := `<parameters><parameter><name>Host</name></parameter><parameter><name>SAP_Custom</name></parameter></parameters>`
	crafted := `<parameters><parameter><name>Host</name></parameter></parameters>`
	zipOf := func(entries ...[2]string) []byte {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for _, e := range entries {
			w, _ := zw.Create(e[0])
			_, _ = w.Write([]byte(e[1]))
		}
		_ = zw.Close()
		return buf.Bytes()
	}
	for _, name := range []string{PropdefFile, "./" + PropdefFile, strings.ReplaceAll(PropdefFile, "/", "\\"), "x/../" + PropdefFile, "/" + PropdefFile} {
		names, ok, err := PropdefNamesFromZip(zipOf([2]string{name, real}))
		if err != nil || !ok || !names["SAP_Custom"] {
			t.Errorf("%s: %v %v %v", name, names, ok, err)
		}
	}
	// a crafted entry outside the archive is not the propdef
	names, ok, err := PropdefNamesFromZip(zipOf([2]string{"../" + PropdefFile, crafted}, [2]string{PropdefFile, real}))
	if err != nil || !ok || !names["SAP_Custom"] {
		t.Errorf("../ entry chosen: %v %v %v", names, ok, err)
	}
	// two entries for the propdef: refused
	if _, _, err := PropdefNamesFromZip(zipOf([2]string{"./" + PropdefFile, crafted}, [2]string{PropdefFile, real})); err == nil {
		t.Error("two propdef entries accepted")
	}
	if _, ok, err := PropdefNamesFromZip(zipOf([2]string{"src/main/resources/parameters.prop", "a=b"})); ok || err != nil {
		t.Errorf("no propdef: ok=%v err=%v", ok, err)
	}
}
