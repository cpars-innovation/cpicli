package scope

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/cpars-innovation/cpicli/pkg/odatatime"
	"github.com/cpars-innovation/cpicli/pkg/testtools"
)

// Refusal is the error for an operation outside the scope (exit code 9,
// errorCategory "scope"). It names the resource only as the caller gave it,
// so a refusal looks the same whether the resource exists or not.
type Refusal struct {
	Resource Resource
	Action   string
	Reason   string // when the request could not be classified
}

func (r *Refusal) Error() string {
	if r.Reason != "" {
		return "scope: " + r.Reason
	}
	return fmt.Sprintf("scope: %s of %s is not granted", r.Action, r.Resource)
}

// ExitCode implements output.ExitCoder.
func (r *Refusal) ExitCode() int { return exitcode.Scope }

// ToolingPrefix marks cpictl's own test tooling flows (harness, receiver
// stub, XSLT test flow): a scope that grants "test" may read and call them
// wherever they are installed, but never change them. Their messages are
// decided by the flow they tested (toolingTarget). Their Partner Directory
// partners (stub answers, stylesheets under test) may be written with "test".
const ToolingPrefix = testtools.IDPrefix

func toolingFlow(id string) bool { return strings.HasPrefix(id, ToolingPrefix) }

func toolingPartner(pid string) bool { return pid == testtools.XSLTPID || pid == testtools.StubPID }

// Guard enforces a scope on every tenant request of the process (see
// httpclnt.SetGuard): writes outside the scope are refused before they are
// sent, lists are filtered, single reads of out-of-scope resources refused.
type Guard struct {
	Scope *Scope
	// Fetch reads a tenant path for the guard's own lookups (artifact to
	// package, message to flow). It runs through the guarded executer; the
	// guard recognises its own requests by their context.
	Fetch func(ctx context.Context, path string) ([]byte, error)
	// OnRefuse is called for every refusal (events, audit); may be nil.
	OnRefuse func(*Refusal)
	// Refresh is the minimum time between two rebuilds of the artifact map
	// on an unknown artifact (default 30 s).
	Refresh time.Duration
	// ProcessDirectAddresses returns the ProcessDirect sender addresses of
	// a flow (ops.ProcessDirectAddresses). Without it, calls through the
	// test harness are refused: the harness reaches any address.
	ProcessDirectAddresses func(ctx context.Context, artifactID string) ([]string, error)

	mu           sync.Mutex
	artifacts    map[string]string    // artifact ID -> package (allowed packages only)
	builtAt      time.Time            // last successful build of artifacts
	failedAt     time.Time            // last failed build (zero after a success)
	building     chan struct{}        // closed when the running build ends
	messages     map[string]string    // message GUID -> flow, for messages seen in allowed answers (a tooling message: the flow it tested)
	children     map[string]string    // IDs of runs, steps, traces, attachments, store entries -> flow of their message
	tooling      map[string]bool      // message GUIDs and child IDs of tooling messages: decided with "test" on the flow tested
	targets      map[string]string    // correlation ID -> allowed flow outside the tooling that shares it
	noTarget     map[string]time.Time // correlation ID -> until when "no allowed flow" holds
	correlations map[string]string    // tooling message GUID -> correlation ID ("": not looked up)
	// allEndpoints are the entry points of every flow of the tenant (the
	// guard's own lookup, replaced on each refresh), so that an allowed
	// flow's entry point does not cover another team's below it
	allEndpoints []endpointEntry
	epBuiltAt    time.Time
	epBuilding   chan struct{}     // closed when the running refresh ends
	epErr        error             // the last refresh's error
	epFailedAt   time.Time         // the last failed refresh (zero after a success)
	pdFlows      map[string]string // ProcessDirect address -> flow, for allowed flows (built on first harness call)
	pdBuiltAt    time.Time
	tenant       string // scheme://host:port of the tenant API
}

type internalKey struct{}

// internal marks the guard's own lookups.
func internal(ctx context.Context) context.Context {
	return context.WithValue(ctx, internalKey{}, true)
}

func isInternal(ctx context.Context) bool {
	v, _ := ctx.Value(internalKey{}).(bool)
	return ctx != nil && v
}

// decision is what a request needs, and how its answer is filtered.
type decision struct {
	need   []need
	filter func(g *Guard, body []byte) ([]byte, error)
	record func(g *Guard, r *httpclnt.GuardRequest, body []byte) // remember IDs from an allowed answer
	allow  bool                                                  // no check needed (metadata, CSRF token)
}

type need struct {
	res    Resource
	action string
	// toolingWrite: a write the test tooling's own partners may take with
	// "test" (stub answers, stylesheets under test); not set for deletes of
	// whole partners
	toolingWrite bool
	// param is the Partner Directory parameter ID a request names; for the
	// tooling's partners it decides whose the parameter is (toolingParamOwned)
	param string
	// entity: the partner itself, without its parameters
	entity bool
}

func (g *Guard) refuse(r *Refusal) error {
	if g.OnRefuse != nil {
		g.OnRefuse(r)
	}
	return r
}

// Before implements httpclnt.Guard.
func (g *Guard) Before(r *httpclnt.GuardRequest) error {
	_, err := g.Check(r)
	return err
}

// Access is one permission a request needed.
type Access struct {
	Resource Resource
	Action   string
}

// Checked describes a request Check allowed.
type Checked struct {
	// Accesses are the permissions the request needed (for an audit
	// record); empty for lists, which are filtered after the answer, and for
	// requests that need none ($metadata, CSRF token).
	Accesses []Access
	// ReadsAnswer: After filters the answer or remembers IDs from it, so a
	// proxy must pass the whole answer through After. Otherwise the answer
	// may be streamed to the caller unchanged.
	ReadsAnswer bool
}

// Check decides r like Before and reports what the request touches.
func (g *Guard) Check(r *httpclnt.GuardRequest) (Checked, error) {
	if isInternal(r.Ctx) && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		return Checked{}, nil
	}
	d, err := g.classify(r)
	if err != nil {
		return Checked{}, err
	}
	c := Checked{ReadsAnswer: d.filter != nil || d.record != nil}
	for _, n := range d.need {
		if err := g.check(r.Ctx, n); err != nil {
			return Checked{}, err
		}
		c.Accesses = append(c.Accesses, Access{Resource: n.res, Action: n.action})
	}
	return c, nil
}

// After implements httpclnt.Guard.
func (g *Guard) After(r *httpclnt.GuardRequest, status int, _ http.Header, body []byte) ([]byte, error) {
	if isInternal(r.Ctx) || status < 200 || status > 299 {
		return body, nil
	}
	d, err := g.classify(r)
	if err != nil {
		return nil, err
	}
	if d.filter != nil {
		if body, err = d.filter(g, body); err != nil {
			return nil, err
		}
	}
	if d.record != nil {
		d.record(g, r, body)
	}
	return body, nil
}

// check decides one need, resolving an artifact's package when required.
func (g *Guard) check(ctx context.Context, n need) error {
	res := n.res
	switch res.Kind {
	case KindArtifact:
		if !ValidID(res.ID) {
			return g.refuse(&Refusal{Resource: Resource{Kind: res.Kind, ID: res.ID}, Action: n.action})
		}
		if toolingFlow(res.ID) && (n.action == ActionRead || n.action == ActionTest) && g.Scope.Grants(ActionTest) {
			return nil
		}
		if res.Package == "" {
			res.Package = g.packageOf(ctx, res.ID)
		}
		if !g.Scope.Allows(res, n.action) {
			// name the artifact only as given: no package for artifacts outside
			return g.refuse(&Refusal{Resource: Resource{Kind: KindArtifact, ID: n.res.ID, Package: n.res.Package}, Action: n.action})
		}
	case KindPackage, KindPartner:
		if !ValidID(res.ID) || !g.Scope.Allows(res, n.action) {
			if res.Kind == KindPartner && toolingPartner(res.ID) && g.Scope.Grants(ActionTest) && (n.action == ActionRead || n.toolingWrite) {
				if n.entity && n.action == ActionRead || g.toolingParamOwned(ctx, n.param) {
					return nil
				}
				if n.param != "" {
					return g.refuse(&Refusal{Resource: res, Action: n.action, Reason: fmt.Sprintf(
						"under a scope, a stub rule or stylesheet under test needs an ID that starts with a flow or partner of the scope and a dot, e.g. <flowId>.%s (not %q)", n.param, n.param)})
				}
			}
			return g.refuse(&Refusal{Resource: res, Action: n.action})
		}
	default:
		allowed := g.Scope.Allows(res, n.action)
		if fn := checkKind[res.Kind]; fn != nil {
			allowed = fn(g, ctx, n)
		}
		if !allowed {
			return g.refuse(&Refusal{Resource: res, Action: n.action})
		}
	}
	return nil
}

// --- artifact to package --------------------------------------------------

var designtimeTypes = []string{"Integration", "ValueMapping", "MessageMapping", "ScriptCollection"}

