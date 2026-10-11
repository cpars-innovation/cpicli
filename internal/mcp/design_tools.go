package mcp

import (
	"context"
	"encoding/json"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/ops"
)

// designTools read and change value mappings and delete artifacts and
// packages. delete_artifact and delete_package are opt-in (see optInTools).
func designTools(cfg Config, readOnly map[string]any) []Tool {
	destructive := map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": true, "openWorldHint": true}
	return []Tool{
		{
			Name: "list_value_mappings", Title: "List value mappings",
			Description: "Value mapping artifacts of the tenant: ID, version, package. get_value_mapping shows their values.",
			InputSchema: object(props{"package_id": str("Only value mappings of this package")}),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					PackageID string `json:"package_id"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				vms, err := ops.ListValueMappings(cfg.Exe, a.PackageID)
				return map[string]any{"valueMappings": vms}, err
			},
		},
		{
			Name: "get_value_mapping", Title: "Read a value mapping",
			Description: "The schemas of a value mapping (source agency/identifier -> target agency/identifier) with their value pairs (id, srcValue, tgtValue) and default values, as the Web UI's value mapping editor shows them. Optional filters limit it to matching schemas.",
			InputSchema: object(props{
				"id":         str("Value mapping ID"),
				"src_agency": str("Only schemas with this source agency"),
				"src_id":     str("Only schemas with this source identifier"),
				"tgt_agency": str("Only schemas with this target agency"),
				"tgt_id":     str("Only schemas with this target identifier"),
			}, "id"),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					ID        string `json:"id"`
					SrcAgency string `json:"src_agency"`
					SrcID     string `json:"src_id"`
					TgtAgency string `json:"tgt_agency"`
					TgtID     string `json:"tgt_id"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				return ops.GetValueMapping(cfg.Exe, a.ID, ops.ValMapFilter{SrcAgency: a.SrcAgency, SrcID: a.SrcID, TgtAgency: a.TgtAgency, TgtID: a.TgtID})
			},
		},
		{
			Name: "set_value_mapping", Title: "Set a value mapping value",
			Description: "Add a value pair to an existing schema of a value mapping, or change the target value of an existing source value (action added, changed with previous, or unchanged). The schema (all four of src_agency, src_id, tgt_agency, tgt_id, as get_value_mapping shows them) must exist. The runtime uses the change after deploy with artifact_type ValueMapping. Requires confirm=true.",
			InputSchema: object(props{
				"id":         str("Value mapping ID"),
				"src_agency": str("Source agency of the schema"),
				"src_id":     str("Source identifier of the schema"),
				"tgt_agency": str("Target agency of the schema"),
				"tgt_id":     str("Target identifier of the schema"),
				"src_value":  str("Source value"),
				"tgt_value":  str("Target value"),
				"confirm":    boolean("Must be true: changes the value mapping on the tenant"),
			}, "id", "src_agency", "src_id", "tgt_agency", "tgt_id", "src_value", "tgt_value", "confirm"),
			Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true, "openWorldHint": true},
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					ID        string `json:"id"`
					SrcAgency string `json:"src_agency"`
					SrcID     string `json:"src_id"`
					TgtAgency string `json:"tgt_agency"`
					TgtID     string `json:"tgt_id"`
					SrcValue  string `json:"src_value"`
					TgtValue  string `json:"tgt_value"`
					Confirm   bool   `json:"confirm"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				if !a.Confirm {
					return nil, output.Usagef("set_value_mapping changes value mapping %s on the tenant; call again with confirm=true", a.ID)
				}
				return ops.SetValueMapping(cfg.Exe, ops.SetValueMappingRequest{ID: a.ID, SrcAgency: a.SrcAgency, SrcID: a.SrcID,
					TgtAgency: a.TgtAgency, TgtID: a.TgtID, SrcValue: a.SrcValue, TgtValue: a.TgtValue})
			},
		},
		{
			Name: "delete_artifact", Title: "Delete a designtime artifact",
			Description: "Delete a designtime artifact (integration flow, message mapping, script collection or value mapping) from the tenant. Cannot be undone: only on the user's explicit request, after download_artifact or a snapshot kept the content. A deployed artifact is refused unless undeploy_first, which undeploys it first. Requires confirm=true.",
			InputSchema: object(props{
				"artifact_id":           str("Artifact to delete"),
				"artifact_type":         enum("Artifact type (default: the type the tenant has an artifact with this ID of)", cpi.ArtifactTypes...),
				"undeploy_first":        boolean("Undeploy the artifact first if it is deployed (default false: a deployed artifact is refused)"),
				"confirm":               boolean("Must be true: deleting cannot be undone"),
				"poll_interval_seconds": integer("Seconds between undeployment status checks"),
				"max_checks":            integer("Maximum number of undeployment status checks"),
			}, "artifact_id", "confirm"),
			Annotations: destructive,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					ArtifactID    string `json:"artifact_id"`
					ArtifactType  string `json:"artifact_type"`
					UndeployFirst bool   `json:"undeploy_first"`
					Confirm       bool   `json:"confirm"`
					PollInterval  *int   `json:"poll_interval_seconds"`
					MaxChecks     *int   `json:"max_checks"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				if !a.Confirm {
					return nil, output.Usagef("delete_artifact deletes %s from the tenant for good; call again with confirm=true only if the user asked for it", a.ArtifactID)
				}
				return ops.DeleteArtifact(ctx, cfg.Exe, ops.DeleteRequest{ArtifactID: a.ArtifactID, Type: a.ArtifactType,
					UndeployFirst: a.UndeployFirst, Options: pollOptions(cfg, a.PollInterval, a.MaxChecks)})
			},
		},
		{
			Name: "delete_package", Title: "Delete an integration package",
			Description: "Delete an integration package from the tenant. Cannot be undone: only on the user's explicit request. A package that still has artifacts is refused unless with_artifacts (then they are deleted first); deployed artifacts are refused unless undeploy_first. Requires confirm=true.",
			InputSchema: object(props{
				"package_id":            str("Package to delete"),
				"with_artifacts":        boolean("Delete the package's artifacts too (default false: a package with artifacts is refused)"),
				"undeploy_first":        boolean("Undeploy deployed artifacts first (default false: refused)"),
				"confirm":               boolean("Must be true: deleting cannot be undone"),
				"poll_interval_seconds": integer("Seconds between undeployment status checks"),
				"max_checks":            integer("Maximum number of undeployment status checks per artifact"),
			}, "package_id", "confirm"),
			Annotations: destructive,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					PackageID     string `json:"package_id"`
					WithArtifacts bool   `json:"with_artifacts"`
					UndeployFirst bool   `json:"undeploy_first"`
					Confirm       bool   `json:"confirm"`
					PollInterval  *int   `json:"poll_interval_seconds"`
					MaxChecks     *int   `json:"max_checks"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				if !a.Confirm {
					return nil, output.Usagef("delete_package deletes package %s from the tenant for good; call again with confirm=true only if the user asked for it", a.PackageID)
				}
				return ops.DeletePackage(ctx, cfg.Exe, ops.DeletePackageRequest{PackageID: a.PackageID, WithArtifacts: a.WithArtifacts,
					UndeployFirst: a.UndeployFirst, Options: pollOptions(cfg, a.PollInterval, a.MaxChecks)})
			},
		},
	}
}
