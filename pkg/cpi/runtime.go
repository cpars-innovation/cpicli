package cpi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/go-errors/errors"
	"github.com/rs/zerolog/log"
)

type Runtime struct {
	exe *httpclnt.HTTPExecuter
}

// RuntimeArtifact is the runtime state of a deployed artifact.
type RuntimeArtifact struct {
	Id         string
	Version    string
	Name       string
	Type       string
	Status     string // STARTING, STARTED, ERROR, ...
	DeployedBy string
	// DeployedOn is the tenant's deployment timestamp (zero if not reported).
	DeployedOn time.Time
}

type runtimeData struct {
	Root struct {
		Id         string `json:"Id"`
		Version    string `json:"Version"`
		Name       string `json:"Name"`
		Type       string `json:"Type"`
		Status     string `json:"Status"`
		DeployedBy string `json:"DeployedBy"`
		DeployedOn string `json:"DeployedOn"`
	} `json:"d"`
}

type runtimeError struct {
	Parameter []string `json:"parameter"`
}

type buildAndDeployStatusData struct {
	Root struct {
		TaskId string `json:"TaskId"`
		Status string `json:"Status"`
	} `json:"d"`
}

// NewRuntime returns an initialised Runtime instance.
func NewRuntime(exe *httpclnt.HTTPExecuter) *Runtime {
	r := new(Runtime)
	r.exe = exe
	return r
}

// UnDeploy triggers undeployment of a runtime artifact. A 404 is returned as an
// *httpclnt.HTTPError so that callers can treat "not deployed" explicitly.
func (r *Runtime) UnDeploy(id string) error {
	log.Info().Msgf("Undeploying runtime artifact %v", id)
	urlPath := fmt.Sprintf("/api/v1/IntegrationRuntimeArtifacts('%v')", id)

	headers := map[string]string{}
	headers["Accept"] = "application/json"
	resp, err := r.exe.Exec(http.MethodDelete, urlPath, http.NoBody, headers)
	if err != nil {
		return err
	}
	switch resp.StatusCode {
	case http.StatusOK, http.StatusAccepted, http.StatusNoContent:
		_, _ = r.exe.ReadRespBody(resp)
		return nil
	}
	_, err = r.exe.LogError(resp, "Undeploy runtime artifact")
	return err
}

// Get returns the version and status of a runtime artifact. The version is
// "NOT_DEPLOYED" if the artifact is not deployed, and empty if its status is
// not STARTED.
func (r *Runtime) Get(id string) (version string, status string, err error) {
	artifact, err := r.GetArtifact(id)
	if err != nil {
		return "", "", err
	}
	if artifact == nil {
		return "NOT_DEPLOYED", "", nil
	}
	if artifact.Status == "STARTED" {
		return artifact.Version, "STARTED", nil
	}
	// artifact runtime deployment failed or not complete
	return "", artifact.Status, nil
}

// GetArtifact returns the runtime details of an artifact, or nil if it is not
// deployed.
func (r *Runtime) GetArtifact(id string) (*RuntimeArtifact, error) {
	log.Debug().Msgf("Getting details of runtime artifact %v", id)
	urlPath := fmt.Sprintf("/api/v1/IntegrationRuntimeArtifacts('%v')", id)

	callType := "Get runtime artifact"
	resp, err := readOnlyCall(urlPath, callType, r.exe)
	if err != nil {
		if httpclnt.StatusCode(err) == http.StatusNotFound { // artifact not deployed to runtime
			return nil, nil
		}
		if resp != nil {
			bytes, readErr := io.ReadAll(resp.Body)
			if readErr != nil {
				return nil, readErr
			}
			if strings.Contains(string(bytes), "Requested entity could not be found") { // artifact not deployed to runtime
				return nil, nil
			}
		}
		return nil, err
	}
	// Process response to extract version and status
	var jsonData *runtimeData
	respBody, err := r.exe.ReadRespBody(resp)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(respBody, &jsonData)
	if err != nil {
		log.Error().Msgf("Error unmarshalling response as JSON. Response body = %s", respBody)
		return nil, errors.Wrap(err, 0)
	}
	deployedOn, err := ParseODataTime(jsonData.Root.DeployedOn)
	if err != nil {
		log.Warn().Msgf("Ignoring unparseable DeployedOn %q of runtime artifact %v: %v", jsonData.Root.DeployedOn, id, err)
	}
	return &RuntimeArtifact{
		Id:         jsonData.Root.Id,
		Version:    jsonData.Root.Version,
		Name:       jsonData.Root.Name,
		Type:       jsonData.Root.Type,
		Status:     jsonData.Root.Status,
		DeployedBy: jsonData.Root.DeployedBy,
		DeployedOn: deployedOn,
	}, nil
}

