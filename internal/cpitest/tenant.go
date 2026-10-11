// Package cpitest provides an in-memory SAP CPI tenant (httptest server) for
// offline tests, and a seeded demo tenant for local development
// (cpictl mock-tenant). It never talks to a real tenant.
package cpitest

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	mrand "math/rand/v2"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
)

// Runtime is the runtime state of an artifact.
type Runtime struct {
	Version    string
	Status     string
	DeployedOn time.Time // zero: not reported
}

// Artifact scripts the tenant behaviour for one artifact ID.
type Artifact struct {
	Type          string // designtime type, e.g. Integration
	DesignVersion string // empty: designtime artifact does not exist
	// ModifiedAt is the designtime artifact's last change (zero: not
	// reported). ConfigBumpsModified sets it to now on parameter updates.
	ModifiedAt time.Time
	// ModifiedBy is reported in the package artifact list when set.
	ModifiedBy          string
	ConfigBumpsModified bool
	// SavedVersions records SaveAsVersion calls (the designtime version is
	// set to each).
	SavedVersions []string
	// SaveAsVersionRejectsLower answers 400 to a version lower than the
	// current designtime version; SaveAsVersionIgnored answers 200 without
	// changing the version (the real tenant's behaviour is not known).
	SaveAsVersionRejectsLower bool
	SaveAsVersionIgnored      bool

	// Runtime is what GET IntegrationRuntimeArtifacts returns before a deploy
	// is triggered (nil: 404 not deployed).
	Runtime *Runtime
	// AfterDeploy is returned by runtime GETs after the deploy trigger, one
	// entry per GET; the last entry repeats. nil entries mean 404.
	AfterDeploy []*Runtime
	// TaskStatuses are returned by BuildAndDeployStatus, one per GET; the last
	// repeats. Empty: the status endpoint answers 404.
	TaskStatuses []string
	ErrorInfo    string
	// DeployStatusCode overrides the 202 of the deploy trigger.
	DeployStatusCode int

	// UndeployAfter is the number of runtime GETs after DELETE that still
	// return the artifact (-1: never disappears).
	UndeployAfter int

	// Package and Name are used by the package artifact listing.
	Package string
	Name    string
	// Parameters are the externalised configuration parameters.
	Parameters map[string]string
	// ConfigUpdateStatus overrides the 202 of a parameter update.
	ConfigUpdateStatus int

	// ValidationResult is the body of ValidateIntegrationDesigntimeArtifact
	// (default: the structural check of the flow, see checkFlow).
	ValidationResult string
	// GuidelineStatuses are returned by the execution list after an execute
	// call, one per GET (last repeats); Guidelines are the results.
	GuidelineStatuses []string
	Guidelines        []map[string]any
	guidelineRuns     int
	guidelineGets     int
	// EndpointURL makes the deployed artifact appear in ServiceEndpoints.
	EndpointURL string
	// Resources: name -> type and content; Zip is the $value download
	// (and the content stored by create/update).
	Resources map[string]Resource
	Zip       []byte
	// Uploads counts designtime creates and updates, Deploys the deploy
	// triggers.
	Uploads int
	Deploys int

	cached         *flowInfo
	triggered      bool
	runtimeGets    int
	taskGets       int
	undeployed     bool
	undeployedGets int
}

// Inbound is the behaviour of a runtime endpoint.
type Inbound struct {
	// Artifact receives the message: in Live mode each call adds a
	// COMPLETED message processing log for it.
	Artifact    string
	Status      int // default 200
	Response    string
	ContentType string
	// MessageGuid is returned as SAP_MessageProcessingLogID.
	MessageGuid string
	// RequireCSRF makes the endpoint CSRF protected: modifying requests need a
	// token fetched from the endpoint itself.
	RequireCSRF bool
	token       string
}

// ReceivedMessage is a request received by a runtime endpoint.
type ReceivedMessage struct {
	Method, Path string
	Header       http.Header
	Body         string
}

// PDBinary is a binary Partner Directory parameter.
type PDBinary struct {
	ContentType string
	Content     []byte
}

// Package is an integration package of the mock tenant.
type Package struct {
	ID, Name, Version string
	// ShortText is required by a real tenant (create fails without it).
	ShortText, Description string
}

// Lock is an edit lock of an artifact (IntegrationDesigntimeLocks).
type Lock struct {
	ResourceID string    `json:"resourceId"`
	ArtifactID string    `json:"artifactId"`
	CreatedBy  string    `json:"createdBy"`
	CreatedAt  time.Time `json:"createdAt"`
}

// odataError writes an error the way the tenant's OData services do.
func odataError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	writeJSON(w, map[string]any{"error": map[string]any{"code": code, "message": map[string]string{"lang": "en", "value": message}}})
}

// Resource is an iFlow resource of the mock tenant.
type Resource struct {
	Type    string
	Content []byte
}

// MessageLog is a message processing log of the mock tenant.
type MessageLog struct {
	Guid, Artifact, Status string
	CorrelationID          string
	// ApplicationID is ApplicationMessageId; Package defaults to "P".
	ApplicationID, Predecessor, Package string
	Start, End                          time.Time
	ErrorText                           string
	Headers                             map[string]string
	Attachments                         map[string]string // name -> content (ids att-<guid>-<name>)
	StoreEntries                        map[string]string // id -> payload
	Steps                               []Step
}

// Step is a processing step of a message (one run per message).
type Step struct {
	StepID, ModelStepID, Activity, Status, Error string
	// Properties are the step's properties (RunStepProperties, $expand only).
	Properties map[string]string
	// Traces are the trace messages of the step (log level TRACE).
	Traces []Trace
}

// Trace is a trace message: the message at one step.
type Trace struct {
	ID                 string // numeric
	Payload            string
	Headers            map[string]string
	ExchangeProperties map[string]string
}

// KeystoreEntry is a certificate of the mock tenant keystore.
type KeystoreEntry struct {
	Alias    string
	NotAfter time.Time
	DER      []byte
}

