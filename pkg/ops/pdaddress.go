package ops

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"

	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
)

// ProcessDirectAddresses returns the ProcessDirect sender addresses of an
// integration flow on the tenant, with {{parameters}} resolved to their
// configured values. Used by the access scope to decide which flow a test
// harness call reaches.
func ProcessDirectAddresses(exe *httpclnt.HTTPExecuter, id string) ([]string, error) {
	zipped, err := cpi.DownloadArtifact(exe, "Integration", id, "active")
	if err != nil {
		return nil, err
	}
	zr, err := zip.NewReader(bytes.NewReader(zipped), int64(len(zipped)))
	if err != nil {
		return nil, err
	}
	var raw []string
	for _, f := range zr.File {
		if !strings.HasSuffix(f.Name, ".iflw") || f.UncompressedSize64 > 64<<20 {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		model, err := io.ReadAll(io.LimitReader(rc, 64<<20))
		rc.Close()
		if err != nil {
			return nil, err
		}
		for _, s := range findSenderAddresses(string(model)) {
			if s.adapter == "ProcessDirect" {
				raw = append(raw, s.value)
			}
		}
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var params map[string]string
	out := make([]string, 0, len(raw))
	for _, a := range raw {
		if m := reWholeParameter.FindStringSubmatch(a); m != nil {
			if params == nil {
				params = map[string]string{}
				if cfg, err := cpi.NewConfiguration(exe).Get(id, "active"); err == nil {
					for _, p := range cfg.Root.Results {
						params[p.ParameterKey] = p.ParameterValue
					}
				}
			}
			a = params[m[1]]
		}
		if a != "" {
			out = append(out, a)
		}
	}
	return out, nil
}