// packageOf returns the package of an artifact in an allowed package, or "".
// It lists the artifacts of the allowed packages (a few calls) instead of
// every package; the map is rebuilt when it is older than Refresh.
//
// One rebuild runs at a time; other callers wait for it. A rebuild runs
// detached from the caller's context, so a cancelled request does not fail
// it for everyone. A failed rebuild keeps the last good map, retried after
// mapRetry; that map is trusted at most mapMaxStale after it was built,
// then lookups are refused until a rebuild succeeds.
func (g *Guard) packageOf(ctx context.Context, id string) string {
	for {
		g.mu.Lock()
		pkg := g.artifacts[id]
		age := time.Since(g.builtAt)
		fresh := g.artifacts != nil && age <= cmpDuration(g.Refresh, 30*time.Second)
		// a known artifact is trusted only while the map is fresh: an
		// artifact that moved to another package (or was deleted and
		// created again elsewhere) must not keep the access its old
		// package gave
		if fresh || g.Fetch == nil {
			g.mu.Unlock()
			return pkg
		}
		if !g.failedAt.IsZero() && time.Since(g.failedAt) < mapRetry {
			usable := g.artifacts != nil && age <= max(mapMaxStale, g.Refresh)
			g.mu.Unlock()
			if usable {
				return pkg
			}
			return ""
		}
		if ch := g.building; ch != nil {
			g.mu.Unlock()
			select {
			case <-ch:
				continue
			case <-ctx.Done():
				return ""
			}
		}
		ch := make(chan struct{})
		g.building = ch
		g.mu.Unlock()

		m, err := func() (m map[string]string, err error) {
			// a panic must not leave g.building set (every caller would wait)
			defer func() {
				if p := recover(); p != nil {
					err = fmt.Errorf("listing the artifacts failed: %v", p)
				}
			}()
			bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), mapBuildTimeout)
			defer cancel()
			return g.buildArtifactMap(bctx)
		}()
		g.mu.Lock()
		if err == nil {
			g.artifacts, g.builtAt, g.failedAt = m, time.Now(), time.Time{}
		} else {
			g.failedAt = time.Now()
		}
		g.building = nil
		g.mu.Unlock()
		close(ch)
	}
}

const (
	// mapRetry is the pause after a failed rebuild of the artifact map.
	mapRetry = 5 * time.Second
	// mapMaxStale is how long the last good artifact map is used while
	// rebuilds fail.
	mapMaxStale = 5 * time.Minute
	// mapBuildTimeout limits one rebuild.
	mapBuildTimeout = 2 * time.Minute
	// mapParallel is how many packages a rebuild lists at once.
	mapParallel = 8
)

// pdMaxAge is how long a known ProcessDirect address is trusted without a
// rebuild of the address map.
const pdMaxAge = 5 * time.Minute

func cmpDuration(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}

// buildArtifactMap lists the artifacts of every allowed package. Any failed
// call fails the whole map: a partial map would drop artifacts.
func (g *Guard) buildArtifactMap(ctx context.Context) (map[string]string, error) {
	ctx = internal(ctx)
	// the package list takes no $select on a tenant ("$select is not supported")
	body, err := g.Fetch(ctx, "/api/v1/IntegrationPackages")
	if err != nil {
		return nil, err
	}
	var pkgs []string
	for _, p := range results(body) {
		if pkg, _ := p["Id"].(string); g.Scope.SeesPackage(pkg) {
			pkgs = append(pkgs, pkg)
		}
	}
	var (
		mu    sync.Mutex
		m     = map[string]string{}
		first error
		wg    sync.WaitGroup
		slots = make(chan struct{}, mapParallel)
	)
	for _, pkg := range pkgs {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer func() { <-slots; wg.Done() }()
			for _, t := range designtimeTypes {
				b, err := g.Fetch(ctx, "/api/v1/IntegrationPackages("+httpKey(pkg)+")/"+t+"DesigntimeArtifacts?$select=Id")
				mu.Lock()
				if err != nil {
					first = cmp.Or(first, err)
					mu.Unlock()
					return
				}
				for _, a := range results(b) {
					if id, _ := a["Id"].(string); id != "" {
						m[id] = pkg
					}
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if first != nil {
		return nil, first
	}
	return m, nil
}

func httpKey(s string) string { return "'" + url.PathEscape(strings.ReplaceAll(s, "'", "''")) + "'" }

// --- answers --------------------------------------------------------------

// results returns the entries of an OData v2 collection answer.
func results(body []byte) []map[string]any {
	var v struct {
		D struct {
			Results []map[string]any `json:"results"`
		} `json:"d"`
	}
	if json.Unmarshal(body, &v) != nil {
		return nil
	}
	return v.D.Results
}

// filterResults keeps the collection entries keep accepts; a single entity
// passes only if keep accepts it.
func filterResults(body []byte, keep func(map[string]any) bool) ([]byte, error) {
	var v map[string]any
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, &Refusal{Reason: "unreadable answer withheld"}
	}
	d, _ := v["d"].(map[string]any)
	if d == nil {
		return nil, &Refusal{Reason: "unexpected answer withheld"}
	}
	list, ok := d["results"].([]any)
	if !ok { // a single entity
		if keep(d) {
			return body, nil
		}
		return nil, &Refusal{Reason: "answer outside the scope withheld"}
	}
	kept := make([]any, 0, len(list))
	for _, e := range list {
		if m, ok := e.(map[string]any); ok && keep(m) {
			kept = append(kept, e)
		}
	}
	d["results"] = kept
	delete(d, "__count") // the total would reveal hidden entries
	return json.Marshal(v)
}

func str(m map[string]any, path ...string) string {
	var cur any = m
	for _, k := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = mm[k]
	}
	s, _ := cur.(string)
	return s
}

// collectIDs gathers the values of ID fields anywhere in an answer.
func collectIDs(v any, into map[string]bool) {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			if s, ok := x.(string); ok && (k == "Id" || k == "TraceId" || k == "RunId" || k == "StepId" || k == "MessageGuid") {
				into[s] = true
			}
			collectIDs(x, into)
		}
	case []any:
		for _, x := range t {
			collectIDs(x, into)
		}
	}
}

// --- classification -------------------------------------------------------

var (
	reKey = `'((?:[^']|'')*)'`
	// the designtime types the guard knows (designtimeTypes): another type
	// is an unknown URL
	reTypes    = `(Integration|ValueMapping|MessageMapping|ScriptCollection)`
	rePackage  = regexp.MustCompile(`^IntegrationPackages\(` + reKey + `\)(?:/` + reTypes + `DesigntimeArtifacts)?$`)
	reArtifact = regexp.MustCompile(`^` + reTypes + `DesigntimeArtifacts\(Id=` + reKey + `,Version=` + reKey + `\)(/.*)?$`)
	reRuntime  = regexp.MustCompile(`^IntegrationRuntimeArtifacts\(` + reKey + `\)(/.*)?$`)
	reMessage  = regexp.MustCompile(`^MessageProcessingLogs\(` + reKey + `\)(/.*)?$`)
	reChild    = regexp.MustCompile(`^(MessageProcessingLogAttachments|MessageStoreEntries|MessageProcessingLogRuns|MessageProcessingLogRunSteps|TraceMessages)\(` + `([^)]*)` + `\)(/.*)?$`)
	rePDParam  = regexp.MustCompile(`^(String|Binary)Parameters\(Pid=` + reKey + `,Id=` + reKey + `\)(/.*)?$`)
	rePartner  = regexp.MustCompile(`^Partners\(` + reKey + `\)(/.*)?$`)
)

// key decodes an OData key literal taken from an escaped path.
// childKey is the ID a child entity is recorded under: its key, or for the
// compound key of a run step (RunId='…',ChildCount=n) its run, which the
// run list recorded as a child of the message's flow.
func childKey(raw string) string {
	k := key(raw)
	if !strings.Contains(k, "=") {
		return strings.Trim(k, "'")
	}
	for _, part := range strings.Split(k, ",") {
		if name, v, ok := strings.Cut(part, "="); ok && strings.TrimSpace(name) == "RunId" {
			return strings.Trim(strings.TrimSpace(v), "'")
		}
	}
	return k // an unknown compound key: matches nothing recorded
}

func key(s string) string {
	if u, err := url.PathUnescape(s); err == nil {
		s = u
	}
	return strings.ReplaceAll(s, "''", "'")
}

func queryParam(q url.Values, name string) string {
	v := q.Get(name)
	return strings.ReplaceAll(strings.TrimSuffix(strings.TrimPrefix(v, "'"), "'"), "''", "'")
}

func write(method string) bool { return method != http.MethodGet && method != http.MethodHead }

