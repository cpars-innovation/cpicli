package content

import (
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
