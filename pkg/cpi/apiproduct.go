package cpi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/go-errors/errors"
	"github.com/rs/zerolog/log"
)

type APIProduct struct {
	exe *httpclnt.HTTPExecuter
}

func NewAPIProduct(exe *httpclnt.HTTPExecuter) *APIProduct {
	a := new(APIProduct)
	a.exe = exe
	return a
}

type APIResource struct {
	exe *httpclnt.HTTPExecuter
}

func NewAPIResource(exe *httpclnt.HTTPExecuter) *APIResource {
	a := new(APIResource)
	a.exe = exe
	return a
}

type apiProductGetResponse struct {
	Root struct {
		apiProductModel
		AdditionalProperties struct {
			Results []struct {
				additionalPropertiesModel
			} `json:"results"`
		} `json:"additionalProperties"`
		ApiProxies struct {
			Results []struct {
				apiProxiesModel
			} `json:"results"`
		} `json:"apiProxies"`
		ApiResources struct {
			Results []struct {
				apiResourcesModel
				ApiProxyEndPoint struct {
					apiProxyEndPointModel
				} `json:"apiProxyEndPoint"`
			} `json:"results"`
		} `json:"apiResources"`
	} `json:"d"`
}

type apiProductModel struct {
	Name          string `json:"name"`
	Version       string `json:"version,omitempty"`
	IsPublished   bool   `json:"isPublished,omitempty"`
	Status_code   string `json:"status_code"`
	Title         string `json:"title"`
	ShortText     string `json:"shortText,omitempty"`
	Description   string `json:"description,omitempty"`
	Scope         string `json:"scope,omitempty"`
	QuotaCount    int32  `json:"quotaCount,omitempty"`
	QuotaInterval int32  `json:"quotaInterval,omitempty"`
	QuotaTimeUnit string `json:"quotaTimeUnit,omitempty"`
}

type additionalPropertiesModel struct {
	EntityId string `json:"entityId"`
	Name     string `json:"name"`
	Value    string `json:"value"`
}

type apiProxiesModel struct {
	Metadata struct {
		Uri string `json:"uri"`
	} `json:"__metadata"`
	Name string `json:"name,omitempty"`
}

type apiResourcesModel struct {
	Id              string `json:"id"`
	IsDeleteChecked bool   `json:"isDeleteChecked"`
	IsGetChecked    bool   `json:"isGetChecked"`
	IsPostChecked   bool   `json:"isPostChecked"`
	IsPutChecked    bool   `json:"isPutChecked"`
	Name            string `json:"name"`
}

type apiProxyEndPointModel struct {
	ApiName string `json:"FK_API_NAME"`
}

type apiProductCreateRequest struct {
	apiProductModel
	AdditionalProperties []struct {
		additionalPropertiesModel
	} `json:"additionalProperties"`
	ApiProxies []struct {
		apiProxiesModel
	} `json:"apiProxies"`
	ApiResources []struct {
		apiResourcesModel
		ApiProxyEndPoint struct {
			apiProxyEndPointModel
		} `json:"apiProxyEndPoint"`
	} `json:"apiResources"`
}

type apiProductListResponseData struct {
	Root struct {
		Results []struct {
			Name    string `json:"name"`
			Version string `json:"version"`
			Status  string `json:"status_code"`
		} `json:"results"`
	} `json:"d"`
}

type APIProductMetadata struct {
	Name    string
	Version string
	Status  string
}

type apiResourceListResponseData struct {
	Root struct {
		Results []struct {
			apiResourcesModel
			ApiProxyEndPoint struct {
				apiProxyEndPointModel
			} `json:"apiProxyEndPoint"`
		} `json:"results"`
	} `json:"d"`
}

type APIResourceMetadata struct {
	Name    string
	Id      string
	APIName string
}

