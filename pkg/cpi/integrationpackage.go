package cpi

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/go-errors/errors"
	"github.com/rs/zerolog/log"
)

type IntegrationPackage struct {
	exe *httpclnt.HTTPExecuter
}

type PackageSingleData struct {
	Root PackageFields `json:"d"`
}

type artifactData struct {
	Root struct {
		Results []struct {
			Id         string `json:"Id"`
			Name       string `json:"Name"`
			Version    string `json:"Version"`
			ModifiedAt string `json:"ModifiedAt"`
			ModifiedBy string `json:"ModifiedBy"`
			CreatedBy  string `json:"CreatedBy"`
		} `json:"results"`
	} `json:"d"`
}

type packageMultipleData struct {
	Root struct {
		Results []PackageFields `json:"results"`
	} `json:"d"`
}

type PackageFields struct {
	Id             string `json:"Id"`
	Name           string `json:"Name"`
	Description    string `json:"Description"`
	ShortText      string `json:"ShortText"`
	Version        string `json:"Version"`
	Vendor         string `json:"Vendor,omitempty"`
	Mode           string `json:"Mode,omitempty"`
	Products       string `json:"Products,omitempty"`
	Keywords       string `json:"Keywords,omitempty"`
	Countries      string `json:"Countries,omitempty"`
	Industries     string `json:"Industries,omitempty"`
	LineOfBusiness string `json:"LineOfBusiness,omitempty"`
}

type ArtifactDetails struct {
	Id           string
	Name         string
	IsDraft      bool
	Version      string
	ArtifactType string
	// ModifiedAt is the last change of the designtime artifact; zero when the
	// tenant does not report it. ModifiedBy (else CreatedBy) is who made it,
	// empty when the tenant does not report it.
	ModifiedAt time.Time
	ModifiedBy string
}

// NewIntegrationPackage returns an initialised IntegrationPackage instance.
func NewIntegrationPackage(exe *httpclnt.HTTPExecuter) *IntegrationPackage {
	ip := new(IntegrationPackage)
	ip.exe = exe
	return ip
}

func (ip *IntegrationPackage) GetPackagesList() ([]string, error) {
	// Get the list of packages of the current tenant
	log.Info().Msg("Getting list of IntegrationPackages")
	urlPath := "/api/v1/IntegrationPackages"

	callType := "Get IntegrationPackages list"
	resp, err := readOnlyCall(urlPath, callType, ip.exe)
	if err != nil {
		return nil, err
	}
	// Process response to extract packages
	var jsonData *packageMultipleData
	respBody, err := ip.exe.ReadRespBody(resp)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(respBody, &jsonData)
	if err != nil {
		log.Error().Msgf("Error unmarshalling response as JSON. Response body = %s", respBody)
		return nil, errors.Wrap(err, 0)
	}
	var packageIds []string
	for _, result := range jsonData.Root.Results {
		packageIds = append(packageIds, result.Id)
	}
	return packageIds, nil
}

// GetPackagesData returns the details of all packages of the current tenant in a single call.
// The collection returns the same entity fields as the single package call, so callers can use
// this to avoid one Get call per package.
func (ip *IntegrationPackage) GetPackagesData() ([]*PackageSingleData, error) {
	log.Info().Msg("Getting details of all IntegrationPackages")
	urlPath := "/api/v1/IntegrationPackages"

	callType := "Get IntegrationPackages list"
	resp, err := readOnlyCall(urlPath, callType, ip.exe)
	if err != nil {
		return nil, err
	}
	var jsonData *packageMultipleData
	respBody, err := ip.exe.ReadRespBody(resp)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(respBody, &jsonData)
	if err != nil {
		log.Error().Msgf("Error unmarshalling response as JSON. Response body = %s", respBody)
		return nil, errors.Wrap(err, 0)
	}
	var packages []*PackageSingleData
	for _, result := range jsonData.Root.Results {
		packages = append(packages, &PackageSingleData{Root: result})
	}
	return packages, nil
}

func (ip *IntegrationPackage) Get(id string) (packageData *PackageSingleData, readOnly bool, exists bool, err error) {
	log.Info().Msgf("Getting details of integration package %v", id)
	urlPath := fmt.Sprintf("/api/v1/IntegrationPackages('%v')", id)

	callType := "Get IntegrationPackages by ID"
	resp, err := readOnlyCall(urlPath, callType, ip.exe)
	if err != nil {
		if httpclnt.StatusCode(err) == http.StatusNotFound {
			return nil, false, false, nil
		} else {
			return nil, false, false, err
		}
	}
	// Process response to extract details
	respBody, err := ip.exe.ReadRespBody(resp)
	if err != nil {
		return nil, false, false, err
	}
	err = json.Unmarshal(respBody, &packageData)
	if err != nil {
		log.Error().Msgf("Error unmarshalling response as JSON. Response body = %s", respBody)
		return nil, false, false, errors.Wrap(err, 0)
	}
	if packageData.Root.Mode == "READ_ONLY" {
		readOnly = true
	}
	return packageData, readOnly, true, nil
}

