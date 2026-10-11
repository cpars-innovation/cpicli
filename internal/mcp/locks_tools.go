package mcp

import (
	"context"
	"encoding/json"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/ops"
)

// lockTools list and remove edit locks. unlock_artifact is opt-in (see
// optInTools): a server offers it only when --tools names it.
func lockTools(cfg Config, readOnly map[string]any) []Tool {
	return []Tool{
		{
			Name: "list_locks", Title: "List edit locks",
			Description: "Artifacts open for editing in the Web UI (locked for everyone else): artifact, package, who holds the lock, since when. A lock left behind by a closed browser blocks updates until it is removed.",
			InputSchema: object(props{"artifact_id": str("Only this artifact's lock"), "package_id": str("Only locks in this package")}),
			Annotations: readOnly,
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					ArtifactID string `json:"artifact_id"`
					PackageID  string `json:"package_id"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				locks, err := ops.ListLocks(cfg.Exe, a.ArtifactID, a.PackageID)
				return map[string]any{"locks": locks}, err
			},
		},
		{
			Name: "unlock_artifact", Title: "Remove an edit lock",
			Description: "Remove the edit lock of an artifact (as Unlock in the Web UI). Whoever holds the lock loses unsaved changes: only on the user's explicit request, after list_locks showed who holds it. Requires confirm=true.",
			InputSchema: object(props{"artifact_id": str("Artifact to unlock"), "confirm": boolean("Must be true: the lock holder's unsaved changes are lost")}, "artifact_id", "confirm"),
			Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": true, "openWorldHint": true},
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					ArtifactID string `json:"artifact_id"`
					Confirm    bool   `json:"confirm"`
				}
				if err := decode(raw, &a); err != nil {
					return nil, err
				}
				if !a.Confirm {
					return nil, output.Usagef("unlock_artifact discards the unsaved changes of whoever holds the lock on %s; call again with confirm=true only if the user asked for it", a.ArtifactID)
				}
				return ops.Unlock(cfg.Exe, a.ArtifactID)
			},
		},
	}
}
