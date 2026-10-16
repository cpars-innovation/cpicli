package cpi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/rs/zerolog/log"
)

const (
	// DefaultBatchSize for Partner Directory operations
	DefaultBatchSize = 90
)

// PartnerDirectory handles Partner Directory API operations
type PartnerDirectory struct {
	exe *httpclnt.HTTPExecuter
}

// NewPartnerDirectory creates a new Partner Directory API client
func NewPartnerDirectory(exe *httpclnt.HTTPExecuter) *PartnerDirectory {
	return &PartnerDirectory{
		exe: exe,
	}
}

// StringParameter represents a partner directory string parameter
type StringParameter struct {
	Pid              string `json:"Pid"`
	ID               string `json:"Id"`
	Value            string `json:"Value"`
	CreatedBy        string `json:"CreatedBy,omitempty"`
	LastModifiedBy   string `json:"LastModifiedBy,omitempty"`
	CreatedTime      string `json:"CreatedTime,omitempty"`
	LastModifiedTime string `json:"LastModifiedTime,omitempty"`
}

// BinaryParameter represents a partner directory binary parameter
type BinaryParameter struct {
	Pid              string `json:"Pid"`
	ID               string `json:"Id"`
	Value            string `json:"Value"` // Base64 encoded
	ContentType      string `json:"ContentType"`
	CreatedBy        string `json:"CreatedBy,omitempty"`
	LastModifiedBy   string `json:"LastModifiedBy,omitempty"`
	CreatedTime      string `json:"CreatedTime,omitempty"`
	LastModifiedTime string `json:"LastModifiedTime,omitempty"`
}

// BatchResult represents the results of a batch operation
type BatchResult struct {
	Created   []string `json:"created,omitempty"`
	Updated   []string `json:"updated,omitempty"`
	Unchanged []string `json:"unchanged,omitempty"`
	Deleted   []string `json:"deleted,omitempty"`
	Errors    []string `json:"errors,omitempty"`
}

// GetStringParameters retrieves all string parameters from partner directory
func (pd *PartnerDirectory) GetStringParameters(selectFields string) ([]StringParameter, error) {
	all := []StringParameter{}
	err := pd.listAll("/api/v1/StringParameters", selectQuery(selectFields), "Get string parameters", func(raw json.RawMessage) (int, error) {
		var page []StringParameter
		err := json.Unmarshal(raw, &page)
		all = append(all, page...)
		return len(page), err
	})
	if err != nil {
		return nil, err
	}
	log.Info().Msgf("Retrieved %d total string parameters", len(all))
	return all, nil
}

// GetBinaryParameters retrieves all binary parameters from partner directory
func (pd *PartnerDirectory) GetBinaryParameters(selectFields string) ([]BinaryParameter, error) {
	all := []BinaryParameter{}
	err := pd.listAll("/api/v1/BinaryParameters", selectQuery(selectFields), "Get binary parameters", func(raw json.RawMessage) (int, error) {
		var page []BinaryParameter
		err := json.Unmarshal(raw, &page)
		all = append(all, page...)
		return len(page), err
	})
	if err != nil {
		return nil, err
	}
	log.Info().Msgf("Retrieved %d total binary parameters", len(all))
	return all, nil
}

// GetStringParameter retrieves a single string parameter
func (pd *PartnerDirectory) GetStringParameter(pid, id string) (*StringParameter, error) {
	path := fmt.Sprintf("/api/v1/StringParameters(Pid=%s,Id=%s)",
		pathKey(pid),
		pathKey(id))

	log.Debug().Msgf("Getting string parameter %s/%s", pid, id)

	resp, err := pd.exe.ExecGetRequest(path, map[string]string{
		"Accept": "application/json",
	})
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, pdStatusError("Get string parameter", resp)
	}

	body, err := pd.exe.ReadRespBody(resp)
	if err != nil {
		return nil, err
	}

	var result struct {
		D StringParameter `json:"d"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result.D, nil
}

// GetBinaryParameter retrieves a single binary parameter
func (pd *PartnerDirectory) GetBinaryParameter(pid, id string) (*BinaryParameter, error) {
	path := fmt.Sprintf("/api/v1/BinaryParameters(Pid=%s,Id=%s)",
		pathKey(pid),
		pathKey(id))

	log.Debug().Msgf("Getting binary parameter %s/%s", pid, id)

	resp, err := pd.exe.ExecGetRequest(path, map[string]string{
		"Accept": "application/json",
	})
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, pdStatusError("Get binary parameter", resp)
	}

	body, err := pd.exe.ReadRespBody(resp)
	if err != nil {
		return nil, err
	}

	var result struct {
		D BinaryParameter `json:"d"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result.D, nil
}

