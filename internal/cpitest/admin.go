package cpitest

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// The admin API (/_mock/...) changes the mock while it runs: it is how
// tests and demos put the tenant into a state (a failing system, an expired
// login, extra message logs) without restarting it. It needs the admin token
// (Authorization: Bearer) or, when the mock has none, a loopback client, and
// is not part of the SAP API.

// fault answers the next Count requests whose path starts with PathPrefix
// with Status.
type fault struct {
	PathPrefix string `json:"pathPrefix"`
	Status     int    `json:"status"`
	// Count is how many requests get the fault; -1 until the faults are
	// deleted (an API area that answers like this on a tenant).
	Count int `json:"count"`
	// Message, if set, is sent as the tenant's OData error body.
	Message string `json:"message,omitempty"`
}

// mockState is the exportable state of the tenant (/_mock/state).
type mockState struct {
	Packages    []Package                            `json:"packages"`
	Artifacts   map[string]*Artifact                 `json:"artifacts"`
	Credentials map[string]map[string]map[string]any `json:"credentials"`
	Keystore    []KeystoreEntry                      `json:"keystore"`
	PDStrings   map[string]string                    `json:"pdStrings"`
	PDBinaries  map[string]PDBinary                  `json:"pdBinaries"`
	MessageLogs []MessageLog                         `json:"messageLogs"`
	Inbound     map[string]*Inbound                  `json:"inbound"`
	Systems     map[string]SystemSpec                `json:"systems"`
	LogLevels   map[string]map[string]string         `json:"logLevels"`
	Locks       []Lock                               `json:"locks"`
}

// maxRunCount bounds /_mock/run (the runs happen under the tenant's lock).
const maxRunCount = 1000

