package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/pkg/mask"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// With payloads withheld (the cpictl mcp default), message error texts and
// business keys are message data too; every other string passes the value
// masker; nothing of the test values reaches the result (cpicli#71).
func TestRedactorWithholdsMessageData(t *testing.T) {
	r, err := NewRedactor("", nil, true, nil)
	require.NoError(t, err)
	secrets := []string{"jane.doe@example.com", "DE89370400440532013000", "ORD-4711", "HTTP 500 from erp: customer Jane Doe"}
	res := map[string]any{
		"messageGuid": "AGrSWKfjlBddQU_blYCHKdnThF-G",
		"errorText":   "HTTP 500 from erp: customer Jane Doe, jane.doe@example.com",
		"customHeaderProperties": []any{
			map[string]any{"name": "OrderNumber", "value": "ORD-4711"},
		},
		"steps":      []any{map[string]any{"stepId": "s1", "activity": "Request Reply", "error": "HTTP 500 from erp: customer Jane Doe"}},
		"failedStep": map[string]any{"stepId": "s1", "error": "HTTP 500 from erp: customer Jane Doe"},
		"groups":     []any{map[string]any{"fingerprint": "f1", "sample": "HTTP 500 from erp: customer Jane Doe", "count": 3}},
		"keys":       map[string]any{"OrderNumber": "ORD-4711"},
		"note":       "pay to DE89 3704 0044 0532 0130 00",
		"artifactId": "Orders_In",
	}
	data, err := json.Marshal(r.Apply("get_message_log", res))
	require.NoError(t, err)
	out := string(data)
	for _, s := range append(secrets, "DE89 3704 0044 0532 0130 00", "jane.doe") {
		assert.NotContains(t, out, s)
	}
	m := map[string]any{}
	require.NoError(t, json.Unmarshal(data, &m))
	assert.Equal(t, "[error text withheld: 58 bytes; start cpictl mcp with --send-payloads to include it]", m["errorText"])
	assert.Equal(t, Masked, m["customHeaderProperties"].([]any)[0].(map[string]any)["value"])
	assert.Equal(t, "OrderNumber", m["customHeaderProperties"].([]any)[0].(map[string]any)["name"])
	assert.Contains(t, m["failedStep"].(map[string]any)["error"], "[error text withheld")
	assert.Contains(t, m["groups"].([]any)[0].(map[string]any)["sample"], "[error text withheld")
	assert.EqualValues(t, 3, m["groups"].([]any)[0].(map[string]any)["count"])
	assert.Equal(t, "AGrSWKfjlBddQU_blYCHKdnThF-G", m["messageGuid"], "IDs are not masked")
	assert.Equal(t, "Orders_In", m["artifactId"])

	// --send-payloads: the texts go through, masked
	send, err := NewRedactor("", nil, false, nil)
	require.NoError(t, err)
	data, err = json.Marshal(send.Apply("get_message_log", map[string]any{
		"errorText": "HTTP 500 from erp: jane.doe@example.com",
		"content":   ops.NewContent([]byte(`{"iban":"DE89370400440532013000","amount":12.50}`), 0),
	}))
	require.NoError(t, err)
	out = string(data)
	assert.Contains(t, out, "HTTP 500 from erp: ••••@••••")
	assert.NotContains(t, out, "DE89370400440532013000")
	assert.Contains(t, out, `12.50`, "numbers are kept")
}

// A write tool refuses an argument that carries the masked value: a masked
// view must not be written back. Read tools take it (a search for it is
// harmless).
func TestServerRefusesMaskedWrites(t *testing.T) {
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"A": {Type: "Integration", Package: "P", DesignVersion: "1.0.0", Parameters: map[string]string{"Password": "x"}},
	})
	resp := session(t, mock, t.TempDir(),
		call(1, "set_parameters", map[string]any{"artifact_id": "A", "parameters": map[string]string{"Password": Masked}}),
		call(2, "set_parameters", map[string]any{"artifact_id": "A", "parameters": map[string]string{"Password": "prefix " + Masked}}),
	)
	for _, id := range []string{"1", "2"} {
		r := toolResult(t, resp[id])
		assert.True(t, r.IsError)
		assert.Equal(t, "usage", r.StructuredContent.ErrorCategory)
		assert.Contains(t, r.StructuredContent.Error, "cannot be written back")
	}
	assert.Equal(t, "x", mock.Artifacts["A"].Parameters["Password"], "nothing was written")

	assert.True(t, carriesMasked(json.RawMessage(`{"a":["••••"]}`)), "escaped in JSON")
	assert.True(t, carriesMasked(json.RawMessage(`{"`+Masked+`":"x"}`)), "in a key")
	assert.False(t, carriesMasked(json.RawMessage(`{"a":"•••"}`)))
	assert.False(t, strings.Contains(Masked, "*"))
}

// A script's lines with a hard-coded secret read as their indentation and
// the masked value; the rest of the file is unchanged (cpicli#74).
func TestRedactorMasksScriptSecretLines(t *testing.T) {
	r, err := NewRedactor("", nil, true, nil)
	require.NoError(t, err)
	res := &ops.ResourceContent{ArtifactID: "A", Name: "auth.groovy", Content: ops.NewContent([]byte(
		"def cred = secureStore.getUserCredential(\"ERP\")\n    def password = \"S3cr3t!x9\"\nreturn message\n"), 0)}
	m := applyJSON(t, r, "get_resource", res)
	assert.Equal(t, "def cred = secureStore.getUserCredential(\"ERP\")\n    "+Masked+"\nreturn message\n", m["text"])
}

