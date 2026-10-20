package scope

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
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
// wherever they are installed, but never change them. Their Partner Directory
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

	mu        sync.Mutex
	artifacts map[string]string // artifact ID -> package (allowed packages only)
	builtAt   time.Time
	messages  map[string]string // message GUID -> flow, for messages seen in allowed answers
	children  map[string]string // IDs of runs, steps, traces, attachments, store entries -> flow of their message
	endpoints map[string]string // entry point URL -> flow, for allowed flows
	pdFlows   map[string]string // ProcessDirect address -> flow, for allowed flows (built on first harness call)
	pdBuiltAt time.Time
	tenant    string // scheme://host:port of the tenant API
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
				return nil
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
// every package; an unknown ID rebuilds the map at most every Refresh.
func (g *Guard) packageOf(ctx context.Context, id string) string {
	g.mu.Lock()
	pkg, ok := g.artifacts[id]
	stale := g.artifacts == nil || time.Since(g.builtAt) > cmpDuration(g.Refresh, 30*time.Second)
	g.mu.Unlock()
	if ok || !stale || g.Fetch == nil {
		return pkg
	}
	m := g.buildArtifactMap(ctx)
	g.mu.Lock()
	g.artifacts, g.builtAt = m, time.Now()
	pkg = m[id]
	g.mu.Unlock()
	return pkg
}

func cmpDuration(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}