// tenantAreas maps the first path segment of tenant-wide collections to their
// scope area.
var tenantAreas = map[string]string{
	"KeystoreEntries": "keystore", "HistoryKeystoreEntries": "keystore", "CertificateResources": "keystore",
	"CertificateChainResources": "keystore", "KeyPairGenerationRequests": "keystore", "SSHKeyGenerationRequests": "keystore",
	"PgpKeyEntries": "keystore", "PgpPublicKeyrings": "keystore", "PgpSecretKeyrings": "keystore", "PgpKeyringPublicResources": "keystore",
	"UserCredentials": "credentials", "OAuth2ClientCredentials": "credentials", "SecureParameters": "credentials", "OAuth2AuthorizationCodes": "credentials",
	"Queues": "jms", "MessagingQueues": "jms", "MessagingMessages": "jms", "JmsBrokers": "jms", "RetryMessagingMessages": "jms",
	"MoveMessagingMessages": "jms", "activateQueue": "jms", "deactivateQueue": "jms",
	"Variables": "variables", "NumberRanges": "numberranges", "DataStores": "datastores", "DataStoreEntries": "datastores",
	"AccessPolicies": "accesspolicies", "ArtifactReferences": "accesspolicies",
	"LogFiles": "logfiles", "LogFileArchives": "logfiles",
	"activateExternalLogging": "settings", "deactivateExternalLogging": "settings", "ExternalLoggingActivationStatus": "settings",
	"ArchivingConfigurations": "settings", "activateArchivingConfiguration": "settings", "ArchivingKeyPerformanceIndicators": "settings",
	"IntegrationConnections": "settings", "AuditLogs": "auditlog", "IdMapFromIds": "idmappings", "IdMapToIds": "idmappings",
	"IdempotentRepositoryEntries": "idempotency", "IntegrationDesigntimeLocks": "locks",
}

const (
	apiPrefix = "/api/v1/"
	opsPrefix = "/Operations/"
)

func (g *Guard) classify(r *httpclnt.GuardRequest) (decision, error) {
	p, rawQuery, _ := strings.Cut(r.Path, "?")
	api := strings.HasPrefix(p, apiPrefix) || p+"/" == apiPrefix || strings.HasPrefix(p, opsPrefix)
	if reason := unsafeRequest(p, r.Header, api); reason != "" {
		return decision{}, g.refuse(&Refusal{Reason: reason})
	}
	q, err := url.ParseQuery(rawQuery)
	if api {
		if reason := unsafeQuery(q, err); reason != "" {
			return decision{}, g.refuse(&Refusal{Reason: reason})
		}
	}
	switch {
	case strings.HasPrefix(p, apiPrefix) || p+"/" == apiPrefix:
		g.mu.Lock()
		if g.tenant == "" {
			g.tenant = r.Base
		}
		g.mu.Unlock()
		return g.classifyAPI(r, strings.TrimPrefix(p, strings.TrimSuffix(apiPrefix, "/")), q)
	case strings.HasPrefix(p, opsPrefix):
		return g.classifyOperations(r, strings.TrimPrefix(p, opsPrefix))
	case strings.HasPrefix(p, "/apiportal/"):
		return decision{}, g.refuse(&Refusal{Reason: "API Management is not available under a scope"})
	}
	return g.classifyEndpoint(r)
}

// unsafeRequest refuses what a server could read differently from the
// guard, which decides on the literal path: dot segments (also encoded, or
// with a path parameter as Tomcat reads "..;"), encoded slashes,
// backslashes and percent signs, empty segments, control characters, and
// in API paths any ";" (no ID contains one). Also method overrides, which
// make a checked POST a DELETE, and compressed bodies the guard cannot read.
func unsafeRequest(path string, header map[string]string, api bool) string {
	for k, v := range header {
		switch strings.ToLower(k) {
		case "x-http-method", "x-http-method-override", "x-method-override":
			return "method override headers are not available under a scope"
		case "content-encoding":
			if v != "" && !strings.EqualFold(v, "identity") {
				return "compressed request bodies are not available under a scope"
			}
		}
	}
	if !strings.HasPrefix(path, "/") {
		return "the request path is not accepted under a scope"
	}
	segs := strings.Split(path[1:], "/")
	for i, seg := range segs {
		dec, err := url.PathUnescape(seg)
		bad := err != nil || strings.ContainsAny(dec, "/\\%") || strings.ContainsFunc(dec, func(r rune) bool { return r < 0x20 || r == 0x7f }) ||
			(seg == "" && i < len(segs)-1) || (api && strings.Contains(dec, ";"))
		if name, _, _ := strings.Cut(dec, ";"); name == "." || name == ".." {
			bad = true
		}
		if bad {
			return "the request path is not accepted under a scope"
		}
	}
	return ""
}

// unsafeQuery refuses query strings the guard and the tenant could read
// differently: unparsable ones and repeated parameters (also differing in
// case), where the guard reads the first and the tenant may take the last.
func unsafeQuery(q url.Values, parseErr error) string {
	if parseErr != nil {
		return "the query is not accepted under a scope"
	}
	seen := map[string]bool{}
	for k, v := range q {
		lk := strings.ToLower(k)
		if len(v) > 1 || seen[lk] {
			return "repeated query parameter " + k + " is not accepted under a scope"
		}
		seen[lk] = true
	}
	return ""
}

// messageSubPaths and runtimeSubPaths are the sub-paths of a message and a
// runtime artifact a scope allows: the lists cpictl reads. Anything below
// them could navigate to content checked only as a read.
var (
	messageSubPaths = []string{"", "/ErrorInformation", "/ErrorInformation/$value", "/Attachments", "/MessageStoreEntries",
		"/Runs", "/CustomHeaderProperties", "/AdapterAttributes"}
	runtimeSubPaths = []string{"", "/ErrorInformation", "/ErrorInformation/$value"}
)

// expandAllowed lists the $expand values a request may carry, by collection:
// an expansion can return related entities the scope does not grant (the
// artifacts of a package past the artifact patterns).
var expandAllowed = map[string][]string{
	"ServiceEndpoints":                           {"EntryPoints"},
	"MessageProcessingLogCustomHeaderProperties": {"Log"},
}

func (g *Guard) classifyAPI(r *httpclnt.GuardRequest, p string, q url.Values) (decision, error) {
	p = strings.TrimPrefix(p, "/")
	first, _, _ := strings.Cut(p, "(")
	first, _, _ = strings.Cut(first, "/")
	if exp := q.Get("$expand"); exp != "" {
		_, tenantWide := tenantAreas[first]
		ok := tenantWide || slices.Contains(expandAllowed[p], exp) ||
			(strings.Contains(p, "/RunSteps") && exp == "RunStepProperties") ||
			(strings.Contains(p, "/DesignGuidelineExecutionResults(") && exp == "DesignGuidelines")
		if !ok {
			return decision{}, g.refuse(&Refusal{Reason: "$expand=" + exp + " on " + first + " is not available under a scope"})
		}
	}
	d, err := g.classifyAPIPath(r, p, q)
	if err == nil && d.filter != nil && write(r.Method) {
		// a list the guard filters is read-only: no create, update or delete
		// through it unless modelled explicitly
		return decision{}, g.refuse(&Refusal{Reason: r.Method + " on " + first + " is not available under a scope"})
	}
	return d, err
}

