package sync

import "github.com/cpars-innovation/cpicli/pkg/snapshot"

// The snapshot state is public (pkg/snapshot); these names keep the
// internal callers (orchestrator baseline) unchanged.
type (
	SnapshotState = snapshot.State
	ArtifactState = snapshot.ArtifactState
)

// LoadSnapshotState reads a state file; a missing file is an empty state.
func LoadSnapshotState(path string) (*SnapshotState, error) { return snapshot.LoadState(path) }