func (g *Guard) buildArtifactMap(ctx context.Context) map[string]string {
	m := map[string]string{}
	ctx = internal(ctx)
	// the package list takes no $select on a tenant ("$select is not supported")
	body, err := g.Fetch(ctx, "/api/v1/IntegrationPackages")
	if err != nil {
		return m
	}
	for _, p := range results(body) {
		pkg, _ := p["Id"].(string)
		if !g.Scope.SeesPackage(pkg) {
			continue
		}
		for _, t := range designtimeTypes {
			b, err := g.Fetch(ctx, "/api/v1/IntegrationPackages("+httpKey(pkg)+")/"+t+"DesigntimeArtifacts?$select=Id")
			if err != nil {
				continue
			}
			for _, a := range results(b) {
				if id, _ := a["Id"].(string); id != "" {
					m[id] = pkg
				}
			}
		}
	}
	return m
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
		case strings.HasSuffix(sub, "/$value") || sub == "/$value":
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
		if m == http.MethodDelete {
			return art(key(mm[1]), ActionDeploy), nil
		}
		return art(key(mm[1]), ActionRead), nil
	case strings.HasPrefix(p, "BuildAndDeployStatus("):
		return decision{allow: true}, nil

	case p == "ServiceEndpoints":
		return decision{filter: func(g *Guard, b []byte) ([]byte, error) {
			return filterResults(b, func(e map[string]any) bool { return g.allowsArtifact(r.Ctx, str(e, "Name"), ActionRead) })
		}, record: (*Guard).recordEndpoints}, nil

	case p == "MessageProcessingLogs":
		return decision{filter: func(g *Guard, b []byte) ([]byte, error) {
			return filterResults(b, func(e map[string]any) bool { return g.allowsLog(e) })
		}, record: (*Guard).recordMessages}, nil
	case reMessage.MatchString(p):
		mm := reMessage.FindStringSubmatch(p)
		guid := key(mm[1])
		// the message and its lists; contents are read through their own IDs (download)
		return decision{need: []need{{res: Resource{Kind: "message", ID: guid}, action: ActionRead}}, record: (*Guard).recordChildren}, nil
	case reChild.MatchString(p):
		mm := reChild.FindStringSubmatch(p)
		action := ActionRead
		if strings.Contains(mm[3], "$value") || mm[1] == "TraceMessages" {
			action = ActionDownload
		}
		return decision{need: []need{{res: Resource{Kind: "child", ID: key(strings.Trim(mm[2], "'"))}, action: action}}, record: (*Guard).recordChildren}, nil
	case p == "MessageProcessingLogCustomHeaderProperties":
		return decision{filter: func(g *Guard, b []byte) ([]byte, error) {
			return filterResults(b, func(e map[string]any) bool {
				l, _ := e["Log"].(map[string]any)
				return l != nil && g.allowsLog(l)
			})
		}}, nil

	case p == "StringParameters" || p == "BinaryParameters" || p == "Partners" || p == "AlternativePartners" || p == "AuthorizedUsers":
		if write(m) {
			param := p == "StringParameters" || p == "BinaryParameters"
			return decision{need: []need{{res: Resource{Kind: KindPartner, ID: bodyField(r.Body, "Pid")}, action: ActionPDWrite, toolingWrite: param && m == http.MethodPost}}}, nil
		}
		return decision{filter: func(g *Guard, b []byte) ([]byte, error) {
			return filterResults(b, func(e map[string]any) bool { return g.allowsPartner(str(e, "Pid"), ActionRead) })
		}}, nil
	case rePDParam.MatchString(p):
		mm := rePDParam.FindStringSubmatch(p)
		action := ActionRead
		if write(m) {
			action = ActionPDWrite
		}
		return decision{need: []need{{res: Resource{Kind: KindPartner, ID: key(mm[2])}, action: action, toolingWrite: true}}}, nil
	case rePartner.MatchString(p):
		mm := rePartner.FindStringSubmatch(p)
		action := ActionRead
		if write(m) {
			action = ActionPDWrite
		}
		return decision{need: []need{{res: Resource{Kind: KindPartner, ID: key(mm[1])}, action: action}}}, nil
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
// at or below an entry point the guard has seen for an allowed flow, with
// "test". A call through the test harness must name a ProcessDirect address
// of an allowed flow.
func (g *Guard) classifyEndpoint(r *httpclnt.GuardRequest) (decision, error) {
	if !g.Scope.Grants(ActionTest) {
		return decision{}, g.refuse(&Refusal{Reason: "sending messages needs the test action"})
	}
	target, _, _ := strings.Cut(withoutDefaultPort(r.Base)+r.Path, "?")
	g.mu.Lock()
	flow, found := "", false
	for ep, f := range g.endpoints {
		if endpointCovers(withoutDefaultPort(ep), target) {
			flow, found = f, true
			break
		}
	}
	g.mu.Unlock()
	if !found {
		return decision{}, g.refuse(&Refusal{Reason: "the endpoint " + target + " is not an entry point of a flow in the scope"})
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
		return decision{need: []need{{res: Resource{Kind: KindArtifact, ID: to}, action: ActionTest}}}, nil
	}
	// the entry point was seen in a list of flows the scope may read; with
	// grants, read on a package does not mean test on it
	return decision{need: []need{{res: Resource{Kind: KindArtifact, ID: flow}, action: ActionTest}}}, nil
}

// endpointCovers reports whether target is the entry point ep or below it
// ("/..."); an entry point ending in "*" covers everything after its prefix.
func endpointCovers(ep, target string) bool {
	if prefix, ok := strings.CutSuffix(ep, "*"); ok {
		return strings.HasPrefix(target, prefix)
	}
	ep = strings.TrimSuffix(ep, "/")
	return target == ep || strings.HasPrefix(target, ep+"/")
}

// processDirectFlow returns the allowed flow whose ProcessDirect sender
// listens on addr, or "".
func (g *Guard) processDirectFlow(ctx context.Context, addr string) string {
	if addr == "" || g.ProcessDirectAddresses == nil {
		return ""
	}
	g.mu.Lock()
	flow := g.pdFlows[addr]
	fresh := g.pdFlows != nil && time.Since(g.pdBuiltAt) < cmpDuration(g.Refresh, 30*time.Second)
	g.mu.Unlock()
	if flow != "" || fresh {
		return flow // known, or rebuilt a moment ago: no new downloads
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

// allowsLog decides a message processing log entry by its flow and package.
func (g *Guard) allowsLog(e map[string]any) bool {
	id, pkg := str(e, "IntegrationArtifact", "Id"), str(e, "IntegrationArtifact", "PackageId")
	if id == "" {
		return false
	}
	if toolingFlow(id) && g.Scope.Grants(ActionTest) {
		return true
	}
	return g.Scope.Allows(Resource{Kind: KindArtifact, ID: id, Package: pkg}, ActionRead)
}

func (g *Guard) recordMessages(_ *httpclnt.GuardRequest, body []byte) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.messages == nil {
		g.messages = map[string]string{}
	}
	for _, e := range results(body) {
		if guid := str(e, "MessageGuid"); guid != "" {
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
	flow := ""
	if mm := reMessage.FindStringSubmatch(p); mm != nil {
		flow = g.messages[key(mm[1])]
	} else if mm := reChild.FindStringSubmatch(p); mm != nil {
		flow = g.children[key(strings.Trim(mm[2], "'"))]
	}
	ids := map[string]bool{}
	collectIDs(v, ids)
	if g.children == nil {
		g.children = map[string]string{}
	}
	for id := range ids {
		if _, ok := g.children[id]; !ok {
			g.children[id] = flow
		}
	}
}

func (g *Guard) recordEndpoints(_ *httpclnt.GuardRequest, body []byte) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.endpoints == nil {
		g.endpoints = map[string]string{}
	}
	for _, e := range results(body) {
		eps, _ := e["EntryPoints"].(map[string]any)
		list, _ := eps["results"].([]any)
		for _, x := range list {
			if m, ok := x.(map[string]any); ok {
				if u := str(m, "Url"); u != "" {
					g.endpoints[u] = str(e, "Name")
				}
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
	b, err := g.Fetch(internal(ctx), "/api/v1/MessageProcessingLogs("+httpKey(guid)+")?$select=MessageGuid,IntegrationArtifact")
	if err != nil {
		return false
	}
	var v struct {
		D map[string]any `json:"d"`
	}
	if json.Unmarshal(b, &v) != nil || !g.allowsLog(v.D) {
		return false
	}
	g.mu.Lock()
	if g.messages == nil {
		g.messages = map[string]string{}
	}
	g.messages[guid] = str(v.D, "IntegrationArtifact", "Id")
	g.mu.Unlock()
	return true
}

func init() {
	checkKind["message"] = func(g *Guard, ctx context.Context, n need) bool {
		return g.Scope.Grants(n.action) && g.checkMessage(ctx, n.res.ID)
	}
	checkKind["child"] = func(g *Guard, ctx context.Context, n need) bool {
		g.mu.Lock()
		flow, ok := g.children[n.res.ID]
		g.mu.Unlock()
		if !ok {
			return false
		}
		// the test tooling's own recordings (stub requests) come with "test"
		if toolingFlow(flow) && g.Scope.Grants(ActionTest) {
			return true
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
