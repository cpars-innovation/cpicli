package lint

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/cpars-innovation/cpicli/pkg/iflow"
	"github.com/cpars-innovation/cpicli/pkg/ops"
)

// Rules for the SAP adapters: OData (HCIOData, SuccessFactors OData) and
// IDoc. The adapter properties are the ones the Web UI writes into the
// .iflw (operation, resourcePath, queryOptions, pagination, address, ...).

// sapChannel is an OData or IDoc channel of a model with the step it is
// connected to.
type sapChannel struct {
	file  string
	m     *iflow.Model
	ad    *iflow.Element
	step  *iflow.Element // nil when the channel ends at an event
	odata *ops.ODataChannel
	idoc  *ops.IDocChannel
}

func sapChannels(a *lintArtifact, direction string) []sapChannel {
	var out []sapChannel
	for _, file := range sortedModelFiles(a) {
		m := a.Models[file]
		for _, ad := range m.Adapters {
			if ad.Props["direction"] != direction {
				continue
			}
			c := sapChannel{file: file, m: m, ad: ad, odata: ops.ODataChannelOf(ad.Props), idoc: ops.IDocChannelOf(ad.Props)}
			if c.odata == nil && c.idoc == nil {
				continue
			}
			for _, id := range []string{ad.Source, ad.Target} {
				if el := m.Elements[id]; el != nil && el.IsStep() {
					c.step = el
				}
			}
			out = append(out, c)
		}
	}
	return out
}

// channelName is the channel's name, else its ID.
func channelName(ad *iflow.Element) string { return firstNonEmpty(ad.Name, ad.ID) }

