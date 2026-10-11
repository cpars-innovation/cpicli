package ops

import (
	"net/http"
	"testing"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetRuntimeStatus(t *testing.T) {
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"A": {Runtime: &cpitest.Runtime{Version: "1.0.0", Status: "STARTED", DeployedOn: t0}},
		"B": {Runtime: &cpitest.Runtime{Version: "2", Status: "ERROR"}, ErrorInfo: "mapping failed"},
		"C": {},
	})
	got := GetRuntimeStatus(mock.Executer(), []string{"A", "B", "C"})
	require.Len(t, got, 3)
	assert.True(t, got[0].Deployed)
	assert.Equal(t, "STARTED", got[0].Status)
	require.NotNil(t, got[0].DeployedOn)
	assert.Equal(t, t0, *got[0].DeployedOn)
	assert.Equal(t, "mapping failed", got[1].ErrorInfo)
	assert.False(t, got[2].Deployed)
	assert.Empty(t, got[2].Error)
}

func TestListPackagesAndArtifacts(t *testing.T) {
	mock := cpitest.NewTenant(t, map[string]*cpitest.Artifact{
		"Flow1": {Type: "Integration", DesignVersion: "1.0.0", Package: "P1", Name: "Flow One", ModifiedBy: "jane", ModifiedAt: t1},
		"Map1":  {Type: "MessageMapping", DesignVersion: "1.0.1", Package: "P1"},
		"Other": {Type: "Integration", DesignVersion: "1", Package: "P2"},
	})
	mock.Packages = []cpitest.Package{{ID: "P2", Name: "Two"}, {ID: "P1", Name: "One", Version: "1.0"}}

	pkgs, err := ListPackages(mock.Executer())
	require.NoError(t, err)
	assert.Equal(t, []Package{{ID: "P1", Name: "One", Version: "1.0"}, {ID: "P2", Name: "Two"}}, pkgs)

	arts, err := ListArtifacts(mock.Executer(), "P1")
	require.NoError(t, err)
	assert.Equal(t, []DesigntimeArtifact{
		{ID: "Flow1", Name: "Flow One", Type: "Integration", Version: "1.0.0", ModifiedBy: "jane", ModifiedAt: &t1},
		{ID: "Map1", Type: "MessageMapping", Version: "1.0.1"},
	}, arts)
}

func TestUpdateConfiguration(t *testing.T) {
	newMock := func(t *testing.T) (*cpitest.Tenant, *cpitest.Artifact) {
		a := &cpitest.Artifact{Type: "Integration", DesignVersion: "1", Parameters: map[string]string{"Host": "old", "Port": "443"}}
		return cpitest.NewTenant(t, map[string]*cpitest.Artifact{"A": a}), a
	}

	t.Run("writes only changed values", func(t *testing.T) {
		mock, a := newMock(t)
		res, err := UpdateConfiguration(mock.Executer(), "A", "", map[string]string{"Host": "new", "Port": "443"}, false)
		require.NoError(t, err)
		assert.Equal(t, []string{"Host"}, res.Updated)
		assert.Equal(t, []string{"Port"}, res.Unchanged)
		assert.Equal(t, "new", a.Parameters["Host"])
		assert.Equal(t, 1, mock.Count("PUT "))
	})

	t.Run("dry run writes nothing", func(t *testing.T) {
		mock, a := newMock(t)
		res, err := UpdateConfiguration(mock.Executer(), "A", "", map[string]string{"Host": "new"}, true)
		require.NoError(t, err)
		assert.Equal(t, []string{"Host"}, res.Updated)
		assert.Equal(t, "old", a.Parameters["Host"])
		assert.Zero(t, mock.Count("PUT "))
	})

	t.Run("unknown key aborts before writing", func(t *testing.T) {
		mock, _ := newMock(t)
		res, err := UpdateConfiguration(mock.Executer(), "A", "", map[string]string{"Host": "new", "Typo": "x"}, false)
		assert.Equal(t, exitcode.Usage, output.ExitCode(err))
		assert.Equal(t, []string{"Typo"}, res.NotFound)
		assert.Zero(t, mock.Count("PUT "))
	})

	t.Run("tenant rejects update", func(t *testing.T) {
		mock, a := newMock(t)
		a.ConfigUpdateStatus = http.StatusInternalServerError
		res, err := UpdateConfiguration(mock.Executer(), "A", "", map[string]string{"Host": "new"}, false)
		assert.Equal(t, exitcode.TenantHTTP, output.ExitCode(err))
		require.Len(t, res.Failed, 1)
	})
}

func TestUploadArtifactValidates(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	_, err := UploadArtifact(mock.Executer(), UploadRequest{ID: "A", Type: "Bogus", PackageID: "P", Dir: t.TempDir()})
	assert.Equal(t, exitcode.Usage, output.ExitCode(err))
	_, err = UploadArtifact(mock.Executer(), UploadRequest{ID: "A", Type: "Integration", PackageID: "P", Dir: "/does/not/exist"})
	assert.Equal(t, exitcode.Usage, output.ExitCode(err))
	assert.Empty(t, mock.Requests())
}
