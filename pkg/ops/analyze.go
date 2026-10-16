package ops

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"github.com/cpars-innovation/cpicli/pkg/iflow"
	"io"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/cpars-innovation/cpicli/pkg/manifest"
)

// IFlowFacts are the facts discovered about one integration flow: what it is
// built of, not how it behaves. They are input for deriving conventions.
type IFlowFacts struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	PackageID string `json:"packageId,omitempty"`
	Version   string `json:"version,omitempty"`
	// Path is the local directory (local discovery only).
	Path          string `json:"path,omitempty"`
	RuntimeStatus string `json:"runtimeStatus,omitempty"`

	// Triggers are how the flow is started: sender adapters with their
	// address (HTTPS path, ProcessDirect address, ...) and timers.
	Triggers []Trigger `json:"triggers"`
	// ProcessDirectCalls are the ProcessDirect addresses the flow sends to.
	ProcessDirectCalls []string `json:"processDirectCalls"`
	// Receivers are the receiver adapters with their address (URL, queue,
	// ProcessDirect address; may be a {{parameter}} or empty).
	Receivers []Trigger `json:"receivers"`
	// AddressParameters are the values in parameters.prop of the parameters
	// used in sender and receiver addresses and OData resource paths (the
	// design-time values; a configured value on the tenant can differ).
	AddressParameters map[string]string `json:"addressParameters,omitempty"`
	// PDReferences are the literal Partner Directory parameters the flow
	// reads ("PID:ID"; ":ID" when only the ID is literal).
	PDReferences     []string       `json:"pdReferences"`
	SenderAdapters   []string       `json:"senderAdapters"`
	ReceiverAdapters []string       `json:"receiverAdapters"`
	Steps            map[string]int `json:"steps"`
	// ExceptionSubprocess is true when the flow has an exception subprocess.
	ExceptionSubprocess bool `json:"exceptionSubprocess"`
	// ExceptionSubprocesses describe each exception subprocess: how many
	// steps it has and how it ends.
	ExceptionSubprocesses []ExceptionSubprocessFacts `json:"exceptionSubprocesses,omitempty"`
	// ScriptSteps counts the script steps; CollectionScripts are those that
	// call a script collection.
	ScriptSteps             int    `json:"scriptSteps"`
	LogLevel                string `json:"logLevel,omitempty"`
	ReturnExceptionToSender string `json:"returnExceptionToSender,omitempty"`
	// Parameters are the externalised parameter keys (values are not read).
	Parameters []string `json:"parameters"`
	// CredentialRefs are names of credentials and key aliases the flow refers to.
	CredentialRefs []string `json:"credentialRefs"`
	// Components are the cmdVariantUri of the flow's adapters and steps
	// (versions the tenant accepted; see the component catalogue).
	Components    []string      `json:"components,omitempty"`
	HeadersSet    []string      `json:"headersSet"`
	PropertiesSet []string      `json:"propertiesSet"`
	Scripts       []ScriptFacts `json:"scripts"`
	// CollectionScripts are the scripts of script collections the flow's
	// steps call.
	CollectionScripts []CollectionScriptRef `json:"collectionScripts,omitempty"`
	// IDocTypes are the IDoc types the flow handles, from its mappings,
	// schemas, conditions and payloads (not from the IDoc adapter, which
	// does not name them).
	IDocTypes []IDocType     `json:"idocTypes,omitempty"`
	Resources map[string]int `json:"resources"`
	Error     string         `json:"error,omitempty"`

	idocTypes map[string]map[string]bool // type -> sources, while reading
}

// Trigger is one way a flow is started.
type Trigger struct {
	Adapter string `json:"adapter"`
	// Address is the adapter's endpoint, path or directory as configured
	// (may be a {{parameter}}); empty when unknown.
	Address string `json:"address,omitempty"`
	// OData and IDoc describe OData and IDoc channels.
	OData *ODataChannel `json:"odata,omitempty"`
	IDoc  *IDocChannel  `json:"idoc,omitempty"`
}

// addressKeys are adapter properties that hold the address of an endpoint,
// in order of preference.
var addressKeys = []string{"urlPath", "address", "httpAddressWithoutQuery", "path", "directory", "QueueName_inbound", "QueueName_outbound", "queueName"}

