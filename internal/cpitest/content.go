package cpitest

import (
	"archive/zip"
	"bytes"
	"cmp"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/cpars-innovation/cpicli/pkg/catalog"
	"github.com/cpars-innovation/cpicli/pkg/iflow"
)

// artifactContent is what a tenant derives from an artifact archive: the
// resources it lists, the externalised parameters, the manifest and the
// integration flow model.
type artifactContent struct {
	Manifest   map[string]string
	Resources  map[string]Resource
	Parameters map[string]string // parameters.prop (unescaped values)
	// ParameterOrder keeps the keys in file order.
	ParameterOrder []string
	Model          []byte // the .iflw, nil for other artifact types
	ModelPath      string // archive path of the .iflw
	Files          map[string][]byte
}

const resourcesDir = "src/main/resources/"

// readArtifactZip reads an artifact archive as the tenant does on upload.
func readArtifactZip(data []byte) (*artifactContent, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	c := &artifactContent{Manifest: map[string]string{}, Resources: map[string]Resource{}, Parameters: map[string]string{}, Files: map[string][]byte{}}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		content, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return nil, err
		}
		c.addFile(strings.TrimPrefix(f.Name, "/"), content)
	}
	return c, nil
}

func (c *artifactContent) addFile(name string, content []byte) {
	c.Files[name] = content
	switch {
	case name == "META-INF/MANIFEST.MF":
		c.Manifest = parseManifest(content)
	case name == resourcesDir+"parameters.prop":
		c.Parameters, c.ParameterOrder = parseProperties(content)
	case name == resourcesDir+"parameters.propdef":
	case strings.HasPrefix(name, resourcesDir):
		base := path.Base(name)
		ext := strings.ToLower(strings.TrimPrefix(path.Ext(base), "."))
		if ext == "" {
			return
		}
		c.Resources[base] = Resource{Type: ext, Content: content}
		if ext == "iflw" {
			c.Model, c.ModelPath = content, name
		}
	}
}

// artifactType maps SAP-BundleType to the API's artifact type.
func (c *artifactContent) artifactType() string {
	switch c.Manifest["SAP-BundleType"] {
	case "ScriptCollection":
		return "ScriptCollection"
	case "MessageMapping":
		return "MessageMapping"
	case "ValueMapping":
		return "ValueMapping"
	default:
		return "Integration"
	}
}

// parseManifest reads MANIFEST.MF headers (continuation lines start with a space).
func parseManifest(data []byte) map[string]string {
	out := map[string]string{}
	var key string
	for line := range strings.SplitSeq(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, " ") && key != "" {
			out[key] += strings.TrimPrefix(line, " ")
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			key = ""
			continue
		}
		key = strings.TrimSpace(k)
		out[key] = strings.TrimSpace(v)
	}
	return out
}

var propertyUnescaper = strings.NewReplacer(`\:`, ":", `\=`, "=", `\\`, `\`, `\ `, " ", `\#`, "#", `\!`, "!")

// parseProperties reads a Java properties file as CPI writes parameters.prop.
func parseProperties(data []byte) (map[string]string, []string) {
	out := map[string]string{}
	var order []string
	for line := range strings.SplitSeq(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' || line[0] == '!' {
			continue
		}
		i := strings.IndexFunc(line, func(r rune) bool { return r == '=' || r == ':' })
		for i > 0 && line[i-1] == '\\' {
			j := strings.IndexFunc(line[i+1:], func(r rune) bool { return r == '=' || r == ':' })
			if j < 0 {
				i = -1
				break
			}
			i += j + 1
		}
		k, v := line, ""
		if i >= 0 {
			k, v = line[:i], line[i+1:]
		}
		k = propertyUnescaper.Replace(strings.TrimSpace(k))
		if _, seen := out[k]; !seen {
			order = append(order, k)
		}
		out[k] = propertyUnescaper.Replace(strings.TrimSpace(v))
	}
	return out, order
}

var reParamRef = regexp.MustCompile(`\{\{([^{}]+)\}\}`)

// resolve replaces {{parameter}} references with the configured values.
func resolve(s string, params map[string]string) string {
	return reParamRef.ReplaceAllStringFunc(s, func(ref string) string {
		if v, ok := params[strings.TrimSpace(ref[2:len(ref)-2])]; ok {
			return v
		}
		return ref
	})
}