func (a *APIProduct) Download(name string, targetRootDir string) error {
	log.Info().Msgf("Downloading APIProduct %v", name)
	urlPath := fmt.Sprintf("/apiportal/api/1.0/Management.svc/APIProducts('%v')?$expand=additionalProperties,apiProxies,apiResources,apiResources/apiProxyEndPoint", name)

	resp, err := readOnlyCall(urlPath, "Get APIProduct", a.exe)
	if err != nil {
		return err
	}

	// Process response to extract details
	var jsonData *apiProductGetResponse
	respBody, err := a.exe.ReadRespBody(resp)
	if err != nil {
		return err
	}
	err = json.Unmarshal(respBody, &jsonData)
	if err != nil {
		log.Error().Msgf("Error unmarshalling response as JSON. Response body = %s", respBody)
		return errors.Wrap(err, 0)
	}

	// Change the structure to match the expected format for creation
	jsonCreateData := &apiProductCreateRequest{
		apiProductModel:      jsonData.Root.apiProductModel,
		AdditionalProperties: jsonData.Root.AdditionalProperties.Results,
		ApiProxies:           jsonData.Root.ApiProxies.Results,
		ApiResources:         jsonData.Root.ApiResources.Results,
	}

	// For all entries in jsonCreateData.ApiProxies, update the Metadata.Uri to the expected format
	for i, proxy := range jsonCreateData.ApiProxies {
		jsonCreateData.ApiProxies[i].Metadata.Uri = fmt.Sprintf("APIProxies(name='%s')", proxy.Name)
		jsonCreateData.ApiProxies[i].Name = "" // Set the Name field to empty string as it is not used in the request
	}

	targetFile := fmt.Sprintf("%v/%v.json", targetRootDir, name)
	// Create directory for target file if it doesn't exist yet
	err = os.MkdirAll(filepath.Dir(targetFile), os.ModePerm)
	if err != nil {
		return errors.Wrap(err, 0)
	}
	content, err := json.MarshalIndent(jsonCreateData, "", "  ")
	if err != nil {
		return errors.Wrap(err, 0)
	}
	err = os.WriteFile(targetFile, content, os.ModePerm)
	if err != nil {
		return errors.Wrap(err, 0)
	}
	return nil
}

func (a *APIProduct) Upload(sourceFile string, workDir string) error {

	log.Info().Msgf("Uploading API Product from file %v", sourceFile)
	var createData *apiProductCreateRequest

	fileContent, err := os.ReadFile(sourceFile)
	if err != nil {
		return errors.Wrap(err, 0)
	}
	err = json.Unmarshal(fileContent, &createData)
	if err != nil {
		log.Error().Msgf("Error unmarshalling file as JSON. Response body = %s", fileContent)
		return errors.Wrap(err, 0)
	}

	r := NewAPIResource(a.exe)

	resourcesCache := make(map[string][]*APIResourceMetadata)

	// Check if the API resource IDs exist, if not, retrieve the latest Ids
	for i, resource := range createData.ApiResources {
		log.Debug().Msgf("APIResource %d: ApiName=%s, Name=%s, Id=%s", i, resource.ApiProxyEndPoint.ApiName, resource.Name, resource.Id)
		resourceExists, err := r.Exists(resource.Id)
		if err != nil {
			return err
		}
		if !resourceExists {
			log.Warn().Msgf("APIResource with Id %s does not exist. Trying to retrieve the latest Id", resource.Id)
			// Retrieve the ID and update the value of resource.Id
			// Store the results in a cache to avoid multiple calls for the same API name
			var resourceDetails []*APIResourceMetadata
			resourceDetails, foundInCache := resourcesCache[resource.ApiProxyEndPoint.ApiName]
			if !foundInCache {
				resourceDetails, err = r.GetByName(resource.ApiProxyEndPoint.ApiName)
				if err != nil {
					return err
				}
				// Store only if resourceDetails is not empty
				if len(resourceDetails) != 0 {
					resourcesCache[resource.ApiProxyEndPoint.ApiName] = resourceDetails // Cache the result
				}
			}
			foundReplacementId := false
			// Loop through the resourceDetails and find the one with the matching name and apiName
			for _, detail := range resourceDetails {
				if detail.Name == resource.Name && detail.APIName == resource.ApiProxyEndPoint.ApiName {
					log.Info().Msgf("Replacing Id %s with %s", resource.Id, detail.Id)
					createData.ApiResources[i].Id = detail.Id // Update the Id in createData
					foundReplacementId = true
					break
				}
			}
			if !foundReplacementId {
				log.Error().Msgf("No matching APIResource found for API %s with name %s", resource.ApiProxyEndPoint.ApiName, resource.Name)
				return fmt.Errorf("no matching APIResource found")
			}
		}
	}

	log.Info().Msgf("Creating API Product %v", createData.Name)
	urlPath := "/apiportal/api/1.0/Management.svc/APIProducts"

	requestBody, err := json.Marshal(createData)
	if err != nil {
		return errors.Wrap(err, 0)
	}

	return modifyingCall("POST", urlPath, requestBody, 201, "Create APIProduct", a.exe)
}

