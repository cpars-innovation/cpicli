package ops

import (
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/cpars-innovation/cpicli/pkg/pdaddress"
)

// ProcessDirectAddresses returns the ProcessDirect sender addresses of an
// integration flow on the tenant, with {{parameters}} resolved to their
// configured values. Used by the access scope to decide which flow a test
// harness call reaches. Programs without the API client use
// pdaddress.Reader.
func ProcessDirectAddresses(exe *httpclnt.HTTPExecuter, id string) ([]string, error) {
	zipped, err := cpi.DownloadArtifact(exe, "Integration", id, "active")
	if err != nil {
		return nil, err
	}
	raw, err := pdaddress.FromArchive(zipped)
	if err != nil || len(raw) == 0 {
		return nil, err
	}
	return pdaddress.Resolve(raw, func() map[string]string {
		params := map[string]string{}
		if cfg, err := cpi.NewConfiguration(exe).Get(id, "active"); err == nil {
			for _, p := range cfg.Root.Results {
				params[p.ParameterKey] = p.ParameterValue
			}
		}
		return params
	}), nil
}