func (g *Guard) classifyAPIPath(r *httpclnt.GuardRequest, p string, q url.Values) (decision, error) {
	m := r.Method
	art := func(id, action string) decision {
		return decision{need: []need{{res: Resource{Kind: KindArtifact, ID: id}, action: action}}}
	}
	switch {
	case p == "" || p == "$metadata":
		return decision{allow: true}, nil

	case p == "$batch":
		ops, err := batchOperations(r.Header, r.Body)
		if err != nil {
			return decision{}, g.refuse(&Refusal{Reason: "$batch: " + err.Error()})
		}
		var needs []need
		for _, op := range ops {
			d, err := g.classify(&httpclnt.GuardRequest{Ctx: r.Ctx, Method: op.method, Base: r.Base, Path: op.path, Header: op.header, Body: op.body})
			if err != nil {
				return decision{}, err
			}
			if d.filter != nil || d.record != nil {
				return decision{}, g.refuse(&Refusal{Reason: "list and message reads in a $batch are not available under a scope"})
			}
			needs = append(needs, d.need...)
		}
		return decision{need: needs}, nil

	case p == "IntegrationPackages":
		if m == http.MethodPost {
			return decision{need: []need{{res: Resource{Kind: KindPackage, ID: bodyField(r.Body, "Id")}, action: ActionUpload}}}, nil
		}
		return decision{filter: func(g *Guard, b []byte) ([]byte, error) {
			return filterResults(b, func(e map[string]any) bool { return g.Scope.SeesPackage(str(e, "Id")) })
		}}, nil

	case rePackage.MatchString(p):
		mm := rePackage.FindStringSubmatch(p)
		pkg := key(mm[1])
		res := Resource{Kind: KindPackage, ID: pkg}
		if mm[2] != "" { // artifacts of the package
			if write(m) {
				// creating through the navigation: the artifact lands in this
				// package, whatever the body says
				if m != http.MethodPost {
					return decision{}, g.refuse(&Refusal{Reason: m + " on the artifacts of a package is not available under a scope"})
				}
				if bp := bodyField(r.Body, "PackageId"); bp != "" && bp != pkg {
					return decision{}, g.refuse(&Refusal{Reason: "the artifact's package does not match the request"})
				}
				return decision{need: []need{{res: Resource{Kind: KindArtifact, ID: bodyField(r.Body, "Id"), Package: pkg}, action: ActionUpload}}}, nil
			}
			return decision{need: []need{{res: res, action: ActionRead}}, filter: func(g *Guard, b []byte) ([]byte, error) {
				return filterResults(b, func(e map[string]any) bool {
					return g.Scope.Allows(Resource{Kind: KindArtifact, ID: str(e, "Id"), Package: pkg}, ActionRead)
				})
			}}, nil
		}
		switch m {
		case http.MethodDelete:
			return decision{need: []need{{res: res, action: ActionDelete}}}, nil
		case http.MethodGet, http.MethodHead:
			return decision{need: []need{{res: res, action: ActionRead}}}, nil
		}
		return decision{need: []need{{res: res, action: ActionUpload}}}, nil

	case slices.Contains([]string{"IntegrationDesigntimeArtifacts", "ValueMappingDesigntimeArtifacts", "MessageMappingDesigntimeArtifacts", "ScriptCollectionDesigntimeArtifacts"}, p):
		if m != http.MethodPost {
			return decision{}, g.refuse(&Refusal{Reason: "listing all artifacts of the tenant is not available under a scope"})
		}
		return decision{need: []need{{res: Resource{Kind: KindArtifact, ID: bodyField(r.Body, "Id"), Package: bodyField(r.Body, "PackageId")}, action: ActionUpload}}}, nil

	case reArtifact.MatchString(p):
		mm := reArtifact.FindStringSubmatch(p)
		id, sub := key(mm[2]), mm[4]
		if !ValidID(key(mm[3])) { // "active" or "1.0.3"
			return decision{}, g.refuse(&Refusal{Reason: "the artifact version is not accepted under a scope"})
		}
		switch {
		case m == http.MethodDelete && sub == "":
			return art(id, ActionDelete), nil
		case write(m) && strings.HasPrefix(sub, "/$links/Configurations"):
			return art(id, ActionConfigure), nil
		case write(m):
			return art(id, ActionUpload), nil
		case strings.Contains(sub, "$value"): // the artifact, or one of its resources
			return art(id, ActionDownload), nil
		}
		return art(id, ActionRead), nil

	case strings.HasPrefix(p, "Deploy") && strings.HasSuffix(p, "DesigntimeArtifact"):
		return art(queryParam(q, "Id"), ActionDeploy), nil
	case p == "ValidateIntegrationDesigntimeArtifact" || p == "ExecuteIntegrationDesigntimeArtifactsGuidelines":
		return art(queryParam(q, "Id"), ActionRead), nil
	case p == "UpsertValMaps":
		return art(queryParam(q, "Id"), ActionUpload), nil

	case p == "IntegrationRuntimeArtifacts":
		return decision{filter: func(g *Guard, b []byte) ([]byte, error) {
			return filterResults(b, func(e map[string]any) bool { return g.allowsArtifact(r.Ctx, str(e, "Id"), ActionRead) })
		}}, nil
	case reRuntime.MatchString(p):
		mm := reRuntime.FindStringSubmatch(p)
		if !slices.Contains(runtimeSubPaths, mm[2]) {
			return decision{}, g.refuse(&Refusal{Reason: m + " /api/v1/IntegrationRuntimeArtifacts(…)" + mm[2] + " is not available under a scope"})
		}
		if m == http.MethodDelete {
			return art(key(mm[1]), ActionDeploy), nil
		}
		return art(key(mm[1]), ActionRead), nil
	case strings.HasPrefix(p, "BuildAndDeployStatus("):
		return decision{allow: true}, nil

	case p == "ServiceEndpoints":
		return decision{filter: func(g *Guard, b []byte) ([]byte, error) {
			return filterResults(b, func(e map[string]any) bool { return g.allowsArtifact(r.Ctx, str(e, "Name"), ActionRead) })
		}}, nil

	case p == "MessageProcessingLogs":
		return decision{filter: func(g *Guard, b []byte) ([]byte, error) {
			hidden := g.prefetchTooling(r.Ctx, results(b))
			return filterResults(b, func(e map[string]any) bool { return !hidden[str(e, "MessageGuid")] && g.allowsLog(r.Ctx, e) })
		}, record: (*Guard).recordMessages}, nil
	case reMessage.MatchString(p):
		mm := reMessage.FindStringSubmatch(p)
		// the tenant navigates into children (Attachments('…')/$value is the
		// attachment's content): only the lists cpictl reads, whose
		// contents are then read through their own IDs (download)
		if !slices.Contains(messageSubPaths, mm[2]) || write(m) {
			return decision{}, g.refuse(&Refusal{Reason: m + " /api/v1/MessageProcessingLogs(…)" + mm[2] + " is not available under a scope"})
		}
		guid := key(mm[1])
		// the message and its lists; contents are read through their own IDs (download)
		return decision{need: []need{{res: Resource{Kind: "message", ID: guid}, action: ActionRead}}, record: (*Guard).recordChildren}, nil
	case reChild.MatchString(p):
		mm := reChild.FindStringSubmatch(p)
		action := ActionRead
		if strings.Contains(mm[3], "$value") || strings.Contains(mm[3], "TraceMessages") || mm[1] == "TraceMessages" {
			action = ActionDownload
		}
		d := decision{need: []need{{res: Resource{Kind: "child", ID: childKey(mm[2])}, action: action}}}
		// IDs are recorded from SAP's lists only: a $value answer is the
		// payload the flow wrote, and IDs in it must not become children of
		// the flow (#52); the payload is then streamed, not buffered
		if !strings.Contains(mm[3], "$value") {
			d.record = (*Guard).recordChildren
		}
		return d, nil
	case p == "MessageProcessingLogCustomHeaderProperties":
		return decision{filter: func(g *Guard, b []byte) ([]byte, error) {
			var logs []map[string]any
			for _, e := range results(b) {
				if l, _ := e["Log"].(map[string]any); l != nil {
					logs = append(logs, l)
				}
			}
			hidden := g.prefetchTooling(r.Ctx, logs)
			return filterResults(b, func(e map[string]any) bool {
				l, _ := e["Log"].(map[string]any)
				return l != nil && !hidden[str(l, "MessageGuid")] && g.allowsLog(r.Ctx, l)
			})
		}}, nil

	case p == "StringParameters" || p == "BinaryParameters" || p == "Partners" || p == "AlternativePartners" || p == "AuthorizedUsers":
		if write(m) {
			param := p == "StringParameters" || p == "BinaryParameters"
			return decision{need: []need{{res: Resource{Kind: KindPartner, ID: bodyField(r.Body, "Pid")}, action: ActionPDWrite, toolingWrite: param && m == http.MethodPost,
				param: bodyField(r.Body, "Id")}}}, nil
		}
		return decision{filter: func(g *Guard, b []byte) ([]byte, error) {
			return filterResults(b, func(e map[string]any) bool {
				if pid := str(e, "Pid"); toolingPartner(pid) && p != "Partners" {
					return g.Scope.Grants(ActionTest) && g.toolingParamOwned(r.Ctx, str(e, "Id"))
				}
				return g.allowsPartner(str(e, "Pid"), ActionRead)
			})
		}}, nil
	case rePDParam.MatchString(p):
		mm := rePDParam.FindStringSubmatch(p)
		action := ActionRead
		if write(m) {
			action = ActionPDWrite
		}
		return decision{need: []need{{res: Resource{Kind: KindPartner, ID: key(mm[2])}, action: action, toolingWrite: true, param: key(mm[3])}}}, nil
	case rePartner.MatchString(p):
		mm := rePartner.FindStringSubmatch(p)
		action := ActionRead
		if write(m) {
			action = ActionPDWrite
		}
		// the partner itself; below it (its parameters) only for partners
		// in the scope: the tooling's are read one parameter at a time
		return decision{need: []need{{res: Resource{Kind: KindPartner, ID: key(mm[1])}, action: action, entity: mm[2] == ""}}}, nil
	case strings.HasPrefix(p, "AlternativePartners(") || strings.HasPrefix(p, "AuthorizedUsers("):
		// keys without the partner ID: changes need the entry's partner
		if !write(m) {
			return decision{filter: func(g *Guard, b []byte) ([]byte, error) {
				return filterResults(b, func(e map[string]any) bool { return g.allowsPartner(str(e, "Pid"), ActionRead) })
			}}, nil
		}
		pid := g.lookupField(r.Ctx, apiPrefix+p, "Pid")
		return decision{need: []need{{res: Resource{Kind: KindPartner, ID: pid}, action: ActionPDWrite}}}, nil
	}

	first, _, _ := strings.Cut(p, "(")
	first, _, _ = strings.Cut(first, "/")
	if area, ok := tenantAreas[first]; ok {
		action := ActionRead
		if write(r.Method) {
			action = "write"
		}
		return decision{need: []need{{res: Resource{Kind: KindTenant, ID: area}, action: action}}}, nil
	}
	return decision{}, g.refuse(&Refusal{Reason: fmt.Sprintf("%s %s is not available under a scope", r.Method, "/api/v1/"+p)})
}

func (g *Guard) classifyOperations(r *httpclnt.GuardRequest, cmd string) (decision, error) {
	name, _, _ := strings.Cut(cmd, "?")
	switch name {
	case "com.sap.it.op.srv.web.cf.RuntimeLocationListCommand":
		return decision{allow: true}, nil
	case "com.sap.it.op.tmn.commands.dashboard.webui.IntegrationComponentsListCommand":
		return decision{filter: func(g *Guard, b []byte) ([]byte, error) { return g.filterComponents(r.Ctx, b), nil }}, nil
	case "com.sap.it.op.tmn.commands.dashboard.webui.IntegrationComponentSetMplLogLevelCommand":
		return decision{need: []need{{res: Resource{Kind: KindArtifact, ID: bodyField(r.Body, "artifactSymbolicName")}, action: ActionLogLevel}}}, nil
	}
	return decision{}, g.refuse(&Refusal{Reason: "/Operations/" + name + " is not available under a scope"})
}