func (a *APIProduct) Exists(id string) (bool, error) {
	log.Info().Msgf("Checking existence of APIProduct %v", id)
	urlPath := fmt.Sprintf("/apiportal/api/1.0/Management.svc/APIProducts('%v')", id)

	callType := "Get APIProduct"
	_, err := readOnlyCall(urlPath, callType, a.exe)
	if err != nil {
		if httpclnt.StatusCode(err) == http.StatusNotFound {
			return false, nil
		} else {
			return false, err
		}
	}
	return true, nil
}

func (a *APIProduct) List() ([]*APIProductMetadata, error) {
	log.Info().Msgf("Getting list of APIProducts")
	urlPath := "/apiportal/api/1.0/Management.svc/APIProducts"

	resp, err := readOnlyCall(urlPath, "List APIProducts", a.exe)
	if err != nil {
		return nil, err
	}
	// Process response to extract product details
	var jsonData *apiProductListResponseData
	respBody, err := a.exe.ReadRespBody(resp)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(respBody, &jsonData)
	if err != nil {
		log.Error().Msgf("Error unmarshalling response as JSON. Response body = %s", respBody)
		return nil, errors.Wrap(err, 0)
	}
	var details []*APIProductMetadata
	for _, result := range jsonData.Root.Results {
		details = append(details, &APIProductMetadata{
			Name:    result.Name,
			Version: result.Version,
			Status:  result.Status,
		})
	}
	return details, nil
}

func (a *APIProduct) Delete(id string) error {
	log.Info().Msgf("Deleting APIProduct %v", id)

	urlPath := fmt.Sprintf("/apiportal/api/1.0/Management.svc/APIProducts('%v')", id)
	return modifyingCall("DELETE", urlPath, nil, 204, "Delete APIProduct", a.exe)
}

func (a *APIResource) Exists(id string) (bool, error) {
	log.Info().Msgf("Checking existence of APIResource %v", id)
	urlPath := fmt.Sprintf("/apiportal/api/1.0/Management.svc/APIResources('%v')", id)

	callType := "Get APIResource"
	_, err := readOnlyCall(urlPath, callType, a.exe)
	if err != nil {
		if httpclnt.StatusCode(err) == http.StatusNotFound {
			return false, nil
		} else {
			return false, err
		}
	}
	return true, nil
}

func (a *APIResource) GetByName(apiName string) ([]*APIResourceMetadata, error) {
	log.Info().Msgf("Getting list of APIResources for %v", apiName)
	urlPath := fmt.Sprintf("/apiportal/api/1.0/Management.svc/APIResources?$expand=apiProxyEndPoint&$filter=apiProxyEndPoint/FK_API_NAME%veq%v'%v'", "%20", "%20", apiName)

	resp, err := readOnlyCall(urlPath, "List APIResources", a.exe)
	if err != nil {
		return nil, err
	}
	// Process response to extract resource details
	var jsonData *apiResourceListResponseData
	respBody, err := a.exe.ReadRespBody(resp)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(respBody, &jsonData)
	if err != nil {
		log.Error().Msgf("Error unmarshalling response as JSON. Response body = %s", respBody)
		return nil, errors.Wrap(err, 0)
	}
	var details []*APIResourceMetadata
	for _, result := range jsonData.Root.Results {
		details = append(details, &APIResourceMetadata{
			Name:    result.Name,
			Id:      result.Id,
			APIName: result.ApiProxyEndPoint.ApiName,
		})
	}
	return details, nil
}
