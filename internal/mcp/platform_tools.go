package mcp

import (
	"context"
	"encoding/json"

	"github.com/cpars-innovation/cpicli/pkg/ops"
)

// platformTools read tenant-wide runtime information.
func platformTools(cfg Config, readOnly map[string]any) []Tool {
	return []Tool{
		{
			Name: "list_integration_connections", Title: "List integration connections",
			Description: "The connections the tenant computes from its deployed flows: sender host -> flows -> receiver host (Web UI \"Integration Connections\"). filter keeps connections with a host or flow containing it; refresh=true recomputes instead of answering from the tenant's cache.",
			InputSchema: object(props{"filter": str("Host or flow substring (case-insensitive)"), "refresh": boolean("Recompute from the deployed flows")}),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Filter  string `json:"filter"`
					Refresh bool   `json:"refresh"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				return ops.ListConnections(cfg.Exe, a.Refresh, a.Filter)
			},
		},
		{
			Name: "get_tenant_settings", Title: "Read tenant-wide runtime settings",
			Description: "Whether external logging (e.g. to Splunk) and archiving of message processing logs are on, and the last archiving runs (logs archived, failed). Parts the credentials cannot read are listed as warnings.",
			InputSchema: object(props{}),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				return ops.GetTenantSettings(cfg.Exe), nil
			},
		},
		{
			Name: "scan_secrets", Title: "Find passwords, tokens and keys outside the secure store",
			Description: "Reads the Partner Directory (string and binary parameters, files inside ZIPs) and the configuration parameters of every integration flow, and reports values that are security material: " +
				"private keys, JSON web tokens, provider access keys, HTTP authorization values, URLs with credentials, values of parameters named like a password, secret, token or key that are not a credential name. " +
				"Never returns a value: kind, reason and a fingerprint (SHA-256 prefix). only limits the areas, packages the flows read. Security material belongs in the secure store or keystore.",
			InputSchema: object(props{"only": enum("Only this area", ops.AreaPartnerDirectory, ops.AreaConfiguration),
				"packages": strArray("Configuration: only these packages (IDs or patterns)")}),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Only     string   `json:"only"`
					Packages []string `json:"packages"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				res := &ops.SecretScanResult{Source: "tenant", Findings: []ops.SecretFinding{}}
				if a.Only != ops.AreaConfiguration {
					if err := ops.ScanTenantPartnerDirectory(cfg.Exe, res); err != nil {
						return nil, err
					}
				}
				if a.Only != ops.AreaPartnerDirectory {
					if err := ops.ScanTenantConfigurations(ctx, cfg.Exe, a.Packages, 8, res); err != nil {
						return nil, err
					}
				}
				ops.SortSecretFindings(res.Findings)
				return res, nil
			},
		},
		{
			Name: "download_log_archive", Title: "Download the latest log files as a zip",
			Description: "Saves the zip of the latest runtime log files of a type (http: HTTP access logs, trace: trace logs) to a file below the server root; use list_log_files / get_log_file to read single files.",
			InputSchema: object(props{"type": enum("Log file type", "http", "trace"), "path": str("Zip file to write, relative to the server root")}, "type", "path"),
			Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true, "openWorldHint": true},
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Type string `json:"type"`
					Path string `json:"path"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				file, err := resolvePath(cfg.Root, a.Path)
				if err != nil {
					return nil, err
				}
				return ops.DownloadLogArchive(cfg.Exe, a.Type, file)
			},
		},
	}
}
