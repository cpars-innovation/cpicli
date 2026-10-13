package testrun

import (
	"maps"
	"slices"
)

// Listing is the overview of the cases (test list, MCP list_tests).
type Listing struct {
	Cases int           `json:"cases"`
	Flows []FlowListing `json:"flows"`
}

// FlowListing lists the cases of one flow.
type FlowListing struct {
	Flow string `json:"flow"`
	// ConfigKeys are the parameters of the flow's config.yaml (keys only).
	ConfigKeys []string `json:"configKeys,omitempty"`
	// ConfigFlows are the other flows config.yaml configures (flows:), with
	// their keys.
	ConfigFlows map[string][]string `json:"configFlows,omitempty"`
	Cases       []CaseInfo          `json:"cases"`
}

// CaseInfo describes a case without its payload.
type CaseInfo struct {
	Case        string   `json:"case"`
	File        string   `json:"file,omitempty"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Skip        string   `json:"skip,omitempty"`
	Order       int      `json:"order,omitempty"`
	// Trigger is how the message gets in: http, idoc, odata or
	// process_direct.
	Trigger string `json:"trigger"`
	// StubCalls is set when the case checks receiver stub calls (it runs alone).
	StubCalls bool `json:"stubCalls,omitempty"`
}

// List groups cases by flow (in their order).
func List(cases []Case) *Listing {
	l := &Listing{Cases: len(cases), Flows: []FlowListing{}}
	for _, c := range cases {
		if n := len(l.Flows); n == 0 || l.Flows[n-1].Flow != c.Flow {
			fl := FlowListing{Flow: c.Flow, ConfigKeys: slices.Sorted(maps.Keys(c.Config))}
			for f, values := range c.FlowConfig {
				if fl.ConfigFlows == nil {
					fl.ConfigFlows = map[string][]string{}
				}
				fl.ConfigFlows[f] = slices.Sorted(maps.Keys(values))
			}
			l.Flows = append(l.Flows, fl)
		}
		f := &l.Flows[len(l.Flows)-1]
		f.Cases = append(f.Cases, CaseInfo{Case: c.Name, File: c.File, Description: c.Description, Tags: c.Tags, Skip: c.Skip,
			Order: c.Order, Trigger: c.Request.trigger(), StubCalls: len(c.Expect.StubCalls) > 0})
	}
	return l
}

func (r Request) trigger() string {
	switch {
	case r.IDoc != "" || r.IDocFile != "":
		return "idoc"
	case r.OData != nil:
		return "odata"
	case r.ProcessDirectAddress != "":
		return "process_direct"
	}
	return "http"
}
