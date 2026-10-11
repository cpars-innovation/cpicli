package content

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUnifiedDiff(t *testing.T) {
	d, ok := UnifiedDiff("a\nb\nc\n", "a\nb\nc\n", "x", "y", 3)
	assert.True(t, ok)
	assert.Empty(t, d)

	a := strings.Join([]string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10"}, "\n") + "\n"
	b := strings.Replace(a, "5\n", "five\n", 1)
	b = strings.Replace(b, "10\n", "10\n11\n", 1)
	d, ok = UnifiedDiff(a, b, "a/f", "b/f", 1)
	assert.True(t, ok)
	assert.Equal(t, "--- a/f\n+++ b/f\n@@ -4,3 +4,3 @@\n 4\n-5\n+five\n 6\n@@ -10,1 +10,2 @@\n 10\n+11\n", d)

	d, _ = UnifiedDiff("", "new\n", "a", "b", 3)
	assert.Equal(t, "--- a\n+++ b\n@@ -0,0 +1,1 @@\n+new\n", d)
	d, _ = UnifiedDiff("old\r\n", "", "a", "b", 3)
	assert.Equal(t, "--- a\n+++ b\n@@ -1,1 +0,0 @@\n-old\n", d)

	_, ok = UnifiedDiff(strings.Repeat("x\n", 3000), strings.Repeat("y\n", 3000), "a", "b", 3)
	assert.False(t, ok, "too large")
}
