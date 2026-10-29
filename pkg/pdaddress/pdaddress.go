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
	"errors"
	"fmt"
	"io"
	"net/url"
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
// flow archive as written ({{parameter}} not resolved). The models are read
// as XML (comments and other markup are not properties), and a sender with
// a repeated or ambiguous address property makes the archive unreadable:
// the access scope must not guess which address a flow listens on
// (cpicli#65).
func FromArchive(zipped []byte) ([]string, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipped), int64(len(zipped)))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, f := range zr.File {
		if !strings.HasSuffix(f.Name, ".iflw") || f.UncompressedSize64 > 64<<20 {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		model, err := io.ReadAll(io.LimitReader(rc, 64<<20))
		rc.Close()
		if err != nil {
			return nil, err
		}
		addrs, err := ProcessDirectSenders(model)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.Name, err)
		}
		out = append(out, addrs...)
	}
	return out, nil
}

// ProcessDirectSenders reads a model (.iflw) as XML and returns the address
// of each ProcessDirect sender. A property repeated within one message flow,
// or a sender with more than one address property, is an error.
func ProcessDirectSenders(model []byte) ([]string, error) {
	dec := xml.NewDecoder(bytes.NewReader(model))
	dec.Strict = true
	var (
		out      []string
		inFlow   bool
		props    map[string]string
		key, val *strings.Builder
		field    *strings.Builder
		inProp   bool
	)
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
			switch {
			case t.Name.Local == "messageFlow":
				if inFlow {
					return nil, errors.New("nested messageFlow")
				}
				inFlow, props = true, map[string]string{}
			case inFlow && t.Name.Local == "property":
				inProp, key, val = true, &strings.Builder{}, &strings.Builder{}
			case inProp && t.Name.Local == "key":
				field = key
			case inProp && t.Name.Local == "value":
				field = val
			}
		case xml.CharData:
			if field != nil {
				field.Write(t)
			}
		case xml.EndElement:
			switch {
			case t.Name.Local == "key" || t.Name.Local == "value":
				field = nil
			case inProp && t.Name.Local == "property":
				inProp = false
				if _, dup := props[key.String()]; dup {
					return nil, fmt.Errorf("property %s repeated in a message flow", key.String())
				}
				props[key.String()] = val.String()
			case inFlow && t.Name.Local == "messageFlow":
				inFlow = false
				addr, err := processDirectAddress(props)
				if err != nil {
					return nil, err
				}
				if addr != "" {
					out = append(out, addr)
				}
			}
		}
	}
	return out, nil
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
