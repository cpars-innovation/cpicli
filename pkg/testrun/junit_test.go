package testrun

import (
	"bytes"
	"encoding/xml"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "rewrite golden files")

func sampleResult() *Result {
	return &Result{Passed: 1, Failed: 1, Errors: 1, Skipped: 1, StartedAt: time.Date(2026, 10, 13, 9, 30, 0, 0, time.UTC), DurationMs: 4321,
		Cases: []CaseResult{
			{Flow: "Orders_In", Case: "valid-order", File: ".cpi/tests/Orders_In/valid-order.yaml", Status: StatusPassed, DurationMs: 1200,
				MessageGuid: "AGo1", HTTPStatus: 200, MessageStatus: "COMPLETED"},
			{Flow: "Orders_In", Case: "missing-customer", File: ".cpi/tests/Orders_In/missing-customer.yaml", Status: StatusFailed, DurationMs: 2100,
				MessageGuid: "AGo2", HTTPStatus: 500, MessageStatus: "FAILED",
				Failures: []Failure{
					{Expectation: "http_status", Expected: "400", Actual: "500", Message: "HTTP status 500, expected 400"},
					{Expectation: "response_contains", Expected: "<Error>", Actual: "x < y & z", Message: `the response does not contain "<Error>"`},
				},
				Diagnostics: &Diagnostics{MessageGuid: "AGo2", MessageStatus: "FAILED", LastError: "com.sap.Mapping: field Customer missing",
					ErrorInformation: &cpi.ErrorInformation{LastErrorModelStepID: "CallActivity_2"},
					FailedStep:       &cpi.MessageRunStep{ModelStepID: "CallActivity_2", Activity: "Map Order"}}},
			{Flow: "Orders_In", Case: "later", Status: StatusSkipped, SkipReason: "partner not ready"},
			{Flow: "Billing", Case: "invoice", Status: StatusError, DurationMs: 20, Error: "auth: 401 Unauthorized", ErrorCategory: "auth"},
		}}
}

func TestWriteJUnit(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, WriteJUnit(&buf, sampleResult()))
	golden := filepath.Join("testdata", "junit.golden.xml")
	if *update {
		require.NoError(t, os.WriteFile(golden, buf.Bytes(), 0o644))
	}
	want, err := os.ReadFile(golden)
	require.NoError(t, err, "run go test ./pkg/testrun -run TestWriteJUnit -update")
	assert.Equal(t, string(want), buf.String())

	var doc junitSuites
	require.NoError(t, xml.Unmarshal(buf.Bytes(), &doc), "well-formed")
	assert.Len(t, doc.Suites, 2)
}
