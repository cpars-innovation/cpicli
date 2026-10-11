package ops

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/internal/exitcode"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/pdrepo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func pdTenant(t *testing.T) (*cpitest.Tenant, *cpi.PartnerDirectory) {
	t.Helper()
	mock := cpitest.NewTenant(t, nil)
	mock.PDStrings = map[string]string{"ONE_OMS/endpoint": "https://oms", "ONE_OMS/old": "x", "OTHER/k": "v"}
	mock.PDBinaries = map[string]cpitest.PDBinary{
		"ONE_OMS/now_email": {ContentType: "xsl", Content: []byte("<xsl:stylesheet/>")},
		"ONE_OMS/remote":    {ContentType: "xml", Content: []byte("<a/>")},
	}
	return mock, cpi.NewPartnerDirectory(mock.Executer())
}

func TestGetPDParameters(t *testing.T) {
	_, api := pdTenant(t)
	res, err := GetPDParameters(api, "ONE_OMS", nil, false, 0)
	require.NoError(t, err)
	assert.Equal(t, []PDString{{"endpoint", "https://oms"}, {"old", "x"}}, res.Strings)
	require.Len(t, res.Binaries, 2)
	b := res.Binaries[0]
	assert.Equal(t, "now_email", b.ID)
	assert.Equal(t, 17, b.Size)
	assert.Equal(t, sha256Hex([]byte("<xsl:stylesheet/>")), b.SHA256)
	assert.Nil(t, b.Content, "content only on request")

	res, err = GetPDParameters(api, "ONE_OMS", []string{"now_email", "nope"}, true, 0)
	require.NoError(t, err)
	assert.Empty(t, res.Strings)
	require.Len(t, res.Binaries, 1)
	assert.Equal(t, "<xsl:stylesheet/>", res.Binaries[0].Content.Text)
	assert.Equal(t, []string{"nope"}, res.Missing)
}

func writeLocalPD(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "ONE_OMS", "String.properties"), "endpoint=https://oms-new\nnew=1\n")
	writeFile(t, filepath.Join(dir, "ONE_OMS", "Binary", "now_email.xsl"), "<xsl:stylesheet/>")
	writeFile(t, filepath.Join(dir, "ONE_OMS", "Binary", "fresh.xslt"), "<xsl:new/>")
	writeFile(t, filepath.Join(dir, "ONE_OMS", "Binary", "data.bin"), "x")
	return dir
}

func TestPDDiff(t *testing.T) {
	_, api := pdTenant(t)
	dir := writeLocalPD(t)
	res, err := PDDiff(api, pdrepo.New(dir), nil)
	require.NoError(t, err)
	changes := map[string]string{}
	for _, it := range res.Items {
		changes[it.Kind+":"+it.ID] = it.Change
	}
	assert.Equal(t, map[string]string{
		"string:endpoint": PDUpdate, "string:new": PDCreate, "string:old": PDRemoteOnly,
		"binary:now_email": PDUnchanged, "binary:fresh": PDCreate, "binary:data": PDCreate, "binary:remote": PDRemoteOnly,
	}, changes)
	assert.Equal(t, 2, res.Summary[PDRemoteOnly])

	_, err = PDDiff(api, pdrepo.New(dir), []string{"MISSING"})
	assert.Equal(t, exitcode.Usage, output.ExitCode(err))
}

func TestPDDiffUnreadablePIDIsAnError(t *testing.T) {
	_, api := pdTenant(t)
	dir := writeLocalPD(t)
	// a directory where the properties file should be: unreadable for any user
	props := filepath.Join(dir, "ONE_OMS", "String.properties")
	require.NoError(t, os.Remove(props))
	require.NoError(t, os.Mkdir(props, 0o755))
	res, err := PDDiff(api, pdrepo.New(dir), nil)
	require.Error(t, err)
	assert.Equal(t, exitcode.Partial, output.ExitCode(err))
	for _, it := range res.Items {
		assert.NotEqual(t, PDRemoteOnly, it.Change, "an unreadable PID must not look empty")
	}
	assert.Len(t, res.Errors, 1)
}

