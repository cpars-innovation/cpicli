package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/catalog"
	"github.com/cpars-innovation/cpicli/pkg/ops"
)

// componentTools read the component catalogue (local file, no tenant call).
func componentTools(cfg Config, readOnly map[string]any) []Tool {
	return []Tool{
		{
			Name: "list_components", Title: "List adapter and step versions the tenant offers",
			Description: "The component catalogue (.cpi/components.json): adapter variants (name, direction, transport/message protocol) and step variants with the cmdVariantUri and version to use in flow XML. Filter by name (substring), kind (adapter, step) and direction. Built by 'cpictl components import' (the Web UI's editor metadata) and discover (versions in use).",
			InputSchema: object(props{"name": str("Only variants whose name contains this (case-insensitive), e.g. HTTP, Groovy"),
				"kind": enum("adapter or step", "adapter", "step"), "direction": enum("Adapters only: Sender or Receiver", "Sender", "Receiver")}),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Name      string `json:"name"`
					Kind      string `json:"kind"`
					Direction string `json:"direction"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				base, err := resolvePath(cfg.Root, ".")
				if err != nil {
					return nil, err
				}
				c, err := catalog.Load(filepath.Join(base, filepath.FromSlash(ops.ComponentsFile)))
				if errors.Is(err, fs.ErrNotExist) {
					return nil, output.Usagef("no component catalogue (%s): ask the user to run 'cpictl components import <editor metadata>' or run discover_tenant", ops.ComponentsFile)
				}
				if err != nil {
					return nil, err
				}
				return ops.FilterComponents(c, a.Name, a.Kind, a.Direction), nil
			},
		},
	}
}
