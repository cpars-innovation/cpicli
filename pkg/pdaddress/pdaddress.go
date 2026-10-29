// Package pdaddress reads the sender addresses of integration flows, and the
// ProcessDirect addresses an access scope needs to decide test-harness calls.
// It depends on the standard library only, so that programs embedding
// pkg/scope (the cpictl gateway) can enable the test harness without the API
// client (cpicli#63).
package pdaddress

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// Sender is the address property of a sender adapter in an integration flow
// model.
type Sender struct {
	Adapter    string
	Value      string // unescaped
	Start, End int    // byte range of the escaped value in the model
}

// AddressKeys are the adapter properties that hold the address of an
// endpoint, in the order they are chosen.
var AddressKeys = []string{"urlPath", "address", "httpAddressWithoutQuery", "path", "directory", "QueueName_inbound", "QueueName_outbound", "queueName"}

var (
	reMessageFlow    = regexp.MustCompile(`(?s)<(?:\w+:)?messageFlow\b.*?</(?:\w+:)?messageFlow>`)
	reModelProperty  = regexp.MustCompile(`(?s)<key>([^<]*)</key>\s*<value>([^<]*)</value>`)
	reWholeParameter = regexp.MustCompile(`^\{\{([^{}]+)\}\}$`)
)

// Senders returns the address of every sender adapter in a model (.iflw)
// that has one.
func Senders(model string) []Sender {
	var res []Sender
	for _, mf := range reMessageFlow.FindAllStringIndex(model, -1) {
		block := model[mf[0]:mf[1]]
		props := map[string][]int{}
		values := map[string]string{}
		for _, m := range reModelProperty.FindAllStringSubmatchIndex(block, -1) {
			key := block[m[2]:m[3]]
			props[key] = []int{mf[0] + m[4], mf[0] + m[5]}
			values[key] = XMLUnescape(block[m[4]:m[5]])
		}
		if values["direction"] != "Sender" {
			continue
		}
		adapter := values["ComponentType"]
		if adapter == "" {
			for part := range strings.SplitSeq(values["cmdVariantUri"], "/") {
				if v, ok := strings.CutPrefix(part, "cname::"); ok {
					adapter = strings.TrimPrefix(v, "sap:")
				}
			}
		}
		for _, k := range AddressKeys {
			if strings.TrimSpace(values[k]) != "" {
				res = append(res, Sender{Adapter: adapter, Value: values[k], Start: props[k][0], End: props[k][1]})
				break
			}
		}
	}
	return res
}

// XMLUnescape undoes the escaping of a model property value.
func XMLUnescape(s string) string {
	return strings.NewReplacer("&lt;", "<", "&gt;", ">", "&quot;", `"`, "&apos;", "'", "&#xA;", "\n", "&#xD;", "\r", "&#x9;", "\t", "&amp;", "&").Replace(s)
}

// FromArchive returns the ProcessDirect sender addresses of an integration
// flow archive as written ({{parameter}} not resolved). Only the flow's model
// counts: the one .iflw directly in src/main/resources/scenarioflows/
// integrationflow/. An archive without one, with several, with repeated
// entry names or over the size limits is an error, as is a model with a
// repeated or ambiguous address: the access scope must not guess which
// address a flow listens on (cpicli#65).
func FromArchive(zipped []byte) ([]string, error) {
	if len(zipped) > maxArchive {
		return nil, fmt.Errorf("archive larger than %d bytes", maxArchive)
	}
	zr, err := zip.NewReader(bytes.NewReader(zipped), int64(len(zipped)))
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	var model *zip.File
	for _, f := range zr.File {
		name := f.Name
		// names a loader could read differently are refused, not skipped
		// (cpicli#65): ./, .., //, \, a leading /, and a name in the zip's
		// central directory that its local header does not repeat
		if !canonicalEntry(name) {
			return nil, fmt.Errorf("archive entry %q is not a plain path", name)
		}
		if err := sameLocalName(zipped, f); err != nil {
			return nil, err
		}
		key := strings.ToLower(strings.TrimSuffix(name, "/"))
		if names[key] {
			return nil, fmt.Errorf("archive entry %s repeated", name)
		}
		names[key] = true
		lower := strings.ToLower(name)
		if !strings.Contains(lower, ".iflw") {
			continue
		}
		// a model counts only directly in modelDir, spelled exactly so; any
		// other .iflw (another folder, another case, a folder named *.iflw)
		// makes the archive ambiguous
		dir, file := path.Split(name)
		if dir != modelDir || !strings.HasSuffix(file, ".iflw") || strings.Count(lower, ".iflw") != 1 {
			return nil, fmt.Errorf("archive entry %q: a model outside %s", name, modelDir)
		}
		if model != nil {
			return nil, fmt.Errorf("archive with more than one model in %s", modelDir)
		}
		model = f
	}
	if model == nil {
		return nil, fmt.Errorf("archive without a model in %s", modelDir)
	}
	if model.UncompressedSize64 > maxModel {
		return nil, fmt.Errorf("model larger than %d bytes", maxModel)
	}
	rc, err := model.Open()
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(rc, maxModel+1))
	rc.Close()
	if err != nil {
		return nil, err
	}
	if len(data) > maxModel {
		return nil, fmt.Errorf("model larger than %d bytes", maxModel)
	}
	return ProcessDirectSenders(data)
}