// channel is a sender or receiver adapter of a flow model.
type channel struct {
	Adapter string // ComponentType, e.g. HTTPS, ProcessDirect, JMS, HTTP, SFTP
	Address string // urlPath, address, queue, host or URL as configured (may hold {{param}})
	Props   map[string]string
	// Source is the model element that calls a receiver: a request-reply
	// step (synchronous call) or an end event (the message leaves the flow).
	Source string
}

var senderAddressKeys = []string{"urlPath", "address", "QueueName_inbound", "queueName", "path", "directory"}
var receiverAddressKeys = []string{"address", "httpAddressWithoutQuery", "QueueName_outbound", "queueName", "host", "path", "directory"}

func firstProp(p map[string]string, keys []string) string {
	for _, k := range keys {
		if v := p[k]; v != "" {
			return v
		}
	}
	return ""
}

// flowModel is the part of an integration flow model the mock executes.
type flowModel struct {
	model     *iflow.Model
	Senders   []channel
	Receivers []channel
	Timer     bool
}

func parseFlowModel(data []byte) (*flowModel, error) {
	m, err := iflow.Parse(data)
	if err != nil {
		return nil, err
	}
	fm := &flowModel{model: m}
	for _, a := range m.Adapters {
		ch := channel{Adapter: adapterOf(a.Props), Props: a.Props, Source: a.Source}
		switch a.Props["direction"] {
		case "Sender":
			ch.Address = firstProp(a.Props, senderAddressKeys)
			fm.Senders = append(fm.Senders, ch)
		case "Receiver":
			ch.Address = firstProp(a.Props, receiverAddressKeys)
			fm.Receivers = append(fm.Receivers, ch)
		}
	}
	for _, id := range m.Order {
		el := m.Elements[id]
		if el.Tag == "startEvent" && strings.Contains(el.Props["cmdVariantUri"], "timer") {
			fm.Timer = true
		}
	}
	sort.SliceStable(fm.Receivers, func(i, j int) bool { return fm.Receivers[i].Source < fm.Receivers[j].Source })
	return fm, nil
}

// Adapters the mock names by their variant URI (cmdVariantUri cname) rather
// than ComponentType: the IDoc adapter (ComponentType IDOC in SAP's content)
// and the OData V2 sender (cname sap:ODataSender; its ComponentType is not
// known from real content).
const (
	adapterIDoc        = "IDOC"
	adapterODataSender = "ODataSender"
)

// adapterOf is the adapter of a channel: the IDoc and OData sender adapters
// by their variant, the others by ComponentType.
func adapterOf(p map[string]string) string {
	switch catalog.ParseURI(p["cmdVariantUri"])["cname"] {
	case "sap:IDOC":
		return adapterIDoc
	case "sap:ODataSender":
		return adapterODataSender
	}
	return p["ComponentType"]
}

// Inbound kinds: how a runtime endpoint takes a message.
const (
	inboundIDoc  = "IDoc"
	inboundOData = "OData"
)

// inboundKind is the Inbound.Kind of a sender adapter ("" for HTTPS and SOAP).
func inboundKind(adapter string) string {
	switch adapter {
	case adapterIDoc:
		return inboundIDoc
	case adapterODataSender:
		return inboundOData
	}
	return ""
}

// odataPrefix is the path of the OData V2 sender's services.
const odataPrefix = "/gw/odata/"

// senderPath is the runtime path of a sender of artifact id: endpointPath,
// and for the OData sender its service root.
func senderPath(id string, a *Artifact, s channel) string {
	if s.Adapter == adapterODataSender {
		return odataServicePath(id, a, s)
	}
	return s.endpointPath(a.Parameters)
}

var (
	reEdmxNamespace = regexp.MustCompile(`<(?:\w+:)?Schema\b[^>]*\bNamespace="([^"]+)"`)
	reEdmxContainer = regexp.MustCompile(`<(?:\w+:)?EntityContainer\b[^>]*\bName="([^"]+)"`)
	reEdmxEntitySet = regexp.MustCompile(`<(?:\w+:)?EntitySet\b[^>]*\bName="([^"]+)"`)
)

