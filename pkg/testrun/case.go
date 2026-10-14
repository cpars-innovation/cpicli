// Package testrun runs the test cases of deployed integration flows: YAML
// files in .cpi/tests/<FlowId>/<case>.yaml (see docs/testing.md and
// docs/test-case.schema.json). Each case sends one message with
// ops.SendTestMessage, waits for its processing log and checks the
// expectations; failures carry diagnostics (failing step, error text, the
// message path across flows).
package testrun

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cpars-innovation/cpicli/internal/output"
	"gopkg.in/yaml.v3"
)

// DefaultDir is where test cases live, relative to the repository root.
const DefaultDir = ".cpi/tests"

// ConfigFile is the file in a flow's case folder with the externalised
// parameter values the flow gets during a test run (--apply-config).
const ConfigFile = "config.yaml"

// DefaultWait is how long a case waits for the message processing log.
const DefaultWait = 60 * time.Second

// Case is one test case: a message to send and the expected outcome.
type Case struct {
	// Name is the file name without .yaml; File the path it was loaded
	// from (body_file and idoc_file are relative to its folder).
	Name string `yaml:"-" json:"name"`
	File string `yaml:"-" json:"file,omitempty"`
	// Flow is the integration flow ID; default the folder name.
	Flow        string   `yaml:"flow,omitempty" json:"flow"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Tags        []string `yaml:"tags,omitempty" json:"tags,omitempty"`
	// Skip, when set, skips the case and gives the reason.
	Skip string `yaml:"skip,omitempty" json:"skip,omitempty"`
	// Order sorts the cases of a flow (lower first, then by name).
	Order   int     `yaml:"order,omitempty" json:"order,omitempty"`
	Request Request `yaml:"request" json:"request"`
	Expect  Expect  `yaml:"expect" json:"expect"`
	// Config is the flow's config.yaml (parameter key -> test value), set
	// by Load; nil when the flow has none.
	Config map[string]string `yaml:"-" json:"-"`
	// FlowConfig are the values config.yaml sets for other flows (its
	// flows: section, flow -> key -> value): receivers of downstream flows,
	// e.g. the flow a ProcessDirect call reaches.
	FlowConfig map[string]map[string]string `yaml:"-" json:"-"`
}

// Request is the message a case sends.
type Request struct {
	Method      string            `yaml:"method,omitempty" json:"method,omitempty"`
	ContentType string            `yaml:"content_type,omitempty" json:"contentType,omitempty"`
	Headers     map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	Body        string            `yaml:"body,omitempty" json:"body,omitempty"`
	BodyFile    string            `yaml:"body_file,omitempty" json:"bodyFile,omitempty"`
	// URL is one of the flow's endpoint URLs or a URL below one; Endpoint
	// is its older name (accepted, URL wins).
	URL      string `yaml:"url,omitempty" json:"url,omitempty"`
	Endpoint string `yaml:"endpoint,omitempty" json:"endpoint,omitempty"`
	// Path is a sub-path (and query) below the flow's endpoint.
	Path                 string        `yaml:"path,omitempty" json:"path,omitempty"`
	ProcessDirectAddress string        `yaml:"process_direct_address,omitempty" json:"processDirectAddress,omitempty"`
	IDoc                 string        `yaml:"idoc,omitempty" json:"idoc,omitempty"`
	IDocFile             string        `yaml:"idoc_file,omitempty" json:"idocFile,omitempty"`
	OData                *ODataRequest `yaml:"odata,omitempty" json:"odata,omitempty"`
	// Wait is how long to wait for the message processing log (default 60s).
	Wait Duration `yaml:"wait,omitempty" json:"wait,omitempty"`
}

// ODataRequest is an OData V2 request to the flow's OData sender.
type ODataRequest struct {
	Method    string `yaml:"method,omitempty" json:"method,omitempty"`
	EntitySet string `yaml:"entity_set,omitempty" json:"entitySet,omitempty"`
	Key       string `yaml:"key,omitempty" json:"key,omitempty"`
	Query     string `yaml:"query,omitempty" json:"query,omitempty"`
}

// Expect is the expected outcome of a case. Every expectation that is set
// must hold. Without http_status (and without error_contains) the HTTP
// status must be 2xx.
type Expect struct {
	HTTPStatus int `yaml:"http_status,omitempty" json:"httpStatus,omitempty"`
	// MessageStatus is the final status of the message (COMPLETED, FAILED,
	// ESCALATED, ...); "" does not check it.
	MessageStatus       string     `yaml:"message_status,omitempty" json:"messageStatus,omitempty"`
	ResponseContains    StringList `yaml:"response_contains,omitempty" json:"responseContains,omitempty"`
	ResponseNotContains StringList `yaml:"response_not_contains,omitempty" json:"responseNotContains,omitempty"`
	// ResponseXPath maps a path (see XPath) to the expected text.
	ResponseXPath map[string]string `yaml:"response_xpath,omitempty" json:"responseXpath,omitempty"`
	// ResponseJSON maps a dotted path (a.b.0.c) to the expected value.
	ResponseJSON map[string]any `yaml:"response_json,omitempty" json:"responseJson,omitempty"`
	// ErrorContains are texts expected in the error: the message log's
	// error text, or the response of a failed call.
	ErrorContains StringList `yaml:"error_contains,omitempty" json:"errorContains,omitempty"`
	// CustomHeaders are custom header properties expected in the log.
	CustomHeaders map[string]string `yaml:"custom_headers,omitempty" json:"customHeaders,omitempty"`
	// StepsInclude are step names (activities) or model step IDs that must
	// appear in the message's processing steps.
	StepsInclude StringList `yaml:"steps_include,omitempty" json:"stepsInclude,omitempty"`
	// MaxDuration is the longest processing time: the message's log
	// (start to end) when the case waited for it, else the HTTP call.
	MaxDuration Duration `yaml:"max_duration,omitempty" json:"maxDuration,omitempty"`
	// StubCalls are requests the receiver stub must have recorded.
	StubCalls []StubMatcher `yaml:"stub_calls,omitempty" json:"stubCalls,omitempty"`
}

// StubMatcher selects calls the receiver stub recorded (ops.StubCall). A
// call matches when every field that is set matches; Count calls must match
// (default 1).
type StubMatcher struct {
	Rule         string            `yaml:"rule,omitempty" json:"rule,omitempty"`
	Method       string            `yaml:"method,omitempty" json:"method,omitempty"`
	PathContains string            `yaml:"path_contains,omitempty" json:"pathContains,omitempty"`
	BodyContains StringList        `yaml:"body_contains,omitempty" json:"bodyContains,omitempty"`
	Header       map[string]string `yaml:"header,omitempty" json:"header,omitempty"`
	Count        *int              `yaml:"count,omitempty" json:"count,omitempty"`
}

// count is the number of calls that must match.
func (m StubMatcher) count() int {
	if m.Count == nil {
		return 1
	}
	return *m.Count
}

// StringList is a list of strings; YAML accepts a single string too.
type StringList []string

// UnmarshalYAML accepts a scalar or a sequence.
func (s *StringList) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		*s = StringList{n.Value}
		return nil
	}
	var l []string
	if err := n.Decode(&l); err != nil {
		return err
	}
	*s = l
	return nil
}

// Duration is a time.Duration written as "60s", "2m" or a number of seconds.
type Duration time.Duration

// UnmarshalYAML parses "90s", "1m30s" or seconds (90).
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode {
		return fmt.Errorf("line %d: a duration such as 60s or 2m", n.Line)
	}
	if secs, err := strconv.ParseFloat(n.Value, 64); err == nil {
		if secs < 0 {
			return fmt.Errorf("line %d: negative duration %s", n.Line, n.Value)
		}
		*d = Duration(time.Duration(secs * float64(time.Second)))
		return nil
	}
	v, err := time.ParseDuration(n.Value)
	if err != nil || v < 0 {
		return fmt.Errorf("line %d: invalid duration %q (e.g. 60s or 2m)", n.Line, n.Value)
	}
	*d = Duration(v)
	return nil
}

// MarshalYAML writes the duration as text (60s).
func (d Duration) MarshalYAML() (any, error) { return time.Duration(d).String(), nil }

// MarshalText writes the duration as text (60s) in JSON.
func (d Duration) MarshalText() ([]byte, error) { return []byte(time.Duration(d).String()), nil }

// UnmarshalText reads a duration written by MarshalText.
func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	*d = Duration(v)
	return err
}

// ID is flow/name, the case's identity in filters and reports.
func (c Case) ID() string { return c.Flow + "/" + c.Name }

// url returns the request URL (url, else the older endpoint).
func (r Request) url() string {
	if r.URL != "" {
		return r.URL
	}
	return r.Endpoint
}

// needsLog reports whether the case waits for the message processing log.
func (e Expect) needsLog() bool {
	return e.MessageStatus != "" || len(e.CustomHeaders) > 0 || len(e.StepsInclude) > 0 || len(e.ErrorContains) > 0
}

// Filter selects cases. Each list that is set must match: Flows and Cases
// are names or path.Match patterns (a case as name or flow/name), Tags
// select cases with at least one of them.
type Filter struct {
	Flows []string `json:"flows,omitempty"`
	Cases []string `json:"cases,omitempty"`
	Tags  []string `json:"tags,omitempty"`
}

func matchAny(patterns []string, values ...string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, p := range patterns {
		for _, v := range values {
			if ok, _ := path.Match(p, v); ok || p == v {
				return true
			}
		}
	}
	return false
}

// Match reports whether c is selected.
func (f Filter) Match(c Case) bool {
	if !matchAny(f.Flows, c.Flow) || !matchAny(f.Cases, c.Name, c.ID()) {
		return false
	}
	return len(f.Tags) == 0 || slices.ContainsFunc(c.Tags, func(t string) bool { return slices.Contains(f.Tags, t) })
}

// Load reads the cases <dir>/<FlowId>/*.yaml selected by filter (config.yaml
// is the flow's test configuration, not a case), sorted by flow, order and
// name. Files are decoded strictly: invalid YAML and unknown keys are usage
// errors naming file:line. body_file and idoc_file must exist (relative to
// the case file).
func Load(dir string, filter Filter) ([]Case, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, output.Usagef("no test cases: %s does not exist (write cases as %s/<FlowId>/<case>.yaml, see the cpi-test skill)", dir, filepath.ToSlash(dir))
		}
		return nil, output.Usage(err)
	}
	var cases []Case
	var problems []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || e.Name() == XSLTDir || e.Name() == ScriptCollectionsDir {
			continue // pd: the XSLT cases (LoadXSLT); collections: script cases (LoadScriptCases)
		}
		flowDir := filepath.Join(dir, e.Name())
		files, err := filepath.Glob(filepath.Join(flowDir, "*.yaml"))
		if err != nil {
			return nil, err
		}
		more, _ := filepath.Glob(filepath.Join(flowDir, "*.yml"))
		files = append(files, more...)
		sort.Strings(files)
		var cfg map[string]string
		var others map[string]map[string]string
		var flowCases []Case
		for _, file := range files {
			base := filepath.Base(file)
			if base == ConfigFile || base == "config.yml" {
				if cfg, others, err = loadConfig(file); err != nil {
					problems = append(problems, err.Error())
				}
				continue
			}
			c, err := LoadCase(file)
			if err != nil {
				problems = append(problems, err.Error())
				continue
			}
			if c.Flow == "" {
				c.Flow = e.Name()
			}
			flowCases = append(flowCases, *c)
		}
		for _, c := range flowCases {
			c.Config, c.FlowConfig = cfg, others
			if filter.Match(c) {
				cases = append(cases, c)
			}
		}
	}
	if len(problems) > 0 {
		return nil, output.Usagef("invalid test case file(s):\n%s", strings.Join(problems, "\n"))
	}
	SortCases(cases)
	return cases, nil
}

// SortCases sorts by flow, order and name.
func SortCases(cases []Case) {
	sort.SliceStable(cases, func(i, j int) bool {
		a, b := cases[i], cases[j]
		if a.Flow != b.Flow {
			return a.Flow < b.Flow
		}
		if a.Order != b.Order {
			return a.Order < b.Order
		}
		return a.Name < b.Name
	})
}

// LoadCase reads one case file strictly (see Load). Flow stays empty when
// the file does not set it.
func LoadCase(file string) (*Case, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var c Case
	if err := decodeStrict(data, &c); err != nil {
		return nil, fileError(file, err)
	}
	c.File = file
	c.Name = strings.TrimSuffix(strings.TrimSuffix(filepath.Base(file), ".yaml"), ".yml")
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("%s: %v", file, err)
	}
	return &c, nil
}

// decodeStrict decodes one YAML document, refusing unknown keys.
func decodeStrict(data []byte, v any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) { // io.EOF: an empty file
		return err
	}
	return nil
}

var reYAMLLine = regexp.MustCompile(`^(?:yaml: )?line (\d+): (.*)$`)

// fileError rewrites YAML errors as file:line: message, one per line.
func fileError(file string, err error) error {
	var lines []string
	var te *yaml.TypeError
	if errors.As(err, &te) {
		lines = te.Errors
	} else {
		lines = []string{err.Error()}
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if m := reYAMLLine.FindStringSubmatch(l); m != nil {
			msg := strings.Replace(m[2], " in type testrun.", " in ", 1)
			out = append(out, fmt.Sprintf("%s:%s: %s", file, m[1], msg))
			continue
		}
		out = append(out, fmt.Sprintf("%s: %s", file, strings.TrimPrefix(l, "yaml: ")))
	}
	return errors.New(strings.Join(out, "\n"))
}

// validate checks the combinations the YAML types cannot.
func (c *Case) validate() error {
	r := c.Request
	switch {
	case r.Body != "" && r.BodyFile != "":
		return fmt.Errorf("request: use either body or body_file")
	case r.IDoc != "" && r.IDocFile != "":
		return fmt.Errorf("request: use either idoc or idoc_file")
	case r.URL != "" && r.Endpoint != "" && r.URL != r.Endpoint:
		return fmt.Errorf("request: url and endpoint differ (endpoint is the old name of url)")
	}
	for _, f := range []string{r.BodyFile, r.IDocFile} {
		if f == "" {
			continue
		}
		if filepath.IsAbs(f) {
			return fmt.Errorf("request: %s must be relative to the case file", f)
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(c.File), f)); err != nil {
			return fmt.Errorf("request: %s not found next to the case file", f)
		}
	}
	for i, m := range c.Expect.StubCalls {
		if m.Count != nil && *m.Count < 0 {
			return fmt.Errorf("expect.stub_calls[%d]: count must not be negative", i)
		}
	}
	if s := c.Expect.HTTPStatus; s != 0 && (s < 100 || s > 599) {
		return fmt.Errorf("expect.http_status %d is not an HTTP status", s)
	}
	return nil
}

// readFile reads a file of the request relative to the case file.
func (c Case) readFile(name string) ([]byte, error) {
	p := name
	if !filepath.IsAbs(p) && c.File != "" {
		p = filepath.Join(filepath.Dir(c.File), name)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, output.Usage(err)
	}
	return b, nil
}

// loadConfig reads a flow's config.yaml: parameter key -> value, and in
// its flows: section the values of other flows (flow -> key -> value).
func loadConfig(file string) (map[string]string, map[string]map[string]string, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, nil, err
	}
	var raw map[string]any
	if err := decodeStrict(data, &raw); err != nil {
		return nil, nil, fileError(file, err)
	}
	scalars := func(where string, m map[string]any) (map[string]string, error) {
		out := make(map[string]string, len(m))
		for k, v := range m {
			switch v := v.(type) {
			case map[string]any, []any:
				return nil, fmt.Errorf("%s: %s%s: parameter values are scalars (key: value)", file, where, k)
			case nil:
				out[k] = ""
			default:
				out[k] = fmt.Sprint(v)
			}
		}
		return out, nil
	}
	var others map[string]map[string]string
	if f, ok := raw["flows"]; ok {
		delete(raw, "flows")
		flows, ok := f.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("%s: flows: a map of flow IDs to parameters", file)
		}
		others = map[string]map[string]string{}
		for flow, v := range flows {
			params, ok := v.(map[string]any)
			if !ok {
				return nil, nil, fmt.Errorf("%s: flows.%s: a map of parameters (key: value)", file, flow)
			}
			if others[flow], err = scalars("flows."+flow+".", params); err != nil {
				return nil, nil, err
			}
		}
	}
	cfg, err := scalars("", raw)
	if err != nil {
		return nil, nil, err
	}
	if len(cfg) == 0 && others != nil {
		cfg = nil
	}
	return cfg, others, nil
}
