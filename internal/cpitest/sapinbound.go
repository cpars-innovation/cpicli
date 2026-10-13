package cpitest

import (
	"cmp"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/cpars-innovation/cpicli/pkg/idoc"
)

// The IDoc and OData V2 senders as the mock serves them. What SAP documents
// but was not verified on a tenant is marked as the mock's assumption in
// docs/mock-tenant.md: the paths (/cxf/<address>, /gw/odata/<namespace>/
// <service>;v=<version>), the IDoc headers and the answers.

// IDoc headers the mock's IDoc sender sets for the flow, from the control
// record of the first IDoc (SAP documents them; not verified on a tenant).
const (
	headerIDocType       = "SapIDocType"
	headerIDocDbID       = "SapIDocDbId"
	headerIDocTransferID = "SapIDocTransferId"
)

// handleIDoc serves an IDoc sender (/cxf/<address>): POST of IDoc XML, with
// or without SOAP envelope. The flow gets the IDoc XML without envelope and
// the IDoc headers; the ApplicationMessageId is the DOCNUM unless the flow
// sets one. The answer is HTTP 200 with an empty body (500 with the error
// text when the run failed, 400 for a message without control record).
func (m *Tenant) handleIDoc(w http.ResponseWriter, r *http.Request, in *Inbound, body []byte) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	doc, err := idoc.Parse(body)
	if err != nil {
		m.Received = append(m.Received, ReceivedMessage{Method: r.Method, Path: r.URL.Path, Header: r.Header.Clone(), Body: string(body)})
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(err.Error()))
		return
	}
	ctl := doc.First()
	header := r.Header.Clone()
	header.Set(headerIDocType, cmp.Or(ctl.CIMTYP, ctl.IDOCTYP))
	header.Set(headerIDocDbID, ctl.DOCNUM)
	header.Set(headerIDocTransferID, ctl.DOCNUM)
	payload := string(doc.XML)
	m.Received = append(m.Received, ReceivedMessage{Method: r.Method, Path: r.URL.Path, Header: header, Body: payload, IDocs: doc.IDocs})
	ran, failed := m.inboundRun(w, r, in, message{key: cmp.Or(m.messageKey(r, payload), ctl.DOCNUM), payload: payload, docnum: ctl.DOCNUM})
	switch {
	case failed != "":
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(failed))
	case ran:
		w.WriteHeader(http.StatusOK)
	default:
		writeFixedResponse(w, in)
	}
}

// odataMethods are the methods the OData sender passes to the flow (MERGE is
// OData V2's PATCH).
var odataMethods = []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, "MERGE", http.MethodDelete}

// handleOData serves an OData V2 sender's service (rest: the path after the
// service root). $metadata is the flow's EDMX resource, the service document
// lists its entity sets; requests to an entity set run the flow with the
// request body. The mock executes no mapping, so the answer is the
// endpoint's configured Response, or else: GET an empty result (a
// collection, or an empty entity for a key), POST 201 with the body passed
// through, PUT, PATCH, MERGE and DELETE 204.
func (m *Tenant) handleOData(w http.ResponseWriter, r *http.Request, in *Inbound, rest string, body []byte) {
	var edmx []byte
	if a := m.Artifacts[in.Artifact]; a != nil {
		edmx = a.edmx()
	}
	sets := []string{}
	for _, mm := range reEdmxEntitySet.FindAllSubmatch(edmx, -1) {
		sets = append(sets, string(mm[1]))
	}
	resource := strings.TrimPrefix(rest, "/")
	switch {
	case !slices.Contains(odataMethods, r.Method):
		w.Header().Set("Allow", strings.Join(odataMethods, ", "))
		odataError(w, http.StatusMethodNotAllowed, "Method Not Allowed", "The method "+r.Method+" is not supported.")
		return
	case resource == "$metadata" && r.Method == http.MethodGet:
		if edmx == nil {
			odataError(w, http.StatusNotFound, "Not Found", "The service has no metadata (no EDMX resource in the flow).")
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write(edmx)
		return
	case resource == "" && r.Method == http.MethodGet: // service document
		writeJSON(w, map[string]any{"d": map[string]any{"EntitySets": sets}})
		return
	}
	set, _, _ := strings.Cut(resource, "/")
	set, key, hasKey := strings.Cut(set, "(")
	if edmx != nil && set != "$batch" && !slices.Contains(sets, set) {
		odataError(w, http.StatusNotFound, "Not Found", fmt.Sprintf("Resource not found for the segment '%s'.", set))
		return
	}
	m.Received = append(m.Received, ReceivedMessage{Method: r.Method, Path: r.URL.Path, Header: r.Header.Clone(), Body: string(body)})
	ran, failed := m.inboundRun(w, r, in, message{key: m.messageKey(r, string(body)), payload: string(body)})
	switch {
	case failed != "":
		odataError(w, http.StatusInternalServerError, "Internal Server Error", failed)
	case !ran || in.Response != "":
		if in.ContentType == "" && in.Response != "" {
			w.Header().Set("Content-Type", "application/json")
		}
		writeFixedResponse(w, in)
	case r.Method == http.MethodGet && hasKey && strings.HasSuffix(key, ")"):
		writeJSON(w, map[string]any{"d": map[string]any{}})
	case r.Method == http.MethodGet:
		writeJSON(w, map[string]any{"d": map[string]any{"results": []any{}}})
	case r.Method == http.MethodPost:
		w.Header().Set("Content-Type", cmp.Or(r.Header.Get("Content-Type"), "application/json"))
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(body)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// endpointProtocol is the Protocol of a ServiceEndpoints entry by its URL:
// ODATA for OData services, SOAP for /cxf/ (SOAP and IDoc senders), REST
// otherwise. The values for IDoc and OData senders are assumptions.
func endpointProtocol(url string) string {
	switch {
	case strings.Contains(url, odataPrefix):
		return "ODATA"
	case strings.Contains(url, "/cxf/"):
		return "SOAP"
	}
	return "REST"
}
