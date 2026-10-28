package testrun

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/cpars-innovation/cpicli/internal/output"
	"gopkg.in/yaml.v3"
)

// Script cases test one script function with a message (the format agreed
// with CPI Studio, #13):
//
//	<dir>/<FlowId>/scripts/<script file>/<case>.yaml   a script step of a flow
//	<dir>/collections/<CollectionId>/<case>.yaml        a function of a script collection
//
// The flow cases of <dir>/<FlowId>/*.yaml are not affected: they are read
// one level higher.
const (
	// ScriptCollectionsDir is the folder of the script collection cases
	// (reserved: no flow can use it as its test folder).
	ScriptCollectionsDir = "collections"
	// scriptsDir is the folder of a flow's script cases.
	scriptsDir = "scripts"
)

// Script case kinds.
const (
	ScriptKindFlow       = "flow"
	ScriptKindCollection = "collection"
)

// ScriptCase is a test of a script function.
type ScriptCase struct {
	// Kind is flow or collection; Owner the flow or collection ID; Script
	// the script file of a flow case (a collection case finds the script
	// that defines the function). Name is the file name without .yaml.
	Kind   string `yaml:"-" json:"kind"`
	Owner  string `yaml:"-" json:"owner"`
	Script string `yaml:"-" json:"script,omitempty"`
	Name   string `yaml:"-" json:"name"`
	File   string `yaml:"-" json:"file"`
	// Root, when set, confines the files the case references to this
	// directory (see refPath); an MCP server sets its root.
	Root string `yaml:"-" json:"-"`

	Description string     `yaml:"description" json:"description,omitempty"`
	Tags        []string   `yaml:"tags" json:"tags,omitempty"`
	Skip        string     `yaml:"skip" json:"skip,omitempty"`
	Call        ScriptCall `yaml:"call" json:"call"`
	// Input is the message the function gets.
	Input ScriptInput `yaml:"input" json:"input"`
	// Params are XSLT parameters: only for mapping cases, refused here.
	Params map[string]string `yaml:"params" json:"params,omitempty"`
	// PD, ValueMappings, Secure and Keystore are what SAP's services answer
	// in the local run (test values only). A run on the tenant skips cases
	// that set them.
	PD            map[string]string      `yaml:"pd" json:"pd,omitempty"`
	ValueMappings map[string]string      `yaml:"value_mappings" json:"valueMappings,omitempty"`
	Secure        map[string]SecureEntry `yaml:"secure" json:"secure,omitempty"`
	Keystore      map[string]string      `yaml:"keystore" json:"keystore,omitempty"`
	Expect        ScriptExpect           `yaml:"expect" json:"expect"`
}

// ScriptCall is the function to call.
type ScriptCall struct {
	// Function defaults to processData.
	Function string `yaml:"function" json:"function,omitempty"`
	// Args are the arguments of the function (a collection function, or a
	// custom function of a flow's message mapping); without them the
	// function gets the message. MappingContext and Output parameters are
	// supplied by the runner; with Output, the returned value is the list of
	// values the function added.
	Args []any `yaml:"args" json:"args,omitempty"`
}

// ScriptInput is the message a script gets.
type ScriptInput struct {
	Body       string            `yaml:"body" json:"body,omitempty"`
	BodyFile   string            `yaml:"body_file" json:"bodyFile,omitempty"`
	Headers    map[string]string `yaml:"headers" json:"headers,omitempty"`
	Properties map[string]string `yaml:"properties" json:"properties,omitempty"`
}

// SecureEntry is a test credential: user and password, or a value (a
// secure parameter or token).
type SecureEntry struct {
	User     string `yaml:"user" json:"user,omitempty"`
	Password string `yaml:"password" json:"password,omitempty"`
	Value    string `yaml:"value" json:"value,omitempty"`
}

// ScriptThrows expects the call to fail.
type ScriptThrows struct {
	Class           string `yaml:"class" json:"class,omitempty"`
	MessageContains string `yaml:"message_contains" json:"messageContains,omitempty"`
}

