package cpitest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cpars-innovation/cpicli/pkg/testtools"
)

// ScriptRun is a call of a generated script test flow
// (testtools.ScriptFlow): the case's message after the flow set its
// headers and properties.
type ScriptRun struct {
	Artifact   string
	Body       string
	Headers    map[string]string
	Properties map[string]string
}

// ScriptRunResult is what the script under test did: the message after it,
// or Error, the exception text the runtime logs (e.g.
// "java.lang.IllegalStateException: OrderNo missing@ line 9 in
// Headers.groovy").
type ScriptRunResult struct {
	Body       string
	Headers    map[string]*string
	Properties map[string]*string
	Error      string
}

// handleScriptFlow answers a generated script test flow with ScriptRunner,
// as the tenant does: the result envelope as JSON, or HTTP 500 with the
// reason only in the message log.
func (m *Tenant) handleScriptFlow(w http.ResponseWriter, r *http.Request, in *Inbound, body []byte) {
	m.Received = append(m.Received, ReceivedMessage{Method: r.Method, Path: r.URL.Path, Header: r.Header.Clone(), Body: string(body)})
	run := ScriptRun{Artifact: in.Artifact, Body: string(body), Headers: map[string]string{}, Properties: map[string]string{}}
	if spec := r.Header.Get(testtools.ScriptCaseHeader); spec != "" {
		var c struct{ Headers, Properties map[string]string }
		if err := json.Unmarshal([]byte(spec), &c); err == nil {
			for k, v := range c.Headers {
				run.Headers[k] = v
			}
			for k, v := range c.Properties {
				run.Properties[k] = v
			}
		}
	}
	res := ScriptRunResult{Error: "the mock tenant does not run scripts (cpitest.Tenant.ScriptRunner is not set)"}
	if m.ScriptRunner != nil {
		res = m.ScriptRunner(run)
	}
	x := m.newExecutor("LIVE")
	m.liveSerial++
	start := time.Now()
	l := MessageLog{Guid: x.guid(), Artifact: in.Artifact, Status: "COMPLETED", CorrelationID: fmt.Sprintf("C-live-%d", m.liveSerial), Start: start, End: start.Add(10 * time.Millisecond)}
	if a := m.Artifacts[in.Artifact]; a != nil {
		l.Package = a.Package
	}
	if res.Error != "" {
		l.Status = "FAILED"
		l.ErrorText = "com.sap.it.rt.adapter.http.api.exception.HttpResponseException: An internal server error occured: " + res.Error +
			".\nThe MPL ID for the failed message is : " + l.Guid + "\n"
	}
	m.addLogs(append(x.logs, l))
	if res.Error != "" {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprintf(w, "An internal server error occured: The MPL ID for the failed message is : %s\n\n", l.Guid)
		return
	}
	w.Header().Set("SAP_MessageProcessingLogID", l.Guid)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"body": res.Body, "headers": res.Headers, "properties": res.Properties})
}

func isScriptFlow(id string) bool { return strings.HasPrefix(id, testtools.ScriptIDPrefix) }