// classifyEndpoint decides a request to a flow's runtime endpoint: allowed
// with "test" on the flow whose entry point covers it most specifically,
// among the entry points of every flow of the tenant. A call through the test harness must name a ProcessDirect address
// of an allowed flow.
func (g *Guard) classifyEndpoint(r *httpclnt.GuardRequest) (decision, error) {
	if !g.Scope.Grants(ActionTest) {
		return decision{}, g.refuse(&Refusal{Reason: "sending messages needs the test action"})
	}
	// The tenant continues a correlation ID the caller sends. The test
	// tooling's messages are decided by their correlation group (#48), so a
	// caller who chose one could join another team's test run and read it.
	for k := range r.Header {
		if strings.EqualFold(k, "SAP_MplCorrelationId") || strings.EqualFold(k, "SAP_MessageProcessingLogID") {
			return decision{}, g.refuse(&Refusal{Reason: "a test message may not set " + k + " under a scope (it would join another message's correlation group)"})
		}
	}
	written, _, _ := strings.Cut(withoutDefaultPort(r.Base)+r.Path, "?")
	target, ok := parseEndpoint(written, false)
	if !ok {
		return decision{}, g.refuse(&Refusal{Reason: "the endpoint " + written + " " + notAnEntryPoint})
	}
	flow, why := g.endpointOwner(r.Ctx, target)
	if flow == "" {
		return decision{}, g.refuse(&Refusal{Reason: "the endpoint " + written + " " + why})
	}
	if flow == testtools.HarnessID {
		addr := ""
		for k, v := range r.Header {
			if strings.EqualFold(k, testtools.HarnessAddressHeader) {
				addr = v
			}
		}
		to := g.processDirectFlow(r.Ctx, addr)
		if to == "" {
			return decision{}, g.refuse(&Refusal{Reason: fmt.Sprintf("the test harness may only call ProcessDirect addresses of flows in the scope (%q is not one)", addr)})
		}
		if why := g.runsAsRead(r.Ctx, to); why != "" {
			return decision{}, g.refuse(&Refusal{Reason: fmt.Sprintf("the test harness may only call %q while flow %s runs the content the address was read from: %s", addr, to, why)})
		}
		return decision{need: []need{{res: Resource{Kind: KindArtifact, ID: to}, action: ActionTest}}}, nil
	}
	// another team's endpoint is refused like one that does not exist: the
	// refusal must not tell which addresses exist and whose flows they reach
	// (cpicli#54). With grants, read on a package does not mean test on it.
	// A flow the scope may read is named (the caller sees it anyway).
	if !g.mayTest(r.Ctx, flow) && !g.mayRead(r.Ctx, flow) {
		return decision{}, g.refuse(&Refusal{Reason: "the endpoint " + written + " " + notAnEntryPoint})
	}
	return decision{need: []need{{res: Resource{Kind: KindArtifact, ID: flow}, action: ActionTest}}}, nil
}

const notAnEntryPoint = "is not an entry point of a flow in the scope"

// mayTest and mayRead report whether the scope grants test or read on a
// flow, as check decides it, without a refusal.
func (g *Guard) mayTest(ctx context.Context, id string) bool { return g.mayOnFlow(ctx, id, ActionTest) }
func (g *Guard) mayRead(ctx context.Context, id string) bool { return g.mayOnFlow(ctx, id, ActionRead) }

func (g *Guard) mayOnFlow(ctx context.Context, id, action string) bool {
	switch {
	case !ValidID(id):
		return false
	case toolingFlow(id):
		return g.Scope.Grants(ActionTest)
	}
	return g.Scope.Allows(Resource{Kind: KindArtifact, ID: id, Package: g.packageOf(ctx, id)}, action)
}

// endpointOwner returns the flow a runtime URL reaches: the flow of the most
// specific entry point that covers it, among the entry points of every flow
// of the tenant (an exact entry point before a longer prefix before a
// shorter one). The caller's need on that flow decides. Two flows equally
// specific (also two flows listing the same URL), an entry point that could
// not be read, or none: "" with the reason.
func (g *Guard) endpointOwner(ctx context.Context, target endpointPath) (string, string) {
	// the reason stays generic: the error would show the caller the
	// tenant's API path and answer
	if err := g.refreshEndpoints(ctx); err != nil {
		return "", "cannot be checked: the tenant's entry points could not be read"
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	// only the tenant-wide list decides: entry points seen in earlier
	// answers may belong to a flow that has moved since (cpicli#55)
	best, owners, variant := -1, map[string]bool{}, -1
	for _, e := range g.allEndpoints {
		n, differs := e.path.covers(target)
		switch {
		case n < 0:
		case differs:
			// a segment of the same name with parameters the runtime may
			// read as the entry point's (";V=2", ";v=02"): this entry point
			// makes what it is as specific as ambiguous
			variant = max(variant, n)
		case n > best:
			best, owners = n, map[string]bool{e.flow: true}
		case n == best:
			owners[e.flow] = true
		}
	}
	if variant >= best {
		return "", notAnEntryPoint
	}
	// two flows equally specific, or an entry point the guard could not
	// read (flow ""): refused, with the text of an unknown endpoint (the
	// caller learns nothing about the other flow)
	if best < 0 || len(owners) > 1 || owners[""] {
		return "", notAnEntryPoint
	}
	for f := range owners {
		return f, ""
	}
	return "", ""
}

// refreshEndpoints reads the entry points of every flow of the tenant (the
// guard's own call, cached like the artifact map).
// An error leaves no decision: without every flow's entry points, an
// allowed one could cover another team's.
// One refresh runs at a time; the others wait for its result.
func (g *Guard) refreshEndpoints(ctx context.Context) error {
	for {
		g.mu.Lock()
		fresh := !g.epBuiltAt.IsZero() && time.Since(g.epBuiltAt) < cmpDuration(g.Refresh, 30*time.Second)
		ch, lastErr := g.epBuilding, g.epErr
		switch {
		case fresh:
			g.mu.Unlock()
			return nil
		case ch == nil && lastErr != nil && time.Since(g.epFailedAt) < mapRetry:
			// a failed read is not repeated for every message in an outage
			g.mu.Unlock()
			return lastErr
		case g.Fetch == nil:
			g.mu.Unlock()
			return errors.New("the guard has no Fetch")
		case ch != nil:
			g.mu.Unlock()
			select {
			case <-ch:
			case <-ctx.Done():
				return ctx.Err()
			}
			g.mu.Lock()
			fresh, lastErr = !g.epBuiltAt.IsZero() && time.Since(g.epBuiltAt) < cmpDuration(g.Refresh, 30*time.Second), g.epErr
			g.mu.Unlock()
			if fresh {
				return nil
			}
			if lastErr != nil {
				return lastErr
			}
			continue
		}
		ch = make(chan struct{})
		g.epBuilding = ch
		g.mu.Unlock()

		err := func() (err error) {
			defer func() {
				if p := recover(); p != nil {
					err = fmt.Errorf("reading the entry points failed: %v", p)
				}
			}()
			bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), mapBuildTimeout)
			defer cancel()
			body, err := g.Fetch(internal(bctx), "/api/v1/ServiceEndpoints?$expand=EntryPoints")
			if err != nil {
				return err
			}
			eps := mergeEndpoints(body)
			g.mu.Lock()
			g.allEndpoints, g.epBuiltAt = eps, time.Now()
			g.mu.Unlock()
			return nil
		}()
		g.mu.Lock()
		g.epBuilding, g.epErr = nil, err
		g.epFailedAt = time.Time{}
		if err != nil {
			g.epFailedAt = time.Now()
		}
		g.mu.Unlock()
		close(ch)
		return err
	}
}

// endpointEntry is one entry point of a flow; flow "" marks an entry point
// the guard could not read, which makes every URL it might cover ambiguous.
type endpointEntry struct {
	path endpointPath
	flow string
}

// mergeEndpoints lists the entry points of an endpoint list.
func mergeEndpoints(body []byte) []endpointEntry {
	var out []endpointEntry
	for _, e := range results(body) {
		eps, _ := e["EntryPoints"].(map[string]any)
		list, _ := eps["results"].([]any)
		for _, x := range list {
			if ep, ok := x.(map[string]any); ok {
				if u := str(ep, "Url"); u != "" {
					p, ok := parseEndpoint(withoutDefaultPort(u), true)
					if !ok {
						out = append(out, endpointEntry{path: rawEndpoint(withoutDefaultPort(u))})
						continue
					}
					out = append(out, endpointEntry{path: p, flow: str(e, "Name")})
				}
			}
		}
	}
	return out
}

// endpointPath is a runtime URL or an entry point the way the runtime routes
// it: per path segment the decoded name and its decoded parameters (";…").
// Matching URLs as written would let "/http/shared/bill%69ng" pass as below
// "/http/shared" while the tenant delivers it to "/http/shared/billing"
// (cpicli#55).
type endpointPath struct {
	origin string // scheme://host
	segs   []endpointSeg
	// wild: an entry point ending in "*"; the last segment's name is a prefix
	wild bool
}

type endpointSeg struct{ name, params string }