// ScriptFacts describe one script of an integration flow.
type ScriptFacts struct {
	Name     string `json:"name"`
	Language string `json:"language"`
	Lines    int    `json:"lines"`
	// Hash identifies identical scripts across flows (sha256, 12 hex digits).
	Hash string `json:"hash"`
	// CustomHeaders are custom header properties the script writes to the
	// message processing log.
	CustomHeaders []string `json:"customHeaders,omitempty"`
	// LogsAttachments is true when the script adds log attachments.
	LogsAttachments bool `json:"logsAttachments,omitempty"`
	// Summary is what the script says it does (its file comment, else the
	// first documented function); Functions are its top-level functions
	// with their doc comments (Groovy).
	Summary   string           `json:"summary,omitempty"`
	Functions []ScriptFunction `json:"functions,omitempty"`
	// Keywords are the words of its @keywords tags.
	Keywords []string `json:"keywords,omitempty"`
	// Reads and Writes are the headers and properties the code reads and
	// writes by literal name ("header SAP_Date", "property orderId").
	Reads  []string `json:"reads,omitempty"`
	Writes []string `json:"writes,omitempty"`
	// Shape is the same for scripts that differ only in comments and
	// formatting.
	Shape string `json:"shape,omitempty"`
	// DocStatus is ok, incomplete or missing (Groovy); DocIssues say what
	// is missing (docs/script-docs.md).
	DocStatus string           `json:"docStatus,omitempty"`
	DocIssues []ScriptDocIssue `json:"docIssues,omitempty"`
}

// ExceptionSubprocessFacts describe an exception subprocess.
type ExceptionSubprocessFacts struct {
	Name string `json:"name"`
	// Process is the integration or local process it belongs to.
	Process string `json:"process,omitempty"`
	// Steps counts its steps (0: start straight to end).
	Steps int `json:"steps"`
	// Ends are the kinds of its end events (MessageEndEvent, ErrorEndEvent,
	// EscalationEndEvent, ...).
	Ends []string `json:"ends"`
	// CallsProcess is true when it calls a local process.
	CallsProcess bool `json:"callsProcess,omitempty"`
}

// CollectionScriptRef is a step calling a script of a script collection.
type CollectionScriptRef struct {
	Collection string `json:"collection"`
	Script     string `json:"script"`
	Function   string `json:"function,omitempty"`
}

const ResourcesDir = "src/main/resources"