// set_masking: with control tighten only more masking is accepted; full
// changes everything; fixed refuses. The policy applies to the next result.
func TestMaskingControl(t *testing.T) {
	r, err := NewRedactor("", nil, true, nil)
	require.NoError(t, err)
	call := func(control string, args map[string]any) (any, error) {
		tools := MaskingTools(func() *Redactor { return r }, control)
		raw, _ := json.Marshal(args)
		if _, ok := args["get"]; ok {
			return tools[0].Handler(context.Background(), json.RawMessage(`{}`))
		}
		return tools[1].Handler(context.Background(), raw)
	}
	v, err := call(MaskingTighten, map[string]any{"get": true})
	require.NoError(t, err)
	assert.Equal(t, "withheld", v.(maskingView).Payloads)
	assert.Len(t, v.(maskingView).Detectors, len(mask.BuiltIns))

	_, err = call(MaskingTighten, map[string]any{"names": []string{"^customerNo$"}, "rules": []map[string]string{{"kind": "regex", "pattern": `ORD-\d+`}}})
	require.NoError(t, err)
	out := applyJSON(t, r, "get_resource", map[string]any{"customerNo": "C-77", "note": "see ORD-4711"})
	assert.Equal(t, Masked, out["customerNo"])
	assert.Equal(t, "see "+Masked, out["note"])

	for _, loosen := range []map[string]any{
		{"payloads": "sent"},
		{"mode": "replace", "names": []string{}},
		{"detectors": []string{"email"}, "mode": "replace"},
	} {
		_, err := call(MaskingTighten, loosen)
		require.Error(t, err, "%v", loosen)
		assert.Contains(t, err.Error(), "only accepts more masking")
	}
	_, err = call(MaskingTighten, map[string]any{"rules": []map[string]string{{"kind": "regex", "pattern": "a*"}}})
	assert.Error(t, err, "an invalid rule changes nothing")

	_, err = call(MaskingFixed, map[string]any{"names": []string{"x"}})
	assert.Error(t, err)

	v, err = call(MaskingFull, map[string]any{"mode": "replace", "detectors": []string{"email"}, "payloads": "sent"})
	require.NoError(t, err)
	assert.Equal(t, "sent", v.(maskingView).Payloads)
	assert.Equal(t, []string{"email"}, v.(maskingView).Detectors)
	assert.False(t, r.Withholding())
	out = applyJSON(t, r, "get_resource", map[string]any{"iban": "DE89370400440532013000", "mail": "a@example.com", "customerNo": "C-77"})
	assert.Equal(t, "DE89370400440532013000", out["iban"], "the IBAN detector is off now")
	assert.Equal(t, "••••@••••", out["mail"])
	assert.Equal(t, "C-77", out["customerNo"], "the name was replaced away")
}

// Concurrent set_masking calls under tighten lose no change (cpicli#75).
func TestSetMaskingConcurrent(t *testing.T) {
	for range 200 {
		r, err := NewRedactor("", nil, false, nil)
		require.NoError(t, err)
		set := MaskingTools(func() *Redactor { return r }, MaskingTighten)[1]
		var wg sync.WaitGroup
		wg.Go(func() { _, _ = set.Handler(context.Background(), json.RawMessage(`{"payloads":"withheld"}`)) })
		wg.Go(func() { _, _ = set.Handler(context.Background(), json.RawMessage(`{"names":["^x$"]}`)) })
		wg.Wait()
		cfg, withhold := r.Policy()
		require.True(t, withhold, "the tightening to withheld was lost")
		require.Equal(t, []string{"^x$"}, cfg.Names)
	}
}

// Pairs in any spelling, identifiers with secrets, sensitive identifiers:
// the same as cpictl mask (cpicli#75).
func TestRedactorPairsAndIdentifiers(t *testing.T) {
	r, err := NewRedactor("", nil, false, nil)
	require.NoError(t, err)
	data, err := json.Marshal(r.Apply("get_parameters", map[string]any{
		"parameters": []any{map[string]any{"key": "SFTP_Password", "value": "hunter2"}, map[string]any{"key": "Host", "value": "erp"}},
		"pd":         map[string]any{"id": "ClientSecret", "value": "s3cr3t"},
		"cap":        map[string]any{"Name": "Password", "Value": "x1"},
		"tokenId":    "t1", "customerId": "max@example.com", "correlationId": "Bearer abcdef123456789",
		"messageGuid": "AGrSWKfj4111111111111111", "credentialName": "ERP_User", "passwordType": "basic",
	}))
	require.NoError(t, err)
	out := string(data)
	for _, leak := range []string{"hunter2", "s3cr3t", `"x1"`, "max@example.com", "abcdef123456789", `"t1"`} {
		assert.NotContains(t, out, leak)
	}
	for _, kept := range []string{`"erp"`, "AGrSWKfj4111111111111111", `"ERP_User"`, `"basic"`} {
		assert.Contains(t, out, kept)
	}
}