// Tenant is a mock CPI tenant.
type Tenant struct {
	// Credentials: collection (UserCredentials, OAuth2ClientCredentials,
	// SecureParameters) -> name -> stored properties (including secrets,
	// which are never returned, like on a real tenant).
	Credentials map[string]map[string]map[string]any
	// SecureParametersUnavailable answers 404 for SecureParameters (Cloud Foundry).
	SecureParametersUnavailable bool
	Keystore                    []KeystoreEntry
	// MessageLogSteps: each MessageProcessingLogs query returns the next step
	// (the last step repeats). Single-message endpoints look up all steps.
	MessageLogSteps [][]MessageLog
	// FilterMessageLogs makes MPL queries honour the equality filters
	// (IntegrationFlowName, ApplicationMessageId, CorrelationId) and
	// $top/$skip, like the tenant; otherwise every query returns the next
	// MessageLogSteps entry unfiltered.
	FilterMessageLogs bool
	// LastMessageLogQuery is the raw query string of the last MPL query.
	LastMessageLogQuery string
	mplQueries          int

	// DownloadLatency delays every artifact download ($value), outside the
	// mock's lock; PeakDownloads reports how many overlapped at most.
	DownloadLatency time.Duration
	inFlight        atomic.Int64
	peakInFlight    atomic.Int64

	// PDStrings and PDBinaries are the Partner Directory parameters, keyed
	// "<pid>/<id>".
	PDStrings  map[string]string
	PDBinaries map[string]PDBinary
	// Listings page like the tenant: $skip/$top, at most 30 binary and 1000
	// string parameters per page whatever $top asks for, __count with
	// $inlinecount=allpages. PDNextLinks adds __next links (absolute URLs).
	PDNextLinks bool

	// LogLevels records the log level set per artifact via the operations
	// command (request body as received).
	LogLevels map[string]map[string]string

	// Inbound configures runtime endpoints (paths starting with /http/ or
	// /cxf/, as in ServiceEndpoints URLs) that receive test messages.
	Inbound map[string]*Inbound
	// Received are the messages received by runtime endpoints.
	Received []ReceivedMessage

	// Locks are the edit locks (artifacts open for editing in the Web UI).
	Locks []Lock

	// Queues are the JMS queues by name. Queues that running flows read from
	// are created on demand; failed runs of JMS consumers leave their message
	// in the queue (status RETRY).
	Queues map[string]*JMSQueue
	// NoCustomHeaderSearch answers 404 for
	// MessageProcessingLogCustomHeaderProperties (a tenant without it).
	NoCustomHeaderSearch bool

	mu        sync.Mutex
	Artifacts map[string]*Artifact
	Packages  []Package
	// Raw serves fixed OData answers by exact (decoded) path: a slice is
	// returned as {"d":{"results":...}}, anything else as {"d":...}; []byte
	// is returned as is ($value). DELETE on a Raw path answers 202 and is
	// recorded in Deleted. RawQueries records the query string per path.
	Raw        map[string]any
	Deleted    []string
	RawQueries map[string]string

	// ForbidTraces answers 403 for TraceMessages (key without the role to
	// read message content).
	ForbidTraces bool
	// StatusOverride, if non-zero, is returned for every API call (e.g. 401).
	StatusOverride int
	// ForbidPaths answers 403 for paths with one of these prefixes (an API
	// area the credentials have no role for).
	ForbidPaths []string
	// OAuth serves client credential tokens at /oauth/token (any client).
	OAuth bool
	// EndpointBase is the base URL of runtime endpoints in ServiceEndpoints
	// (default: the mock's URL), e.g. the name other containers reach it by.
	EndpointBase string
	// Live makes the tenant behave like a running system instead of a
	// scripted one: a deploy starts the designtime version (task SUCCESS), an
	// undeploy removes it, uploads record who and when, messages sent to an
	// Inbound endpoint are logged, and message log queries honour the status
	// and time filters (newest first).
	Live bool
	// LogRetention is how long live message logs are kept (0: 168h, negative:
	// forever) and MaxLogs how many (0: 100000); the oldest are dropped when
	// logs are added.
	LogRetention time.Duration
	MaxLogs      int
	// AdminToken protects the admin API (/_mock/...): requests need
	// "Authorization: Bearer <token>". Empty: only loopback clients are allowed.
	AdminToken string
	// NoCSRF disables CSRF enforcement (by default modifying Basic Auth
	// requests need the token and session cookie from a "Fetch" request).
	NoCSRF     bool
	csrfToken  string
	csrfSerial int
	liveSerial int
	// landscape and tierSpec are set by SeedLandscape.
	landscape   *Landscape
	traceSerial int64
	msgSerial   int
	rng         *mrand.Rand
	systems     map[string]SystemSpec
	faults      []fault
	tierSpec    *TierSpec
	requests    []string
	server      *httptest.Server
}

// NewTenant starts a mock tenant; it is closed when the test ends.
func NewTenant(t *testing.T, artifacts map[string]*Artifact) *Tenant {
	t.Helper()
	m := Start(artifacts)
	t.Cleanup(m.Close)
	return m
}

// Start starts a mock tenant on a free loopback port; Close stops it.
func Start(artifacts map[string]*Artifact) *Tenant {
	m := &Tenant{Artifacts: artifacts}
	m.server = httptest.NewServer(http.HandlerFunc(m.handle))
	return m
}

// Serve starts a mock tenant on a listener the caller opened (any address,
// optionally TLS with config). Close stops it.
func Serve(l net.Listener, config *tls.Config) *Tenant {
	m := &Tenant{}
	m.server = httptest.NewUnstartedServer(http.HandlerFunc(m.handle))
	_ = m.server.Listener.Close()
	m.server.Listener = l
	if config != nil {
		m.server.TLS = config
		m.server.StartTLS()
	} else {
		m.server.Start()
	}
	return m
}

// URL is the base URL of the mock (http://127.0.0.1:port).
func (m *Tenant) URL() string { return m.server.URL }

// Close stops the mock server.
func (m *Tenant) Close() { m.server.Close() }

// HostPort returns the host and port of the mock server.
func (m *Tenant) HostPort() (string, int) {
	return httpclnt.GetHostPort(m.server.URL)
}

// Executer returns an HTTP executer (Basic Auth) pointing at the mock.
func (m *Tenant) Executer() *httpclnt.HTTPExecuter {
	host, port := m.HostPort()
	return httpclnt.New("", "", "", "", "user", "secret", host, "http", port, false)
}

// ExpireCSRF invalidates the current CSRF token (as a tenant does when the
// session ends); the next fetch issues a new one.
func (m *Tenant) ExpireCSRF() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.csrfToken = ""
}

// CSRFFetches returns the number of CSRF token fetch requests.
func (m *Tenant) CSRFFetches() int {
	n := 0
	for _, r := range m.Requests() {
		if r == "GET /api/v1/" {
			n++
		}
	}
	return n
}

// PeakDownloads is the highest number of concurrent downloads seen (with
// DownloadLatency).
func (m *Tenant) PeakDownloads() int64 { return m.peakInFlight.Load() }

// Requests returns "METHOD path" for every request received.
func (m *Tenant) Requests() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.requests...)
}

// Count returns how many requests started with prefix ("METHOD /path").
func (m *Tenant) Count(prefix string) int {
	n := 0
	for _, r := range m.Requests() {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}
	return n
}

