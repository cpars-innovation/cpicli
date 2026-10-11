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

	"github.com/cpars-innovation/cpicli/internal/manifest"
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
	// used in sender and receiver addresses (the design-time values; a
	// configured value on the tenant can differ).
	AddressParameters map[string]string `json:"addressParameters,omitempty"`
	// PDReferences are the literal Partner Directory parameters the flow
	// reads ("PID:ID"; ":ID" when only the ID is literal).
	PDReferences     []string       `json:"pdReferences"`
	SenderAdapters   []string       `json:"senderAdapters"`
	ReceiverAdapters []string       `json:"receiverAdapters"`
	Steps            map[string]int `json:"steps"`
	// ExceptionSubprocess is true when the flow has an exception subprocess.
	ExceptionSubprocess     bool   `json:"exceptionSubprocess"`
	LogLevel                string `json:"logLevel,omitempty"`
	ReturnExceptionToSender string `json:"returnExceptionToSender,omitempty"`
	// Parameters are the externalised parameter keys (values are not read).
	Parameters []string `json:"parameters"`
	// CredentialRefs are names of credentials and key aliases the flow refers to.
	CredentialRefs []string       `json:"credentialRefs"`
	HeadersSet     []string       `json:"headersSet"`
	PropertiesSet  []string       `json:"propertiesSet"`
	Scripts        []ScriptFacts  `json:"scripts"`
	Resources      map[string]int `json:"resources"`
	Error          string         `json:"error,omitempty"`
}

// Trigger is one way a flow is started.
type Trigger struct {
	Adapter string `json:"adapter"`
	// Address is the adapter's endpoint, path or directory as configured
	// (may be a {{parameter}}); empty when unknown.
	Address string `json:"address,omitempty"`
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
		for _, m := range rePDLiteral.FindAllSubmatch(data, -1) {
			f.PDReferences = append(f.PDReferences, string(m[1])+":"+string(m[2]))
		}
	}
	if props, err := fs.ReadFile(fsys, ResourcesDir+"/parameters.prop"); err == nil {
		values := PropertyValues(props)
		f.Parameters = sortedMapKeys(values)
		for _, t := range slices.Concat(f.Triggers, f.Receivers) {
			for _, m := range reParameterRef.FindAllStringSubmatch(t.Address, -1) {
				if v, ok := values[m[1]]; ok {
					if f.AddressParameters == nil {
						f.AddressParameters = map[string]string{}
					}
					f.AddressParameters[m[1]] = v
				}
			}
		}
	}
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
		if dir == "script" {
			if data, err := fs.ReadFile(fsys, p); err == nil {
				f.Scripts = append(f.Scripts, analyzeScript(path.Base(p), data))
				for _, m := range reGroovyParam.FindAllSubmatch(data, -1) {
					f.PDReferences = append(f.PDReferences, string(m[2])+":"+string(m[1]))
				}
			}
		}
		return nil
	})
	for _, l := range []*[]string{&f.ProcessDirectCalls, &f.PDReferences, &f.SenderAdapters, &f.ReceiverAdapters, &f.CredentialRefs, &f.HeadersSet, &f.PropertiesSet} {
		*l = sortedUnique(*l)
	}
	return f, nil
}

func analyzeScript(name string, data []byte) ScriptFacts {
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
	return s
}

// modelElement is a BPMN element with its ifl:property key/values.
type modelElement struct {
	name  string
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
			stack = append(stack, &modelElement{name: t.Name.Local})
		case xml.CharData:
			if n := len(stack); n > 0 && (stack[n-1].name == "key" || stack[n-1].name == "value") {
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

func (f *IFlowFacts) addElement(el *modelElement) {
	p := el.props
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
		if p["direction"] == "Sender" {
			f.SenderAdapters = append(f.SenderAdapters, adapter)
			f.Triggers = append(f.Triggers, Trigger{Adapter: adapter, Address: address})
		} else {
			f.ReceiverAdapters = append(f.ReceiverAdapters, adapter)
			f.Receivers = append(f.Receivers, Trigger{Adapter: adapter, Address: address})
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