// uniqueInOrder drops repeated values, keeping the first.
func uniqueInOrder(values []string) []string {
	var out []string
	for _, v := range values {
		if !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

func literal(v string) bool {
	v = strings.TrimSpace(v)
	return v != "" && !strings.Contains(v, "{{") && !strings.Contains(v, "${")
}

// --- OData -------------------------------------------------------------------

// reKeyPredicate: a resource path addressing one entity (Products(1),
// A_SalesOrder('4711')), which needs no paging.
var reKeyPredicate = regexp.MustCompile(`\([^)]*\)\s*$`)

func ruleODataQueryWithoutPaging(lc *lintContext, a *lintArtifact) []Finding {
	var out []Finding
	for _, c := range sapChannels(a, "Receiver") {
		o := c.odata
		if o == nil || !strings.HasPrefix(strings.ToLower(o.Operation), "query") || o.Paging || o.Batch {
			continue
		}
		if strings.Contains(strings.ToLower(o.QueryOptions), "$top") || reKeyPredicate.MatchString(o.ResourcePath) ||
			strings.Contains(o.ResourcePath, "$count") {
			continue
		}
		out = append(out, a.finding("odata-query-without-paging", c.file, c.ad.ID, c.ad.Name, o.ResourcePath,
			fmt.Sprintf("the %s receiver %q queries %s without $top and without paging", c.ad.Props["ComponentType"], channelName(c.ad), firstNonEmpty(o.ResourcePath, "its entity set")),
			"switch on paging (Process in Pages, with a page size, in a Looping Process Call) or limit the result with $top; an unbounded query loads the whole entity set into one message"))
	}
	return out
}

var (
	reSAPClientParam  = regexp.MustCompile(`(?i)\bsap-client=([0-9]{3})\b`)
	reOrgUnitLiteral  = regexp.MustCompile(`\b(CompanyCode|BUKRS|SalesOrganization|VKORG|Plant|WERKS|ControllingArea|KOKRS)\s+eq\s+'([^'{}$]+)'`)
	odataLiteralQuery = []string{"queryOptions", "customQueryOptions", "resourcePath"}
)

func ruleODataHardcodedQuery(lc *lintContext, a *lintArtifact) []Finding {
	var out []Finding
	for _, c := range sapChannels(a, "Receiver") {
		if c.odata == nil {
			continue
		}
		for _, k := range odataLiteralQuery {
			v := c.ad.Props[k]
			if !literal(v) {
				continue
			}
			var found []string
			for _, m := range reSAPClientParam.FindAllStringSubmatch(v, -1) {
				found = append(found, "sap-client="+m[1])
			}
			for _, m := range reOrgUnitLiteral.FindAllStringSubmatch(v, -1) {
				found = append(found, m[1]+" eq '"+m[2]+"'")
			}
			if len(found) == 0 {
				continue
			}
			out = append(out, a.finding("odata-hardcoded-query", c.file, c.ad.ID, c.ad.Name, k,
				fmt.Sprintf("the OData receiver %q has environment-specific values in its %s: %s", channelName(c.ad), k, strings.Join(found, ", ")),
				"move the values into {{parameters}} (or ${property.x} set from the Partner Directory), so the client and the organisational units can differ per landscape and partner"))
			break
		}
	}
	return out
}

// perRecord returns the steps of a model that run once per record (or page)
// with the reason: steps after a splitter up to its gather, and the steps of
// local processes called by a Looping Process Call or by a process call
// that itself runs per record.
func perRecord(m *iflow.Model) map[string]string {
	steps := map[string]string{}
	for _, id := range m.Order {
		el := m.Elements[id]
		if el.Props["activityType"] != "Splitter" || strings.Contains(el.Props["splitType"], "PKCS") {
			continue
		}
		queue := slices.Clone(el.Outgoing)
		for len(queue) > 0 {
			f := m.Flow(queue[0])
			queue = queue[1:]
			if f == nil {
				continue
			}
			next := m.Elements[f.Target]
			if next == nil || steps[next.ID] != "" || next.Props["activityType"] == "Gather" {
				continue
			}
			steps[next.ID] = fmt.Sprintf("after the splitter %q", el.Name)
			queue = append(queue, next.Outgoing...)
		}
	}
	processes := map[string]string{}
	for changed := true; changed; {
		changed = false
		for _, id := range m.Order {
			el := m.Elements[id]
			target := el.Props["processId"]
			if el.Props["activityType"] != "ProcessCallElement" || target == "" || processes[target] != "" {
				continue
			}
			reason := ""
			switch {
			case el.Kind() == "LoopingProcess" || el.Props["subActivityType"] == "LoopingProcess":
				reason = fmt.Sprintf("in the Looping Process Call %q", el.Name)
			case steps[el.ID] != "":
				reason = fmt.Sprintf("called by %q %s", el.Name, steps[el.ID])
			case processes[el.Container] != "":
				reason = processes[el.Container]
			}
			if reason != "" {
				processes[target] = reason
				changed = true
			}
		}
	}
	for _, id := range m.Order {
		el := m.Elements[id]
		if r := processes[el.Container]; r != "" && steps[id] == "" {
			steps[id] = r
		}
	}
	return steps
}

func ruleODataSingleCallsInLoop(lc *lintContext, a *lintArtifact) []Finding {
	var out []Finding
	loops := map[*iflow.Model]map[string]string{}
	for _, c := range sapChannels(a, "Receiver") {
		o := c.odata
		if o == nil || c.step == nil || o.Batch || ops.ODataOperationKind(o.Operation, o.Batch) != ops.ODataWrite ||
			strings.Contains(strings.ToLower(o.Operation), "function") {
			continue
		}
		if loops[c.m] == nil {
			loops[c.m] = perRecord(c.m)
		}
		reason := loops[c.m][c.step.ID]
		if reason == "" {
			continue
		}
		out = append(out, a.finding("odata-single-calls-in-loop", c.file, c.ad.ID, c.ad.Name, c.step.ID,
			fmt.Sprintf("the OData receiver %q sends one %s per record: its step %q runs %s", channelName(c.ad), o.Operation, c.step.Name, reason),
			"collect the records and send them in one $batch request (OData adapter operation $batch, payload with one changeset per record); one call per record is slow and fails half-way"))
	}
	return out
}

// --- IDoc ----------------------------------------------------------------------

func ruleIDocSAPClientLiteral(lc *lintContext, a *lintArtifact) []Finding {
	var out []Finding
	for _, c := range sapChannels(a, "Receiver") {
		if c.idoc == nil {
			continue
		}
		addr := strings.TrimSpace(c.ad.Props["address"])
		// a fully literal address is hardcoded-endpoint's finding
		if addr == "" || literal(addr) {
			continue
		}
		host, _, query := ops.SplitAddress(addr)
		var fixed []string
		if literal(host) && !strings.Contains(host, "<") {
			fixed = append(fixed, "host "+host)
		}
		if client := ops.QueryValue(query, "sap-client"); literal(client) && !strings.Contains(client, "<") {
			fixed = append(fixed, "sap-client "+client)
		}
		if len(fixed) == 0 {
			continue
		}
		out = append(out, a.finding("idoc-sap-client-literal", c.file, c.ad.ID, c.ad.Name, "address",
			fmt.Sprintf("the IDoc receiver %q has a fixed %s in its address %s", channelName(c.ad), strings.Join(fixed, " and "), addr),
			"externalise the whole address ({{IDoc_Address}}) or host and client separately (https://{{S4_Host}}/sap/bc/srt/idoc?sap-client={{S4_Client}}): the client differs between development, test and production"))
	}
	return out
}

func ruleIDocNoIdempotency(lc *lintContext, a *lintArtifact) []Finding {
	senders := sapChannels(a, "Sender")
	var idoc *sapChannel
	for i := range senders {
		if senders[i].idoc != nil {
			idoc = &senders[i]
			break
		}
	}
	if idoc == nil {
		return nil
	}
	for _, file := range sortedModelFiles(a) {
		m := a.Models[file]
		for _, ad := range m.Adapters {
			if ad.Props["direction"] == "Receiver" && (ad.Props["ComponentType"] == "JMS" || strings.HasSuffix(ad.Kind(), "JMS")) {
				return nil // decoupled: the JMS queue retries
			}
		}
		for _, id := range m.Order {
			el := m.Elements[id]
			if el.Kind() == "IdempotentProcessCall" || strings.Contains(el.Props["activityType"]+el.Props["subActivityType"], "Idempotent") {
				return nil
			}
		}
	}
	return []Finding{a.finding("idoc-no-idempotency", idoc.file, idoc.ad.ID, idoc.ad.Name, "idempotency",
		fmt.Sprintf("the flow receives IDocs (%q) without an Idempotent Process Call and without JMS decoupling", channelName(idoc.ad)),
		"wrap the processing in an Idempotent Process Call keyed on the IDoc number (SapIDocDbId / SapIDocTransferId header), or write the IDoc to a JMS queue and process it in a second flow; SAP resends IDocs after errors and timeouts, and the receiver would get them twice")}
}

var (
	// reControlField: <SNDPRN>value</SNDPRN> with literal text (XSLT literal
	// result elements, content modifier bodies)
	reControlField = regexp.MustCompile(`<(SNDPRN|RCVPRN|SNDPOR|RCVPOR)>([^<]*)</(SNDPRN|RCVPRN|SNDPOR|RCVPOR)>`)
	reMmapDstField = regexp.MustCompile(`/(?:[\w-]+:)?EDI_DC40/(?:[\w-]+:)?(SNDPRN|RCVPRN|SNDPOR|RCVPOR)$`)
)

// literalControlFields returns field=value of EDI_DC40 partner fields set to
// literal text in an XML text.
func literalControlFields(text string) []string {
	var out []string
	for _, m := range reControlField.FindAllStringSubmatch(text, -1) {
		if v := strings.TrimSpace(m[2]); m[1] == m[3] && literal(v) {
			out = append(out, m[1]+"="+v)
		}
	}
	return out
}

// mmapConstControlFields returns field=value of EDI_DC40 partner fields a
// message mapping fills with a constant (a target brick whose argument is
// the const function).
func mmapConstControlFields(data []byte) []string {
	type brick struct {
		path, typ, fname string
		value            strings.Builder
		inValue          bool
	}
	var out []string
	var bricks []*brick
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := dec.Token()
		if err == io.EOF || err != nil {
			return out
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "brick":
				b := &brick{}
				for _, at := range t.Attr {
					switch at.Name.Local {
					case "path":
						b.path = at.Value
					case "type":
						b.typ = at.Value
					case "fname":
						b.fname = at.Value
					}
				}
				bricks = append(bricks, b)
			case "value":
				if n := len(bricks); n > 0 && bricks[n-1].fname == "const" {
					bricks[n-1].inValue = true
				}
			}
		case xml.CharData:
			if n := len(bricks); n > 0 && bricks[n-1].inValue {
				bricks[n-1].value.Write(t)
			}
		case xml.EndElement:
			n := len(bricks)
			switch {
			case t.Name.Local == "value" && n > 0:
				bricks[n-1].inValue = false
			case t.Name.Local == "brick" && n > 0:
				b := bricks[n-1]
				bricks = bricks[:n-1]
				if b.fname != "const" || n < 2 {
					continue
				}
				parent := bricks[n-2]
				if m := reMmapDstField.FindStringSubmatch(parent.path); m != nil && parent.typ == "Dst" && literal(b.value.String()) {
					out = append(out, m[1]+"="+strings.TrimSpace(b.value.String()))
				}
			}
		}
	}
}