var (
	reDesign          = regexp.MustCompile(`^/api/v1/(\w+)DesigntimeArtifacts\(Id='([^']+)',Version='active'\)$`)
	reDeploy          = regexp.MustCompile(`^/api/v1/Deploy(\w+)DesigntimeArtifact$`)
	reTask            = regexp.MustCompile(`^/api/v1/BuildAndDeployStatus\(TaskId='task-([^']+)'\)$`)
	reRuntime         = regexp.MustCompile(`^/api/v1/IntegrationRuntimeArtifacts\('([^']+)'\)$`)
	rePkgArts         = regexp.MustCompile(`^/api/v1/IntegrationPackages\('([^']+)'\)/(\w+)DesigntimeArtifacts$`)
	reConfigs         = regexp.MustCompile(`^/api/v1/IntegrationDesigntimeArtifacts\(Id='([^']+)',Version='active'\)/Configurations$`)
	reConfig          = regexp.MustCompile(`^/api/v1/IntegrationDesigntimeArtifacts\(Id='([^']+)',Version='active'\)/\$links/Configurations\('([^']+)'\)$`)
	reMPL             = regexp.MustCompile(`^/api/v1/MessageProcessingLogs\('([^']+)'\)(.*)$`)
	reAttValue        = regexp.MustCompile(`^/api/v1/MessageProcessingLogAttachments\('([^']+)'\)/\$value$`)
	reStoreValue      = regexp.MustCompile(`^/api/v1/MessageStoreEntries\('([^']+)'\)/\$value$`)
	rePDCollection    = regexp.MustCompile(`^/api/v1/(String|Binary)Parameters$`)
	rePDEntity        = regexp.MustCompile(`^/api/v1/(String|Binary)Parameters\(Pid='([^']*)',Id='([^']*)'\)$`)
	rePDFilter        = regexp.MustCompile(`^Pid eq '([^']*)'$`)
	reStepTraces      = regexp.MustCompile(`^/api/v1/MessageProcessingLogRunSteps\(RunId='([^']+)',ChildCount=([0-9]+)\)/TraceMessages$`)
	reTrace           = regexp.MustCompile(`^/api/v1/TraceMessages\(([0-9]+)\)/(\$value|Properties|ExchangeProperties)$`)
	reRunSteps        = regexp.MustCompile(`^/api/v1/MessageProcessingLogRuns\('([^']+)'\)/RunSteps$`)
	reGuidelineList   = regexp.MustCompile(`^/api/v1/IntegrationDesigntimeArtifacts\(Id='([^']+)',Version='active'\)/DesignGuidelineExecutionResults$`)
	reGuidelineResult = regexp.MustCompile(`^/api/v1/IntegrationDesigntimeArtifacts\(Id='([^']+)',Version='active'\)/DesignGuidelineExecutionResults\('([^']+)'\)$`)
	reResources       = regexp.MustCompile(`^/api/v1/IntegrationDesigntimeArtifacts\(Id='([^']+)',Version='active'\)/Resources(\(Name='([^']+)',ResourceType='([^']+)'\)/\$value)?$`)
	reDesignValue     = regexp.MustCompile(`^/api/v1/(\w+)DesigntimeArtifacts\(Id='([^']+)',Version='active'\)/\$value$`)
	reCredential      = regexp.MustCompile(`^/api/v1/(UserCredentials|OAuth2ClientCredentials|SecureParameters)(?:\('(.+)'\))?$`)
	reKeystoreCert    = regexp.MustCompile(`^/api/v1/KeystoreEntries\('([0-9A-Fa-f]+)'\)/Certificate/\$value$`)
	reCertImport      = regexp.MustCompile(`^/api/v1/CertificateResources\('([0-9A-Fa-f]+)'\)/\$value$`)
	rePackage         = regexp.MustCompile(`^/api/v1/IntegrationPackages\('([^']+)'\)$`)
	reDesignCreate    = regexp.MustCompile(`^/api/v1/(\w+)DesigntimeArtifacts$`)
	reSaveAsVersion   = regexp.MustCompile(`^/api/v1/(\w+)DesigntimeArtifactSaveAsVersion$`)
	reErrInfo         = regexp.MustCompile(`^/api/v1/IntegrationRuntimeArtifacts\('([^']+)'\)/ErrorInformation/\$value$`)
)

var secretFields = map[string]string{"UserCredentials": "Password", "OAuth2ClientCredentials": "ClientSecret", "SecureParameters": "SecureParam"}