// parseEndpoint reads a runtime URL (entry false) or an entry point. A
// segment that cannot be decoded, or decodes to a separator, gives false.
func parseEndpoint(u string, entry bool) (endpointPath, bool) {
	var p endpointPath
	rest := u
	if i := strings.Index(u, "://"); i >= 0 {
		host, path, _ := strings.Cut(u[i+3:], "/")
		// host names are not case-sensitive
		p.origin, rest = strings.ToLower(u[:i+3]+host), "/"+path
		if path == "" && !strings.HasSuffix(u, "/") {
			rest = ""
		}
	}
	if entry {
		// an entry point the runtime would not list (no scheme, a query):
		// unreadable, so ambiguous (rawEndpoint)
		if p.origin == "" || strings.ContainsAny(rest, "?#") {
			return p, false
		}
		rest = strings.TrimSuffix(rest, "/")
		if r, ok := strings.CutSuffix(rest, "*"); ok {
			p.wild, rest = true, r
		}
	}
	if rest == "" {
		if p.wild {
			p.segs = []endpointSeg{{}}
		}
		return p, true
	}
	if !strings.HasPrefix(rest, "/") {
		return p, false
	}
	raws := strings.Split(rest[1:], "/")
	for i, raw := range raws {
		name, params, _ := strings.Cut(raw, ";")
		// in an entry point, a dot segment or an empty one (other than the
		// prefix of a closing "*") is unreadable too
		if entry && (name == "." || name == ".." || (raw == "" && !(p.wild && i == len(raws)-1))) {
			return p, false
		}
		n, err1 := url.PathUnescape(name)
		ps, err2 := url.PathUnescape(params)
		if err1 != nil || err2 != nil || strings.ContainsAny(n, "/\\;*") || strings.ContainsAny(ps, "/\\*") ||
			strings.Count(ps, ";") != strings.Count(params, ";") {
			return p, false
		}
		p.segs = append(p.segs, endpointSeg{n, ps})
	}
	if p.wild && p.segs[len(p.segs)-1].params != "" {
		return p, false // "…;v=1*": what it covers is unclear
	}
	return p, true
}

// rawEndpoint is an entry point the guard cannot read, as a runtime might
// read it: the whole path decoded, dot segments resolved, the query dropped.
// If even that fails: its readable segments, then any segment. It owns
// nothing (flow ""), so wherever it covers a URL as specifically as an
// owner, the URL is refused.
func rawEndpoint(u string) endpointPath {
	u, _, _ = strings.Cut(u, "?")
	u, _, _ = strings.Cut(u, "#")
	origin, rest := u, ""
	if i := strings.Index(u, "://"); i >= 0 {
		host, path, _ := strings.Cut(u[i+3:], "/")
		origin, rest = strings.ToLower(u[:i+3]+host), "/"+path
	}
	if dec, err := url.PathUnescape(rest); err == nil && strings.HasPrefix(dec, "/") {
		p := endpointPath{origin: origin}
		p.wild = strings.HasSuffix(dec, "*")
		clean := path.Clean(strings.TrimSuffix(dec, "*"))
		if p.wild && strings.HasSuffix(strings.TrimSuffix(dec, "*"), "/") && clean != "/" {
			clean += "/"
		}
		for _, raw := range strings.Split(strings.TrimPrefix(clean, "/"), "/") {
			name, params, _ := strings.Cut(raw, ";")
			p.segs = append(p.segs, endpointSeg{name, params})
		}
		return p
	}
	p, _ := parseEndpoint(u, true) // the readable segments
	p.segs = append(p.segs, endpointSeg{})
	for i := range p.segs {
		p.segs[i].params = ""
	}
	p.wild = true
	return p
}

// covers returns how specifically the entry point e covers the URL t, or -1:
// an exact entry point the highest, then by the length of what it fixes. A
// segment matches by its name, and by its parameters when the entry point's
// segment has some: ";v=1" and ";v=2" are different OData services
// (cpicli#56), while "billing;x" is "billing" to a servlet container.
//
// differs reports a covering by name only: some segment of the entry point
// has parameters the URL's segment does not have exactly, but may be read as
// having (paramsMayMatch). Such an entry point does not own the URL, but
// nothing as specific or less may decide it either (cpicli#57, #58).
func (e endpointPath) covers(t endpointPath) (n int, differs bool) {
	if e.origin != t.origin || len(t.segs) < len(e.segs) {
		return -1, false
	}
	for i, es := range e.segs {
		ts := t.segs[i]
		last := i == len(e.segs)-1
		switch {
		case last && e.wild:
			if !strings.HasPrefix(ts.name, es.name) {
				return -1, false
			}
		case es.name != ts.name:
			return -1, false
		case es.params != "" && es.params != ts.params:
			if !paramsMayMatch(es.params, ts.params) {
				return -1, false
			}
			differs = true
		}
		n += 1 + len(es.name)
		if es.params != "" {
			n += 1 + len(es.params)
		}
	}
	switch {
	case e.wild:
		return n, differs
	case len(t.segs) == len(e.segs):
		return 1 << 20, differs
	}
	return n + 1, differs
}

// paramsMayMatch reports whether a runtime may read the URL segment's
// parameters as the entry point's: with case, spaces and leading zeros of
// numbers ignored, and extra parameters dropped. ";v=1" never matches
// ";v=2"; ";V=02;x=1" may match ";v=2".
func paramsMayMatch(entry, url string) bool {
	norm := func(s string) map[string]bool {
		m := map[string]bool{}
		for _, p := range strings.Split(s, ";") {
			k, v, _ := strings.Cut(strings.ToLower(strings.Join(strings.Fields(p), "")), "=")
			if k == "" {
				continue
			}
			if t := strings.TrimLeft(v, "0"); t != v && strings.Trim(t, "0123456789") == "" {
				v = cmp.Or(t, "0")
			}
			m[k+"="+v] = true
		}
		return m
	}
	have := norm(url)
	for k := range norm(entry) {
		if !have[k] {
			return false
		}
	}
	return true
}

// processDirectFlow returns the allowed flow whose ProcessDirect sender
// listens on addr, or "".
func (g *Guard) processDirectFlow(ctx context.Context, addr string) string {
	if addr == "" || g.ProcessDirectAddresses == nil {
		return ""
	}
	g.mu.Lock()
	flow := g.pdFlows[addr]
	age := time.Since(g.pdBuiltAt)
	fresh := g.pdFlows != nil && age < cmpDuration(g.Refresh, 30*time.Second)
	g.mu.Unlock()
	// a known address is trusted for pdMaxAge (a rebuild downloads every
	// allowed flow); then the map is rebuilt, so an address that moved to
	// another team's flow is decided anew
	if fresh || (flow != "" && age < max(pdMaxAge, g.Refresh)) {
		return flow
	}
	// (re)build from the allowed flows, with the guard's own lookups
	ictx := internal(ctx)
	g.packageOf(ictx, "") // make sure the artifact map exists
	g.mu.Lock()
	ids := make([]string, 0, len(g.artifacts))
	for id := range g.artifacts {
		ids = append(ids, id)
	}
	g.mu.Unlock()
	m := map[string]string{}
	for _, id := range ids {
		addrs, err := g.ProcessDirectAddresses(ictx, id)
		if err != nil {
			continue
		}
		for _, a := range addrs {
			m[a] = id
		}
	}
	g.mu.Lock()
	g.pdFlows, g.pdBuiltAt = m, time.Now()
	g.mu.Unlock()
	return m[addr]
}

// runsAsRead returns "" when flow runs the content its ProcessDirect
// addresses were read from (the active designtime version): started, the
// same version, and not changed after its deployment. Otherwise, or when
// the tenant's answers leave it open, the reason. The address map comes
// from designtime content; a flow that is not deployed, or runs an older
// version, may not listen on the address at all, and another team's flow
// might.
func (g *Guard) runsAsRead(ctx context.Context, flow string) string {
	if g.Fetch == nil {
		return "the guard has no Fetch to read it"
	}
	ictx := internal(ctx)
	entity := func(path string) map[string]any {
		b, err := g.Fetch(ictx, path)
		if err != nil {
			return nil
		}
		var v struct {
			D map[string]any `json:"d"`
		}
		if json.Unmarshal(b, &v) != nil {
			return nil
		}
		return v.D
	}
	rt := entity("/api/v1/IntegrationRuntimeArtifacts(" + httpKey(flow) + ")")
	if rt == nil || str(rt, "Id") != flow {
		return "it is not deployed"
	}
	if st := str(rt, "Status"); st != "STARTED" {
		return "it is not running (status " + cmp.Or(st, "unknown") + ")"
	}
	dt := entity("/api/v1/IntegrationDesigntimeArtifacts(Id=" + httpKey(flow) + ",Version='active')")
	if dt == nil {
		return "its designtime artifact could not be read"
	}
	if rv, dv := str(rt, "Version"), str(dt, "Version"); rv == "" || rv != dv {
		return fmt.Sprintf("it runs version %q, the designtime artifact is version %q; deploy it first", rv, dv)
	}
	deployed, err1 := odatatime.Parse(str(rt, "DeployedOn"))
	modified, err2 := odatatime.Parse(str(dt, "ModifiedAt"))
	switch {
	case err1 != nil || err2 != nil || deployed.IsZero() || modified.IsZero():
		return "the tenant does not report when it was deployed and last changed"
	case modified.After(deployed):
		return "it was changed after its deployment; deploy it first"
	}
	return ""
}

