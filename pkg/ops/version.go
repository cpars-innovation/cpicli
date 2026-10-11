package ops

import "github.com/cpars-innovation/cpicli/pkg/versioning"

// CompareVersions compares two artifact versions segment by segment (see
// versioning.Compare). It returns -1, 0 or 1.
func CompareVersions(a, b string) int { return versioning.Compare(a, b) }
