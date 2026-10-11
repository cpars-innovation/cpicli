// Package content compares, hashes and normalises the content of artifacts
// (a folder with META-INF/MANIFEST.MF and src/main/resources) exactly as
// cpictl does, so that "changed" means the same everywhere:
//
//   - UploadHash: what an upload compares (no Bundle-Version); equal hashes
//     mean an upload would report UNCHANGED. Stored per artifact in the
//     snapshot state (snapshot.ArtifactState.UploadHash).
//   - ContentHash: the significant content including Bundle-Version.
//   - TreeHash: every file, exact bytes (equal hashes: git sees no change).
//   - NormalizeTree, NormalizeProperties: the stable form a snapshot
//     writes (LF line endings, no timestamp comments, sorted keys).
//   - TreeDiff, DiffDirectories, UnifiedDiff: which files differ, and how.
//
// The functions are pure (local files only) and safe for concurrent use.
package content
