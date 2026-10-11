package cpitest

import (
	"cmp"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/mail"
	"path"
	"strconv"
	"strings"
	"sync"

	"github.com/cpars-innovation/cpicli/pkg/catalog"
	"github.com/cpars-innovation/cpicli/pkg/iflow"
)

// checkIssue is one finding of ValidateIntegrationDesigntimeArtifact in the
// shape a tenant returns it.
type checkIssue struct {
	Severity     string `json:"severity"`
	Message      string `json:"message"`
	SourceObject string `json:"sourceObject"`
	ResourcePath string `json:"resourcePath"`
	ResourceName string `json:"resourceName"`
}

// requiredProps are the channel attributes a tenant's Check requires, by
// adapter and direction. The demo flows found them on a real tenant.
var requiredProps = map[string][]string{
	"ProcessDirect/Sender":   {"address"},
	"ProcessDirect/Receiver": {"address"},
	"HTTPS/Sender":           {"urlPath"},
	"HTTP/Receiver":          {"httpAddressWithoutQuery", "httpMethod", "authenticationMethod"},
	"JMS/Sender":             {"QueueName_inbound"},
	"JMS/Receiver":           {"QueueName_outbound", "ExpirationPeriod", "RetentionThresholdAlerting"},
	"SFTP/Receiver":          {"host"},
	"Mail/Receiver":          {"server"},
}

// numericProps must hold whole numbers (or a {{parameter}}).
var numericProps = map[string][]string{
	"JMS/Sender": {"NumberConcurrentProcesses", "RetryInterval", "MaxRetryInterval"},
}

// propLabel is the attribute name the Web UI shows in a message, where the
// tenant's wording is known.
var propLabel = map[string]string{"server": "Address", "address": "Address", "httpAddressWithoutQuery": "Address"}

// runtimeProfile is the runtime profile whose component versions the mock's
// Check accepts (the profile of a Cloud Foundry tenant).
const runtimeProfile = "integrationcell"

// defaultComponentsJSON is the component catalogue of the mock's runtime
// profile: SAP's editor metadata of an integrationcell tenant (Web UI,
// /api/1.0/metadata), imported with catalog.Import (cpictl components
// import) and stored one entry per line.
//
//go:embed components.json
var defaultComponentsJSON []byte

var defaultComponents = sync.OnceValues(func() (*catalog.Catalog, error) {
	return parseCatalog(defaultComponentsJSON, "components.json")
})

// DefaultComponents is the mock's built-in component catalogue.
func DefaultComponents() *catalog.Catalog {
	c, err := defaultComponents()
	if err != nil {
		panic(err) // embedded and tested
	}
	return c
}

// parseCatalog reads a component catalogue (cpictl components format).
func parseCatalog(data []byte, name string) (*catalog.Catalog, error) {
	var c catalog.Catalog
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if c.Schema != catalog.Schema {
		return nil, fmt.Errorf("%s: not a component catalogue (schema %q, expected %q)", name, c.Schema, catalog.Schema)
	}
	return &c, nil
}

// checkFlow runs the structural part of a tenant's Check on an integration
// flow: component versions the runtime profile supports (cat; nil: no
// version check), required channel attributes, numeric values, mail
// addresses and the scripts the steps reference. The messages for the
// component versions, adapter details, address and mail attributes are
// worded as a tenant words them; the rest follow the same pattern.
func checkFlow(c *artifactContent, cat *catalog.Catalog) []checkIssue {
	if c.Model == nil {
		return nil
	}
	m, err := iflow.Parse(c.Model)
	if err != nil {
		return []checkIssue{{Severity: "Error", Message: "The integration flow model cannot be read: " + err.Error()}}
	}
	resPath, resName := path.Split(c.ModelPath)
	resPath = strings.TrimSuffix(resPath, "/")
	var issues []checkIssue
	add := func(el *iflow.Element, etype, pid, msg string) {
		src := fmt.Sprintf("ComponentMetadataEditor/eid:%s/etype:%s/location:%s", el.ID, etype, el.Name)
		if pid != "" {
			src += "/pid:" + pid
		}
		issues = append(issues, checkIssue{Severity: "Error", Message: msg, ResourcePath: resPath, ResourceName: resName, SourceObject: src})
	}
	if cat != nil {
		for _, a := range m.Adapters {
			details, version := checkAdapterVersion(cat, a)
			if details {
				add(a, "MessageFlow", "", "Enter adapter details for channel")
			}
			if version != "" {
				add(a, "MessageFlow", "", unsupported(a.Props["ComponentType"], version))
			}
		}
		for _, id := range m.Order {
			el := m.Elements[id]
			uri := el.Props["cmdVariantUri"]
			v := catalog.Version(uri)
			offered, ok := cat.Offered(uri)
			if v != "" && ok && offered != "" && catalog.CompareVersions(v, offered) > 0 {
				add(el, etypeOf(el), "", unsupported(el.Kind(), v))
			}
		}
	}
	for _, a := range m.Adapters {
		kind := a.Props["ComponentType"] + "/" + a.Props["direction"]
		for _, k := range requiredProps[kind] {
			if strings.TrimSpace(a.Props[k]) == "" {
				add(a, "MessageFlow", k, fmt.Sprintf("Enter a value for the '%s' attribute", cmpLabel(k)))
			}
		}
		if kind == "SFTP/Receiver" && a.Props["authentication"] == "user_password" &&
			a.Props["credential_name"] == "" && a.Props["credentialName"] == "" {
			add(a, "MessageFlow", "credential_name", "Enter a value for the 'Credential Name' attribute")
		}
		for _, k := range numericProps[kind] {
			if v := a.Props[k]; v != "" && !isParam(v) {
				if _, err := strconv.Atoi(v); err != nil {
					add(a, "MessageFlow", k, fmt.Sprintf("'%s' must be a whole number", k))
				}
			}
		}
		if kind == "Mail/Receiver" {
			for k, label := range map[string]string{"from": "From", "to": "To"} {
				if v := a.Props[k]; !isParam(v) {
					if _, err := mail.ParseAddress(v); err != nil {
						add(a, "MessageFlow", k, fmt.Sprintf("'%s' must be a valid mail address", label))
					}
				}
			}
		}
	}
	for _, id := range m.Order {
		el := m.Elements[id]
		script := el.Props["script"]
		if el.Kind() != "GroovyScript" || script == "" || el.Props["scriptBundleId"] != "" || isParam(script) {
			continue
		}
		if _, ok := c.Files[resourcesDir+"script/"+script]; !ok {
			add(el, "CallActivity", "script", fmt.Sprintf("The script file '%s' does not exist", script))
		}
	}
	return issues
}