// CreateStringParameter creates a new string parameter
func (pd *PartnerDirectory) CreateStringParameter(param StringParameter) error {
	body := map[string]string{
		"Pid":   param.Pid,
		"Id":    param.ID,
		"Value": param.Value,
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to marshal body: %w", err)
	}

	log.Debug().Msgf("Creating string parameter %s/%s", param.Pid, param.ID)

	resp, err := pd.exe.Exec("POST", "/api/v1/StringParameters",
		bytes.NewReader(bodyJSON), map[string]string{
			"Content-Type": "application/json",
			"Accept":       "application/json",
		})
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusCreated {
		return pdStatusError("Create string parameter", resp)
	}

	return nil
}

// UpdateStringParameter updates an existing string parameter
func (pd *PartnerDirectory) UpdateStringParameter(param StringParameter) error {
	body := map[string]string{"Value": param.Value}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to marshal body: %w", err)
	}

	path := fmt.Sprintf("/api/v1/StringParameters(Pid=%s,Id=%s)",
		pathKey(param.Pid),
		pathKey(param.ID))

	log.Debug().Msgf("Updating string parameter %s/%s", param.Pid, param.ID)

	resp, err := pd.exe.Exec("PUT", path,
		bytes.NewReader(bodyJSON), map[string]string{
			"Content-Type": "application/json",
			"Accept":       "application/json",
		})
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return pdStatusError("Update string parameter", resp)
	}

	return nil
}

// DeleteStringParameter deletes a string parameter
func (pd *PartnerDirectory) DeleteStringParameter(pid, id string) error {
	path := fmt.Sprintf("/api/v1/StringParameters(Pid=%s,Id=%s)",
		pathKey(pid),
		pathKey(id))

	log.Debug().Msgf("Deleting string parameter %s/%s", pid, id)

	resp, err := pd.exe.Exec("DELETE", path, nil, map[string]string{
		"Accept": "application/json",
	})
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return pdStatusError("Delete string parameter", resp)
	}

	return nil
}

// CreateBinaryParameter creates a new binary parameter
func (pd *PartnerDirectory) CreateBinaryParameter(param BinaryParameter) error {
	body := map[string]string{
		"Pid":         param.Pid,
		"Id":          param.ID,
		"Value":       param.Value,
		"ContentType": param.ContentType,
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to marshal body: %w", err)
	}

	log.Debug().Msgf("Creating binary parameter %s/%s", param.Pid, param.ID)

	resp, err := pd.exe.Exec("POST", "/api/v1/BinaryParameters",
		bytes.NewReader(bodyJSON), map[string]string{
			"Content-Type": "application/json",
			"Accept":       "application/json",
		})
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusCreated {
		return pdStatusError("Create binary parameter", resp)
	}

	return nil
}

// UpdateBinaryParameter updates an existing binary parameter
func (pd *PartnerDirectory) UpdateBinaryParameter(param BinaryParameter) error {
	body := map[string]string{
		"Value":       param.Value,
		"ContentType": param.ContentType,
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to marshal body: %w", err)
	}

	path := fmt.Sprintf("/api/v1/BinaryParameters(Pid=%s,Id=%s)",
		pathKey(param.Pid),
		pathKey(param.ID))

	log.Debug().Msgf("Updating binary parameter %s/%s", param.Pid, param.ID)

	resp, err := pd.exe.Exec("PUT", path,
		bytes.NewReader(bodyJSON), map[string]string{
			"Content-Type": "application/json",
			"Accept":       "application/json",
		})
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return pdStatusError("Update binary parameter", resp)
	}

	return nil
}

// DeleteBinaryParameter deletes a binary parameter
func (pd *PartnerDirectory) DeleteBinaryParameter(pid, id string) error {
	path := fmt.Sprintf("/api/v1/BinaryParameters(Pid=%s,Id=%s)",
		pathKey(pid),
		pathKey(id))

	log.Debug().Msgf("Deleting binary parameter %s/%s", pid, id)

	resp, err := pd.exe.Exec("DELETE", path, nil, map[string]string{
		"Accept": "application/json",
	})
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return pdStatusError("Delete binary parameter", resp)
	}

	return nil
}

