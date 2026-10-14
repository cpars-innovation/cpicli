package ops

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cpars-innovation/cpicli/internal/file"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/cpars-innovation/cpicli/pkg/manifest"
)

// DefaultMaxContentBytes limits downloaded content returned inline.
const DefaultMaxContentBytes = 64 * 1024

// Content is downloaded content: UTF-8 text inline, anything else base64.
type Content struct {
	Size      int    `json:"size"`
	Text      string `json:"text,omitempty"`
	Base64    string `json:"base64,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

// NewContent encodes b, truncated to max bytes (0: DefaultMaxContentBytes,
// <0: unlimited).
func NewContent(b []byte, max int) Content {
	c := Content{Size: len(b)}
	if max == 0 {
		max = DefaultMaxContentBytes
	}
	if max > 0 && len(b) > max {
		b, c.Truncated = b[:max], true
	}
	text := b
	if c.Truncated {
		text = trimIncompleteRune(b)
	}
	if len(text) > 0 && utf8.Valid(text) || len(b) == 0 {
		c.Text = string(text)
	} else {
		c.Base64 = base64.StdEncoding.EncodeToString(b)
	}
	return c
}

// Excerpt returns c cut to max bytes; Size stays the full size.
func (c Content) Excerpt(max int) Content {
	b := []byte(c.Text)
	if c.Base64 != "" {
		if d, err := base64.StdEncoding.DecodeString(c.Base64); err == nil {
			b = d
		}
	}
	e := NewContent(b, max)
	e.Size, e.Truncated = c.Size, e.Truncated || c.Truncated
	return e
}

// trimIncompleteRune drops a trailing UTF-8 sequence cut by truncation
// (at most utf8.UTFMax-1 bytes); other invalid input is left unchanged.
func trimIncompleteRune(b []byte) []byte {
	for i := 1; i < utf8.UTFMax && i <= len(b); i++ {
		if utf8.Valid(b[:len(b)-i]) && !utf8.Valid(b) {
			return b[:len(b)-i]
		}
	}
	return b
}

// ValidationResult is the outcome of ValidateArtifact.
type ValidationResult struct {
	ArtifactID string `json:"artifactId"`
	Status     string `json:"status"` // PASSED, FAILED or UNKNOWN
	Details    string `json:"details"`
}

// ValidateArtifact validates an integration flow on the tenant (the same check
// as "Check" in the Web UI). A failed validation returns the result together
// with an error (exit code 5).
func ValidateArtifact(exe *httpclnt.HTTPExecuter, id, version string) (*ValidationResult, error) {
	if id == "" {
		return nil, output.Usagef("artifact ID is required")
	}
	text, err := cpi.NewContent(exe).Validate(id, version)
	if err != nil {
		return nil, err
	}
	res := &ValidationResult{ArtifactID: id, Status: "UNKNOWN", Details: text}
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "failed"):
		res.Status = "FAILED"
		return res, output.Failed(fmt.Errorf("validation of %s failed", id))
	case strings.Contains(lower, "passed"):
		res.Status = "PASSED"
	}
	return res, nil
}

// GuidelineReport is the outcome of CheckGuidelines.
type GuidelineReport struct {
	ArtifactID  string          `json:"artifactId"`
	ExecutionID string          `json:"executionId"`
	Status      string          `json:"status"`
	Total       int             `json:"total"`
	Violations  []cpi.Guideline `json:"violations"`
	Guidelines  []cpi.Guideline `json:"guidelines"`
}

func isNonCompliant(g cpi.Guideline) bool {
	c := strings.ToLower(strings.ReplaceAll(g.Compliance, "-", " "))
	return !g.Skipped && (strings.Contains(c, "not compliant") || strings.Contains(c, "non compliant") || c == "violated")
}

func guidelinePending(status string) bool {
	s := strings.ToUpper(status)
	return s == "" || strings.Contains(s, "PROGRESS") || strings.Contains(s, "RUNNING") ||
		strings.Contains(s, "PENDING") || strings.Contains(s, "QUEUED") || s == "STARTED"
}

// CheckGuidelines runs the design guidelines activated on the tenant against
// an integration flow and waits for the result. Violations return the report
// together with an error (exit code 5).
func CheckGuidelines(ctx context.Context, exe *httpclnt.HTTPExecuter, id, version string, timeout, interval time.Duration) (*GuidelineReport, error) {
	return CheckGuidelinesWith(ctx, exe, id, GuidelineOptions{Version: version, Timeout: timeout, Interval: interval})
}

// GuidelineOptions control CheckGuidelinesWith.
type GuidelineOptions struct {
	// Version is the designtime version (default "active").
	Version string
	// Timeout is the maximum wait for the result, Interval the time
	// between two polls (default 3s).
	Timeout, Interval time.Duration
	// Progress, when set, receives every poll of the execution status.
	Progress ProgressFunc
}

// CheckGuidelinesWith is CheckGuidelines with options.
func CheckGuidelinesWith(ctx context.Context, exe *httpclnt.HTTPExecuter, id string, opts GuidelineOptions) (*GuidelineReport, error) {
	exe = exe.WithContext(ctx)
	version, timeout, interval := opts.Version, opts.Timeout, opts.Interval
	if id == "" {
		return nil, output.Usagef("artifact ID is required")
	}
	if interval <= 0 {
		interval = 3 * time.Second
	}
	prog := newProgress(opts.Progress)
	maxPolls := int(timeout/interval) + 1
	c := cpi.NewContent(exe)
	before := map[string]bool{}
	if previous, err := c.GuidelineExecutions(id, version); err == nil {
		for _, e := range previous {
			before[e.ExecutionID] = true
		}
	}
	execID, err := c.ExecuteGuidelines(id, version)
	if err != nil {
		return nil, err
	}

	deadline := time.Now().Add(timeout)
	status := ""
	for {
		executions, err := c.GuidelineExecutions(id, version)
		if err != nil {
			return nil, err
		}
		for _, e := range executions {
			if (execID != "" && e.ExecutionID == execID) || (execID == "" && !before[e.ExecutionID]) {
				execID, status = e.ExecutionID, e.Status
				break
			}
		}
		state := status
		if state == "" {
			state = "waiting for the execution"
		}
		prog.report(maxPolls, "guideline check of %s: %s, poll %d", id, state, prog.steps()+1)
		if execID != "" && !guidelinePending(status) {
			break
		}
		if time.Now().Add(interval).After(deadline) {
			return nil, &TimeoutError{Msg: fmt.Sprintf("design guideline check of %s did not finish within %s", id, timeout)}
		}
		if err := sleep(ctx, interval); err != nil {
			return nil, err
		}
	}

	exec, guidelines, err := c.GuidelineResults(id, version, execID)
	if err != nil {
		return nil, err
	}
	report := &GuidelineReport{ArtifactID: id, ExecutionID: execID, Status: status, Total: len(guidelines),
		Violations: []cpi.Guideline{}, Guidelines: guidelines}
	if exec != nil && exec.Status != "" {
		report.Status = exec.Status
	}
	for _, g := range guidelines {
		if isNonCompliant(g) {
			report.Violations = append(report.Violations, g)
		}
	}
	if len(report.Violations) > 0 || strings.HasPrefix(strings.ToUpper(report.Status), "FAIL") {
		return report, output.Failed(fmt.Errorf("%s violates %d design guideline(s)", id, len(report.Violations)))
	}
	return report, nil
}

// ListServiceEndpoints returns the callable URLs of deployed integration flows.
func ListServiceEndpoints(exe *httpclnt.HTTPExecuter, artifactID string) ([]cpi.ServiceEndpoint, error) {
	return cpi.NewContent(exe).ServiceEndpoints(artifactID)
}

// ListRuntimeArtifacts returns all deployed artifacts, optionally filtered by
// runtime status. For artifacts in ERROR the error message is included
// (for at most 20 artifacts).
func ListRuntimeArtifacts(exe *httpclnt.HTTPExecuter, statuses []string) ([]RuntimeStatus, error) {
	normalized := make([]string, 0, len(statuses))
	for _, s := range statuses {
		n := strings.ToUpper(strings.TrimSpace(s))
		if !slices.Contains([]string{"STARTED", "STARTING", "ERROR", "STOPPING"}, n) {
			return nil, output.Usagef("invalid runtime status %q (valid: STARTED, STARTING, ERROR, STOPPING)", s)
		}
		normalized = append(normalized, n)
	}
	artifacts, err := cpi.NewContent(exe).RuntimeArtifacts(normalized)
	if err != nil {
		return nil, err
	}
	rt := cpi.NewRuntime(exe)
	out := make([]RuntimeStatus, 0, len(artifacts))
	errorLookups := 0
	for _, a := range artifacts {
		s := RuntimeStatus{ID: a.Id, Deployed: true, Version: a.Version, Status: a.Status, Type: a.Type, DeployedBy: a.DeployedBy}
		if !a.DeployedOn.IsZero() {
			on := a.DeployedOn
			s.DeployedOn = &on
		}
		if a.Status == "ERROR" && errorLookups < 20 {
			errorLookups++
			s.ErrorInfo, _ = rt.GetErrorInfo(a.Id)
		}
		out = append(out, s)
	}
	return out, nil
}

// ListResources lists the resources (scripts, mappings, schemas, ...) of an iFlow.
func ListResources(exe *httpclnt.HTTPExecuter, id, version string) ([]cpi.Resource, error) {
	if id == "" {
		return nil, output.Usagef("artifact ID is required")
	}
	return cpi.NewContent(exe).Resources(id, version)
}

// ResourceContent is one downloaded resource.
type ResourceContent struct {
	ArtifactID string `json:"artifactId"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	Content
}