var (
	reCustomHeader  = regexp.MustCompile(`addCustomHeaderProperty\s*\(\s*["']([^"']+)["']`)
	reTableName     = regexp.MustCompile(`<cell id=['"]Name['"]>([^<]*)</cell>`)
	reCredentialKey = regexp.MustCompile(`(?i)(credential_?(name)?|alias)$`)
	reParameterRef  = regexp.MustCompile(`\{\{([^{}]+)\}\}`)
	// propertyUnescaper resolves the escapes CPI writes in parameters.prop
	// values (https\://host).
	propertyUnescaper = strings.NewReplacer(`\:`, ":", `\=`, "=", `\\`, `\`)
)

// AnalyzeIFlow reads an integration flow from fsys (the root holds META-INF
// and src/main/resources, as in the downloaded archive).
func AnalyzeIFlow(fsys fs.FS) (*IFlowFacts, error) {
	f := &IFlowFacts{Triggers: []Trigger{}, ProcessDirectCalls: []string{}, Receivers: []Trigger{}, PDReferences: []string{}, SenderAdapters: []string{}, ReceiverAdapters: []string{}, Steps: map[string]int{},
		Parameters: []string{}, CredentialRefs: []string{}, HeadersSet: []string{}, PropertiesSet: []string{},
		Scripts: []ScriptFacts{}, Resources: map[string]int{}}

	if mf, err := fs.ReadFile(fsys, "META-INF/MANIFEST.MF"); err == nil {
		h := parseManifest(mf)
		f.ID, _, _ = strings.Cut(h["Bundle-SymbolicName"], ";")
		f.ID = strings.TrimSpace(f.ID)
		f.Name, f.Version = h["Bundle-Name"], h["Bundle-Version"]
	} else {
		return nil, fmt.Errorf("META-INF/MANIFEST.MF: %w", err)
	}

	flows, _ := fs.Glob(fsys, ResourcesDir+"/scenarioflows/integrationflow/*.iflw")
	if len(flows) == 0 {
		return f, fmt.Errorf("no .iflw model found")
	}
	for _, name := range flows {
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return f, err
		}
		if err := f.addModel(data); err != nil {
			return f, fmt.Errorf("%s: %w", path.Base(name), err)
		}
		if m, err := iflow.Parse(data); err == nil {
			f.addExceptionSubprocesses(m)
		}
		for _, m := range rePDLiteral.FindAllSubmatch(data, -1) {
			f.PDReferences = append(f.PDReferences, string(m[1])+":"+string(m[2]))
		}
	}
	if props, err := fs.ReadFile(fsys, ResourcesDir+"/parameters.prop"); err == nil {
		values := PropertyValues(props)
		f.Parameters = sortedMapKeys(values)
		for _, t := range slices.Concat(f.Triggers, f.Receivers) {
			refs := t.Address
			if t.OData != nil {
				refs += " " + t.OData.ResourcePath
			}
			for _, m := range reParameterRef.FindAllStringSubmatch(refs, -1) {
				if v, ok := values[m[1]]; ok {
					if f.AddressParameters == nil {
						f.AddressParameters = map[string]string{}
					}
					f.AddressParameters[m[1]] = v
				}
			}
		}
	}
	var edmx []string
	_ = fs.WalkDir(fsys, ResourcesDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(p, ResourcesDir+"/")
		dir, _, nested := strings.Cut(rel, "/")
		if !nested || dir == "scenarioflows" {
			return nil
		}
		f.Resources[dir]++
		if dir == "edmx" {
			edmx = append(edmx, rel)
		}
		if idocTypeFiles[strings.ToLower(path.Ext(p))] {
			if data, err := fs.ReadFile(fsys, p); err == nil {
				f.addIDocTypes(rel, idocTypesInFile(rel, data))
			}
		}
		if dir == "script" {
			if data, err := fs.ReadFile(fsys, p); err == nil {
				f.Scripts = append(f.Scripts, analyzeScript(path.Base(p), data, false))
				for _, m := range reGroovyParam.FindAllSubmatch(data, -1) {
					f.PDReferences = append(f.PDReferences, string(m[2])+":"+string(m[1]))
				}
			}
		}
		return nil
	})
	f.resolveSAPChannels(edmx)
	f.collectIDocTypes()
	for _, l := range []*[]string{&f.ProcessDirectCalls, &f.PDReferences, &f.SenderAdapters, &f.ReceiverAdapters, &f.CredentialRefs, &f.HeadersSet, &f.PropertiesSet, &f.Components} {
		*l = sortedUnique(*l)
	}
	return f, nil
}

func analyzeScript(name string, data []byte, collection bool) ScriptFacts {
	sum := sha256.Sum256(data)
	s := ScriptFacts{Name: name, Hash: hex.EncodeToString(sum[:6]), Lines: bytes.Count(data, []byte("\n"))}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		s.Lines++
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".groovy", ".gsh":
		s.Language = "groovy"
	case ".js":
		s.Language = "javascript"
	default:
		s.Language = strings.TrimPrefix(strings.ToLower(path.Ext(name)), ".")
	}
	for _, m := range reCustomHeader.FindAllSubmatch(data, -1) {
		s.CustomHeaders = append(s.CustomHeaders, string(m[1]))
	}
	s.CustomHeaders = sortedUnique(s.CustomHeaders)
	s.LogsAttachments = bytes.Contains(data, []byte("addAttachmentAs"))
	s.Shape = ScriptShape(data)
	if s.Language == "groovy" {
		doc := ParseScriptDoc(data)
		s.Summary, s.Functions, s.Keywords = doc.Summary, doc.Functions, doc.Keywords()
		s.Reads, s.Writes = ScriptAccess(data)
		s.DocIssues = CheckScriptDoc(data, collection)
		public := 0
		for _, f := range doc.Functions {
			if !f.Private {
				public++
			}
		}
		if !collection {
			public = 1
		}
		s.DocStatus = DocStatusOf(s.DocIssues, public)
	}
	return s
}

// modelElement is a BPMN element with its ifl:property key/values.
type modelElement struct {
	name  string
	label string // the element's name attribute, else its id
	props map[string]string
	text  strings.Builder
}

// addModel collects adapters, steps and settings from an .iflw (BPMN) model.
// Every element carries its configuration as ifl:property key/value pairs in
// extensionElements; cmdVariantUri names the component type
// (ctype::FlowstepVariant/cname::GroovyScript/...).
func (f *IFlowFacts) addModel(data []byte) error {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var stack []*modelElement
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			el := &modelElement{name: t.Name.Local}
			for _, a := range t.Attr {
				if a.Name.Local == "name" && a.Value != "" || a.Name.Local == "id" && el.label == "" {
					el.label = a.Value
				}
			}
			stack = append(stack, el)
		case xml.CharData:
			if n := len(stack); n > 0 && (stack[n-1].name == "key" || stack[n-1].name == "value" || stack[n-1].name == "conditionExpression") {
				stack[n-1].text.Write(t)
			}
		case xml.EndElement:
			n := len(stack)
			if n == 0 {
				continue
			}
			el := stack[n-1]
			stack = stack[:n-1]
			switch {
			case el.name == "conditionExpression" && n >= 2:
				f.addIDocTypes("condition "+stack[n-2].label, idocTypesIn(el.text.String()))
			case (el.name == "key" || el.name == "value") && n >= 2:
				prop := stack[n-2]
				if prop.props == nil {
					prop.props = map[string]string{}
				}
				prop.props[el.name] = el.text.String()
			case el.name == "property" && n >= 3:
				// property -> extensionElements -> owner
				owner := stack[n-3]
				if owner.props == nil {
					owner.props = map[string]string{}
				}
				owner.props[el.props["key"]] = el.props["value"]
			case el.props != nil:
				f.addElement(el)
			}
		}
	}
}

// addExceptionSubprocesses records what each exception subprocess does.
func (f *IFlowFacts) addExceptionSubprocesses(m *iflow.Model) {
	for _, id := range m.Order {
		sub := m.Elements[id]
		if sub.Tag != "subProcess" || !(sub.TriggeredByEvent || strings.Contains(sub.Kind(), "ErrorEventSubProcess")) {
			continue
		}
		es := ExceptionSubprocessFacts{Name: sub.Name, Ends: []string{}}
		if c := m.Elements[sub.Container]; c != nil {
			es.Process = c.Name
		}
		for _, cid := range m.Order {
			el := m.Elements[cid]
			if el.Container != sub.ID {
				continue
			}
			switch {
			case el.Tag == "endEvent":
				es.Ends = append(es.Ends, el.Kind())
			case el.IsStep() || el.Tag == "subProcess" || strings.HasSuffix(el.Tag, "Gateway"):
				es.Steps++
				if el.Props["activityType"] == "ProcessCallElement" {
					es.CallsProcess = true
				}
			}
		}
		sort.Strings(es.Ends)
		f.ExceptionSubprocesses = append(f.ExceptionSubprocesses, es)
	}
}

func (f *IFlowFacts) addElement(el *modelElement) {
	p := el.props
	if p["activityType"] == "Script" {
		f.ScriptSteps++
	}
	if bundle := p["scriptBundleId"]; bundle != "" && p["script"] != "" {
		f.CollectionScripts = append(f.CollectionScripts, CollectionScriptRef{Collection: bundle, Script: path.Base(p["script"]),
			Function: p["scriptFunction"]})
	}
	if uri := p["cmdVariantUri"]; uri != "" {
		f.Components = append(f.Components, uri)
	}
	ctype, cname := variant(p["cmdVariantUri"])
	switch {
	case el.name == "participant":
	case el.name == "collaboration":
		f.LogLevel, f.ReturnExceptionToSender = p["log"], p["returnExceptionToSender"]
	case ctype == "AdapterVariant":
		adapter := p["ComponentType"]
		if adapter == "" {
			adapter = strings.TrimPrefix(cname, "sap:")
		}
		address := ""
		for _, k := range addressKeys {
			if p[k] != "" {
				address = p[k]
				break
			}
		}
		t := Trigger{Adapter: adapter, Address: address, OData: ODataChannelOf(p), IDoc: IDocChannelOf(p)}
		if p["direction"] == "Sender" {
			f.SenderAdapters = append(f.SenderAdapters, adapter)
			f.Triggers = append(f.Triggers, t)
		} else {
			f.ReceiverAdapters = append(f.ReceiverAdapters, adapter)
			f.Receivers = append(f.Receivers, t)
			if adapter == "ProcessDirect" && address != "" {
				f.ProcessDirectCalls = append(f.ProcessDirectCalls, address)
			}
		}
	case ctype == "FlowstepVariant" || ctype == "FlowElementVariant":
		if cname != "IntegrationProcess" {
			f.Steps[cname]++
		}
		if el.name == "startEvent" && strings.Contains(strings.ToLower(cname), "timer") {
			f.Triggers = append(f.Triggers, Trigger{Adapter: "Timer"})
		}
		if strings.Contains(cname, "ErrorEventSubProcess") || strings.Contains(cname, "ExceptionSubProcess") {
			f.ExceptionSubprocess = true
		}
	}
	if el.name != "messageFlow" {
		for _, k := range sortedMapKeys(p) {
			f.addIDocTypes("step "+el.label, idocTypesIn(p[k]))
		}
	}
	for k, v := range p {
		switch {
		case v == "":
		case k == "headerTable":
			f.HeadersSet = append(f.HeadersSet, TableNames(v)...)
		case k == "propertyTable":
			f.PropertiesSet = append(f.PropertiesSet, TableNames(v)...)
		case reCredentialKey.MatchString(k):
			f.CredentialRefs = append(f.CredentialRefs, v)
		}
	}
}

func variant(uri string) (ctype, cname string) { return iflow.Variant(uri) }

func TableNames(table string) []string {
	var names []string
	for _, m := range reTableName.FindAllStringSubmatch(table, -1) {
		if m[1] != "" {
			names = append(names, m[1])
		}
	}
	return names
}

// parseManifest reads MANIFEST.MF headers (see manifest.Parse).
func parseManifest(data []byte) map[string]string { return manifest.Parse(data) }

// PropertyValues returns the keys and values of a Java properties file
// (single-line entries; escapes in keys are resolved).
func PropertyValues(data []byte) map[string]string {
	values := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' || line[0] == '!' {
			continue
		}
		var key strings.Builder
		i := 0
		for ; i < len(line); i++ {
			c := line[i]
			if c == '\\' && i+1 < len(line) {
				i++
				key.WriteByte(line[i])
				continue
			}
			if c == '=' || c == ':' {
				break
			}
			key.WriteByte(c)
		}
		if k := strings.TrimSpace(key.String()); k != "" {
			v := ""
			if i < len(line) {
				v = strings.TrimSpace(line[i+1:])
			}
			values[k] = propertyUnescaper.Replace(v)
		}
	}
	return values
}

func sortedUnique(list []string) []string {
	if list == nil {
		return []string{}
	}
	sort.Strings(list)
	return slices.Compact(list)
}

// ScriptCollectionFacts is a script collection and its scripts.
type ScriptCollectionFacts struct {
	ID        string        `json:"id"`
	Name      string        `json:"name,omitempty"`
	Version   string        `json:"version,omitempty"`
	PackageID string        `json:"packageId,omitempty"`
	Path      string        `json:"path,omitempty"`
	Scripts   []ScriptFacts `json:"scripts"`
	Error     string        `json:"error,omitempty"`
}

// AnalyzeScriptCollection reads a script collection from fsys (the root
// holds META-INF and src/main/resources). It always returns facts.
func AnalyzeScriptCollection(fsys fs.FS) (*ScriptCollectionFacts, error) {
	f := &ScriptCollectionFacts{Scripts: []ScriptFacts{}}
	mf, err := fs.ReadFile(fsys, "META-INF/MANIFEST.MF")
	if err != nil {
		return f, fmt.Errorf("META-INF/MANIFEST.MF: %w", err)
	}
	h := parseManifest(mf)
	f.ID, _, _ = strings.Cut(h["Bundle-SymbolicName"], ";")
	f.ID = strings.TrimSpace(f.ID)
	f.Name, f.Version = h["Bundle-Name"], h["Bundle-Version"]
	err = fs.WalkDir(fsys, ResourcesDir+"/script", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		data, rerr := fs.ReadFile(fsys, p)
		if rerr != nil {
			return rerr
		}
		f.Scripts = append(f.Scripts, analyzeScript(strings.TrimPrefix(p, ResourcesDir+"/script/"), data, true))
		return nil
	})
	return f, err
}
