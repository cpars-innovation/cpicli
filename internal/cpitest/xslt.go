package cpitest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/cpars-innovation/cpicli/pkg/testtools"
)

var reXSLTID = regexp.MustCompile(`^[A-Za-z0-9_.\-]{1,60}$`)

// handleXSLT emulates the XSLT test flow (testtools.XSLTID, Live mode): the
// stylesheet is the Partner Directory binary XSLTPID/<CpictlXsltId>,
// headers CpictlProp-<name> become the parameter <name>, other headers are
// parameters by their name; Tenant.XSLT transforms. The answer is the
// output, or 500 with the error, with a message log either way.
func (m *Tenant) handleXSLT(w http.ResponseWriter, r *http.Request, in *Inbound, body []byte) {
	m.Received = append(m.Received, ReceivedMessage{Method: r.Method, Path: r.URL.Path, Header: r.Header.Clone(), Body: string(body)})
	x := m.newExecutor("LIVE")
	m.liveSerial++
	start := time.Now()
	l := MessageLog{Guid: x.guid(), Artifact: in.Artifact, Status: "COMPLETED", CorrelationID: fmt.Sprintf("C-live-%d", m.liveSerial), Start: start}
	if a := m.Artifacts[in.Artifact]; a != nil {
		l.Package = a.Package
	}
	out, errText := m.runXSLT(r.Header, body)
	l.End = start.Add(15 * time.Millisecond)
	if errText != "" {
		l.Status, l.ErrorText = "FAILED", errText
	}
	m.addLogs(append(x.logs, l))
	w.Header().Set("SAP_MessageProcessingLogID", l.Guid)
	w.Header().Set("SAP_MplCorrelationId", l.CorrelationID)
	if errText != "" {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(errText))
		return
	}
	w.Header().Set("Content-Type", "application/xml")
	_, _ = w.Write(out)
}

func (m *Tenant) runXSLT(h http.Header, body []byte) ([]byte, string) {
	id := h.Get(testtools.XSLTIDHeader)
	if !reXSLTID.MatchString(id) {
		return nil, "java.lang.IllegalArgumentException: cpictl XSLT test: header CpictlXsltId is missing or invalid"
	}
	bin, ok := m.PDBinaries[testtools.XSLTPID+"/"+id]
	if !ok {
		return nil, fmt.Sprintf("com.sap.it.op.agent.mpl.MappingException: Partner Directory resource pd:%s:%s:Binary not found", testtools.XSLTPID, id)
	}
	if m.XSLT == nil {
		return nil, "the mock tenant does not run XSLT (cpitest.Tenant.XSLT is not set)"
	}
	// the script sets the headers and properties of CpictlXsltParams; the
	// stylesheet sees both as parameters
	params := map[string]string{}
	if raw := h.Get(testtools.XSLTParamsHeader); raw != "" {
		var p struct{ Headers, Properties map[string]string }
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			return nil, "groovy.json.JsonException: " + err.Error()
		}
		for k, v := range p.Headers {
			params[k] = v
		}
		for k, v := range p.Properties {
			params[k] = v
		}
	}
	out, err := m.XSLT(bin.Content, body, params)
	if err != nil {
		return nil, "net.sf.saxon.trans.XPathException: " + err.Error()
	}
	return out, ""
}