// GetBuildAndDeployStatus returns the status of the deploy task returned when
// a deployment was triggered (e.g. DEPLOYING, SUCCESS, FAIL).
func (r *Runtime) GetBuildAndDeployStatus(taskId string) (string, error) {
	log.Debug().Msgf("Getting build and deploy status of task %v", taskId)
	urlPath := fmt.Sprintf("/api/v1/BuildAndDeployStatus(TaskId='%v')", taskId)

	resp, err := readOnlyCall(urlPath, "Get build and deploy status", r.exe)
	if err != nil {
		return "", err
	}
	respBody, err := r.exe.ReadRespBody(resp)
	if err != nil {
		return "", err
	}
	var jsonData buildAndDeployStatusData
	if err = json.Unmarshal(respBody, &jsonData); err != nil {
		log.Error().Msgf("Error unmarshalling response as JSON. Response body = %s", respBody)
		return "", errors.Wrap(err, 0)
	}
	return jsonData.Root.Status, nil
}

func (r *Runtime) GetErrorInfo(id string) (string, error) {
	log.Info().Msgf("Getting error info of runtime artifact %v", id)
	urlPath := fmt.Sprintf("/api/v1/IntegrationRuntimeArtifacts('%v')/ErrorInformation/$value", id)

	callType := "Get runtime artifact error information"
	resp, err := readOnlyCall(urlPath, callType, r.exe)
	// TODO - sometimes the error information is only available after some time, so the API returns 204 No content (instead of 200) in the meantime
	if err != nil {
		return "", err
	}
	// Process response to extract error info
	var jsonData *runtimeError
	respBody, err := r.exe.ReadRespBody(resp)
	if err != nil {
		return "", err
	}
	err = json.Unmarshal(respBody, &jsonData)
	if err != nil {
		log.Error().Msgf("Error unmarshalling response as JSON. Response body = %s", respBody)
		return "", errors.Wrap(err, 0)
	}
	if jsonData == nil || len(jsonData.Parameter) == 0 {
		return string(respBody), nil
	}
	return jsonData.Parameter[0], nil
}

var odataDate = regexp.MustCompile(`^/Date\((-?\d+)([+-]\d{4})?\)/$`)

// ParseODataTime parses an OData v2 JSON date ("/Date(1700000000000)/"),
// epoch milliseconds, an RFC 3339 timestamp, or an ISO time without a zone
// (taken as UTC). An empty string yields the zero time.
func ParseODataTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	if m := odataDate.FindStringSubmatch(value); m != nil {
		ms, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			return time.Time{}, err
		}
		return time.UnixMilli(ms).UTC(), nil
	}
	// epoch milliseconds as a plain string (used by some entities)
	if ms, err := strconv.ParseInt(value, 10, 64); err == nil {
		return time.UnixMilli(ms).UTC(), nil
	}
	t, err := time.Parse(time.RFC3339, value)
	if err == nil {
		return t, nil
	}
	// an ISO time without a zone (some runtimes answer DeployedOn so,
	// e.g. 2026-10-15T12:54:54.223): UTC, as OData's Edm.DateTime
	if t, err2 := time.Parse("2006-01-02T15:04:05.999999999", value); err2 == nil {
		return t.UTC(), nil
	}
	return time.Time{}, err
}