func ruleIDocControlRecordLiteral(lc *lintContext, a *lintArtifact) []Finding {
	var out []Finding
	report := func(file, element, name, what string, fields []string) {
		fields = uniqueInOrder(fields)
		out = append(out, a.finding("idoc-control-record-literal", file, element, name, strings.Join(fields, ","),
			fmt.Sprintf("%s sets the IDoc control record (EDI_DC40) to literal partner values: %s", what, strings.Join(fields, ", ")),
			"take sender and receiver port and partner (logical system) from {{parameters}} or the Partner Directory, so the same flow works in every landscape and for every partner"))
	}
	eachElement(a, func(file string, m *iflow.Model, el *iflow.Element) {
		if !isContentModifier(el) {
			return
		}
		if fields := literalControlFields(el.Props["wrapContent"]); len(fields) > 0 {
			report(file, el.ID, el.Name, fmt.Sprintf("the content modifier %q", el.Name), fields)
		}
	})
	for _, file := range a.Files {
		ext := strings.ToLower(path.Ext(file))
		if ext != ".xsl" && ext != ".xslt" && ext != ".mmap" {
			continue
		}
		data, err := readArtifactFile(a, file)
		if err != nil || !bytes.Contains(data, []byte("EDI_DC40")) {
			continue
		}
		var fields []string
		if ext == ".mmap" {
			fields = mmapConstControlFields(data)
		} else {
			fields = literalControlFields(string(data))
		}
		if len(fields) > 0 {
			report(file, "", "", "the mapping "+path.Base(file), fields)
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
