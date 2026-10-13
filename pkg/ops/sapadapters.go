package ops

import (
	"bytes"
	"encoding/xml"
	"io"
	"path"
	"regexp"
	"strings"
)

// ODataChannel is what discovery reads from an OData channel: the OData
// receiver (HCIOData V2/V4), SuccessFactors OData V2/V4 and the OData
// senders (ODataSender, ODATA2).
type ODataChannel struct {
	// Version is V2 or V4 (the variant's message protocol).
	Version string `json:"version,omitempty"`
	// Operation as configured, e.g. Query(GET) (may be a {{parameter}}).
	Operation string `json:"operation,omitempty"`
	// ResourcePath is the configured resource path (Products,
	// A_SalesOrder('1')/to_Item); EntitySet its first segment without key
	// predicate (empty when the path is computed or not resolvable).
	ResourcePath string `json:"resourcePath,omitempty"`
	EntitySet    string `json:"entitySet,omitempty"`
	// QueryOptions are the query options and custom query options as
	// configured; QueryOptionKeys their keys ($select, $filter, ...).
	QueryOptions    string   `json:"queryOptions,omitempty"`
	QueryOptionKeys []string `json:"queryOptionKeys,omitempty"`
	// Paging is true when the channel reads in pages (PageSize per page).
	Paging   bool   `json:"paging,omitempty"`
	PageSize string `json:"pageSize,omitempty"`
	// Batch is true when the channel sends $batch requests.
	Batch bool `json:"batch,omitempty"`
	// EDMX is the service model under edmx/ the channel was built from.
	EDMX string `json:"edmx,omitempty"`
	// Host is the host of the service URL (parameters resolved).
	Host string `json:"host,omitempty"`
}

// IDocChannel is what discovery reads from an IDoc (SOAP) channel.
type IDocChannel struct {
	Direction string `json:"direction"`
	// Path is the address path: /sap/bc/srt/idoc for a receiver, the
	// endpoint path for a sender.
	Path string `json:"path,omitempty"`
	// SAPClient is the literal sap-client of a receiver address.
	SAPClient   string `json:"sapClient,omitempty"`
	ContentType string `json:"contentType,omitempty"`
}

// IDocType is an IDoc type (basic type or extension, e.g. ORDERS05,
// ZORDERS05) a flow handles, with where it was found.
type IDocType struct {
	Type    string   `json:"type"`
	Sources []string `json:"sources"`
}

// variantPart returns a part of a cmdVariantUri (mp, tp, direction).
func variantPart(uri, part string) string {
	for p := range strings.SplitSeq(uri, "/") {
		if v, ok := strings.CutPrefix(p, part+"::"); ok {
			return v
		}
	}
	return ""
}

func truthy(v string) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	return v == "1" || v == "true"
}

// ODataChannelOf reads an OData channel from the properties of an adapter
// (message flow); nil when it is not an OData channel.
func ODataChannelOf(p map[string]string) *ODataChannel {
	mp := variantPart(p["cmdVariantUri"], "mp")
	if mp == "" {
		mp = p["MessageProtocol"]
	}
	if !strings.HasPrefix(mp, "OData") {
		return nil
	}
	c := &ODataChannel{Version: strings.TrimSpace(strings.TrimPrefix(mp, "OData")), Operation: p["operation"],
		ResourcePath: firstNonEmpty(p["resourcePath"], p["entitySet"])}
	var opts []string
	for _, k := range []string{"queryOptions", "customQueryOptions"} {
		if v := strings.TrimSpace(p[k]); v != "" {
			opts = append(opts, strings.TrimPrefix(v, "?"))
		}
	}
	c.QueryOptions = strings.Join(opts, "&")
	c.QueryOptionKeys = queryOptionKeys(c.QueryOptions)
	c.Paging = truthy(p["pagination"])
	if c.Paging {
		c.PageSize = firstNonEmpty(p["odatapagesize"], p["pageSize"])
	}
	c.Batch = truthy(p["enableBatchProcessing"]) || strings.Contains(strings.ToLower(c.Operation), "batch")
	for _, v := range p {
		if i := strings.Index(v, "edmx/"); i >= 0 && strings.HasSuffix(v, ".edmx") {
			c.EDMX = v[i:]
			break
		}
	}
	return c
}

func queryOptionKeys(opts string) []string {
	var keys []string
	for part := range strings.SplitSeq(opts, "&") {
		k, _, _ := strings.Cut(part, "=")
		if k = strings.TrimSpace(k); k != "" {
			keys = append(keys, k)
		}
	}
	if keys == nil {
		return nil
	}
	return sortedUnique(keys)
}

