package ops

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseScriptDoc(t *testing.T) {
	src := `/*
 * Header helpers for monitoring. Shared by all order flows.
 */
import com.sap.gateway.ip.core.customdev.util.Message

/**
 * Sets the order number as a custom header. Reads header OrderNo.
 *
 * Call: script collection Orders_Scripts, Headers.groovy, function setOrderHeader.
 * @param message the message
 */
def Message setOrderHeader(Message message) {
    if (message) {
        return message
    }
}

// Removes technical headers
// before the receiver call.
Message cleanHeaders(Message message) {
    message
}

def undocumented(String a, int b) {
}

@Deprecated
private static String helper(x) {
    return x
}

class Inner {
    def method() {}
}
`
	d := ParseScriptDoc([]byte(src))
	assert.Equal(t, "Header helpers for monitoring. Shared by all order flows.", d.Header)
	assert.Equal(t, "Header helpers for monitoring.", d.Summary)
	require.Len(t, d.Functions, 4)
	f := d.Functions[0]
	assert.Equal(t, "setOrderHeader", f.Name)
	assert.Equal(t, "Message message", f.Params)
	assert.Equal(t, 12, f.Line)
	assert.Equal(t, "Sets the order number as a custom header.", f.Summary)
	assert.Contains(t, f.Doc, "Call: script collection Orders_Scripts")
	assert.Equal(t, "Removes technical headers before the receiver call.", d.Functions[1].Summary)
	assert.Equal(t, "undocumented", d.Functions[2].Name)
	assert.False(t, d.Functions[2].Documented())
	assert.Equal(t, "helper", d.Functions[3].Name)
	assert.True(t, d.Functions[3].Private)

	// the comment directly above the only function is its doc, not a file header
	d = ParseScriptDoc([]byte("/** Maps the body. */\ndef Message processData(Message message) {\n}\n"))
	assert.Empty(t, d.Header)
	assert.Equal(t, "Maps the body.", d.Summary)
	assert.Equal(t, "Maps the body.", d.Functions[0].Summary)

	// top-level calls and statements are no declarations
	d = ParseScriptDoc([]byte("def x = foo(1)\nif (x) {\n}\nreturn bar(x) {\n}\n"))
	assert.Empty(t, d.Functions)
}

func TestScriptHeaderBeforeImports(t *testing.T) {
	d := ParseScriptDoc([]byte("// Logs the order number.\nimport x.Message\ndef Message processData(Message message) {\n}\n"))
	assert.Equal(t, "Logs the order number.", d.Header)
	assert.False(t, d.Functions[0].Documented(), "an import separates the header from the function")
}