// odataServicePath is the service root of an OData V2 sender:
// /gw/odata/<namespace>/<service>;v=<version>. The sender's property keys
// for namespace, service name and version are not known from real content,
// so the mock derives them: from the channel's address (any of
// senderAddressKeys, e.g. address = "Demo/Orders;v=1"), else from the flow's
// EDMX resource (Schema Namespace and EntityContainer Name, version 1), else
// SAP/<artifact ID>;v=1.
func odataServicePath(id string, a *Artifact, s channel) string {
	if addr := strings.Trim(resolve(s.Address, a.Parameters), "/"); addr != "" && !strings.Contains(addr, "{{") {
		if !strings.Contains(addr, ";v=") {
			addr += ";v=1"
		}
		return odataPrefix + addr
	}
	ns, svc := "SAP", id
	if edmx := a.edmx(); edmx != nil {
		if mm := reEdmxNamespace.FindSubmatch(edmx); mm != nil {
			ns = string(mm[1])
		}
		if mm := reEdmxContainer.FindSubmatch(edmx); mm != nil {
			svc = string(mm[1])
		}
	}
	return odataPrefix + ns + "/" + svc + ";v=1"
}

// edmx is the first EDMX resource of the artifact (by name), nil without one.
func (a *Artifact) edmx() []byte {
	for _, name := range sortedKeys(a.Resources) {
		if a.Resources[name].Type == "edmx" {
			return a.Resources[name].Content
		}
	}
	return nil
}

// endpointPath is the runtime path of an HTTP-like sender (HTTPS: /http/…,
// SOAP and IDoc: /cxf/…), "" for other senders.
func (c channel) endpointPath(params map[string]string) string {
	addr := resolve(c.Address, params)
	if addr == "" || strings.Contains(addr, "{{") {
		return ""
	}
	if !strings.HasPrefix(addr, "/") {
		addr = "/" + addr
	}
	switch c.Adapter {
	case "HTTPS":
		return "/http" + addr
	case "SOAP", adapterIDoc:
		return "/cxf" + addr
	}
	return ""
}

// applyContent updates what the tenant derives from an uploaded archive:
// resources and parameters (configured values of keys that still exist are
// kept, as on a tenant). An archive the mock cannot read keeps the old state.
func (a *Artifact) applyContent(zipData []byte) {
	a.Zip, a.cached = zipData, nil
	c, err := readArtifactZip(zipData)
	if err != nil {
		return
	}
	a.Resources = c.Resources
	params := map[string]string{}
	for k, v := range c.Parameters {
		if old, ok := a.Parameters[k]; ok {
			v = old
		}
		params[k] = v
	}
	a.Parameters = params
}

// flow returns the parsed integration flow model of the artifact's content
// (nil for other artifact types or unreadable content).
func (a *Artifact) flow() *flowModel {
	if a.Type != "Integration" || len(a.Zip) == 0 {
		return nil
	}
	c, err := readArtifactZip(a.Zip)
	if err != nil || c.Model == nil {
		return nil
	}
	fm, err := parseFlowModel(c.Model)
	if err != nil {
		return nil
	}
	return fm
}

// registerEndpoints makes the HTTPS, SOAP, IDoc and OData senders of a deployed flow
// reachable (Inbound) and listed in ServiceEndpoints. Endpoints configured
// before (for example by a seed) keep their behaviour. Like a real tenant it
// registers nothing and returns an error message when an address is already
// used by another deployed flow.
func (m *Tenant) registerEndpoints(id string, a *Artifact) (conflict string) {
	fm := a.flow()
	if fm == nil {
		return ""
	}
	if m.Inbound == nil {
		m.Inbound = map[string]*Inbound{}
	}
	for _, s := range fm.Senders {
		p := senderPath(id, a, s)
		if in := m.Inbound[p]; p != "" && in != nil && in.Artifact != id && in.Artifact != "" {
			if other := m.Artifacts[in.Artifact]; other != nil && other.running() {
				addr := strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(p, "/http"), "/cxf"), strings.TrimSuffix(odataPrefix, "/"))
				return fmt.Sprintf("the address %s is already used by %s", addr, in.Artifact)
			}
		}
	}
	for _, s := range fm.Senders {
		p := senderPath(id, a, s)
		if p == "" {
			continue
		}
		if in := m.Inbound[p]; in == nil || in.Artifact != id {
			m.Inbound[p] = &Inbound{Artifact: id, Kind: inboundKind(s.Adapter)}
		}
		if a.EndpointURL == "" {
			a.EndpointURL = strings.TrimSuffix(cmp.Or(m.EndpointBase, m.URL()), "/") + p
		}
	}
	return ""
}

// running reports whether the artifact is deployed and not in error.
func (a *Artifact) running() bool { return a.Runtime != nil && a.Runtime.Status != "ERROR" }