// entitySet is the first segment of a resource path without key predicate.
func entitySet(resourcePath string) string {
	p := strings.TrimLeft(strings.TrimSpace(resourcePath), "/")
	if p == "" || strings.Contains(p, "{{") || strings.Contains(p, "${") {
		return ""
	}
	if i := strings.IndexAny(p, "/(?"); i >= 0 {
		p = p[:i]
	}
	return p
}

// OData operation kinds (ODataOperationKind).
const (
	ODataRead  = "read"
	ODataWrite = "write"
)

// ODataOperationKind says whether an OData operation reads (Query, Read:
// GET) or writes (Create, Update, Merge, Patch, Delete, Function Import,
// $batch); empty when unknown, e.g. a {{parameter}}.
func ODataOperationKind(operation string, batch bool) string {
	op := strings.ToLower(operation)
	switch {
	case batch || strings.Contains(op, "batch"):
		return ODataWrite
	case strings.Contains(op, "{{") || strings.Contains(op, "${"):
		return ""
	case strings.Contains(op, "function"):
		return ODataWrite
	case strings.HasPrefix(op, "query") || strings.HasPrefix(op, "read") || strings.Contains(op, "(get)"):
		return ODataRead
	}
	for _, w := range []string{"create", "post", "update", "put", "merge", "patch", "delete", "upsert"} {
		if strings.Contains(op, w) {
			return ODataWrite
		}
	}
	return ""
}

// IsIDocChannel reports an IDoc (SOAP) adapter.
func IsIDocChannel(p map[string]string) bool {
	_, cname := variant(p["cmdVariantUri"])
	return p["ComponentType"] == "IDOC" || cname == "sap:IDOC" || variantPart(p["cmdVariantUri"], "mp") == "IDoc SOAP"
}

// IDocChannelOf reads an IDoc channel from the properties of an adapter; nil
// when it is not an IDoc channel. Path and SAP client come from the address
// (see idocAddress).
func IDocChannelOf(p map[string]string) *IDocChannel {
	if !IsIDocChannel(p) {
		return nil
	}
	return &IDocChannel{Direction: p["direction"], ContentType: p["IDocContentType"]}
}

var reSAPClientLiteral = regexp.MustCompile(`^[0-9]{3}$`)

// SplitAddress splits an address into host, path and query without
// requiring a valid URL (addresses hold {{parameters}} and placeholders
// such as <ServerName>:<port>). A plain path has no host.
func SplitAddress(addr string) (host, p, query string) {
	addr = strings.TrimSpace(addr)
	rest, query, _ := strings.Cut(addr, "?")
	if _, after, ok := strings.Cut(rest, "://"); ok {
		host, p, _ = strings.Cut(after, "/")
		if p != "" || strings.HasSuffix(after, "/") {
			p = "/" + p
		}
		return host, p, query
	}
	return "", rest, query
}

