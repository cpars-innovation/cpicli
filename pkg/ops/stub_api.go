package ops

import (
	"context"
	"time"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
)

// placeholder: replaced by pkg/ops/stub.go

// StubCall is a request the receiver stub flow CPICTL_Test_Stub recorded.
type StubCall struct {
	Time        time.Time         `json:"time"`
	Rule        string            `json:"rule"` // the path segment after /cpictl/stub/ (selects the configured answer)
	Method      string            `json:"method"`
	Path        string            `json:"path"` // full path below /cpictl/stub/
	Query       string            `json:"query,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Body        string            `json:"body,omitempty"`
	MessageGuid string            `json:"messageGuid"` // the stub's own message
}

// StubCalls returns the requests the receiver stub recorded since a time.
func StubCalls(ctx context.Context, exe *httpclnt.HTTPExecuter, since time.Time) ([]StubCall, error) {
	return nil, output.Usagef("the receiver stub is not available in this build")
}
