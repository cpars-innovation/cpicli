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
	"encoding/json"
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
// flow archive as written ({{parameter}} not resolved).
func FromArchive(zipped []byte) ([]string, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipped), int64(len(zipped)))
	if err != nil {
		return nil, err
	}
	var raw []string
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
		for _, s := range Senders(string(model)) {
			if s.Adapter == "ProcessDirect" {
				raw = append(raw, s.Value)
			}
		}
	}
	return raw, nil
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
// a flow's active designtime archive and configuration through fetch.
func Reader(fetch Fetch) func(ctx context.Context, id string) ([]string, error) {
	return func(ctx context.Context, id string) ([]string, error) {
		art := "/api/v1/IntegrationDesigntimeArtifacts(Id=" + key(id) + ",Version='active')"
		zipped, err := fetch(ctx, art+"/$value")
		if err != nil {
			return nil, err
		}
		raw, err := FromArchive(zipped)
		if err != nil || len(raw) == 0 {
			return nil, err
		}
		var perr error
		out := Resolve(raw, func() map[string]string {
			m := map[string]string{}
			b, err := fetch(ctx, art+"/Configurations")
			if err != nil {
				perr = err
				return m
			}
			var v struct {
				D struct {
					Results []struct{ ParameterKey, ParameterValue string } `json:"results"`
				} `json:"d"`
			}
			if err := json.Unmarshal(b, &v); err != nil {
				perr = fmt.Errorf("reading the configuration of %s: %w", id, err)
				return m
			}
			for _, r := range v.D.Results {
				m[r.ParameterKey] = r.ParameterValue
			}
			return m
		})
		return out, perr
	}
}

func key(s string) string { return "'" + url.PathEscape(strings.ReplaceAll(s, "'", "''")) + "'" }