// canonicalEntry: a relative path without empty, . or .. segments and
// without backslashes (a directory entry ends in one /).
func canonicalEntry(name string) bool {
	if name == "" || strings.ContainsAny(name, "\\\x00") || strings.HasPrefix(name, "/") {
		return false
	}
	for _, seg := range strings.Split(strings.TrimSuffix(name, "/"), "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// sameLocalName checks that the local file header of f carries the name the
// central directory gives: a loader that reads local headers must see the
// same entry.
func sameLocalName(zipped []byte, f *zip.File) error {
	data, err := f.DataOffset()
	if err != nil {
		return err
	}
	n := len(f.Name)
	le16 := func(p int) int { return int(zipped[p]) | int(zipped[p+1])<<8 }
	for extra := 0; extra <= 0xffff; extra++ {
		p := int(data) - 30 - n - extra
		if p < 0 {
			break
		}
		if zipped[p] == 'P' && zipped[p+1] == 'K' && zipped[p+2] == 3 && zipped[p+3] == 4 &&
			le16(p+26) == n && le16(p+28) == extra {
			if string(zipped[p+30:p+30+n]) != f.Name {
				break
			}
			return nil
		}
	}
	return fmt.Errorf("archive entry %q: the local header names another entry", f.Name)
}

const (
	modelDir   = "src/main/resources/scenarioflows/integrationflow/"
	maxArchive = 256 << 20
	maxModel   = 64 << 20
)

// The namespaces of an integration flow model.
const (
	nsBPMN = "http://www.omg.org/spec/BPMN/20100524/MODEL"
	nsIFL  = "http:///com.sap.ifl.model/Ifl.xsd"
)

// ProcessDirectSenders reads a model (.iflw) as XML and returns the address
// of each ProcessDirect sender. Only properties at their place count:
// bpmn2:definitions/bpmn2:collaboration/bpmn2:messageFlow/
// bpmn2:extensionElements/ifl:property/key|value (by namespace URI), with
// text-only key and value. A property repeated within one message flow, a
// sender with more than one address property, or markup inside a key or
// value is an error.
func ProcessDirectSenders(model []byte) ([]string, error) {
	dec := xml.NewDecoder(bytes.NewReader(model))
	dec.Strict = true
	want := []xml.Name{{Space: nsBPMN, Local: "definitions"}, {Space: nsBPMN, Local: "collaboration"},
		{Space: nsBPMN, Local: "messageFlow"}, {Space: nsBPMN, Local: "extensionElements"}, {Space: nsIFL, Local: "property"}}
	var (
		stack          []xml.Name
		out            []string
		props          map[string]string
		key, val       strings.Builder
		field          *strings.Builder
		keys, vals     int // per property
		roots, collabs int
	)
	at := func(n int) bool { // the stack is the first n names of want
		if len(stack) != n {
			return false
		}
		for i := range n {
			if stack[i] != want[i] {
				return false
			}
		}
		return true
	}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if field != nil {
				return nil, fmt.Errorf("markup inside a property's key or value")
			}
			stack = append(stack, t.Name)
			switch {
			case len(stack) == 1:
				if roots++; roots > 1 {
					return nil, fmt.Errorf("more than one root element")
				}
			case at(2):
				if collabs++; collabs > 1 {
					return nil, fmt.Errorf("more than one collaboration")
				}
			case at(3):
				props = map[string]string{}
			case at(5):
				key.Reset()
				val.Reset()
				keys, vals = 0, 0
			case len(stack) == 6 && at5(stack, want) && t.Name == (xml.Name{Local: "key"}):
				if keys++; keys > 1 {
					return nil, fmt.Errorf("a property with more than one key")
				}
				field = &key
			case len(stack) == 6 && at5(stack, want) && t.Name == (xml.Name{Local: "value"}):
				if vals++; vals > 1 {
					return nil, fmt.Errorf("a property with more than one value")
				}
				field = &val
			}
		case xml.CharData:
			if field != nil {
				field.Write(t)
			}
		case xml.Comment, xml.ProcInst, xml.Directive:
			if field != nil {
				return nil, fmt.Errorf("markup inside a property's key or value")
			}
		case xml.EndElement:
			switch {
			case field != nil:
				field = nil
			case at(5):
				if keys != 1 || vals != 1 {
					return nil, fmt.Errorf("a property without exactly one key and one value")
				}
				if _, dup := props[key.String()]; dup {
					return nil, fmt.Errorf("property %s repeated in a message flow", key.String())
				}
				props[key.String()] = val.String()
			case at(3):
				addr, err := processDirectAddress(props)
				if err != nil {
					return nil, err
				}
				if addr != "" {
					out = append(out, addr)
				}
			}
			stack = stack[:len(stack)-1]
		}
	}
	return out, nil
}

