package ops

import (
	"testing"

	"github.com/cpars-innovation/cpicli/internal/cpitest"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListPDPartners(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	mock.PDStrings = map[string]string{"ACME/url": "https://acme", "ACME/user": "u", "GLOBEX/x": "1"}
	mock.PDBinaries = map[string]cpitest.PDBinary{
		"ACME/map":  {ContentType: "xsl", Content: []byte("<xsl/>")},
		"ACME/cert": {ContentType: "crt", Content: []byte("12345")},
		"ACME/doc":  {ContentType: "xml; encoding=UTF-8", Content: []byte("<a/>")},
	}
	mock.AlternativePartners = []cpitest.AlternativePartner{{Agency: "EDI", Scheme: "GLN", ID: "4000001000002", Pid: "INITECH"}}
	api := cpi.NewPartnerDirectory(mock.Executer())

	partners, err := ListPDPartners(api)
	require.NoError(t, err)
	require.Len(t, partners, 3)
	acme := partners[0]
	assert.Equal(t, PDPartner{Pid: "ACME", Strings: 2, Binaries: 3, ContentTypes: []string{"crt", "xml", "xsl"}}, acme)
	assert.Equal(t, "INITECH", partners[2].Pid, "partners without parameters are listed")
	assert.Equal(t, 1, partners[2].AlternativePartners)
	assert.Equal(t, "Pid,Id,ContentType", mock.LastQuery("/api/v1/BinaryParameters", "$select"), "binary values are not downloaded")
	assert.Equal(t, "Pid,Id", mock.LastQuery("/api/v1/StringParameters", "$select"))

	sized, err := ListPDPartnersWith(api, PDPartnersOptions{Sizes: true, Pid: "acm"})
	require.NoError(t, err)
	require.Len(t, sized, 1)
	assert.Equal(t, int64(len("<xsl/>")+5+len("<a/>")), sized[0].BinaryBytes)
}

func TestPDContentTypes(t *testing.T) {
	for ext, want := range map[string]string{".xml": "xml", ".XSLT": "xsl", ".txt": "txt", ".zip": "zip", ".gz": "gz", ".zlib": "zlib", ".crt": "crt"} {
		ct, ok := PDContentTypeForExtension(ext)
		assert.True(t, ok, ext)
		assert.Equal(t, want, ct, ext)
	}
	_, ok := PDContentTypeForExtension(".exe")
	assert.False(t, ok)
	ct, ok := LookupPDContentType("text/xml; charset=UTF-8")
	require.True(t, ok)
	assert.Equal(t, "application/xml", ct.MIME)
	assert.Len(t, PDContentTypes, 9)
}

func TestAlternativePartnersAndAuthorizedUsers(t *testing.T) {
	mock := cpitest.NewTenant(t, nil)
	api := cpi.NewPartnerDirectory(mock.Executer())

	_, err := CreateAlternativePartner(api, cpi.AlternativePartner{Agency: "EDI"})
	assert.Error(t, err)
	res, err := CreateAlternativePartner(api, cpi.AlternativePartner{Agency: "EDI", Scheme: "GLN", ID: "4000001000002", Pid: "ACME"})
	require.NoError(t, err)
	assert.Equal(t, "CREATED", res.Action)
	alts, err := ListAlternativePartners(api, "ACME")
	require.NoError(t, err)
	require.Len(t, alts, 1)
	assert.Equal(t, "4000001000002", alts[0].ID)
	_, err = DeleteAlternativePartner(api, "EDI", "GLN", "4000001000002")
	require.NoError(t, err)
	assert.Contains(t, mock.Requests(), "DELETE /api/v1/AlternativePartners(Hexagency='"+cpi.HexAlias("EDI")+"',Hexscheme='"+cpi.HexAlias("GLN")+"',Hexid='"+cpi.HexAlias("4000001000002")+"')")
	_, err = DeleteAlternativePartner(api, "EDI", "GLN", "4000001000002")
	assert.ErrorContains(t, err, "not found")

	_, err = CreateAuthorizedUser(api, "CN=acme-client", "ACME")
	require.NoError(t, err)
	users, err := ListAuthorizedUsers(api, "")
	require.NoError(t, err)
	require.Len(t, users, 1)
	_, err = DeleteAuthorizedUser(api, "CN=acme-client")
	require.NoError(t, err)
	assert.Empty(t, mock.AuthorizedUsers)
	_, err = DeleteAuthorizedUser(api, "CN=acme-client")
	assert.ErrorContains(t, err, "not found")
}