// GetResource downloads one resource of an iFlow (see NewContent for max).
func GetResource(exe *httpclnt.HTTPExecuter, id, version, name, resourceType string, max int) (*ResourceContent, error) {
	if id == "" || name == "" || resourceType == "" {
		return nil, output.Usagef("artifact ID, resource name and resource type are required")
	}
	b, err := cpi.NewContent(exe).ResourceContent(id, version, name, resourceType)
	if err != nil {
		if httpclnt.StatusCode(err) == 404 {
			return nil, output.Usagef("resource %s (%s) not found in %s", name, resourceType, id)
		}
		return nil, err
	}
	return &ResourceContent{ArtifactID: id, Name: name, Type: resourceType, Content: NewContent(b, max)}, nil
}

// DownloadResult is the outcome of DownloadArtifactToDir.
type DownloadResult struct {
	ArtifactID string `json:"artifactId"`
	Type       string `json:"type"`
	Dir        string `json:"dir"`
	Files      int    `json:"files"`
}

// DownloadArtifactToDir downloads a designtime artifact and extracts it into
// dir. dir must not exist or be empty unless overwrite is set.
func DownloadArtifactToDir(exe *httpclnt.HTTPExecuter, artifactType, id, version, dir string, overwrite bool) (*DownloadResult, error) {
	if !cpi.IsValidArtifactType(artifactType) {
		return nil, output.Usagef("invalid artifact type %q (valid types: %s)", artifactType, strings.Join(cpi.ArtifactTypes, ", "))
	}
	if id == "" || dir == "" {
		return nil, output.Usagef("artifact ID and target directory are required")
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 && !overwrite {
		return nil, output.Usagef("directory %s is not empty (set overwrite to replace its content)", dir)
	}
	data, err := cpi.DownloadArtifact(exe, artifactType, id, version)
	if err != nil {
		if httpclnt.StatusCode(err) == 404 {
			return nil, output.Usagef("%s artifact %s not found", artifactType, id)
		}
		return nil, err
	}
	tmp, err := os.CreateTemp("", "cpicli-download-*.zip")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return nil, err
	}
	tmp.Close()
	if overwrite {
		if err := os.RemoveAll(dir); err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	if err := file.UnzipSource(tmp.Name(), dir); err != nil {
		return nil, fmt.Errorf("failed to extract %s: %w", id, err)
	}
	// stable between downloads: no timestamp line, sorted keys
	if err := file.NormalizeParametersFile(dir); err != nil {
		return nil, err
	}
	// the download's Bundle-Version is not the designtime version
	if v := version; v == "" || strings.EqualFold(v, "active") {
		if info, exists, err := cpi.GetDesigntimeInfo(exe, artifactType, id, "active"); err == nil && exists {
			version = info.Version
		}
	}
	if version != "" && !strings.EqualFold(version, "active") {
		if err := manifest.SetVersion(dir, version); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	files := 0
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files++
		}
		return nil
	})
	return &DownloadResult{ArtifactID: id, Type: artifactType, Dir: dir, Files: files}, nil
}
