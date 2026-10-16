// Package scope is the access scope of a cpictl process: which packages,
// artifacts and Partner Directory entries it may see, which tenant-wide areas
// it may use and which actions it may take. Without a scope cpictl is not
// restricted; with one, everything not granted is refused or hidden.
//
// Matching is deliberately simple and identical everywhere (CLI, MCP server,
// CPI Studio): exact, case-sensitive comparison with "*" as the only
// wildcard; a pattern starting with "!" excludes; patterns are evaluated in
// order and the last match wins.
package scope

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// Actions a scope can grant.
const (
	ActionRead      = "read"      // metadata: lists, status, parameters, logs without payloads
	ActionDownload  = "download"  // content: artifact files, resources, payloads, attachments, traces
	ActionUpload    = "upload"    // create and update artifacts and packages
	ActionDeploy    = "deploy"    // deploy and undeploy
	ActionConfigure = "configure" // externalised parameters
	ActionTest      = "test"      // send test messages, run tests, receiver stub
	ActionLogLevel  = "log-level" // change the log level (TRACE shows payloads)
	ActionDelete    = "delete"    // delete artifacts and packages
	ActionPDWrite   = "pd-write"  // change Partner Directory entries
)

// Actions lists the valid actions in a stable order.
var Actions = []string{ActionRead, ActionDownload, ActionUpload, ActionDeploy, ActionConfigure, ActionTest,
	ActionLogLevel, ActionDelete, ActionPDWrite}

// TenantAreas are the tenant-wide resources a scope grants only explicitly,
// as "<area>:read" or "<area>:write".
var TenantAreas = []string{"keystore", "credentials", "jms", "variables", "numberranges", "datastores",
	"accesspolicies", "logfiles", "settings", "auditlog", "idmappings", "idempotency", "locks", "components"}

// Scope is the parsed scope file.
type Scope struct {
	Version   int      `json:"version" yaml:"version"`
	Packages  []string `json:"packages,omitempty" yaml:"packages,omitempty"`
	Artifacts []string `json:"artifacts,omitempty" yaml:"artifacts,omitempty"`
	Partners  []string `json:"partners,omitempty" yaml:"partners,omitempty"`
	Tenant    []string `json:"tenant,omitempty" yaml:"tenant,omitempty"`
	Actions   []string `json:"actions,omitempty" yaml:"actions,omitempty"`
}

// Kinds of resources.
const (
	KindPackage  = "package"
	KindArtifact = "artifact"
	KindPartner  = "partner"
	KindTenant   = "tenant"
)

// Resource is what an operation touches. For an artifact, Package is its
// package (empty when unknown: then only the artifact patterns decide if
// the scope has no package restriction, otherwise it is refused). For a
// tenant-wide resource, ID is the area (keystore, jms, ...).
type Resource struct {
	Kind, ID, Package string
}

func (r Resource) String() string {
	switch {
	case r.Kind == KindArtifact && r.Package != "":
		return fmt.Sprintf("artifact %s (package %s)", r.ID, r.Package)
	case r.Kind == KindTenant:
		return "tenant-wide " + r.ID
	}
	return r.Kind + " " + r.ID
}

// Parse reads a scope from JSON or YAML; unknown keys, unknown actions and
// tenant areas, and patterns with characters other than letters, digits,
// "_", ".", "-", "*" and a leading "!" are errors.
func Parse(data []byte) (*Scope, error) {
	var s Scope
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("scope: %w", err)
	}
	if s.Version != 1 {
		return nil, fmt.Errorf("scope: version must be 1, got %d", s.Version)
	}
	for field, list := range map[string][]string{"packages": s.Packages, "artifacts": s.Artifacts, "partners": s.Partners} {
		for _, p := range list {
			if err := checkPattern(p); err != nil {
				return nil, fmt.Errorf("scope: %s: %w", field, err)
			}
		}
	}
	for _, a := range s.Actions {
		if !slices.Contains(Actions, a) {
			return nil, fmt.Errorf("scope: unknown action %q (%s)", a, strings.Join(Actions, ", "))
		}
	}
	for _, t := range s.Tenant {
		area, mode, ok := strings.Cut(t, ":")
		if !ok || !slices.Contains(TenantAreas, area) || (mode != "read" && mode != "write") {
			return nil, fmt.Errorf("scope: tenant grant %q must be <area>:read or <area>:write (areas: %s)", t, strings.Join(TenantAreas, ", "))
		}
	}
	return &s, nil
}