// unregisterEndpoints removes the runtime endpoints of an undeployed flow.
func (m *Tenant) unregisterEndpoints(id string, a *Artifact) {
	for p, in := range m.Inbound {
		if in.Artifact == id {
			delete(m.Inbound, p)
		}
	}
	a.EndpointURL, a.ErrorInfo = "", ""
}

// rePropdefParameter finds the parameters of a parameters.propdef.
var rePropdefParameter = regexp.MustCompile(`(?s)<parameter>(.*?)</parameter>`)

// uploadProblem returns the error a real tenant answers for content it
// cannot import (HTTP 500), or "". A parameters.propdef parameter without
// <constraint> makes the tenant fail with a NullPointerException.
func uploadProblem(zipData []byte) string {
	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return ""
	}
	for _, f := range zr.File {
		if f.Name == "META-INF/MANIFEST.MF" {
			if msg := manifestProblem(zipFile(f)); msg != "" {
				return msg
			}
			continue
		}
		if !strings.HasSuffix(f.Name, "parameters.propdef") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return ""
		}
		data, _ := io.ReadAll(io.LimitReader(rc, 1<<20))
		_ = rc.Close()
		for _, p := range rePropdefParameter.FindAllSubmatch(data, -1) {
			if !bytes.Contains(p[1], []byte("<constraint")) {
				return `Cannot invoke "org.w3c.dom.Node.getFirstChild()" because "constraint" is null`
			}
		}
	}
	return ""
}

// manifestProblem is how a tenant refuses a manifest at upload: a header
// whose name is not followed by ": " (e.g. "Import-Service:" without the
// space) is "invalid header field (line N)" (#22).
func manifestProblem(mf []byte) string {
	for i, line := range strings.Split(strings.ReplaceAll(string(mf), "\r\n", "\n"), "\n") {
		if line == "" || line[0] == ' ' {
			continue // end of a section, or a continuation
		}
		name, rest, ok := strings.Cut(line, ":")
		if !ok || name == "" || !strings.HasPrefix(rest, " ") {
			return fmt.Sprintf("invalid header field (line %d)", i+1)
		}
	}
	return ""
}

// zipFile reads a file of an archive (nil on errors).
func zipFile(f *zip.File) []byte {
	rc, err := f.Open()
	if err != nil {
		return nil
	}
	defer rc.Close()
	data, _ := io.ReadAll(io.LimitReader(rc, 4<<20))
	return data
}

var (
	reVMEntryValue = regexp.MustCompile(`(?s)<entry>.*?<value>\s*</value>.*?</entry>|(?s)<entry>.*?<value\s*/>.*?</entry>`)
)

// deployProblem is how a tenant refuses an artifact at deploy (#22): a
// value mapping with an empty value fails the deploy request (status 500);
// a message mapping without an assigned target element deploys into
// ERROR. status 0: the runtime ends in ERROR with msg.
func deployProblem(typ, id string, zipData []byte) (status int, msg string) {
	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return 0, ""
	}
	for _, f := range zr.File {
		switch {
		case typ == "ValueMapping" && strings.HasSuffix(f.Name, "value_mapping.xml"):
			if reVMEntryValue.Match(zipFile(f)) {
				return 500, "Failed to deploy Value Mapping id: " + id
			}
		case typ == "MessageMapping" && strings.HasSuffix(f.Name, ".mmap"):
			if !bytes.Contains(zipFile(f), []byte("<brick")) {
				return 0, "Target element is not assigned"
			}
		}
	}
	return 0, ""
}

// tenantSerialised returns an uploaded archive as a tenant stores it: the
// tenant rewrites metainfo.prop with java.util.Properties (a comment line,
// ':' and '=' in values escaped), so a download differs from the upload
// byte for byte. Other files are kept. An archive the mock cannot read is
// returned as is.
func tenantSerialised(zipData []byte) []byte {
	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return zipData
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	changed := false
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			return zipData
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return zipData
		}
		if f.Name == "metainfo.prop" {
			props, order := parseProperties(data)
			var b strings.Builder
			b.WriteString("#Store metainfo properties\r\n")
			for _, k := range order {
				b.WriteString(k + "=" + strings.NewReplacer(":", `\:`, "=", `\=`).Replace(props[k]) + "\r\n")
			}
			data, changed = []byte(b.String()), true
		}
		w, err := zw.Create(f.Name)
		if err != nil {
			return zipData
		}
		if _, err := w.Write(data); err != nil {
			return zipData
		}
	}
	if !changed || zw.Close() != nil {
		return zipData
	}
	return buf.Bytes()
}