// ScriptExpect is the expected outcome of a script case.
type ScriptExpect struct {
	// BodyEqualsFile (relative to the case) is compared structurally for
	// XML and JSON, as trimmed text otherwise.
	BodyEqualsFile  string            `yaml:"body_equals_file" json:"bodyEqualsFile,omitempty"`
	BodyContains    []string          `yaml:"body_contains" json:"bodyContains,omitempty"`
	BodyNotContains []string          `yaml:"body_not_contains" json:"bodyNotContains,omitempty"`
	BodyXPath       map[string]string `yaml:"body_xpath" json:"bodyXpath,omitempty"`
	BodyJSON        map[string]any    `yaml:"body_json" json:"bodyJson,omitempty"`
	// Headers and Properties after the call; null: must be absent.
	Headers    map[string]*string `yaml:"headers" json:"headers,omitempty"`
	Properties map[string]*string `yaml:"properties" json:"properties,omitempty"`
	// Returns is the return value of a collection function (any YAML
	// value, null included: HasReturns says whether it is set).
	Returns    any           `yaml:"returns" json:"returns,omitempty"`
	HasReturns bool          `yaml:"-" json:"-"`
	Throws     *ScriptThrows `yaml:"throws" json:"throws,omitempty"`
	// Log are message log entries: custom header or property name -> value.
	Log map[string]string `yaml:"log" json:"log,omitempty"`
	// MaxDuration is the longest the call may take (500ms, 2s, 1m).
	MaxDuration string `yaml:"max_duration" json:"maxDuration,omitempty"`
}

// UnmarshalYAML decodes strictly (the outer decoder's KnownFields does not
// reach a custom unmarshaler) and records whether returns is set, null
// included.
func (e *ScriptExpect) UnmarshalYAML(n *yaml.Node) error {
	type plain ScriptExpect
	known := yamlKeys(reflect.TypeFor[plain]())
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			if !known[k.Value] {
				return &yaml.TypeError{Errors: []string{fmt.Sprintf("line %d: field %s not found in type testrun.ScriptExpect", k.Line, k.Value)}}
			}
			if k.Value == "returns" {
				e.HasReturns = true
			}
		}
	}
	var p plain
	if err := n.Decode(&p); err != nil {
		return err
	}
	has := e.HasReturns
	*e = ScriptExpect(p)
	e.HasReturns = has
	return nil
}

// yamlKeys are the YAML keys of a struct type.
func yamlKeys(t reflect.Type) map[string]bool {
	out := map[string]bool{}
	for f := range t.Fields() {
		if name, _, _ := strings.Cut(f.Tag.Get("yaml"), ","); name != "" && name != "-" {
			out[name] = true
		}
	}
	return out
}

// Key names the function under test: <Owner>/<Script> for a flow script,
// <Owner> for a collection.
func (c ScriptCase) Key() string {
	if c.Kind == ScriptKindFlow {
		return c.Owner + "/" + c.Script
	}
	return c.Owner
}

// Suite is the Flow of the case's CaseResult: script:<FlowId>/<script> or
// collection:<CollectionId>.
func (c ScriptCase) Suite() string {
	if c.Kind == ScriptKindFlow {
		return "script:" + c.Key()
	}
	return "collection:" + c.Owner
}

// Function is the function the case calls.
func (c ScriptCase) Function() string {
	if c.Call.Function == "" {
		return "processData"
	}
	return c.Call.Function
}

