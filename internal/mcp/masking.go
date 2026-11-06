package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"slices"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/mask"
	"github.com/rs/zerolog/log"
)

// Masking control: how far an MCP client may change the masking policy
// while the server runs (--masking-control). An agent is an MCP client too,
// so the default lets it only add masking, never remove it.
const (
	// MaskingTighten: set_masking may add detectors, names and rules and
	// withhold payloads, never take anything away (the default).
	MaskingTighten = "tighten"
	// MaskingFull: set_masking may change everything, also send payloads.
	// For a client that keeps the tool from its agent (CPI Studio, the
	// cpictl gateway): it decides what the agent sees.
	MaskingFull = "full"
	// MaskingFixed: the policy of the start is final; set_masking refuses.
	MaskingFixed = "fixed"
)

// MaskingControls are the values of --masking-control.
var MaskingControls = []string{MaskingTighten, MaskingFull, MaskingFixed}

// maskingView is what get_masking and set_masking return: the policy, never
// a masked value.
type maskingView struct {
	Payloads  string      `json:"payloads"` // withheld or sent
	Detectors []string    `json:"detectors"`
	Names     []string    `json:"names"`
	Rules     []mask.Rule `json:"rules"`
	Control   string      `json:"control"`
	// Available are the built-in detectors.
	Available []string `json:"available"`
}

func maskingViewOf(r *Redactor, control string) maskingView {
	cfg, withhold := r.Policy()
	v := maskingView{Payloads: "sent", Detectors: effectiveDetectors(cfg), Names: nonNilStrings(cfg.Names),
		Rules: cfg.Rules, Control: control, Available: mask.BuiltIns}
	if v.Rules == nil {
		v.Rules = []mask.Rule{}
	}
	if withhold {
		v.Payloads = "withheld"
	}
	return v
}

func effectiveDetectors(cfg mask.Config) []string {
	if cfg.BuiltIns == nil {
		return slices.Clone(mask.BuiltIns)
	}
	return slices.Clone(cfg.BuiltIns)
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// MaskingTools are get_masking and set_masking on the server's redactor.
func MaskingTools(redactor func() *Redactor, control string) []Tool {
	get := Tool{
		Name: "get_masking", Title: "Show the masking policy",
		Description: "What this server masks in every result: whether message payloads and error texts are withheld, " +
			"the detectors (IBAN, e-mail, phone, card, secrets), sensitive names and rules, and how far set_masking may change it (control).",
		InputSchema: object(props{}),
		Annotations: map[string]any{"readOnlyHint": true, "openWorldHint": false},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			return maskingViewOf(redactor(), control), nil
		},
	}
	set := Tool{
		Name: "set_masking", Title: "Change the masking policy",
		Description: "Change what this server masks from now on. With control tighten (the default) only more masking is accepted: " +
			"add detectors, names (regexes of sensitive names) or rules ({kind: path, pattern: //Customer/Name} or {kind: regex, pattern}), " +
			"or withhold payloads. mode add (default) merges into the policy, replace sets it whole.",
		InputSchema: object(props{
			"detectors": strArray("Built-in detectors (see get_masking available)"),
			"names":     strArray("Regexes of further sensitive names (case-insensitive)"),
			"rules": map[string]any{"type": "array", "description": "Path or regex rules", "items": object(props{
				"kind":    map[string]any{"type": "string", "enum": []string{"path", "regex"}},
				"pattern": str("//Element/Path or a regular expression"),
			}, "kind", "pattern")},
			"payloads": map[string]any{"type": "string", "enum": []string{"withheld", "sent"}, "description": "Withhold or send message payloads and error texts"},
			"mode":     map[string]any{"type": "string", "enum": []string{"add", "replace"}, "description": "add (default) or replace"},
		}),
		Annotations: map[string]any{"readOnlyHint": false, "idempotentHint": true, "openWorldHint": false},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var a struct {
				Detectors *[]string   `json:"detectors"`
				Names     []string    `json:"names"`
				Rules     []mask.Rule `json:"rules"`
				Payloads  string      `json:"payloads"`
				Mode      string      `json:"mode"`
			}
			if err := decode(raw, &a); err != nil {
				return nil, err
			}
			r := redactor()
			if control == MaskingFixed {
				return nil, output.Usagef("set_masking: the masking policy of this server is fixed (--masking-control fixed)")
			}
			err := r.UpdatePolicy(func(old mask.Config, withhold bool) (mask.Config, bool, error) {
				next, nextWithhold := old, withhold
				switch a.Mode {
				case "", "add":
					if a.Detectors != nil {
						next.BuiltIns = union(effectiveDetectors(old), *a.Detectors)
					}
					next.Names = union(old.Names, a.Names)
					next.Rules = unionRules(old.Rules, a.Rules)
				case "replace":
					next = mask.Config{Names: a.Names, Rules: a.Rules}
					if a.Detectors != nil {
						next.BuiltIns = *a.Detectors
					}
				default:
					return old, withhold, output.Usagef("set_masking: mode is add or replace")
				}
				switch a.Payloads {
				case "withheld":
					nextWithhold = true
				case "sent":
					nextWithhold = false
				case "":
				default:
					return old, withhold, output.Usagef("set_masking: payloads is withheld or sent")
				}
				if control != MaskingFull {
					if why := loosens(old, withhold, next, nextWithhold); why != "" {
						return old, withhold, output.Usagef("set_masking: %s; this server only accepts more masking (--masking-control tighten)", why)
					}
				}
				return next, nextWithhold, nil
			})
			if err != nil {
				if output.ExitCode(err) == exitcode.Usage {
					return nil, err
				}
				return nil, output.Usage(fmt.Errorf("set_masking: %w", err))
			}
			v := maskingViewOf(r, control)
			log.Info().Str("payloads", v.Payloads).Strs("detectors", v.Detectors).Int("names", len(v.Names)).Int("rules", len(v.Rules)).Msg("Masking policy changed")
			return v, nil
		},
	}
	return []Tool{get, set}
}

// loosens says what next takes away from old ("" when nothing).
func loosens(old mask.Config, oldWithhold bool, next mask.Config, nextWithhold bool) string {
	if oldWithhold && !nextWithhold {
		return "it would send payloads that are withheld"
	}
	for _, d := range effectiveDetectors(old) {
		if !slices.Contains(effectiveDetectors(next), d) {
			return "it would drop the detector " + d
		}
	}
	for _, n := range old.Names {
		if !slices.Contains(next.Names, n) {
			return fmt.Sprintf("it would drop the name %q", n)
		}
	}
	for _, rule := range old.Rules {
		if !slices.Contains(next.Rules, rule) {
			return fmt.Sprintf("it would drop the %s rule %q", rule.Kind, rule.Pattern)
		}
	}
	return ""
}

func union(a, b []string) []string {
	out := slices.Clone(a)
	for _, x := range b {
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

func unionRules(a, b []mask.Rule) []mask.Rule {
	out := slices.Clone(a)
	for _, x := range b {
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}