// withoutDefaultPort drops :443 from https and :80 from http URLs: the
// executer writes the port, the tenant's entry points do not.
func withoutDefaultPort(u string) string {
	for _, d := range []struct{ scheme, port string }{{"https://", ":443"}, {"http://", ":80"}} {
		if strings.HasPrefix(u, d.scheme) {
			host, rest, _ := strings.Cut(strings.TrimPrefix(u, d.scheme), "/")
			host = strings.TrimSuffix(host, d.port)
			if rest != "" || strings.HasSuffix(u, "/") {
				return d.scheme + host + "/" + rest
			}
			return d.scheme + host
		}
	}
	return u
}

// --- helpers for decisions -------------------------------------------------

// bodyField reads a top-level string field of a JSON body. A body that
// repeats a top-level key (also differing in case) gives "": which one the
// tenant takes is its choice, and every decision that reads a body needs a
// valid ID from it, so an empty one is refused.
func bodyField(body []byte, field string) string {
	dec := json.NewDecoder(bytes.NewReader(body))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return ""
	}
	seen := map[string]bool{}
	value := ""
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return ""
		}
		k, _ := t.(string)
		if seen[strings.ToLower(k)] {
			return ""
		}
		seen[strings.ToLower(k)] = true
		var v any
		if err := dec.Decode(&v); err != nil {
			return ""
		}
		if k == field {
			value, _ = v.(string)
		}
	}
	if t, err := dec.Token(); err != nil || t != json.Delim('}') {
		return ""
	}
	if _, err := dec.Token(); err != io.EOF {
		return "" // trailing data
	}
	return value
}

func (g *Guard) lookupField(ctx context.Context, path, field string) string {
	if g.Fetch == nil {
		return ""
	}
	b, err := g.Fetch(internal(ctx), path)
	if err != nil {
		return ""
	}
	var v struct {
		D map[string]any `json:"d"`
	}
	if json.Unmarshal(b, &v) != nil {
		return ""
	}
	return str(v.D, field)
}

func (g *Guard) allowsArtifact(ctx context.Context, id, action string) bool {
	return g.check(ctx, need{res: Resource{Kind: KindArtifact, ID: id}, action: action}) == nil
}

func (g *Guard) allowsPartner(pid, action string) bool {
	if toolingPartner(pid) && action == ActionRead && g.Scope.Grants(ActionTest) {
		return true
	}
	return ValidID(pid) && g.Scope.Allows(Resource{Kind: KindPartner, ID: pid}, action)
}

// allowsLog decides a message processing log entry by its flow and package;
// a message of the test tooling by the flow it tested.
func (g *Guard) allowsLog(ctx context.Context, e map[string]any) bool {
	id, pkg := str(e, "IntegrationArtifact", "Id"), str(e, "IntegrationArtifact", "PackageId")
	if id == "" {
		return false
	}
	if toolingFlow(id) {
		return g.Scope.Grants(ActionTest) && g.toolingTarget(ctx, str(e, "MessageGuid"), str(e, "CorrelationId")) != ""
	}
	return g.Scope.Allows(Resource{Kind: KindArtifact, ID: id, Package: pkg}, ActionRead)
}

// reCorrelation is the form of a correlation ID (and a message GUID).
var reCorrelation = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// toolingTarget returns the flow a message of the test tooling tested, if
// the scope grants "test" on it, else "". The tooling's messages share
// their correlation ID with the flow they reach: harness -> flow under test
// -> receiver stub (verified on a tenant). Teams on one tenant share the
// tooling; without this, "test" anywhere read every team's test payloads
// (#48). A tooling message with no other flow in its group (an XSLT or
// script test run) is refused: nothing says whose it was.
func (g *Guard) toolingTarget(ctx context.Context, guid, correlation string) string {
	if g.Fetch == nil {
		return ""
	}
	if correlation == "" {
		correlation = g.correlationOf(ctx, guid)
	}
	if !reCorrelation.MatchString(correlation) {
		return ""
	}
	target := g.groupTarget(ctx, correlation)
	if target != "" {
		g.markTooling(guid, target)
	}
	return target
}

// noTargetTTL is how long "no allowed flow in this correlation group" is
// kept: the tested flow's log may be written a moment after the tooling's
// (a waiting test run sees it then), but a list of other teams' runs must
// not cost two SAP calls per row on every request (#51).
const noTargetTTL = 10 * time.Second

// maxToolingLookups caps the message lookups and the group lookups one
// answer may cause (each); rows beyond it are hidden in that answer (fail
// closed). Filter the list by flow to see them.
const maxToolingLookups = 100

// correlationOf returns a message's correlation ID (the list may have left
// the field out with $select), cached.
func (g *Guard) correlationOf(ctx context.Context, guid string) string {
	if !reCorrelation.MatchString(guid) {
		return ""
	}
	g.mu.Lock()
	c, ok := g.correlations[guid]
	g.mu.Unlock()
	if ok {
		return c
	}
	b, err := g.Fetch(internal(ctx), "/api/v1/MessageProcessingLogs("+httpKey(guid)+")?$select=CorrelationId")
	if err != nil {
		return ""
	}
	var v struct {
		D map[string]any `json:"d"`
	}
	if json.Unmarshal(b, &v) != nil {
		return ""
	}
	c = str(v.D, "CorrelationId")
	g.mu.Lock()
	if g.correlations == nil {
		g.correlations = map[string]string{}
	}
	g.correlations[guid] = c
	g.mu.Unlock()
	return c
}

// groupTarget returns the allowed flow outside the tooling in a correlation
// group, or "", cached both ways ("" for noTargetTTL).
func (g *Guard) groupTarget(ctx context.Context, correlation string) string {
	g.mu.Lock()
	target := g.targets[correlation]
	until, refused := g.noTarget[correlation]
	g.mu.Unlock()
	if target != "" {
		return target
	}
	if refused && time.Now().Before(until) {
		return ""
	}
	b, err := g.Fetch(internal(ctx), "/api/v1/MessageProcessingLogs?$filter="+url.QueryEscape("CorrelationId eq '"+correlation+"'")+
		"&$select=MessageGuid,IntegrationArtifact&$top=50")
	if err != nil {
		return ""
	}
	for _, e := range results(b) {
		id := str(e, "IntegrationArtifact", "Id")
		if id == "" || toolingFlow(id) || !ValidID(id) {
			continue
		}
		// decided without a refusal event: the other flows of the group
		// are not what was asked for
		pkg := str(e, "IntegrationArtifact", "PackageId")
		if pkg == "" {
			pkg = g.packageOf(ctx, id)
		}
		if g.Scope.Allows(Resource{Kind: KindArtifact, ID: id, Package: pkg}, ActionTest) {
			target = id
			break
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if target != "" {
		if g.targets == nil {
			g.targets = map[string]string{}
		}
		g.targets[correlation] = target
		delete(g.noTarget, correlation)
	} else {
		if g.noTarget == nil {
			g.noTarget = map[string]time.Time{}
		}
		g.noTarget[correlation] = time.Now().Add(noTargetTTL)
	}
	return target
}

// prefetchTooling resolves the tooling rows of one answer before it is
// filtered: each correlation ID once, 8 lookups at a time, at most
// maxToolingLookups. It returns the message GUIDs beyond that, which this
// answer hides (a narrower list shows them).
func (g *Guard) prefetchTooling(ctx context.Context, rows []map[string]any) map[string]bool {
	hidden := map[string]bool{}
	if g.Fetch == nil || !g.Scope.Grants(ActionTest) {
		return hidden
	}
	var guids []string
	seenGUID := map[string]bool{}
	for _, e := range rows {
		if !toolingFlow(str(e, "IntegrationArtifact", "Id")) || str(e, "CorrelationId") != "" {
			continue
		}
		guid := str(e, "MessageGuid")
		g.mu.Lock()
		_, known := g.correlations[guid]
		g.mu.Unlock()
		if !known && reCorrelation.MatchString(guid) && !seenGUID[guid] {
			seenGUID[guid] = true
			guids = append(guids, guid)
		}
	}
	budget := maxToolingLookups
	if len(guids) > budget {
		for _, guid := range guids[budget:] {
			hidden[guid] = true
		}
		guids = guids[:budget]
	}
	parallel(guids, func(guid string) { g.correlationOf(ctx, guid) })
	budget = maxToolingLookups // groups: their own budget

	var groups []string
	seen := map[string]bool{}
	byGroup := map[string][]string{}
	for _, e := range rows {
		if !toolingFlow(str(e, "IntegrationArtifact", "Id")) || hidden[str(e, "MessageGuid")] {
			continue
		}
		c := str(e, "CorrelationId")
		if c == "" {
			g.mu.Lock()
			c = g.correlations[str(e, "MessageGuid")]
			g.mu.Unlock()
		}
		if !reCorrelation.MatchString(c) {
			continue
		}
		byGroup[c] = append(byGroup[c], str(e, "MessageGuid"))
		if seen[c] {
			continue
		}
		seen[c] = true
		g.mu.Lock()
		_, found := g.targets[c]
		until, refused := g.noTarget[c]
		g.mu.Unlock()
		if !found && !(refused && time.Now().Before(until)) {
			groups = append(groups, c)
		}
	}
	if len(groups) > budget {
		for _, c := range groups[max(budget, 0):] {
			for _, guid := range byGroup[c] {
				hidden[guid] = true
			}
		}
		groups = groups[:max(budget, 0)]
	}
	parallel(groups, func(c string) { g.groupTarget(ctx, c) })
	return hidden
}

// parallel runs fn for each item, 8 at a time.
func parallel(items []string, fn func(string)) {
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for _, it := range items {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			fn(it)
		}()
	}
	wg.Wait()
}

// toolingParamOwned reports whether a parameter of the tooling's partners
// (a stub rule, a stylesheet under test) belongs to the scope: under a
// scope its ID starts with "<owner>." where the owner is a flow the scope
// grants "test" on (stub rules) or a partner it may read (stylesheets,
// testrun.OnlineXSLTID). Teams share the tooling's partners; without this
// one team read and overwrote another's stub answers (#48).
func (g *Guard) toolingParamOwned(ctx context.Context, id string) bool {
	for i := 1; i < len(id); i++ {
		if id[i] != '.' {
			continue
		}
		owner := id[:i]
		if !ValidID(owner) {
			continue
		}
		if g.Scope.Allows(Resource{Kind: KindPartner, ID: owner}, ActionRead) || g.allowsFlowQuietly(ctx, owner, ActionTest) {
			return true
		}
	}
	return false
}

// allowsFlowQuietly decides a flow without a refusal event: for lookups
// that are not what the caller asked for.
func (g *Guard) allowsFlowQuietly(ctx context.Context, id, action string) bool {
	if !ValidID(id) || toolingFlow(id) {
		return false
	}
	return g.Scope.Allows(Resource{Kind: KindArtifact, ID: id, Package: g.packageOf(ctx, id)}, action)
}

// markTooling remembers a tooling message as belonging to the flow it tested.
func (g *Guard) markTooling(guid, target string) {
	if guid == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.messages == nil {
		g.messages = map[string]string{}
	}
	if g.tooling == nil {
		g.tooling = map[string]bool{}
	}
	g.messages[guid], g.tooling[guid] = target, true
}

func (g *Guard) recordMessages(_ *httpclnt.GuardRequest, body []byte) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.messages == nil {
		g.messages = map[string]string{}
	}
	for _, e := range results(body) {
		if guid := str(e, "MessageGuid"); guid != "" && !g.tooling[guid] {
			// a tooling message passed the filter as the flow it tested
			// (markTooling); keep that
			g.messages[guid] = str(e, "IntegrationArtifact", "Id")
		}
	}
}