// InjectsValues reports the service answers the case sets (pd,
// value_mappings, secure, keystore): only a local run can provide them.
func (c ScriptCase) InjectsValues() []string {
	var out []string
	for name, n := range map[string]int{"pd": len(c.PD), "value_mappings": len(c.ValueMappings), "secure": len(c.Secure), "keystore": len(c.Keystore)} {
		if n > 0 {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// LoadScriptCases reads the script cases below dir strictly (unknown keys
// and invalid YAML are usage errors with file:line). Filter.Flows matches
// the key (<FlowId>/<script> or <CollectionId>) or the owner, Cases the
// case name, Tags the tags. No cases: an empty list.
func LoadScriptCases(dir string, filter Filter) ([]ScriptCase, error) {
	var files []string
	for _, pattern := range []string{
		filepath.Join(dir, "*", scriptsDir, "*", "*.yaml"), filepath.Join(dir, "*", scriptsDir, "*", "*.yml"),
		filepath.Join(dir, ScriptCollectionsDir, "*", "*.yaml"), filepath.Join(dir, ScriptCollectionsDir, "*", "*.yml"),
	} {
		m, _ := filepath.Glob(pattern)
		files = append(files, m...)
	}
	sort.Strings(files)
	var cases []ScriptCase
	var problems []string
	for _, f := range files {
		rel, _ := filepath.Rel(dir, f)
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if parts[0] == XSLTDir {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var c ScriptCase
		if err := decodeStrict(data, &c); err != nil {
			problems = append(problems, fileError(f, err).Error())
			continue
		}
		c.File = f
		c.Name = strings.TrimSuffix(strings.TrimSuffix(filepath.Base(f), ".yaml"), ".yml")
		if parts[0] == ScriptCollectionsDir && len(parts) == 3 {
			c.Kind, c.Owner = ScriptKindCollection, parts[1]
		} else {
			c.Kind, c.Owner, c.Script = ScriptKindFlow, parts[0], parts[2]
		}
		if err := c.validate(); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", f, err))
			continue
		}
		if (matchAny(filter.Flows, c.Key()) || matchAny(filter.Flows, c.Owner)) && matchAny(filter.Cases, c.Name) &&
			(len(filter.Tags) == 0 || matchAny(filter.Tags, c.Tags...)) {
			cases = append(cases, c)
		}
	}
	if len(problems) > 0 {
		return nil, output.Usagef("invalid script case file(s):\n%s", strings.Join(problems, "\n"))
	}
	sort.SliceStable(cases, func(i, j int) bool {
		if cases[i].Key() != cases[j].Key() {
			return cases[i].Key() < cases[j].Key()
		}
		return cases[i].Name < cases[j].Name
	})
	return cases, nil
}

func (c *ScriptCase) validate() error {
	e := c.Expect
	switch {
	case c.Input.Body != "" && c.Input.BodyFile != "":
		return fmt.Errorf("input: use either body or body_file")
	case len(c.Params) > 0:
		return fmt.Errorf("params are XSLT parameters: they do not apply to script cases")
	case e.BodyEqualsFile == "" && len(e.BodyContains) == 0 && len(e.BodyNotContains) == 0 && len(e.BodyXPath) == 0 &&
		len(e.BodyJSON) == 0 && e.Headers == nil && e.Properties == nil && !e.HasReturns && e.Throws == nil &&
		len(e.Log) == 0 && e.MaxDuration == "":
		return fmt.Errorf("expect: at least one expectation")
	}
	if e.MaxDuration != "" {
		if d, err := time.ParseDuration(e.MaxDuration); err != nil || d <= 0 || strings.ContainsAny(e.MaxDuration, ".hun") {
			return fmt.Errorf("expect.max_duration %q: a number with ms, s or m", e.MaxDuration)
		}
	}
	for alias, s := range c.Secure {
		if (s.Value != "") == (s.User != "" || s.Password != "") {
			return fmt.Errorf("secure.%s: either user and password, or value", alias)
		}
	}
	for alias, v := range c.Keystore {
		if v != "generate:rsa" && v != "generate:cert" && !strings.Contains(v, "-----BEGIN ") {
			return fmt.Errorf("keystore.%s: PEM text, generate:rsa or generate:cert", alias)
		}
	}
	for _, f := range []string{c.Input.BodyFile, e.BodyEqualsFile} {
		if f == "" {
			continue
		}
		if filepath.IsAbs(f) {
			return fmt.Errorf("%s must be relative to the case file", f)
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(c.File), f)); err != nil {
			return fmt.Errorf("%s not found next to the case file", f)
		}
	}
	return nil
}