func checkPattern(p string) error {
	body := strings.TrimPrefix(p, "!")
	if body == "" {
		return fmt.Errorf("empty pattern %q", p)
	}
	for _, r := range body {
		if !(r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)) || strings.ContainsRune("_.-*", r)) {
			return fmt.Errorf("pattern %q: only letters, digits, _ . - and * are allowed", p)
		}
	}
	return nil
}

// ValidID reports whether id is a plausible CPI ID (letters, digits, _ . -).
// Under a scope, other IDs are refused before any tenant call.
func ValidID(id string) bool {
	if id == "" || len(id) > 255 {
		return false
	}
	for _, r := range id {
		if !(r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)) || strings.ContainsRune("_.-", r)) {
			return false
		}
	}
	return true
}

// matches evaluates patterns for id: last match wins; a list that starts with
// an exclusion implicitly starts with "*". An empty list matches nothing.
func matches(patterns []string, id string) bool {
	in := len(patterns) > 0 && strings.HasPrefix(patterns[0], "!")
	for _, p := range patterns {
		neg := strings.HasPrefix(p, "!")
		if glob(strings.TrimPrefix(p, "!"), id) {
			in = !neg
		}
	}
	return in
}

// glob matches pattern against s with "*" as the only wildcard.
func glob(pattern, s string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == s
	}
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	for _, part := range parts[1 : len(parts)-1] {
		i := strings.Index(s, part)
		if i < 0 {
			return false
		}
		s = s[i+len(part):]
	}
	return strings.HasSuffix(s, parts[len(parts)-1])
}

// Grants reports whether the scope grants action at all.
func (s *Scope) Grants(action string) bool { return slices.Contains(s.Actions, action) }

// Allows reports whether action on r is in the scope.
func (s *Scope) Allows(r Resource, action string) bool {
	if s == nil {
		return true
	}
	switch r.Kind {
	case KindTenant:
		mode := "read"
		if action != ActionRead && action != ActionDownload {
			mode = "write"
		}
		return slices.Contains(s.Tenant, r.ID+":"+mode) || (mode == "read" && slices.Contains(s.Tenant, r.ID+":write"))
	case KindPartner:
		return s.Grants(action) && matches(s.Partners, r.ID)
	case KindPackage:
		return s.Grants(action) && matches(s.Packages, r.ID)
	case KindArtifact:
		if !s.Grants(action) {
			return false
		}
		if len(s.Artifacts) > 0 && !matches(s.Artifacts, r.ID) {
			return false
		}
		if r.Package == "" {
			return false // package unknown: refuse
		}
		return matches(s.Packages, r.Package)
	}
	return false
}

// SeesPackage reports whether the package is visible (read).
func (s *Scope) SeesPackage(id string) bool {
	return s.Allows(Resource{Kind: KindPackage, ID: id}, ActionRead)
}

// Hash is the SHA-256 of the scope's canonical JSON form, so that a caller
// can verify which scope a server enforces.
func (s *Scope) Hash() string {
	c := *s
	for _, l := range []*[]string{&c.Tenant, &c.Actions} {
		*l = slices.Clone(*l)
		slices.Sort(*l)
		*l = slices.Compact(*l)
	}
	data, _ := json.Marshal(c) // packages, artifacts, partners keep their order: it matters
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