func unsupported(component, version string) string {
	return fmt.Sprintf("This component %s with version %s is not supported in %s profile.", component, version, runtimeProfile)
}

// etypeOf is the element type in a sourceObject (callActivity -> CallActivity).
func etypeOf(el *iflow.Element) string {
	if el.Tag == "" {
		return ""
	}
	return strings.ToUpper(el.Tag[:1]) + el.Tag[1:]
}

// checkAdapterVersion checks a channel against the runtime profile's
// catalogue as a tenant does. details: the variant URI names no variant the
// tenant knows (it then asks for the adapter details). version: the version
// the profile does not support (empty: supported). The tenant reads the
// version from componentVersion and falls back to 1.0 without it; the
// profile supports versions up to the one its editor offers, and no 1.0 of
// an adapter that has a newer version (SAP's adapters of the first
// generation), which is what a real tenant answered for HTTPS, HTTP,
// ProcessDirect and JMS channels with version 1.0.
func checkAdapterVersion(cat *catalog.Catalog, a *iflow.Element) (details bool, version string) {
	uri := a.Props["cmdVariantUri"]
	offered, ok := "", false
	if uri != "" {
		if _, known := cat.Latest(uri); !known {
			details = true
		}
		offered, ok = cat.Offered(uri)
	}
	if !ok {
		offered, ok = offeredAdapter(cat, a)
	}
	if !ok {
		return details, ""
	}
	cv := cmp.Or(a.Props["componentVersion"], "1.0")
	offeredCV := majorMinor(offered)
	switch {
	case catalog.CompareVersions(cv, offeredCV) > 0:
		return details, cv
	case uri != "" && catalog.Version(uri) != "" && catalog.CompareVersions(catalog.Version(uri), offered) > 0:
		return details, catalog.Version(uri)
	case catalog.CompareVersions(majorMinor(cv), "1.0") <= 0 && catalog.CompareVersions(offeredCV, "1.0") > 0:
		return details, cv
	}
	return details, ""
}

// offeredAdapter finds the editor's version of a channel's adapter by its
// properties (ComponentType, direction, protocols) when the variant URI does
// not name it; the newest of the matching variants.
func offeredAdapter(cat *catalog.Catalog, a *iflow.Element) (string, bool) {
	best, ok := "", false
	for _, e := range cat.Adapters {
		if e.Source != catalog.SourceEditor || !strings.EqualFold(e.Name, a.Props["ComponentType"]) || !strings.EqualFold(e.Direction, a.Props["direction"]) {
			continue
		}
		if tp := a.Props["TransportProtocol"]; tp != "" && e.TransportProtocol != "" && !strings.EqualFold(tp, e.TransportProtocol) {
			continue
		}
		if mp := a.Props["MessageProtocol"]; mp != "" && e.MessageProtocol != "" && !strings.EqualFold(mp, e.MessageProtocol) {
			continue
		}
		if !ok || catalog.CompareVersions(e.Version, best) > 0 {
			best, ok = e.Version, true
		}
	}
	return best, ok
}

// majorMinor is the first two parts of a version (1.6.3 -> 1.6).
func majorMinor(v string) string {
	parts := strings.SplitN(v, ".", 3)
	if len(parts) > 2 {
		parts = parts[:2]
	}
	return strings.Join(parts, ".")
}

func cmpLabel(k string) string {
	if l, ok := propLabel[k]; ok {
		return l
	}
	return k
}

func isParam(v string) bool { return strings.HasPrefix(v, "{{") && strings.HasSuffix(v, "}}") }

// checkResult is the response text of the Check: Passed, or Failed with the
// findings as SAP's JSON array.
func checkResult(issues []checkIssue) string {
	if len(issues) == 0 {
		return "Check execution result: Passed"
	}
	b, _ := json.MarshalIndent(issues, "", "  ")
	return "Check execution result: Failed\n" + string(b)
}
