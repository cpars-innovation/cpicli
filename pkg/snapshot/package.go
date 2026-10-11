package snapshot

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/rs/zerolog/log"
)

// WritePackageDetails writes <dir>/<packageID>.json with the package details
// as the tenant reports them, unless the file exists with the same details
// (name, description, short text, version, vendor, mode, products, keywords,
// countries, industries, line of business). It reports whether it wrote.
func WritePackageDetails(dir, packageID string, details *cpi.PackageSingleData) (bool, error) {
	path := filepath.Join(dir, packageID+".json")
	if _, err := os.Stat(path); err == nil {
		local, err := cpi.GetPackageDetails(path)
		if err != nil {
			return false, err
		}
		if !packageContentDiffer(details, local) {
			log.Info().Msgf("🏆 No changes to package %v detected. Update to Git not required", packageID)
			return false, nil
		}
		log.Info().Msgf("🏆 Changes to package %v detected and will be updated to Git", packageID)
	} else {
		log.Info().Msgf("🏆 Saving new file for package %v to Git", packageID)
	}
	data, err := json.MarshalIndent(details, "", "  ")
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(dir, os.ModePerm); err != nil {
		return false, err
	}
	return true, os.WriteFile(path, data, 0o666)
}

func packageContentDiffer(source *cpi.PackageSingleData, target *cpi.PackageSingleData) bool {
	a, b := source.Root, target.Root
	return a.Name != b.Name || a.Description != b.Description || a.ShortText != b.ShortText || a.Version != b.Version ||
		a.Vendor != b.Vendor || a.Mode != b.Mode || a.Products != b.Products || a.Keywords != b.Keywords ||
		a.Countries != b.Countries || a.Industries != b.Industries || a.LineOfBusiness != b.LineOfBusiness
}