// adminAllowed decides who may use the admin API. With an AdminToken every
// request needs it as Bearer token, TLS or not (encryption is not
// authorization). Without one only loopback clients are allowed.
func (m *Tenant) adminAllowed(r *http.Request) bool {
	if m.AdminToken != "" {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		return ok && subtle.ConstantTimeCompare([]byte(got), []byte(m.AdminToken)) == 1
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// handleAdmin serves /_mock/... (called with the lock held).
func (m *Tenant) handleAdmin(w http.ResponseWriter, r *http.Request) {
	if !m.adminAllowed(r) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("the mock admin API needs the admin token (Authorization: Bearer), or a loopback client when no token is set"))
		return
	}
	bad := func(err error) {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]string{"error": err.Error()})
	}
	path := strings.TrimPrefix(r.URL.Path, "/_mock")
	switch {
	case path == "/state" && r.Method == http.MethodGet:
		writeJSON(w, m.exportState())

	case path == "/state" && r.Method == http.MethodPut:
		var s mockState
		if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
			bad(err)
			return
		}
		m.importState(s)
		w.WriteHeader(http.StatusNoContent)

	case path == "/messagelogs" && r.Method == http.MethodPost:
		var logs []MessageLog
		if err := json.NewDecoder(r.Body).Decode(&logs); err != nil {
			bad(err)
			return
		}
		for i := range logs {
			if logs[i].Guid == "" || logs[i].Artifact == "" || logs[i].Status == "" {
				bad(fmt.Errorf("message log %d: Guid, Artifact and Status are required", i))
				return
			}
			if logs[i].Start.IsZero() {
				logs[i].Start = time.Now()
			}
			if logs[i].End.IsZero() {
				logs[i].End = logs[i].Start.Add(100 * time.Millisecond)
			}
		}
		kept := m.addLogs(logs)
		writeJSON(w, map[string]int{"added": len(logs), "kept": kept})

	case path == "/systems" && r.Method == http.MethodGet:
		writeJSON(w, m.systems)

	case strings.HasPrefix(path, "/systems/") && r.Method == http.MethodPut:
		name := strings.TrimPrefix(path, "/systems/")
		var s SystemSpec
		if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
			bad(err)
			return
		}
		if name == "" || s.Match == "" || s.FailRate < 0 || s.FailRate > 1 {
			bad(fmt.Errorf("system %q: match is required and failRate between 0 and 1", name))
			return
		}
		if m.systems == nil {
			m.systems = map[string]SystemSpec{}
		}
		m.systems[name] = s
		writeJSON(w, s)

	case path == "/faults" && r.Method == http.MethodPost:
		var f fault
		if err := json.NewDecoder(r.Body).Decode(&f); err != nil {
			bad(err)
			return
		}
		if !strings.HasPrefix(f.PathPrefix, "/") || f.Status < 400 || f.Status > 599 {
			bad(fmt.Errorf("pathPrefix starting with / and status 400..599 are required"))
			return
		}
		if f.Count != -1 {
			f.Count = max(f.Count, 1)
		}
		m.faults = append(m.faults, f)
		writeJSON(w, f)

	case path == "/faults" && r.Method == http.MethodDelete:
		m.faults = nil
		w.WriteHeader(http.StatusNoContent)

	case path == "/run" && r.Method == http.MethodPost:
		var req struct {
			Artifact string `json:"artifact"`
			Key      string `json:"key"`
			Payload  string `json:"payload"`
			Count    int    `json:"count"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			bad(err)
			return
		}
		a := m.Artifacts[req.Artifact]
		if a == nil || !a.running() || a.info() == nil {
			bad(fmt.Errorf("artifact %q is not a deployed integration flow", req.Artifact))
			return
		}
		if req.Count > maxRunCount {
			bad(fmt.Errorf("count %d: at most %d runs per request", req.Count, maxRunCount))
			return
		}
		var runs []map[string]string
		for range max(req.Count, 1) {
			m.liveSerial++
			x := m.newExecutor("LIVE")
			payload := req.Payload
			if payload == "" {
				payload = m.payload(req.Key)
			}
			res := x.run(req.Artifact, message{correlation: fmt.Sprintf("C-live-%d", m.liveSerial), key: req.Key, payload: payload, keyHeaders: m.keyHeaders()}, time.Now(), "", 0)
			m.addLogs(x.logs)
			runs = append(runs, map[string]string{"messageGuid": res.guid, "status": res.status, "error": res.errorText})
		}
		writeJSON(w, map[string]any{"runs": runs})

	default:
		notFound(w)
	}
}

// fault returns the pending fault for path (consuming one of its count),
// or nil.
func (m *Tenant) fault(path string) *fault {
	for i := range m.faults {
		f := &m.faults[i]
		if (f.Count > 0 || f.Count == -1) && strings.HasPrefix(path, f.PathPrefix) {
			if f.Count > 0 {
				f.Count--
			}
			return f
		}
	}
	return nil
}

func (m *Tenant) exportState() mockState {
	s := mockState{Packages: m.Packages, Artifacts: m.Artifacts, Credentials: m.Credentials, Keystore: m.Keystore,
		PDStrings: m.PDStrings, PDBinaries: m.PDBinaries, Inbound: m.Inbound, Systems: m.systems, LogLevels: m.LogLevels, Locks: m.Locks}
	if n := len(m.MessageLogSteps); n > 0 {
		s.MessageLogs = m.MessageLogSteps[n-1]
	}
	return s
}

func (m *Tenant) importState(s mockState) {
	m.Packages, m.Artifacts, m.Credentials, m.Keystore = s.Packages, s.Artifacts, s.Credentials, s.Keystore
	m.PDStrings, m.PDBinaries, m.Inbound, m.systems, m.LogLevels = s.PDStrings, s.PDBinaries, s.Inbound, s.Systems, s.LogLevels
	m.Locks = s.Locks
	if m.Artifacts == nil {
		m.Artifacts = map[string]*Artifact{}
	}
	m.MessageLogSteps = [][]MessageLog{s.MessageLogs}
	m.FilterMessageLogs, m.Live = true, true
}
