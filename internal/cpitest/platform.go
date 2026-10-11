package cpitest

import (
	"archive/zip"
	"bytes"
	"net/http"
	"sort"
	"strings"
)

// Tenant-wide runtime endpoints: integration connections, log file
// archives, external logging and archiving settings.

// connections derives integration connections from the running flows: per
// flow and external receiver, the flow's sender (its first sender adapter
// and address) to the receiver's host. ProcessDirect and JMS stay inside the
// tenant. An approximation: how the tenant names sender hosts is not known.
func (m *Tenant) connections() []map[string]any {
	type key struct{ from, to string }
	flows := map[key][]map[string]any{}
	var keys []key
	ids := sortedKeys(m.Artifacts)
	for _, id := range ids {
		a := m.Artifacts[id]
		if !a.running() {
			continue
		}
		fm := a.flow()
		if fm == nil {
			continue
		}
		from := "Timer"
		if len(fm.Senders) > 0 {
			from = fm.Senders[0].Adapter
		}
		for _, ch := range fm.Receivers {
			if ch.Adapter == "ProcessDirect" || ch.Adapter == "JMS" {
				continue
			}
			to := receiverHost(ch, resolve(ch.Address, a.Parameters))
			if to == "" {
				continue
			}
			k := key{from, to}
			if _, ok := flows[k]; !ok {
				keys = append(keys, k)
			}
			flows[k] = append(flows[k], map[string]any{"Id": id, "Name": cmpOr(a.Name, id), "SenderHostType": from, "ReceiverHostType": ch.Adapter})
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].from+keys[i].to < keys[j].from+keys[j].to })
	out := make([]map[string]any, 0, len(keys))
	for i, k := range keys {
		out = append(out, map[string]any{"Id": i + 1, "SenderHost": k.from, "ReceiverHost": k.to, "ResolvedConnection": true,
			"IntegrationFlows": map[string]any{"results": flows[k]}})
	}
	return out
}

// logArchive is a zip with one log file of the type.
func logArchive(logType string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create(logType + "_latest.log")
	_, _ = w.Write([]byte("mock " + logType + " log\n"))
	_ = zw.Close()
	return buf.Bytes()
}

// handlePlatform serves the endpoints of this file; it reports whether it
// answered.
func (m *Tenant) handlePlatform(w http.ResponseWriter, r *http.Request, path string) bool {
	switch {
	case r.Method == http.MethodGet && path == "/api/v1/IntegrationConnections":
		if r.Header.Get("InvalidateCache") == "" { // as the tenant
			odataError(w, http.StatusBadRequest, "Bad Request", "InvalidateCache http request header is missing")
			return true
		}
		writeJSON(w, map[string]any{"d": map[string]any{"results": m.connections()}})
	case r.Method == http.MethodGet && path == "/api/v1/LogFileArchives":
		rows := []map[string]any{}
		for _, t := range []string{"http", "trace"} {
			rows = append(rows, map[string]any{"Scope": "latest", "LogFileType": t, "NodeScope": "worker", "ContentType": "application/zip"})
		}
		writeJSON(w, map[string]any{"d": map[string]any{"results": rows}})
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/api/v1/LogFileArchives(") && strings.HasSuffix(path, "/$value"):
		if !strings.Contains(path, "Scope='latest'") {
			odataError(w, http.StatusNotImplemented, "Not Implemented", "Not implemented")
			return true
		}
		t := "http"
		if strings.Contains(path, "LogFileType='trace'") {
			t = "trace"
		}
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(logArchive(t))
	case r.Method == http.MethodGet && path == "/api/v1/ExternalLoggingActivationStatus":
		writeJSON(w, map[string]any{"d": map[string]any{"results": []map[string]any{{"Id": "mock", "Active": m.ExternalLogging}}}})
	case r.Method == http.MethodGet && path == "/api/v1/ArchivingConfigurations":
		writeJSON(w, map[string]any{"d": map[string]any{"results": []map[string]any{{"Id": "mock", "Active": m.Archiving}}}})
	case r.Method == http.MethodGet && path == "/api/v1/ArchivingKeyPerformanceIndicators":
		writeJSON(w, map[string]any{"d": map[string]any{"results": []map[string]any{}}})
	case r.Method == http.MethodPost && (path == "/api/v1/activateExternalLogging" || path == "/api/v1/deactivateExternalLogging"):
		m.ExternalLogging = path == "/api/v1/activateExternalLogging"
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodPost && path == "/api/v1/activateArchivingConfiguration":
		m.Archiving = true
		w.WriteHeader(http.StatusOK)
	default:
		return false
	}
	return true
}
