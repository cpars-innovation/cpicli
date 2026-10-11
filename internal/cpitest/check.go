package cpitest

import (
	"encoding/json"
	"fmt"
	"net/mail"
	"path"
	"strconv"
	"strings"

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

// checkFlow runs the structural part of a tenant's Check on an integration
// flow: required channel attributes, numeric values, mail addresses and the
// scripts the steps reference. Only the messages for the address and mail
// attributes are worded exactly as a tenant words them; the rest follow the
// same pattern.
func checkFlow(c *artifactContent) []checkIssue {
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
		issues = append(issues, checkIssue{Severity: "Error", Message: msg, ResourcePath: resPath, ResourceName: resName,
			SourceObject: fmt.Sprintf("ComponentMetadataEditor/eid:%s/etype:%s/location:%s/pid:%s", el.ID, etype, el.Name, pid)})
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