func TestPDDeployKeys(t *testing.T) {
	mock, api := pdTenant(t)
	dir := writeLocalPD(t)
	local := pdrepo.New(dir)

	res, err := PDDeploy(api, local, PDDeployOptions{Keys: []string{"ONE_OMS:fresh", "ONE_OMS:endpoint", "ONE_OMS:now_email"}})
	require.NoError(t, err)
	assert.Equal(t, []PDKeyResult{
		{Key: "ONE_OMS:fresh", Kind: "binary", Action: "CREATED"},
		{Key: "ONE_OMS:endpoint", Kind: "string", Action: "UPDATED"},
		{Key: "ONE_OMS:now_email", Kind: "binary", Action: "UNCHANGED"},
	}, res.Keys)
	assert.Equal(t, "xsl", mock.PDBinaries["ONE_OMS/fresh"].ContentType, ".xslt falls back to xsl")
	assert.Equal(t, "https://oms-new", mock.PDStrings["ONE_OMS/endpoint"])
	assert.Equal(t, "x", mock.PDStrings["ONE_OMS/old"], "nothing else is touched")
	_, hasNew := mock.PDStrings["ONE_OMS/new"]
	assert.False(t, hasNew, "keys not named are not written")
	assert.Equal(t, 1, mock.Count("POST /api/v1/BinaryParameters")+mock.Count("POST /api/v1/StringParameters"))
	assert.Equal(t, 1, mock.Count("PUT /api/v1/StringParameters"))

	for _, opts := range []PDDeployOptions{
		{Keys: []string{"ONE_OMS:fresh"}, FullSync: true},
		{Keys: []string{"ONE_OMS:data"}},    // unknown extension
		{Keys: []string{"ONE_OMS:missing"}}, // not local
		{Keys: []string{"no-colon"}},
	} {
		_, err := PDDeploy(api, local, opts)
		assert.Equal(t, exitcode.Usage, output.ExitCode(err), "%+v", opts)
	}

	// every SAP content type deploys by its file extension
	for ext, ct := range map[string]string{".txt": "txt", ".zip": "zip", ".gz": "gz", ".zlib": "zlib", ".crt": "crt"} {
		writeFile(t, filepath.Join(dir, "ONE_OMS", "Binary", "file_"+ct+ext), "content "+ct)
		_, err := PDDeploy(api, local, PDDeployOptions{Keys: []string{"ONE_OMS:file_" + ct}})
		require.NoError(t, err, ext)
		assert.Equal(t, ct, mock.PDBinaries["ONE_OMS/file_"+ct].ContentType, ext)
	}

	before := len(mock.Requests())
	res, err = PDDeploy(api, local, PDDeployOptions{Keys: []string{"ONE_OMS:new"}, DryRun: true})
	require.NoError(t, err)
	assert.Equal(t, "CREATED", res.Keys[0].Action)
	for _, r := range mock.Requests()[before:] {
		assert.Regexp(t, `^GET `, r, "dry run writes nothing")
	}
}

// The tenant returns at most 30 binary parameters per page, whatever $top
// asks for. ONE_EDM had more: every page must be read, by $skip or by
// following __next.
func TestPDManyBinaries(t *testing.T) {
	for _, next := range []bool{false, true} {
		t.Run(fmt.Sprintf("next links %v", next), func(t *testing.T) {
			mock := cpitest.NewTenant(t, nil)
			mock.PDNextLinks = next
			mock.PDBinaries = map[string]cpitest.PDBinary{"OTHER/x": {ContentType: "xml", Content: []byte("<x/>")}}
			for i := range 75 {
				mock.PDBinaries[fmt.Sprintf("ONE_EDM/map%02d", i)] = cpitest.PDBinary{ContentType: "xsl", Content: []byte("<xsl/>")}
			}
			api := cpi.NewPartnerDirectory(mock.Executer())

			res, err := GetPDParameters(api, "ONE_EDM", nil, false, 0)
			require.NoError(t, err)
			assert.Len(t, res.Binaries, 75)

			// a requested key on the third page is found, not "missing"
			res, err = GetPDParameters(api, "ONE_EDM", []string{"map74"}, false, 0)
			require.NoError(t, err)
			assert.Empty(t, res.Missing)
			assert.Len(t, res.Binaries, 1)

			// the snapshot listing (all partner IDs) as well
			all, err := api.GetBinaryParameters("")
			require.NoError(t, err)
			assert.Len(t, all, 76)

			assert.GreaterOrEqual(t, mock.Count("GET /api/v1/BinaryParameters"), 3*3, "three pages per listing")
		})
	}
}
