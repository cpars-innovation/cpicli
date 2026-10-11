package cpi

import (
	"net/http"
	"time"

	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
)

// DesigntimeLock is the edit lock of an artifact: someone opened it for
// editing in the Web UI (or an editor session ended without saving).
type DesigntimeLock struct {
	ResourceID   string     `json:"resourceId"`
	ArtifactID   string     `json:"artifactId"`
	ArtifactName string     `json:"artifactName,omitempty"`
	ArtifactType string     `json:"artifactType,omitempty"`
	PackageID    string     `json:"packageId,omitempty"`
	PackageName  string     `json:"packageName,omitempty"`
	CreatedAt    *time.Time `json:"createdAt,omitempty"`
	CreatedBy    string     `json:"createdBy,omitempty"`
}

// DesigntimeLocks lists the edit locks of the tenant
// (/api/v1/IntegrationDesigntimeLocks).
func DesigntimeLocks(exe *httpclnt.HTTPExecuter) ([]DesigntimeLock, error) {
	var rows []struct {
		ResourceId, ArtifactId, ArtifactName, ArtifactType, PackageId, PackageName, CreatedAt, CreatedBy string
	}
	if err := getResults(exe, "/api/v1/IntegrationDesigntimeLocks", "Get design-time locks", &rows); err != nil {
		return nil, err
	}
	out := make([]DesigntimeLock, 0, len(rows))
	for _, r := range rows {
		l := DesigntimeLock{ResourceID: r.ResourceId, ArtifactID: r.ArtifactId, ArtifactName: r.ArtifactName, ArtifactType: r.ArtifactType,
			PackageID: r.PackageId, PackageName: r.PackageName, CreatedBy: r.CreatedBy}
		if t, err := ParseODataTime(r.CreatedAt); err == nil && r.CreatedAt != "" {
			l.CreatedAt = &t
		}
		out = append(out, l)
	}
	return out, nil
}

// DeleteDesigntimeLock removes an edit lock (unlock). Unsaved changes of the
// editor holding it are lost when that editor saves later.
func DeleteDesigntimeLock(exe *httpclnt.HTTPExecuter, resourceID string) error {
	resp, err := exe.Exec(http.MethodDelete, "/api/v1/IntegrationDesigntimeLocks("+pathKey(resourceID)+")", http.NoBody, map[string]string{"Accept": "application/json"})
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, err = exe.LogError(resp, "Delete design-time lock")
		return err
	}
	_ = resp.Body.Close()
	return nil
}
