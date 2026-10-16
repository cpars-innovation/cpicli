package lint

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBlankComments(t *testing.T) {
	src := "def a = \"http://x\" // Thread.sleep\n/* new Thread()\n */ Thread.sleep(1)\ndef s = '''// kept\n'''\n"
	got := string(blankComments([]byte(src)))
	assert.Equal(t, len(src), len(got))
	assert.Contains(t, got, `"http://x"`, "strings are not comments")
	assert.NotContains(t, got, "// Thread.sleep")
	assert.NotContains(t, got, "new Thread")
	assert.Contains(t, got, " Thread.sleep(1)\n")
	assert.Contains(t, got, "'''// kept\n'''")
	assert.Equal(t, 3, lineOf([]byte(got), len("def a = \"http://x\" // Thread.sleep\n/* new Thread()\n */ ")))
}
