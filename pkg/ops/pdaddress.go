package ops

import (
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/cpars-innovation/cpicli/pkg/pdaddress"
)

// ProcessDirectAddresses returns the ProcessDirect sender addresses of an
// integration flow on the tenant, for the access scope to decide which flow
// a test harness call reaches. Addresses set by a configuration parameter
// are left out (see pdaddress.Reader); an ambiguous model is an error.
// Programs without the API client use pdaddress.Reader.
func ProcessDirectAddresses(exe *httpclnt.HTTPExecuter, id string) ([]string, error) {
	zipped, err := cpi.DownloadArtifact(exe, "Integration", id, "active")
	if err != nil {
		return nil, err
	}
	raw, err := pdaddress.FromArchive(zipped)
	if err != nil {
		return nil, err
	}
	return pdaddress.Fixed(raw), nil
}