// recordChildren remembers the IDs in an answer about a message (or one of
// its children) as belonging to that message's flow.
func (g *Guard) recordChildren(r *httpclnt.GuardRequest, body []byte) {
	var v any
	if json.Unmarshal(body, &v) != nil {
		return
	}
	p, _, _ := strings.Cut(r.Path, "?")
	p = strings.TrimPrefix(p, apiPrefix)
	g.mu.Lock()
	defer g.mu.Unlock()
	flow, of := "", ""
	if mm := reMessage.FindStringSubmatch(p); mm != nil {
		of = key(mm[1])
		flow = g.messages[of]
	} else if mm := reChild.FindStringSubmatch(p); mm != nil {
		of = childKey(mm[2])
		flow = g.children[of]
	}
	tooling := g.tooling[of]
	ids := map[string]bool{}
	collectIDs(v, ids)
	if g.children == nil {
		g.children = map[string]string{}
	}
	if g.tooling == nil {
		g.tooling = map[string]bool{}
	}
	for id := range ids {
		if _, ok := g.children[id]; !ok {
			g.children[id] = flow
			if tooling {
				g.tooling[id] = true
			}
		}
	}
}

var reComponent = regexp.MustCompile(`(?s)<artifactInformations>.*?</artifactInformations>`)
var reSymbolic = regexp.MustCompile(`<symbolicName>([^<]*)</symbolicName>`)

// filterComponents keeps the runtime list entries of allowed flows.
func (g *Guard) filterComponents(ctx context.Context, body []byte) []byte {
	return reComponent.ReplaceAllFunc(body, func(block []byte) []byte {
		m := reSymbolic.FindSubmatch(block)
		if m != nil && g.allowsArtifact(ctx, string(m[1]), ActionRead) {
			return block
		}
		return nil
	})
}

// --- messages and their children -----------------------------------------

// The check of a message GUID or one of its children (run, step, trace,
// attachment, store entry): allowed when it came with an allowed answer;
// otherwise the message is looked up and decided by its flow.
func (g *Guard) checkMessage(ctx context.Context, guid string) bool {
	g.mu.Lock()
	_, ok := g.messages[guid]
	g.mu.Unlock()
	if ok {
		return true
	}
	if g.Fetch == nil {
		return false
	}
	b, err := g.Fetch(internal(ctx), "/api/v1/MessageProcessingLogs("+httpKey(guid)+")?$select=MessageGuid,CorrelationId,IntegrationArtifact")
	if err != nil {
		return false
	}
	var v struct {
		D map[string]any `json:"d"`
	}
	if json.Unmarshal(b, &v) != nil || !g.allowsLog(ctx, v.D) {
		return false
	}
	g.mu.Lock()
	if g.messages == nil {
		g.messages = map[string]string{}
	}
	if !g.tooling[guid] { // a tooling message is kept as the flow it tested
		g.messages[guid] = str(v.D, "IntegrationArtifact", "Id")
	}
	g.mu.Unlock()
	return true
}

func init() {
	checkKind["message"] = func(g *Guard, ctx context.Context, n need) bool {
		if !g.checkMessage(ctx, n.res.ID) {
			return false
		}
		g.mu.Lock()
		tooling := g.tooling[n.res.ID]
		g.mu.Unlock()
		return tooling || g.Scope.Grants(n.action) // a tooling message was decided with "test"
	}
	checkKind["child"] = func(g *Guard, ctx context.Context, n need) bool {
		g.mu.Lock()
		flow, ok := g.children[n.res.ID]
		tooling := g.tooling[n.res.ID]
		g.mu.Unlock()
		if !ok || flow == "" {
			return false
		}
		// the test tooling's recordings (stub requests) come with "test" on
		// the flow that was tested
		if tooling {
			return g.Scope.Grants(ActionTest) && g.allowsArtifact(ctx, flow, ActionTest)
		}
		// decided on the message's flow: with grants, reading a flow's
		// messages does not mean downloading their payloads
		return g.Scope.Grants(n.action) && g.allowsArtifact(ctx, flow, n.action)
	}
}

// checkKind decides the kinds the scope itself does not know.
var checkKind = map[string]func(g *Guard, ctx context.Context, n need) bool{}

type batchOp struct {
	method, path string
	header       map[string]string
	body         []byte
}

// batchOperations parses an OData $batch body (multipart/mixed, changesets
// nested one level) into its operations. Anything it cannot parse exactly
// refuses the whole batch: the tenant's parser must never see an operation
// the guard did not.
func batchOperations(header map[string]string, body []byte) ([]batchOp, error) {
	ct := ""
	for k, v := range header {
		if strings.EqualFold(k, "Content-Type") {
			ct = v
		}
	}
	mt, params, err := mime.ParseMediaType(ct)
	if err != nil || mt != "multipart/mixed" || params["boundary"] == "" {
		return nil, errors.New("not a multipart/mixed body")
	}
	var ops []batchOp
	if err := readBatchParts(multipart.NewReader(bytes.NewReader(body), params["boundary"]), 0, &ops); err != nil {
		return nil, err
	}
	if len(ops) == 0 {
		return nil, errors.New("no operations")
	}
	return ops, nil
}

func readBatchParts(mr *multipart.Reader, depth int, ops *[]batchOp) error {
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		mt, params, err := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if err != nil {
			return err
		}
		switch {
		case mt == "multipart/mixed" && depth == 0 && params["boundary"] != "":
			if err := readBatchParts(multipart.NewReader(part, params["boundary"]), 1, ops); err != nil {
				return err
			}
		case mt == "application/http":
			req, err := http.ReadRequest(bufio.NewReader(part))
			if err != nil {
				return fmt.Errorf("operation: %w", err)
			}
			if req.Proto != "HTTP/1.1" || !slices.Contains([]string{"GET", "POST", "PUT", "PATCH", "MERGE", "DELETE"}, req.Method) {
				return fmt.Errorf("operation %q %q not accepted", req.Method, req.Proto)
			}
			b, err := io.ReadAll(io.LimitReader(req.Body, 1<<20))
			if err != nil {
				return err
			}
			path := req.RequestURI
			if !strings.HasPrefix(path, "/") {
				path = apiPrefix + path
			}
			h := map[string]string{}
			for k, v := range req.Header {
				if len(v) != 1 {
					return fmt.Errorf("operation header %q repeated", k)
				}
				h[k] = v[0]
			}
			*ops = append(*ops, batchOp{method: req.Method, path: path, header: h, body: b})
		default:
			return fmt.Errorf("part of type %q not accepted", mt)
		}
	}
}