// QueryValue returns a parameter of a query string (empty when absent).
func QueryValue(query, key string) string {
	for part := range strings.SplitSeq(query, "&") {
		if k, v, _ := strings.Cut(part, "="); strings.EqualFold(strings.TrimSpace(k), key) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// resolveSAPChannels fills what needs the parameter values and the
// resource files: OData host, entity set and EDMX, IDoc path and client.
func (f *IFlowFacts) resolveSAPChannels(edmx []string) {
	for _, list := range [][]Trigger{f.Triggers, f.Receivers} {
		for i := range list {
			t := &list[i]
			addr, _, ok := resolveAddress(t.Address, f.AddressParameters)
			if o := t.OData; o != nil {
				if ok {
					o.Host = urlHost(addr)
				}
				if rp, _, rok := resolveAddress(o.ResourcePath, f.AddressParameters); rok {
					o.EntitySet = entitySet(rp)
				}
				if o.EDMX == "" && ok {
					o.EDMX = edmxFor(addr, edmx)
				}
			}
			if d := t.IDoc; d != nil {
				host, p, query := SplitAddress(addr)
				switch {
				case host == "" && d.Direction == "Sender":
					d.Path = addr // a sender's address is its path
				case strings.HasPrefix(p, "/"):
					d.Path = p
				}
				if c := QueryValue(query, "sap-client"); reSAPClientLiteral.MatchString(c) {
					d.SAPClient = c
				}
			}
		}
	}
}

var reNonAlnum = regexp.MustCompile(`[^A-Za-z0-9]+`)

// edmxFor finds the EDMX the OData wizard stored for a service URL: it
// names the file after the URL without scheme
// (http://services.odata.org/V2/Northwind/Northwind.svc/ ->
// edmx/services_odata_org_V2_Northwind_Northwind_svc.edmx).
func edmxFor(addr string, edmx []string) string {
	_, rest, ok := strings.Cut(addr, "://")
	if !ok {
		return ""
	}
	rest, _, _ = strings.Cut(rest, "?")
	want := strings.Trim(reNonAlnum.ReplaceAllString(rest, "_"), "_")
	for _, e := range edmx {
		if strings.EqualFold(strings.TrimSuffix(path.Base(e), path.Ext(e)), want) {
			return e
		}
	}
	return ""
}

// IDoc type heuristics. An IDoc type named in an explicit IDoc context (an
// XPath /<TYPE>/IDOC, an IDOCTYP field) may be any SAP name ending in two
// digits (CRMXIF_PARTNER_SAVE_M03); a name taken from a file or schema
// element must look like a basic type or extension (ORDERS05, ZORDERS05)
// and the file must contain the control record EDI_DC40.
var (
	reIDocPath     = regexp.MustCompile(`/(?:[\w-]+:)?([A-Z][A-Z0-9_]{1,28}[0-9]{2})/(?:[\w-]+:)?IDOC\b`)
	reIDocTypField = regexp.MustCompile(`IDOCTYP[})\]]?\s*(?:>|=|!=|\beq\b|\bne\b)\s*['"]?([A-Z][A-Z0-9_]{1,28}[0-9]{2})\b`)
	reIDocTypeName = regexp.MustCompile(`^[A-Z]{4,8}[0-9]{2}$`)
	idocTypeFiles  = map[string]bool{".xsd": true, ".wsdl": true, ".mmap": true, ".xsl": true, ".xslt": true}
)

// idocTypesIn returns the IDoc types named in explicit IDoc contexts of a
// text (a property value, a condition, a mapping).
func idocTypesIn(text string) []string {
	var out []string
	for _, re := range []*regexp.Regexp{reIDocPath, reIDocTypField} {
		for _, m := range re.FindAllStringSubmatch(text, -1) {
			if m[1] != "EDI_DC40" {
				out = append(out, m[1])
			}
		}
	}
	return out
}

// idocTypesInFile returns the IDoc types of a mapping or schema (rel is its
// path below src/main/resources).
func idocTypesInFile(rel string, data []byte) []string {
	if !idocTypeFiles[strings.ToLower(path.Ext(rel))] {
		return nil
	}
	types := idocTypesIn(string(data))
	if !bytes.Contains(data, []byte("EDI_DC40")) {
		return types
	}
	name := path.Base(rel)
	for _, tok := range reNonAlnum.Split(strings.TrimSuffix(name, path.Ext(name)), -1) {
		if reIDocTypeName.MatchString(tok) {
			types = append(types, tok)
		}
	}
	if ext := strings.ToLower(path.Ext(rel)); ext == ".xsd" || ext == ".wsdl" {
		types = append(types, topLevelElements(data)...)
	}
	return types
}

// topLevelElements returns the names of the global element declarations of
// a schema that look like IDoc types (an IDoc schema declares the type as
// its root: <xsd:element name="ORDERS05"> with the IDOC element inside).
func topLevelElements(data []byte) []string {
	var out []string
	dec := xml.NewDecoder(bytes.NewReader(data))
	depthInSchema := -1
	depth := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF || err != nil {
			return out
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "schema" {
				depthInSchema = depth
			} else if t.Name.Local == "element" && depthInSchema >= 0 && depth == depthInSchema+1 {
				for _, a := range t.Attr {
					if a.Name.Local == "name" && reIDocTypeName.MatchString(a.Value) {
						out = append(out, a.Value)
					}
				}
			}
			depth++
		case xml.EndElement:
			depth--
			if depth == depthInSchema {
				depthInSchema = -1
			}
		}
	}
}

func (f *IFlowFacts) addIDocTypes(source string, types []string) {
	if len(types) == 0 {
		return
	}
	if f.idocTypes == nil {
		f.idocTypes = map[string]map[string]bool{}
	}
	for _, t := range types {
		if f.idocTypes[t] == nil {
			f.idocTypes[t] = map[string]bool{}
		}
		f.idocTypes[t][source] = true
	}
}

// collectIDocTypes moves the IDoc types found into IDocTypes, sorted.
func (f *IFlowFacts) collectIDocTypes() {
	for _, t := range sortedMapKeys(f.idocTypes) {
		sources := sortedMapKeys(f.idocTypes[t])
		f.IDocTypes = append(f.IDocTypes, IDocType{Type: t, Sources: sources})
	}
	f.idocTypes = nil
}

// HasIDocChannel reports whether the flow has an IDoc channel of a direction
// (Sender, Receiver).
func (f *IFlowFacts) HasIDocChannel(direction string) bool {
	for _, t := range append(append([]Trigger{}, f.Triggers...), f.Receivers...) {
		if t.IDoc != nil && t.IDoc.Direction == direction {
			return true
		}
	}
	return false
}
