package scope

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
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

// ToolingPrefix marks cpictl's own test tooling (harness, receiver stub, XSLT
// test flow): a scope that grants "test" may read and call it wherever it is
// installed, but never change it.
const ToolingPrefix = "CPICTL_Test"

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

	mu        sync.Mutex
	artifacts map[string]string // artifact ID -> package (allowed packages only)
	builtAt   time.Time
	messages  map[string]string // message GUID -> flow, for messages seen in allowed answers
	children  map[string]string // IDs of runs, steps, traces, attachments, store entries -> flow of their message
	endpoints []string          // entry point URLs of allowed flows
	tenant    string            // scheme://host:port of the tenant API
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
}

func (g *Guard) refuse(r *Refusal) error {
	if g.OnRefuse != nil {
		g.OnRefuse(r)
	}
	return r
}

// Before implements httpclnt.Guard.
func (g *Guard) Before(r *httpclnt.GuardRequest) error {
	if isInternal(r.Ctx) && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		return nil
	}
	d, err := g.classify(r)
	if err != nil {
		return err
	}
	for _, n := range d.need {
		if err := g.check(r.Ctx, n); err != nil {
			return err
		}
	}
	return nil
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
		if strings.HasPrefix(res.ID, ToolingPrefix) && (n.action == ActionRead || n.action == ActionTest) && g.Scope.Grants(ActionTest) {
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
			if res.Kind == KindPartner && strings.HasPrefix(res.ID, ToolingPrefix) && g.Scope.Grants(ActionTest) {
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
	reKey      = `'((?:[^']|'')*)'`
	rePackage  = regexp.MustCompile(`^IntegrationPackages\(` + reKey + `\)(?:/(\w+)DesigntimeArtifacts)?$`)
	reArtifact = regexp.MustCompile(`^(\w+)DesigntimeArtifacts\(Id=` + reKey + `,Version=` + reKey + `\)(/.*)?$`)
	reRuntime  = regexp.MustCompile(`^IntegrationRuntimeArtifacts\(` + reKey + `\)(/.*)?$`)
	reMessage  = regexp.MustCompile(`^MessageProcessingLogs\(` + reKey + `\)(/.*)?$`)
	reChild    = regexp.MustCompile(`^(MessageProcessingLogAttachments|MessageStoreEntries|MessageProcessingLogRuns|MessageProcessingLogRunSteps|TraceMessages)\(` + `([^)]*)` + `\)(/.*)?$`)
	rePDParam  = regexp.MustCompile(`^(String|Binary)Parameters\(Pid=` + reKey + `,Id=` + reKey + `\)(/.*)?$`)
	rePartner  = regexp.MustCompile(`^Partners\(` + reKey + `\)(/.*)?$`)
	reBatchOp  = regexp.MustCompile(`(?m)^(GET|POST|PUT|PATCH|MERGE|DELETE) (\S+) HTTP/1\.1\r?$`)
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
	q, _ := url.ParseQuery(rawQuery)
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

func (g *Guard) classifyAPI(r *httpclnt.GuardRequest, p string, q url.Values) (decision, error) {
	p = strings.TrimPrefix(p, "/")
	m := r.Method
	art := func(id, action string) decision {
		return decision{need: []need{{Resource{Kind: KindArtifact, ID: id}, action}}}
	}
	switch {
	case p == "" || p == "$metadata":
		return decision{allow: true}, nil

	case p == "$batch":
		var needs []need
		for _, op := range reBatchOp.FindAllStringSubmatch(string(r.Body), -1) {
			path := op[2]
			if !strings.HasPrefix(path, "/") {
				path = apiPrefix + path
			}
			d, err := g.classify(&httpclnt.GuardRequest{Ctx: r.Ctx, Method: op[1], Base: r.Base, Path: path})
			if err != nil {
				return decision{}, err
			}
			if d.filter != nil {
				return decision{}, g.refuse(&Refusal{Reason: "list reads in a $batch are not available under a scope"})
			}
			needs = append(needs, d.need...)
		}
		return decision{need: needs}, nil

	case p == "IntegrationPackages":
		if m == http.MethodPost {
			return decision{need: []need{{Resource{Kind: KindPackage, ID: bodyField(r.Body, "Id")}, ActionUpload}}}, nil
		}
		return decision{filter: func(g *Guard, b []byte) ([]byte, error) {
			return filterResults(b, func(e map[string]any) bool { return g.Scope.SeesPackage(str(e, "Id")) })
		}}, nil

	case rePackage.MatchString(p):
		mm := rePackage.FindStringSubmatch(p)
		pkg := key(mm[1])
		res := Resource{Kind: KindPackage, ID: pkg}
		if mm[2] != "" { // artifacts of the package
			return decision{need: []need{{res, ActionRead}}, filter: func(g *Guard, b []byte) ([]byte, error) {
				return filterResults(b, func(e map[string]any) bool {
					return g.Scope.Allows(Resource{Kind: KindArtifact, ID: str(e, "Id"), Package: pkg}, ActionRead)
				})
			}}, nil
		}
		switch m {
		case http.MethodDelete:
			return decision{need: []need{{res, ActionDelete}}}, nil
		case http.MethodGet, http.MethodHead:
			return decision{need: []need{{res, ActionRead}}}, nil
		}
		return decision{need: []need{{res, ActionUpload}}}, nil

	case slices.Contains([]string{"IntegrationDesigntimeArtifacts", "ValueMappingDesigntimeArtifacts", "MessageMappingDesigntimeArtifacts", "ScriptCollectionDesigntimeArtifacts"}, p):
		if m != http.MethodPost {
			return decision{}, g.refuse(&Refusal{Reason: "listing all artifacts of the tenant is not available under a scope"})
		}
		return decision{need: []need{{Resource{Kind: KindArtifact, ID: bodyField(r.Body, "Id"), Package: bodyField(r.Body, "PackageId")}, ActionUpload}}}, nil

	case reArtifact.MatchString(p):
		mm := reArtifact.FindStringSubmatch(p)
		id, sub := key(mm[2]), mm[4]
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
		return decision{need: []need{{Resource{Kind: "message", ID: guid}, ActionRead}}, record: (*Guard).recordChildren}, nil
	case reChild.MatchString(p):
		mm := reChild.FindStringSubmatch(p)
		action := ActionRead
		if strings.Contains(mm[3], "$value") || mm[1] == "TraceMessages" {
			action = ActionDownload
		}
		return decision{need: []need{{Resource{Kind: "child", ID: key(strings.Trim(mm[2], "'"))}, action}}, record: (*Guard).recordChildren}, nil
	case p == "MessageProcessingLogCustomHeaderProperties":
		return decision{filter: func(g *Guard, b []byte) ([]byte, error) {
			return filterResults(b, func(e map[string]any) bool {
				l, _ := e["Log"].(map[string]any)
				return l != nil && g.allowsLog(l)
			})
		}}, nil

	case p == "StringParameters" || p == "BinaryParameters" || p == "Partners" || p == "AlternativePartners" || p == "AuthorizedUsers":
		if write(m) {
			return decision{need: []need{{Resource{Kind: KindPartner, ID: bodyField(r.Body, "Pid")}, ActionPDWrite}}}, nil
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
		return decision{need: []need{{Resource{Kind: KindPartner, ID: key(mm[2])}, action}}}, nil
	case rePartner.MatchString(p):
		mm := rePartner.FindStringSubmatch(p)
		action := ActionRead
		if write(m) {
			action = ActionPDWrite
		}
		return decision{need: []need{{Resource{Kind: KindPartner, ID: key(mm[1])}, action}}}, nil
	case strings.HasPrefix(p, "AlternativePartners(") || strings.HasPrefix(p, "AuthorizedUsers("):
		// keys without the partner ID: changes need the entry's partner
		if !write(m) {
			return decision{filter: func(g *Guard, b []byte) ([]byte, error) {
				return filterResults(b, func(e map[string]any) bool { return g.allowsPartner(str(e, "Pid"), ActionRead) })
			}}, nil
		}
		pid := g.lookupField(r.Ctx, apiPrefix+p, "Pid")
		return decision{need: []need{{Resource{Kind: KindPartner, ID: pid}, ActionPDWrite}}}, nil
	}

	first, _, _ := strings.Cut(p, "(")
	first, _, _ = strings.Cut(first, "/")
	if area, ok := tenantAreas[first]; ok {
		action := ActionRead
		if write(r.Method) {
			action = "write"
		}
		return decision{need: []need{{Resource{Kind: KindTenant, ID: area}, action}}}, nil
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
		return decision{need: []need{{Resource{Kind: KindArtifact, ID: bodyField(r.Body, "artifactSymbolicName")}, ActionLogLevel}}}, nil
	}
	return decision{}, g.refuse(&Refusal{Reason: "/Operations/" + name + " is not available under a scope"})
}

// classifyEndpoint decides a request to a flow's runtime endpoint: allowed
// below an entry point the guard has seen for an allowed flow, with "test".
func (g *Guard) classifyEndpoint(r *httpclnt.GuardRequest) (decision, error) {
	if !g.Scope.Grants(ActionTest) {
		return decision{}, g.refuse(&Refusal{Reason: "sending messages needs the test action"})
	}
	target := withoutDefaultPort(r.Base) + r.Path
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, ep := range g.endpoints {
		if strings.HasPrefix(target, withoutDefaultPort(strings.TrimSuffix(ep, "*"))) {
			return decision{allow: true}, nil
		}
	}
	return decision{}, g.refuse(&Refusal{Reason: "the endpoint " + r.Base + strings.SplitN(r.Path, "?", 2)[0] + " is not an entry point of a flow in the scope"})
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

func bodyField(body []byte, field string) string {
	var v map[string]any
	if json.Unmarshal(body, &v) != nil {
		return ""
	}
	s, _ := v[field].(string)
	return s
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
	return g.check(ctx, need{Resource{Kind: KindArtifact, ID: id}, action}) == nil
}

func (g *Guard) allowsPartner(pid, action string) bool {
	if strings.HasPrefix(pid, ToolingPrefix) && g.Scope.Grants(ActionTest) {
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
	if strings.HasPrefix(id, ToolingPrefix) && g.Scope.Grants(ActionTest) {
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
	for _, e := range results(body) {
		eps, _ := e["EntryPoints"].(map[string]any)
		list, _ := eps["results"].([]any)
		for _, x := range list {
			if m, ok := x.(map[string]any); ok {
				if u := str(m, "Url"); u != "" && !slices.Contains(g.endpoints, u) {
					g.endpoints = append(g.endpoints, u)
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
		tooling := strings.HasPrefix(flow, ToolingPrefix) && g.Scope.Grants(ActionTest)
		return g.Scope.Grants(n.action) || tooling
	}
}

// checkKind decides the kinds the scope itself does not know.
var checkKind = map[string]func(g *Guard, ctx context.Context, n need) bool{}