// at5 reports whether the first five names of stack are want's.
func at5(stack, want []xml.Name) bool {
	for i := range 5 {
		if stack[i] != want[i] {
			return false
		}
	}
	return true
}

// processDirectAddress is the address of a ProcessDirect sender ("" for
// other message flows).
func processDirectAddress(props map[string]string) (string, error) {
	if props["direction"] != "Sender" {
		return "", nil
	}
	adapter := props["ComponentType"]
	if adapter == "" {
		for part := range strings.SplitSeq(props["cmdVariantUri"], "/") {
			if v, ok := strings.CutPrefix(part, "cname::"); ok {
				adapter = strings.TrimPrefix(v, "sap:")
			}
		}
	}
	if adapter != "ProcessDirect" {
		return "", nil
	}
	var addrs []string
	for _, k := range AddressKeys {
		if v := strings.TrimSpace(props[k]); v != "" {
			addrs = append(addrs, v)
		}
	}
	switch len(addrs) {
	case 0:
		return "", nil
	case 1:
		return addrs[0], nil
	}
	return "", fmt.Errorf("a ProcessDirect sender with %d address properties", len(addrs))
}

// Resolve replaces addresses that are a whole {{parameter}} by the
// parameter's configured value (params is called once, when needed). Empty
// addresses are dropped.
func Resolve(raw []string, params func() map[string]string) []string {
	var p map[string]string
	out := make([]string, 0, len(raw))
	for _, a := range raw {
		if m := reWholeParameter.FindStringSubmatch(a); m != nil {
			if p == nil {
				p = params()
			}
			a = p[m[1]]
		}
		if a != "" {
			out = append(out, a)
		}
	}
	return out
}

// Fetch reads a tenant API path (GET) and returns the body of a 200 answer.
type Fetch func(ctx context.Context, path string) ([]byte, error)

// Reader returns a function for scope.Guard.ProcessDirectAddresses that reads
// a flow's active designtime archive through fetch. An address set by a
// configuration parameter ({{...}}) is left out: a configuration change
// takes effect only when the flow is deployed again, and nothing tells which
// value the running flow uses, so the access scope must not rely on it
// (cpicli#65). A flow whose archive is ambiguous is an error.
func Reader(fetch Fetch) func(ctx context.Context, id string) ([]string, error) {
	return func(ctx context.Context, id string) ([]string, error) {
		zipped, err := fetch(ctx, "/api/v1/IntegrationDesigntimeArtifacts(Id="+key(id)+",Version='active')/$value")
		if err != nil {
			return nil, err
		}
		raw, err := FromArchive(zipped)
		if err != nil {
			return nil, err
		}
		return Fixed(raw), nil
	}
}

// Fixed keeps the addresses that contain no {{parameter}}.
func Fixed(raw []string) []string {
	var out []string
	for _, a := range raw {
		if a != "" && !strings.Contains(a, "{{") {
			out = append(out, a)
		}
	}
	return out
}

func key(s string) string { return "'" + url.PathEscape(strings.ReplaceAll(s, "'", "''")) + "'" }