// pdStatusError turns an unexpected Partner Directory response into an
// *httpclnt.HTTPError, so that callers classify it like every other tenant
// error (401/403 = auth, ...).
func pdStatusError(callType string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	_ = resp.Body.Close()
	return &httpclnt.HTTPError{CallType: callType, StatusCode: resp.StatusCode, Body: body}
}

// ListParameters returns the string and binary parameters of one partner ID
// (binary values included, base64).
func (pd *PartnerDirectory) ListParameters(pid string) ([]StringParameter, []BinaryParameter, error) {
	filter := "$filter=" + url.QueryEscape("Pid eq '"+strings.ReplaceAll(pid, "'", "''")+"'")
	var strs []StringParameter
	if err := pd.listAll("/api/v1/StringParameters", filter, "Get string parameters", func(raw json.RawMessage) (int, error) {
		var page []StringParameter
		err := json.Unmarshal(raw, &page)
		strs = append(strs, page...)
		return len(page), err
	}); err != nil {
		return nil, nil, err
	}
	var bins []BinaryParameter
	if err := pd.listAll("/api/v1/BinaryParameters", filter, "Get binary parameters", func(raw json.RawMessage) (int, error) {
		var page []BinaryParameter
		err := json.Unmarshal(raw, &page)
		bins = append(bins, page...)
		return len(page), err
	}); err != nil {
		return nil, nil, err
	}
	return strs, bins, nil
}

func selectQuery(fields string) string {
	if fields == "" {
		return ""
	}
	return "$select=" + url.QueryEscape(fields)
}

// pageSize is the $top cpictl asks for. The tenant may return fewer entries
// per page (binary parameters: 30, as they carry their content), so a short
// page never means the end.
const pageSize = 1000

// maxPages stops a server that ignores $skip.
const maxPages = 100000

// listAll reads every page of an OData v2 collection (query without "?"):
// it follows __next when the server sends one; otherwise it moves $skip on by
// the number of entries received until __count is reached or a page is
// empty.
func (pd *PartnerDirectory) listAll(basePath, query, callType string, add func(json.RawMessage) (int, error)) error {
	base := basePath + "?$inlinecount=allpages&$top=" + fmt.Sprint(pageSize)
	if query != "" {
		base += "&" + query
	}
	next, total, seen, skip := "", -1, 0, 0
	var previous json.RawMessage
	for page := 0; page < maxPages; page++ {
		path := next
		if path == "" {
			path = fmt.Sprintf("%s&$skip=%d", base, skip)
		}
		log.Debug().Msgf("%s: %s", callType, path)
		resp, err := pd.exe.ExecGetRequest(path, map[string]string{"Accept": "application/json"})
		if err != nil {
			return err
		}
		if resp.StatusCode != http.StatusOK {
			return pdStatusError(callType, resp)
		}
		body, err := pd.exe.ReadRespBody(resp)
		if err != nil {
			return err
		}
		var data struct {
			D struct {
				Results json.RawMessage `json:"results"`
				Count   string          `json:"__count"`
				Next    string          `json:"__next"`
			} `json:"d"`
		}
		if err := json.Unmarshal(body, &data); err != nil {
			return fmt.Errorf("%s: failed to decode response: %w", callType, err)
		}
		if total < 0 && data.D.Count != "" {
			fmt.Sscanf(data.D.Count, "%d", &total)
		}
		n := 0
		if len(data.D.Results) > 0 && string(data.D.Results) != "null" {
			if next == "" && previous != nil && bytes.Equal(previous, data.D.Results) {
				// $skip ignored: without a count the first page was everything
				if total < 0 {
					return nil
				}
				return fmt.Errorf("%s: the tenant returned the same page again ($skip=%d ignored, %d of %d read)", callType, skip, seen, total)
			}
			previous = data.D.Results
			if n, err = add(data.D.Results); err != nil {
				return fmt.Errorf("%s: failed to decode response: %w", callType, err)
			}
		}
		seen += n
		if data.D.Next != "" {
			next = relativeNext(data.D.Next)
			continue
		}
		if next != "" || n == 0 || (total >= 0 && seen >= total) {
			return nil // last page of a __next chain, empty page, or all counted
		}
		skip += n
	}
	return fmt.Errorf("%s: more than %d pages", callType, maxPages)
}

// relativeNext turns a __next link into a path for the executer.
func relativeNext(next string) string {
	if i := strings.Index(next, "/api/"); i >= 0 {
		return next[i:]
	}
	if strings.HasPrefix(next, "/") {
		return next
	}
	// relative to the collection, e.g. "BinaryParameters?$skiptoken=30"
	return "/api/v1/" + next
}