func (ip *IntegrationPackage) GetArtifactsData(id string, artifactType string) ([]*ArtifactDetails, error) {
	log.Info().Msgf("Getting %v designtime artifacts of package %v", artifactType, id)
	urlPath := fmt.Sprintf("/api/v1/IntegrationPackages('%v')/%vDesigntimeArtifacts", id, artifactType)

	callType := fmt.Sprintf("Get %v designtime artifacts of IntegrationPackages", artifactType)
	resp, err := readOnlyCall(urlPath, callType, ip.exe)
	if err != nil {
		return nil, err
	}
	// Process response to extract artifact details
	var jsonData *artifactData
	respBody, err := ip.exe.ReadRespBody(resp)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(respBody, &jsonData)
	if err != nil {
		log.Error().Msgf("Error unmarshalling response as JSON. Response body = %s", respBody)
		return nil, errors.Wrap(err, 0)
	}
	var details []*ArtifactDetails
	for _, result := range jsonData.Root.Results {
		var draft bool
		if result.Version == "Active" {
			draft = true
		} else {
			draft = false
		}
		modified, err := ParseODataTime(result.ModifiedAt)
		if err != nil {
			modified = time.Time{}
		}
		details = append(details, &ArtifactDetails{
			Id:           result.Id,
			Name:         result.Name,
			IsDraft:      draft,
			Version:      result.Version,
			ArtifactType: artifactType,
			ModifiedAt:   modified,
			ModifiedBy:   cmp.Or(result.ModifiedBy, result.CreatedBy),
		})
	}
	return details, nil
}

func (ip *IntegrationPackage) GetAllArtifacts(id string) ([]*ArtifactDetails, error) {
	// The four artifact types are independent calls - run them concurrently and merge in a fixed order
	artifactTypes := []string{"Integration", "MessageMapping", "ScriptCollection", "ValueMapping"}
	results := make([][]*ArtifactDetails, len(artifactTypes))
	errs := make([]error, len(artifactTypes))

	var wg sync.WaitGroup
	for i, artifactType := range artifactTypes {
		wg.Add(1)
		go func(i int, artifactType string) {
			defer wg.Done()
			results[i], errs[i] = ip.GetArtifactsData(id, artifactType)
		}(i, artifactType)
	}
	wg.Wait()

	var details []*ArtifactDetails
	for i := range artifactTypes {
		if errs[i] != nil {
			return nil, errs[i]
		}
		details = append(details, results[i]...)
	}
	return details, nil
}

func (ip *IntegrationPackage) Create(packageData *PackageSingleData) error {
	packageId := packageData.Root.Id
	log.Info().Msgf("Creating integration package %v", packageId)
	urlPath := "/api/v1/IntegrationPackages"
	if strings.TrimSpace(packageData.Root.ShortText) == "" {
		// the tenant refuses a package without short text (HTTP 400)
		packageData.Root.ShortText = cmp.Or(strings.TrimSpace(packageData.Root.Name), packageId)
		log.Warn().Msgf("Package %v has no ShortText: using %q (the tenant requires one)", packageId, packageData.Root.ShortText)
	}

	requestBody, err := ip.constructBody(packageData)
	if err != nil {
		return err
	}

	return modifyingCall("POST", urlPath, requestBody, 201, "Create integration package", ip.exe)
}

func (ip *IntegrationPackage) Update(packageData *PackageSingleData) error {
	packageId := packageData.Root.Id
	log.Info().Msgf("Updating integration package %v", packageId)
	urlPath := fmt.Sprintf("/api/v1/IntegrationPackages('%v')", packageId)

	requestBody, err := ip.constructBody(packageData)
	if err != nil {
		return err
	}

	return modifyingCall("PUT", urlPath, requestBody, 202, "Update integration package", ip.exe)
}

func (ip *IntegrationPackage) Delete(packageId string) error {
	log.Info().Msgf("Deleting integration package %v", packageId)
	urlPath := fmt.Sprintf("/api/v1/IntegrationPackages('%v')", packageId)
	return modifyingCall("DELETE", urlPath, nil, 202, "Delete integration package", ip.exe)
}

func (ip *IntegrationPackage) constructBody(packageData *PackageSingleData) ([]byte, error) {
	// Clear Mode field as it is not allowed in create/update
	packageData.Root.Mode = ""

	requestBody, err := json.Marshal(packageData)
	if err != nil {
		return nil, err
	}
	return requestBody, nil
}

func FindArtifactById(key string, list []*ArtifactDetails) *ArtifactDetails {
	for _, s := range list {
		if s.Id == key {
			return s
		}
	}
	return nil
}

func GetPackageDetails(file string) (*PackageSingleData, error) {
	var jsonData *PackageSingleData

	fileContent, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(fileContent, &jsonData)
	if err != nil {
		log.Error().Msgf("Error unmarshalling file as JSON. Response body = %s", fileContent)
		return nil, errors.Wrap(err, 0)
	}
	return jsonData, nil
}