func (m *Tenant) handleCredential(w http.ResponseWriter, r *http.Request, mm []string) {
	coll, name := mm[1], strings.ReplaceAll(mm[2], "''", "'")
	if coll == "SecureParameters" && m.SecureParametersUnavailable {
		notFound(w)
		return
	}
	if m.Credentials == nil {
		m.Credentials = map[string]map[string]map[string]any{}
	}
	if m.Credentials[coll] == nil {
		m.Credentials[coll] = map[string]map[string]any{}
	}
	store := m.Credentials[coll]
	public := func(props map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range props {
			out[k] = v
		}
		out[secretFields[coll]] = nil
		out["SecurityArtifactDescriptor"] = map[string]any{"Type": "CREDENTIALS", "DeployedBy": "tester", "Status": "DEPLOYED"}
		return out
	}
	switch {
	case r.Method == http.MethodGet && mm[2] == "":
		rows := []map[string]any{}
		for _, n := range sortedKeys(store) {
			rows = append(rows, public(store[n]))
		}
		writeJSON(w, map[string]any{"d": map[string]any{"results": rows}})
	case r.Method == http.MethodGet:
		if props, ok := store[name]; ok {
			writeJSON(w, map[string]any{"d": public(props)})
			return
		}
		notFound(w)
	case r.Method == http.MethodPost || r.Method == http.MethodPut:
		var props map[string]any
		if err := json.NewDecoder(r.Body).Decode(&props); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, exists := store[fmt.Sprint(props["Name"])]
		if (r.Method == http.MethodPost && exists) || (r.Method == http.MethodPut && (name == "" || !exists)) {
			w.WriteHeader(http.StatusConflict)
			return
		}
		store[fmt.Sprint(props["Name"])] = props
		w.WriteHeader(http.StatusAccepted)
	case r.Method == http.MethodDelete:
		if _, ok := store[name]; !ok {
			notFound(w)
			return
		}
		delete(store, name)
		w.WriteHeader(http.StatusAccepted)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (m *Tenant) handlePD(w http.ResponseWriter, r *http.Request) {
	if m.PDStrings == nil {
		m.PDStrings = map[string]string{}
	}
	if m.PDBinaries == nil {
		m.PDBinaries = map[string]PDBinary{}
	}
	row := func(kind, key string) map[string]string {
		pid, id, _ := strings.Cut(key, "/")
		if kind == "String" {
			return map[string]string{"Pid": pid, "Id": id, "Value": m.PDStrings[key]}
		}
		b := m.PDBinaries[key]
		return map[string]string{"Pid": pid, "Id": id, "ContentType": b.ContentType, "Value": base64.StdEncoding.EncodeToString(b.Content)}
	}
	exists := func(kind, key string) bool {
		if kind == "String" {
			_, ok := m.PDStrings[key]
			return ok
		}
		_, ok := m.PDBinaries[key]
		return ok
	}
	store := func(kind, key string, body map[string]string) bool {
		if kind == "String" {
			m.PDStrings[key] = body["Value"]
			return true
		}
		content, err := base64.StdEncoding.DecodeString(body["Value"])
		if err != nil {
			return false
		}
		m.PDBinaries[key] = PDBinary{ContentType: body["ContentType"], Content: content}
		return true
	}
	if mm := rePDCollection.FindStringSubmatch(r.URL.Path); mm != nil {
		kind := mm[1]
		switch r.Method {
		case http.MethodGet:
			pidFilter := ""
			if f := r.URL.Query().Get("$filter"); f != "" {
				fm := rePDFilter.FindStringSubmatch(f)
				if fm == nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				pidFilter = fm[1]
			}
			keys := sortedKeys(m.PDStrings)
			if kind == "Binary" {
				keys = sortedKeys(m.PDBinaries)
			}
			rows := []map[string]string{}
			for _, k := range keys {
				if pidFilter == "" || strings.HasPrefix(k, pidFilter+"/") {
					rows = append(rows, row(kind, k))
				}
			}
			q := r.URL.Query()
			skip, _ := strconv.Atoi(q.Get("$skip"))
			top, _ := strconv.Atoi(q.Get("$top"))
			limit := 1000
			if kind == "Binary" {
				limit = 30
			}
			if top > 0 && top < limit {
				limit = top
			}
			total := len(rows)
			page := rows[min(skip, total):min(skip+limit, total)]
			d := map[string]any{"results": page}
			if q.Get("$inlinecount") == "allpages" {
				d["__count"] = strconv.Itoa(total)
			}
			if m.PDNextLinks && skip+len(page) < total {
				q.Set("$skip", strconv.Itoa(skip+len(page)))
				d["__next"] = "https://" + r.Host + r.URL.Path + "?" + q.Encode()
			}
			writeJSON(w, map[string]any{"d": d})
		case http.MethodPost:
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["Pid"] == "" || body["Id"] == "" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			key := body["Pid"] + "/" + body["Id"]
			if exists(kind, key) {
				w.WriteHeader(http.StatusConflict)
				return
			}
			if !store(kind, key, body) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusCreated)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	mm := rePDEntity.FindStringSubmatch(r.URL.Path)
	kind, key := mm[1], mm[2]+"/"+mm[3]
	if !exists(kind, key) {
		notFound(w)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, map[string]any{"d": row(kind, key)})
	case http.MethodPut:
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !store(kind, key, body) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		if kind == "String" {
			delete(m.PDStrings, key)
		} else {
			delete(m.PDBinaries, key)
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (m *Tenant) handleInbound(w http.ResponseWriter, r *http.Request) {
	in := m.Inbound[r.URL.Path]
	if in == nil {
		notFound(w)
		return
	}
	if in.RequireCSRF {
		if r.Method == http.MethodGet && strings.EqualFold(r.Header.Get("X-CSRF-Token"), "fetch") {
			if in.token == "" {
				m.csrfSerial++
				in.token = fmt.Sprintf("rt-token-%d", m.csrfSerial)
			}
			w.Header().Set("X-CSRF-Token", in.token)
			return
		}
		if r.Method != http.MethodGet && (in.token == "" || r.Header.Get("X-CSRF-Token") != in.token) {
			w.Header().Set("X-CSRF-Token", "Required")
			w.WriteHeader(http.StatusForbidden)
			return
		}
	}
	body, _ := io.ReadAll(r.Body)
	m.Received = append(m.Received, ReceivedMessage{Method: r.Method, Path: r.URL.Path, Header: r.Header.Clone(), Body: string(body)})
	if a := m.Artifacts[in.Artifact]; m.Live && a != nil && a.running() && a.info() != nil {
		m.liveSend(w, r, in, string(body))
		return
	}
	if m.Live && in.Artifact != "" {
		m.liveSerial++
		now := time.Now()
		l := MessageLog{Guid: fmt.Sprintf("AGLIVE%022d", m.liveSerial), Artifact: in.Artifact, Status: "COMPLETED",
			CorrelationID: fmt.Sprintf("C-live-%d", m.liveSerial), ApplicationID: r.Header.Get("SAP_ApplicationID"),
			Start: now, End: now.Add(150 * time.Millisecond)}
		if a := m.Artifacts[in.Artifact]; a != nil {
			l.Package = a.Package
		}
		m.addLogs([]MessageLog{l})
		w.Header().Set("SAP_MessageProcessingLogID", l.Guid)
		w.Header().Set("SAP_MplCorrelationId", l.CorrelationID)
	} else if in.MessageGuid != "" {
		w.Header().Set("SAP_MessageProcessingLogID", in.MessageGuid)
	}
	if in.ContentType != "" {
		w.Header().Set("Content-Type", in.ContentType)
	}
	status := in.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write([]byte(in.Response))
}

var (
	reMPLStatus = regexp.MustCompile(`Status eq '([A-Z]+)'`)
	reMPLTime   = regexp.MustCompile(`(LogEnd ge|LogStart le) datetime'([^']+)'`)
)

// liveFilter applies the status and time filters of an MPL query and orders
// newest first, like the tenant.
func liveFilter(logs []MessageLog, filter string) []MessageLog {
	var statuses []string
	for _, mm := range reMPLStatus.FindAllStringSubmatch(filter, -1) {
		statuses = append(statuses, mm[1])
	}
	var since, until time.Time
	for _, mm := range reMPLTime.FindAllStringSubmatch(filter, -1) {
		t, err := time.Parse("2006-01-02T15:04:05.000", mm[2])
		if err != nil {
			continue
		}
		if mm[1] == "LogEnd ge" {
			since = t
		} else {
			until = t
		}
	}
	out := []MessageLog{}
	for _, l := range logs {
		switch {
		case len(statuses) > 0 && !slices.Contains(statuses, l.Status):
		case !since.IsZero() && l.End.Before(since):
		case !until.IsZero() && l.Start.After(until):
		default:
			out = append(out, l)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start.After(out[j].Start) })
	return out
}

var reMPLEq = regexp.MustCompile(`(IntegrationFlowName|ApplicationMessageId|CorrelationId) eq '([^']*)'`)

// filterMessageLogs applies the equality filters and $skip/$top; it returns
// the page and the number of matches.
func filterMessageLogs(logs []MessageLog, q url.Values) ([]MessageLog, int) {
	var out []MessageLog
	for _, l := range logs {
		ok := true
		for _, mm := range reMPLEq.FindAllStringSubmatch(q.Get("$filter"), -1) {
			v := map[string]string{"IntegrationFlowName": l.Artifact, "ApplicationMessageId": l.ApplicationID, "CorrelationId": l.CorrelationID}[mm[1]]
			ok = ok && v == mm[2]
		}
		if ok {
			out = append(out, l)
		}
	}
	total := len(out)
	skip, _ := strconv.Atoi(q.Get("$skip"))
	top, _ := strconv.Atoi(q.Get("$top"))
	if skip > len(out) {
		skip = len(out)
	}
	out = out[skip:]
	if top > 0 && top < len(out) {
		out = out[:top]
	}
	return out, total
}

func mplJSON(l MessageLog) map[string]any {
	pkg := l.Package
	if pkg == "" {
		pkg = "P"
	}
	return map[string]any{
		"MessageGuid": l.Guid, "CorrelationId": l.CorrelationID, "Status": l.Status, "CustomStatus": l.Status, "LogLevel": "INFO",
		"LogStart": odataDate(l.Start), "LogEnd": odataDate(l.End), "IntegrationFlowName": l.Artifact,
		"ApplicationMessageId": l.ApplicationID, "PredecessorMessageGuid": l.Predecessor,
		"IntegrationArtifact": map[string]string{"Id": l.Artifact, "Name": l.Artifact, "Type": "INTEGRATION_FLOW", "PackageId": pkg, "PackageName": pkg + " name"},
	}
}

func (m *Tenant) findMessageLog(guid string) *MessageLog {
	for j := len(m.MessageLogSteps) - 1; j >= 0; j-- { // latest state first
		step := m.MessageLogSteps[j]
		for i := range step {
			if step[i].Guid == guid {
				return &step[i]
			}
		}
	}
	return nil
}

func (m *Tenant) findTrace(id string) *Trace {
	for _, step := range m.MessageLogSteps {
		for _, l := range step {
			for _, st := range l.Steps {
				for i := range st.Traces {
					if st.Traces[i].ID == id {
						return &st.Traces[i]
					}
				}
			}
		}
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func odataDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return fmt.Sprintf("/Date(%d)/", t.UnixMilli())
}

func pick[T any](list []T, i int) T {
	if i >= len(list) {
		return list[len(list)-1]
	}
	return list[i]
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func notFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`{"error":{"code":"Not Found","message":{"value":"Requested entity could not be found."}}}`))
}

func (m *Tenant) handle(w http.ResponseWriter, r *http.Request) {
	// simulated download time, outside the lock so that parallel
	// downloads overlap as on a real tenant
	if m.DownloadLatency > 0 && strings.HasSuffix(r.URL.Path, "/$value") {
		n := m.inFlight.Add(1)
		for {
			peak := m.peakInFlight.Load()
			if n <= peak || m.peakInFlight.CompareAndSwap(peak, n) {
				break
			}
		}
		time.Sleep(m.DownloadLatency)
		m.inFlight.Add(-1)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	path := r.URL.Path
	m.requests = append(m.requests, r.Method+" "+path)

	if m.Live && r.Method == http.MethodGet && r.URL.Query().Has("$top") {
		// collections that refuse $top on a real tenant, with its answers
		switch path {
		case "/api/v1/UserCredentials", "/api/v1/OAuth2ClientCredentials", "/api/v1/SecureParameters":
			odataError(w, http.StatusNotImplemented, "Not Implemented", "$top is not supported.")
			return
		case "/api/v1/KeystoreEntries":
			odataError(w, http.StatusBadRequest, "Bad Request", "Request uri contains the 'top' query option. The 'top' query option is not supported.")
			return
		case "/api/v1/DataStores":
			odataError(w, http.StatusBadRequest, "Bad Request", "The query option $top is not supported")
			return
		}
	}
	if m.Live && r.Method == http.MethodGet && path == "/api/v1/LogFiles" && (r.URL.Query().Has("$top") || r.URL.Query().Has("$orderby")) {
		odataError(w, http.StatusNotImplemented, "Not Implemented", "Not implemented")
		return
	}
	if strings.HasPrefix(path, "/_mock/") {
		m.handleAdmin(w, r)
		return
	}
	if f := m.fault(path); f != nil {
		if f.Message != "" {
			odataError(w, f.Status, http.StatusText(f.Status), f.Message)
		} else {
			w.WriteHeader(f.Status)
		}
		return
	}
	for _, prefix := range m.ForbidPaths {
		if strings.HasPrefix(path, prefix) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
	}
	if m.StatusOverride != 0 {
		w.WriteHeader(m.StatusOverride)
		return
	}
	if strings.HasPrefix(path, "/http/") || strings.HasPrefix(path, "/cxf/") {
		m.handleInbound(w, r)
		return
	}
	if m.OAuth && r.Method == http.MethodPost && path == "/oauth/token" { // client credentials, any client accepted
		writeJSON(w, map[string]any{"access_token": "mock-token", "token_type": "bearer", "expires_in": 3600})
		return
	}
	if r.Method == http.MethodGet && path == "/api/v1/" { // CSRF token fetch
		if strings.EqualFold(r.Header.Get("X-CSRF-Token"), "fetch") {
			if m.csrfToken == "" {
				m.csrfSerial++
				m.csrfToken = fmt.Sprintf("token-%d", m.csrfSerial)
			}
			w.Header().Set("X-CSRF-Token", m.csrfToken)
			http.SetCookie(w, &http.Cookie{Name: "JSESSIONID", Value: "session-" + m.csrfToken})
		}
		return
	}
	if !m.NoCSRF && r.Method != http.MethodGet && strings.HasPrefix(r.Header.Get("Authorization"), "Basic ") {
		cookie, err := r.Cookie("JSESSIONID")
		if m.csrfToken == "" || r.Header.Get("X-CSRF-Token") != m.csrfToken || err != nil || cookie.Value != "session-"+m.csrfToken {
			w.Header().Set("X-CSRF-Token", "Required")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("CSRF token validation failed"))
			return
		}
	}

	if v, ok := m.Raw[path]; ok {
		if m.RawQueries == nil {
			m.RawQueries = map[string]string{}
		}
		m.RawQueries[path] = r.URL.RawQuery
		switch {
		case r.Method == http.MethodDelete:
			m.Deleted = append(m.Deleted, path)
			w.WriteHeader(http.StatusAccepted)
		case r.Method != http.MethodGet:
			w.WriteHeader(http.StatusMethodNotAllowed)
		default:
			switch val := v.(type) {
			case []byte:
				_, _ = w.Write(val)
			case []map[string]any:
				writeJSON(w, map[string]any{"d": map[string]any{"results": val}})
			default:
				writeJSON(w, map[string]any{"d": val})
			}
		}
		return
	}

	if m.handleJMS(w, r, path) {
		return
	}

	switch {
	case rePDCollection.MatchString(path) || rePDEntity.MatchString(path):
		m.handlePD(w, r)

	case reCredential.MatchString(path):
		m.handleCredential(w, r, reCredential.FindStringSubmatch(path))

	case r.Method == http.MethodGet && path == "/api/v1/KeystoreEntries":
		rows := []map[string]any{}
		for _, e := range m.Keystore {
			rows = append(rows, map[string]any{"Hexalias": strings.ToUpper(hex.EncodeToString([]byte(e.Alias))), "Alias": e.Alias,
				"Type": "Certificate", "KeyType": "RSA", "KeySize": 2048, "ValidNotAfter": odataDate(e.NotAfter), "SubjectDN": "CN=" + e.Alias})
		}
		writeJSON(w, map[string]any{"d": map[string]any{"results": rows}})

	case r.Method == http.MethodGet && reKeystoreCert.MatchString(path):
		alias, _ := hex.DecodeString(reKeystoreCert.FindStringSubmatch(path)[1])
		for _, e := range m.Keystore {
			if e.Alias == string(alias) {
				_, _ = w.Write(e.DER)
				return
			}
		}
		notFound(w)

	case r.Method == http.MethodPut && reCertImport.MatchString(path):
		alias, _ := hex.DecodeString(reCertImport.FindStringSubmatch(path)[1])
		body, _ := io.ReadAll(r.Body)
		der, err := base64.StdEncoding.DecodeString(string(body))
		if err != nil || r.Header.Get("Content-Type") != "application/pkix-cert" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		for i, e := range m.Keystore {
			if e.Alias == string(alias) {
				if r.URL.Query().Get("update") != "true" {
					w.WriteHeader(http.StatusConflict)
					return
				}
				m.Keystore[i].DER = der
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		m.Keystore = append(m.Keystore, KeystoreEntry{Alias: string(alias), DER: der, NotAfter: time.Now().Add(365 * 24 * time.Hour)})
		w.WriteHeader(http.StatusNoContent)

	case r.Method == http.MethodGet && path == "/api/v1/MessageProcessingLogs":
		m.LastMessageLogQuery = r.URL.RawQuery
		var logs []MessageLog
		if len(m.MessageLogSteps) > 0 {
			logs = pick(m.MessageLogSteps, m.mplQueries)
		}
		m.mplQueries++
		total := len(logs)
		if m.Live {
			logs = liveFilter(logs, r.URL.Query().Get("$filter"))
			total = len(logs)
		}
		if m.FilterMessageLogs {
			logs, total = filterMessageLogs(logs, r.URL.Query())
		}
		results := []map[string]any{}
		for _, l := range logs {
			results = append(results, mplJSON(l))
		}
		writeJSON(w, map[string]any{"d": map[string]any{"results": results, "__count": fmt.Sprint(total)}})

	case r.Method == http.MethodGet && reMPL.MatchString(path):
		mm := reMPL.FindStringSubmatch(path)
		l := m.findMessageLog(mm[1])
		if l == nil {
			notFound(w)
			return
		}
		switch mm[2] {
		case "":
			writeJSON(w, map[string]any{"d": mplJSON(*l)})
		case "/ErrorInformation":
			if l.ErrorText == "" {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			info := map[string]any{"MessageGuid": l.Guid, "Type": "MPL"}
			for _, st := range l.Steps {
				if st.Error != "" {
					info["LastErrorModelStepId"] = st.ModelStepID
				}
			}
			writeJSON(w, map[string]any{"d": info})
		case "/ErrorInformation/$value":
			if l.ErrorText == "" {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte(l.ErrorText))
		case "/CustomHeaderProperties":
			rows := []map[string]string{}
			for _, k := range sortedKeys(l.Headers) {
				rows = append(rows, map[string]string{"Name": k, "Value": l.Headers[k]})
			}
			writeJSON(w, map[string]any{"d": map[string]any{"results": rows}})
		case "/AdapterAttributes":
			writeJSON(w, map[string]any{"d": map[string]any{"results": []map[string]string{{"AdapterId": "HTTPS", "Name": "Status", "Value": "200"}}}})
		case "/Attachments":
			rows := []map[string]any{}
			for _, n := range sortedKeys(l.Attachments) {
				rows = append(rows, map[string]any{"Id": "att-" + l.Guid + "-" + n, "Name": n, "ContentType": "text/plain",
					"PayloadSize": len(l.Attachments[n]), "TimeStamp": odataDate(l.End)})
			}
			writeJSON(w, map[string]any{"d": map[string]any{"results": rows}})
		case "/MessageStoreEntries":
			rows := []map[string]any{}
			for _, id := range sortedKeys(l.StoreEntries) {
				rows = append(rows, map[string]any{"Id": id, "MessageGuid": l.Guid, "MessageStoreId": "store", "TimeStamp": odataDate(l.End), "HasAttachments": false})
			}
			writeJSON(w, map[string]any{"d": map[string]any{"results": rows}})
		case "/Runs":
			writeJSON(w, map[string]any{"d": map[string]any{"results": []map[string]any{
				{"Id": "run-" + l.Guid, "RunStart": odataDate(l.Start), "RunStop": odataDate(l.End), "OverallState": l.Status}}}})
		default:
			notFound(w)
		}

	case r.Method == http.MethodGet && reAttValue.MatchString(path):
		id := reAttValue.FindStringSubmatch(path)[1]
		for _, step := range m.MessageLogSteps {
			for _, l := range step {
				for name, content := range l.Attachments {
					if "att-"+l.Guid+"-"+name == id {
						_, _ = w.Write([]byte(content))
						return
					}
				}
			}
		}
		notFound(w)

	case r.Method == http.MethodGet && reStoreValue.MatchString(path):
		id := reStoreValue.FindStringSubmatch(path)[1]
		for _, step := range m.MessageLogSteps {
			for _, l := range step {
				if content, ok := l.StoreEntries[id]; ok {
					_, _ = w.Write([]byte(content))
					return
				}
			}
		}
		notFound(w)

	case r.Method == http.MethodGet && reRunSteps.MatchString(path):
		guid := strings.TrimPrefix(reRunSteps.FindStringSubmatch(path)[1], "run-")
		l := m.findMessageLog(guid)
		if l == nil {
			notFound(w)
			return
		}
		rows := []map[string]any{}
		for i, st := range l.Steps {
			row := map[string]any{"StepId": st.StepID, "ModelStepId": st.ModelStepID, "Activity": st.Activity, "ChildCount": i,
				"Status": st.Status, "Error": st.Error, "StepStart": odataDate(l.Start), "StepStop": odataDate(l.End)}
			if r.URL.Query().Get("$expand") == "RunStepProperties" {
				props := []map[string]any{}
				for _, k := range sortedKeys(st.Properties) {
					props = append(props, map[string]any{"RunId": "run-" + l.Guid, "ChildCount": i, "Name": k, "Value": st.Properties[k]})
				}
				row["RunStepProperties"] = map[string]any{"results": props}
			}
			rows = append(rows, row)
		}
		writeJSON(w, map[string]any{"d": map[string]any{"results": rows}})

	case m.ForbidTraces && (reStepTraces.MatchString(path) || reTrace.MatchString(path)):
		w.WriteHeader(http.StatusForbidden)

	case r.Method == http.MethodGet && reStepTraces.MatchString(path):
		mm := reStepTraces.FindStringSubmatch(path)
		l := m.findMessageLog(strings.TrimPrefix(mm[1], "run-"))
		i, _ := strconv.Atoi(mm[2])
		if l == nil || i >= len(l.Steps) {
			notFound(w)
			return
		}
		rows := []map[string]any{}
		for _, tr := range l.Steps[i].Traces {
			rows = append(rows, map[string]any{"TraceId": tr.ID, "ModelStepId": l.Steps[i].ModelStepID, "PayloadSize": fmt.Sprint(len(tr.Payload)), "MimeType": "text/plain"})
		}
		writeJSON(w, map[string]any{"d": map[string]any{"results": rows}})

	case r.Method == http.MethodGet && reTrace.MatchString(path):
		mm := reTrace.FindStringSubmatch(path)
		tr := m.findTrace(mm[1])
		if tr == nil {
			notFound(w)
			return
		}
		var props map[string]string
		switch mm[2] {
		case "$value":
			_, _ = w.Write([]byte(tr.Payload))
			return
		case "Properties":
			props = tr.Headers
		case "ExchangeProperties":
			props = tr.ExchangeProperties
		}
		rows := []map[string]string{}
		for _, k := range sortedKeys(props) {
			rows = append(rows, map[string]string{"Name": k, "Value": props[k]})
		}
		writeJSON(w, map[string]any{"d": map[string]any{"results": rows}})

	case r.Method == http.MethodGet && path == "/api/v1/IntegrationDesigntimeLocks":
		rows := []map[string]any{}
		for _, l := range m.Locks {
			row := map[string]any{"ResourceId": l.ResourceID, "ArtifactId": l.ArtifactID, "ArtifactType": "INTEGRATION_FLOW",
				"CreatedBy": l.CreatedBy, "CreatedAt": odataDate(l.CreatedAt)}
			if a := m.Artifacts[l.ArtifactID]; a != nil {
				row["ArtifactName"], row["PackageId"] = cmpOr(a.Name, l.ArtifactID), a.Package
				for _, p := range m.Packages {
					if p.ID == a.Package {
						row["PackageName"] = p.Name
					}
				}
			}
			rows = append(rows, row)
		}
		writeJSON(w, map[string]any{"d": map[string]any{"results": rows}})

	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/api/v1/IntegrationDesigntimeLocks("):
		id := strings.Trim(strings.TrimSuffix(strings.TrimPrefix(path, "/api/v1/IntegrationDesigntimeLocks("), ")"), "'")
		i := slices.IndexFunc(m.Locks, func(l Lock) bool { return l.ResourceID == id })
		if i < 0 {
			notFound(w)
			return
		}
		m.Locks = slices.Delete(m.Locks, i, i+1)
		w.WriteHeader(http.StatusOK)

	case r.Method == http.MethodPost && path == "/api/v1/ValidateIntegrationDesigntimeArtifact":
		a := m.Artifacts[strings.Trim(r.URL.Query().Get("Id"), "'")]
		if a == nil || a.DesignVersion == "" {
			notFound(w)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		result := a.ValidationResult
		if result == "" {
			var issues []checkIssue
			if c, err := readArtifactZip(a.Zip); err == nil && a.Type == "Integration" {
				issues = checkFlow(c)
			}
			result = checkResult(issues)
		}
		_, _ = w.Write([]byte(result))

	case r.Method == http.MethodPost && path == "/api/v1/ExecuteIntegrationDesigntimeArtifactsGuidelines":
		a := m.Artifacts[strings.Trim(r.URL.Query().Get("Id"), "'")]
		if a == nil || a.DesignVersion == "" {
			notFound(w)
			return
		}
		a.guidelineRuns++
		a.guidelineGets = 0
		writeJSON(w, map[string]any{"d": map[string]any{"ExecutionId": fmt.Sprintf("exec-%d", a.guidelineRuns)}})

	case r.Method == http.MethodGet && reGuidelineList.MatchString(path):
		a := m.Artifacts[reGuidelineList.FindStringSubmatch(path)[1]]
		if a == nil {
			notFound(w)
			return
		}
		rows := []map[string]any{}
		if a.guidelineRuns > 0 && len(a.GuidelineStatuses) > 0 {
			rows = append(rows, map[string]any{"ExecutionId": fmt.Sprintf("exec-%d", a.guidelineRuns), "ArtifactVersion": a.DesignVersion,
				"ExecutionStatus": pick(a.GuidelineStatuses, a.guidelineGets), "ExecutionTime": fmt.Sprint(time.Now().UnixMilli())})
			a.guidelineGets++
		}
		writeJSON(w, map[string]any{"d": map[string]any{"results": rows}})

	case r.Method == http.MethodGet && reGuidelineResult.MatchString(path):
		mm := reGuidelineResult.FindStringSubmatch(path)
		a := m.Artifacts[mm[1]]
		if a == nil {
			notFound(w)
			return
		}
		status := ""
		if len(a.GuidelineStatuses) > 0 {
			status = a.GuidelineStatuses[len(a.GuidelineStatuses)-1]
		}
		writeJSON(w, map[string]any{"d": map[string]any{"ExecutionId": mm[2], "ExecutionStatus": status,
			"DesignGuidelines": map[string]any{"results": a.Guidelines}}})

	case r.Method == http.MethodGet && path == "/api/v1/ServiceEndpoints":
		rows := []map[string]any{}
		filter := r.URL.Query().Get("$filter")
		for _, id := range sortedKeys(m.Artifacts) {
			a := m.Artifacts[id]
			if a.EndpointURL == "" || (filter != "" && filter != "Name eq '"+id+"'") {
				continue
			}
			rows = append(rows, map[string]any{"Name": id, "Id": id + "$endpointAddress=x", "Title": id, "Version": a.DesignVersion, "Protocol": "REST",
				"EntryPoints": map[string]any{"results": []map[string]string{{"Name": id, "Url": a.EndpointURL, "Type": "PROD"}}}})
		}
		writeJSON(w, map[string]any{"d": map[string]any{"results": rows}})

	case r.Method == http.MethodGet && path == "/api/v1/IntegrationRuntimeArtifacts":
		filter := r.URL.Query().Get("$filter")
		rows := []map[string]any{}
		for _, id := range sortedKeys(m.Artifacts) {
			rt := m.Artifacts[id].Runtime
			if rt == nil || (filter != "" && !strings.Contains(filter, "'"+rt.Status+"'")) {
				continue
			}
			rows = append(rows, map[string]any{"Id": id, "Version": rt.Version, "Name": id, "Type": "INTEGRATION_FLOW", "Status": rt.Status, "DeployedOn": odataDate(rt.DeployedOn)})
		}
		writeJSON(w, map[string]any{"d": map[string]any{"results": rows}})

	case r.Method == http.MethodGet && reResources.MatchString(path):
		mm := reResources.FindStringSubmatch(path)
		a := m.Artifacts[mm[1]]
		if a == nil {
			notFound(w)
			return
		}
		if mm[2] == "" {
			rows := []map[string]any{}
			for _, n := range sortedKeys(a.Resources) {
				rows = append(rows, map[string]any{"Name": n, "ResourceType": a.Resources[n].Type, "ResourceSize": fmt.Sprint(len(a.Resources[n].Content))}) // a string, as real tenants send it
			}
			writeJSON(w, map[string]any{"d": map[string]any{"results": rows}})
			return
		}
		res, ok := a.Resources[mm[3]]
		if !ok || res.Type != mm[4] {
			notFound(w)
			return
		}
		_, _ = w.Write(res.Content)

	case r.Method == http.MethodGet && reDesignValue.MatchString(path):
		mm := reDesignValue.FindStringSubmatch(path)
		a := m.Artifacts[mm[2]]
		if a == nil || a.Zip == nil || a.Type != mm[1] {
			notFound(w)
			return
		}
		_, _ = w.Write(a.Zip)

	case r.Method == http.MethodPost && path == "/Operations/com.sap.it.op.tmn.commands.dashboard.webui.IntegrationComponentSetMplLogLevelCommand":
		// like the tenant: the command does not produce JSON
		if accept := r.Header.Get("Accept"); accept != "" && !strings.Contains(accept, "*/*") {
			w.WriteHeader(http.StatusNotAcceptable)
			return
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || m.Artifacts[body["artifactSymbolicName"]] == nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if m.LogLevels == nil {
			m.LogLevels = map[string]map[string]string{}
		}
		m.LogLevels[body["artifactSymbolicName"]] = body
		writeJSON(w, map[string]any{})

	case r.Method == http.MethodGet && rePackage.MatchString(path):
		id := rePackage.FindStringSubmatch(path)[1]
		for _, p := range m.Packages {
			if p.ID == id {
				writeJSON(w, map[string]any{"d": map[string]string{"Id": p.ID, "Name": p.Name, "Version": p.Version,
					"ShortText": p.ShortText, "Description": p.Description}})
				return
			}
		}
		notFound(w)

	case r.Method == http.MethodPost && path == "/api/v1/IntegrationPackages":
		var body struct {
			D struct{ Id, Name, ShortText, Description string } `json:"d"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.D.Id == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(body.D.ShortText) == "" { // as the tenant answers
			odataError(w, http.StatusBadRequest, "Bad Request", "Property 'ShortText' cannot be empty. Enter a valid value.")
			return
		}
		for _, p := range m.Packages {
			if p.ID == body.D.Id {
				w.WriteHeader(http.StatusConflict)
				return
			}
		}
		m.Packages = append(m.Packages, Package{ID: body.D.Id, Name: body.D.Name, Version: "1.0.0", ShortText: body.D.ShortText, Description: body.D.Description})
		w.WriteHeader(http.StatusCreated)

	case r.Method == http.MethodGet && path == "/api/v1/IntegrationPackages":
		results := []map[string]string{}
		for _, p := range m.Packages {
			results = append(results, map[string]string{"Id": p.ID, "Name": p.Name, "Version": p.Version, "ShortText": p.ShortText, "Description": p.Description})
		}
		writeJSON(w, map[string]any{"d": map[string]any{"results": results}})

	case r.Method == http.MethodGet && rePkgArts.MatchString(path):
		mm := rePkgArts.FindStringSubmatch(path)
		results := []map[string]string{}
		for _, id := range sortedKeys(m.Artifacts) {
			a := m.Artifacts[id]
			if a.Package == mm[1] && a.Type == mm[2] {
				row := map[string]string{"Id": id, "Name": a.Name, "Version": a.DesignVersion}
				if a.ModifiedBy != "" {
					row["ModifiedBy"] = a.ModifiedBy
				}
				if !a.ModifiedAt.IsZero() {
					row["ModifiedAt"] = odataDate(a.ModifiedAt)
				}
				results = append(results, row)
			}
		}
		writeJSON(w, map[string]any{"d": map[string]any{"results": results}})

	case r.Method == http.MethodGet && reConfigs.MatchString(path):
		a := m.Artifacts[reConfigs.FindStringSubmatch(path)[1]]
		if a == nil || a.DesignVersion == "" {
			notFound(w)
			return
		}
		results := []map[string]string{}
		for _, k := range sortedKeys(a.Parameters) {
			results = append(results, map[string]string{"ParameterKey": k, "ParameterValue": a.Parameters[k], "DataType": "xsd:string"})
		}
		writeJSON(w, map[string]any{"d": map[string]any{"results": results}})

	case r.Method == http.MethodPut && reConfig.MatchString(path):
		mm := reConfig.FindStringSubmatch(path)
		a := m.Artifacts[mm[1]]
		if a == nil {
			notFound(w)
			return
		}
		if a.ConfigUpdateStatus != 0 {
			w.WriteHeader(a.ConfigUpdateStatus)
			return
		}
		var body struct{ ParameterValue string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		if a.Parameters == nil {
			a.Parameters = map[string]string{}
		}
		a.Parameters[mm[2]] = body.ParameterValue
		if a.ConfigBumpsModified {
			a.ModifiedAt = time.Now()
		}
		w.WriteHeader(http.StatusAccepted)

	case r.Method == http.MethodPost && reDesignCreate.MatchString(path):
		typ := reDesignCreate.FindStringSubmatch(path)[1]
		var body struct{ Id, Name, PackageId, ArtifactContent string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Id == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		zipData, err := base64.StdEncoding.DecodeString(body.ArtifactContent)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if msg := uploadProblem(zipData); msg != "" {
			odataError(w, http.StatusInternalServerError, "Internal Server Error", msg)
			return
		}
		if a := m.Artifacts[body.Id]; a != nil && a.DesignVersion != "" {
			w.WriteHeader(http.StatusConflict)
			return
		}
		if m.Artifacts == nil {
			m.Artifacts = map[string]*Artifact{}
		}
		a := m.Artifacts[body.Id]
		if a == nil {
			a = &Artifact{}
			m.Artifacts[body.Id] = a
		}
		a.Type, a.DesignVersion, a.Package, a.Name = typ, "1.0.0", body.PackageId, body.Name
		a.applyContent(zipData)
		a.Uploads++
		if m.Live {
			a.ModifiedAt, a.ModifiedBy = time.Now(), "mock-user"
		}
		w.WriteHeader(http.StatusCreated)

	case r.Method == http.MethodPut && reDesign.MatchString(path):
		mm := reDesign.FindStringSubmatch(path)
		a := m.Artifacts[mm[2]]
		if a == nil || a.DesignVersion == "" || a.Type != mm[1] {
			notFound(w)
			return
		}
		var body struct{ ArtifactContent string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		zipData, err := base64.StdEncoding.DecodeString(body.ArtifactContent)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if msg := uploadProblem(zipData); msg != "" {
			odataError(w, http.StatusInternalServerError, "Internal Server Error", msg)
			return
		}
		a.applyContent(zipData)
		a.Uploads++
		a.ModifiedAt = time.Now()
		if m.Live {
			a.ModifiedBy = "mock-user"
		}
		w.WriteHeader(http.StatusOK)

	// like the public API: only integration flows can be saved as a version
	case r.Method == http.MethodPost && reSaveAsVersion.MatchString(path):
		typ := reSaveAsVersion.FindStringSubmatch(path)[1]
		id := strings.Trim(r.URL.Query().Get("Id"), "'")
		version := strings.Trim(r.URL.Query().Get("SaveAsVersion"), "'")
		a := m.Artifacts[id]
		if typ != "Integration" || a == nil || a.DesignVersion == "" || version == "" {
			notFound(w)
			return
		}
		a.SavedVersions = append(a.SavedVersions, version)
		if a.SaveAsVersionRejectsLower && compareVersions(version, a.DesignVersion) < 0 {
			w.WriteHeader(http.StatusBadRequest)
			writeJSON(w, map[string]any{"error": map[string]any{"message": map[string]string{"value": "Version must be higher than the current version"}}})
			return
		}
		if !a.SaveAsVersionIgnored {
			a.DesignVersion = version
		}
		writeJSON(w, map[string]any{"d": map[string]string{"Id": id, "Version": version}})

	case r.Method == http.MethodGet && reDesign.MatchString(path):
		mm := reDesign.FindStringSubmatch(path)
		a := m.Artifacts[mm[2]]
		if a == nil || a.DesignVersion == "" || a.Type != mm[1] {
			notFound(w)
			return
		}
		d := map[string]string{"Id": mm[2], "Version": a.DesignVersion}
		if !a.ModifiedAt.IsZero() {
			d["ModifiedAt"] = odataDate(a.ModifiedAt)
		}
		writeJSON(w, map[string]any{"d": d})

	case r.Method == http.MethodPost && reDeploy.MatchString(path):
		id := strings.Trim(r.URL.Query().Get("Id"), "'")
		a := m.Artifacts[id]
		if a == nil {
			notFound(w)
			return
		}
		if a.DeployStatusCode != 0 {
			w.WriteHeader(a.DeployStatusCode)
			return
		}
		a.triggered, a.runtimeGets, a.taskGets = true, 0, 0
		a.Deploys++
		if m.Live && len(a.AfterDeploy) == 0 {
			if a.DesignVersion == "" {
				notFound(w)
				return
			}
			version := a.DesignVersion
			if version == "Active" && a.Runtime != nil { // a draft deploys as its last version
				version = a.Runtime.Version
			}
			a.Runtime, a.undeployed = &Runtime{Version: version, Status: "STARTED", DeployedOn: time.Now()}, false
			a.ErrorInfo = ""
			if conflict := m.registerEndpoints(id, a); conflict != "" {
				a.Runtime.Status, a.ErrorInfo = "ERROR", conflict
			}
			if len(a.TaskStatuses) == 0 {
				a.TaskStatuses = []string{"SUCCESS"}
			}
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("task-" + id))

	case r.Method == http.MethodGet && reTask.MatchString(path):
		a := m.Artifacts[reTask.FindStringSubmatch(path)[1]]
		if a == nil || len(a.TaskStatuses) == 0 {
			notFound(w)
			return
		}
		status := pick(a.TaskStatuses, a.taskGets)
		a.taskGets++
		writeJSON(w, map[string]any{"d": map[string]string{"TaskId": "task", "Status": status}})

	case r.Method == http.MethodGet && reErrInfo.MatchString(path):
		a := m.Artifacts[reErrInfo.FindStringSubmatch(path)[1]]
		if a == nil || a.ErrorInfo == "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSON(w, map[string]any{"parameter": []string{a.ErrorInfo}})

	case r.Method == http.MethodGet && reRuntime.MatchString(path):
		a := m.Artifacts[reRuntime.FindStringSubmatch(path)[1]]
		if a == nil {
			notFound(w)
			return
		}
		var rt *Runtime
		switch {
		case a.undeployed:
			if a.UndeployAfter >= 0 && a.undeployedGets >= a.UndeployAfter {
				rt = nil
			} else {
				rt = a.Runtime
			}
			a.undeployedGets++
		case a.triggered && len(a.AfterDeploy) > 0:
			rt = pick(a.AfterDeploy, a.runtimeGets)
			a.runtimeGets++
		default:
			rt = a.Runtime
		}
		if rt == nil {
			notFound(w)
			return
		}
		writeJSON(w, map[string]any{"d": map[string]string{
			"Id": reRuntime.FindStringSubmatch(path)[1], "Version": rt.Version, "Status": rt.Status,
			"DeployedOn": odataDate(rt.DeployedOn),
		}})

	case r.Method == http.MethodDelete && reRuntime.MatchString(path):
		a := m.Artifacts[reRuntime.FindStringSubmatch(path)[1]]
		if a == nil || a.Runtime == nil {
			notFound(w)
			return
		}
		a.undeployed, a.undeployedGets = true, 0
		if m.Live {
			a.Runtime, a.undeployed, a.triggered = nil, false, false
			m.unregisterEndpoints(reRuntime.FindStringSubmatch(path)[1], a)
		}
		w.WriteHeader(http.StatusAccepted)

	default:
		notFound(w)
	}
}

// compareVersions compares dotted numeric versions (-1, 0, 1).
func compareVersions(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(as), len(bs)); i++ {
		x, y := 0, 0
		if i < len(as) {
			x, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			y, _ = strconv.Atoi(bs[i])
		}
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
	}
	return 0
}
