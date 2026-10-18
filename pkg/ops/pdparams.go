package ops

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/cpars-innovation/cpicli/pkg/pdrepo"
)

// PDString is a string parameter of the Partner Directory.
type PDString struct {
	ID    string `json:"id"`
	Value string `json:"value"`
}

// PDBinary describes a binary parameter; Content only on request.
type PDBinary struct {
	ID          string   `json:"id"`
	ContentType string   `json:"contentType"`
	Size        int      `json:"size"`
	SHA256      string   `json:"sha256"`
	Content     *Content `json:"content,omitempty"`
	// Inspection only with PDGetOptions.Inspect.
	Inspection *PDInspection `json:"inspection,omitempty"`
}

// PDGetOptions are the options of GetPDParametersWith.
type PDGetOptions struct {
	// IncludeContent returns binary content, cut at MaxBytes (see
	// NewContent).
	IncludeContent bool
	MaxBytes       int
	// Inspect runs InspectPDBinary on every binary with these options.
	Inspect *PDInspectOptions
}

// PDParameters are the parameters of one partner ID.
type PDParameters struct {
	Pid      string     `json:"pid"`
	Strings  []PDString `json:"strings"`
	Binaries []PDBinary `json:"binaries"`
	// Missing are requested keys that exist neither as string nor as binary.
	Missing []string `json:"missing,omitempty"`
}

// GetPDParameters reads the parameters of a partner ID (all, or only keys).
// Binary content is returned only with includeContent (truncated to max
// bytes, see NewContent); otherwise size and SHA-256 identify it.
func GetPDParameters(api *cpi.PartnerDirectory, pid string, keys []string, includeContent bool, max int) (*PDParameters, error) {
	return GetPDParametersWith(api, pid, keys, PDGetOptions{IncludeContent: includeContent, MaxBytes: max})
}

// GetPDParametersWith is GetPDParameters with binaries inspected on request.
func GetPDParametersWith(api *cpi.PartnerDirectory, pid string, keys []string, opts PDGetOptions) (*PDParameters, error) {
	if pid == "" {
		return nil, output.Usagef("pid is required")
	}
	strs, bins, err := api.ListParameters(pid)
	if err != nil {
		return nil, err
	}
	want := func(id string) bool { return len(keys) == 0 || slices.Contains(keys, id) }
	res := &PDParameters{Pid: pid, Strings: []PDString{}, Binaries: []PDBinary{}}
	found := map[string]bool{}
	for _, p := range strs {
		if want(p.ID) {
			res.Strings = append(res.Strings, PDString{ID: p.ID, Value: p.Value})
			found[p.ID] = true
		}
	}
	for _, p := range bins {
		if !want(p.ID) {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(p.Value)
		if err != nil {
			return nil, fmt.Errorf("binary parameter %s/%s: invalid base64 value: %w", pid, p.ID, err)
		}
		b := PDBinary{ID: p.ID, ContentType: p.ContentType, Size: len(data), SHA256: sha256Hex(data)}
		if opts.IncludeContent {
			c := NewContent(data, opts.MaxBytes)
			b.Content = &c
		}
		if opts.Inspect != nil {
			b.Inspection, _ = InspectPDBinary(data, p.ContentType, *opts.Inspect)
		}
		res.Binaries = append(res.Binaries, b)
		found[p.ID] = true
	}
	for _, k := range keys {
		if !found[k] {
			res.Missing = append(res.Missing, k)
		}
	}
	sort.Slice(res.Strings, func(i, j int) bool { return res.Strings[i].ID < res.Strings[j].ID })
	sort.Slice(res.Binaries, func(i, j int) bool { return res.Binaries[i].ID < res.Binaries[j].ID })
	return res, nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// PD diff changes.
const (
	PDCreate     = "create"
	PDUpdate     = "update"
	PDUnchanged  = "unchanged"
	PDRemoteOnly = "remote_only"
)

// PDDiffItem is the difference for one parameter.
type PDDiffItem struct {
	Pid    string `json:"pid"`
	Kind   string `json:"kind"` // string or binary
	ID     string `json:"id"`
	Change string `json:"change"`
	// Detail says what differs for an update (value, content, contentType).
	Detail string `json:"detail,omitempty"`
}

// PDDiffResult compares local Partner Directory files with the tenant.
type PDDiffResult struct {
	Items   []PDDiffItem   `json:"items"`
	Summary map[string]int `json:"summary"`
	// Errors are PIDs that could not be compared; they are never reported as
	// empty (which would make every remote parameter remote_only).
	Errors []string `json:"errors,omitempty"`
}

// PDDiff compares the local tree (layout of pd-snapshot) with the tenant.
// remote_only items are what pd_deploy with full_sync would delete.
func PDDiff(api *cpi.PartnerDirectory, local *pdrepo.Repo, pids []string) (*PDDiffResult, error) {
	localPIDs, err := local.GetLocalPIDs()
	if err != nil {
		return nil, output.Usagef("cannot read %s: %v", local.ResourcesPath, err)
	}
	if len(pids) > 0 {
		for _, p := range pids {
			if !slices.Contains(localPIDs, p) {
				return nil, output.Usagef("PID %s has no local directory under %s", p, local.ResourcesPath)
			}
		}
		localPIDs = pids
	}
	res := &PDDiffResult{Items: []PDDiffItem{}, Summary: map[string]int{}}
	for _, pid := range localPIDs {
		items, err := diffPID(api, local, pid)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", pid, err))
			continue
		}
		res.Items = append(res.Items, items...)
	}
	for _, it := range res.Items {
		res.Summary[it.Change]++
	}
	if len(res.Errors) > 0 {
		return res, output.Partial(fmt.Errorf("%d PID(s) could not be compared: %s", len(res.Errors), strings.Join(res.Errors, "; ")))
	}
	return res, nil
}

func diffPID(api *cpi.PartnerDirectory, local *pdrepo.Repo, pid string) ([]PDDiffItem, error) {
	localStrs, err := local.ReadStringParameters(pid)
	if err != nil {
		return nil, err
	}
	localBins, err := local.ReadBinaryFiles(pid)
	if err != nil {
		return nil, err
	}
	remoteStrs, remoteBins, err := api.ListParameters(pid)
	if err != nil {
		return nil, err
	}
	var items []PDDiffItem
	remoteS := map[string]string{}
	for _, p := range remoteStrs {
		remoteS[p.ID] = p.Value
	}
	for _, p := range localStrs {
		it := PDDiffItem{Pid: pid, Kind: "string", ID: p.ID, Change: PDUnchanged}
		switch v, ok := remoteS[p.ID]; {
		case !ok:
			it.Change = PDCreate
		case v != p.Value:
			it.Change, it.Detail = PDUpdate, "value"
		}
		delete(remoteS, p.ID)
		items = append(items, it)
	}
	for id := range remoteS {
		items = append(items, PDDiffItem{Pid: pid, Kind: "string", ID: id, Change: PDRemoteOnly})
	}
	remoteB := map[string]cpi.BinaryParameter{}
	for _, p := range remoteBins {
		remoteB[p.ID] = p
	}
	for _, p := range localBins {
		it := PDDiffItem{Pid: pid, Kind: "binary", ID: p.ID, Change: PDUnchanged}
		if r, ok := remoteB[p.ID]; !ok {
			it.Change = PDCreate
		} else {
			var diffs []string
			if !sameBase64Content(p.Value, r.Value) {
				diffs = append(diffs, "content")
			}
			if !sameContentType(p.ContentType, r.ContentType) {
				diffs = append(diffs, fmt.Sprintf("contentType %s -> %s", r.ContentType, p.ContentType))
			}
			if len(diffs) > 0 {
				it.Change, it.Detail = PDUpdate, strings.Join(diffs, ", ")
			}
		}
		delete(remoteB, p.ID)
		items = append(items, it)
	}
	for id := range remoteB {
		items = append(items, PDDiffItem{Pid: pid, Kind: "binary", ID: id, Change: PDRemoteOnly})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Kind != items[j].Kind {
			return items[i].Kind > items[j].Kind // string before binary
		}
		return items[i].ID < items[j].ID
	})
	return items, nil
}

func sameBase64Content(a, b string) bool {
	x, errA := base64.StdEncoding.DecodeString(a)
	y, errB := base64.StdEncoding.DecodeString(b)
	if errA != nil || errB != nil {
		return a == b
	}
	return sha256Hex(x) == sha256Hex(y)
}

// sameContentType compares content types by their base type ("xml",
// "text/xml" and "xml; encoding=UTF-8" are the same).
func sameContentType(a, b string) bool {
	return baseContentType(a) == baseContentType(b)
}

func baseContentType(ct string) string {
	ct, _, _ = strings.Cut(strings.ToLower(strings.TrimSpace(ct)), ";")
	ct = strings.TrimSpace(ct)
	if _, sub, ok := strings.Cut(ct, "/"); ok {
		ct = sub
	}
	if ct == "xslt" {
		ct = "xsl"
	}
	return ct
}

// PDKeyResult is the outcome for one key of a single-key deployment.
type PDKeyResult struct {
	Key    string `json:"key"`
	Kind   string `json:"kind"`
	Action string `json:"action"` // CREATED, UPDATED, UNCHANGED (dry run: what would happen)
	Error  string `json:"error,omitempty"`
}

// pdDeployKeys creates or updates only the given "<pid>:<id>" parameters
// (merge: nothing is deleted).
func pdDeployKeys(api *cpi.PartnerDirectory, local *pdrepo.Repo, keys []string, dryRun bool) ([]PDKeyResult, error) {
	type want struct{ pid, id string }
	var wanted []want
	byPID := map[string][]string{}
	for _, k := range keys {
		pid, id, ok := strings.Cut(k, ":")
		if !ok || pid == "" || id == "" {
			return nil, output.Usagef("invalid key %q, expected <pid>:<id>", k)
		}
		wanted = append(wanted, want{pid, id})
		byPID[pid] = append(byPID[pid], id)
	}
	// Resolve every key locally before writing anything
	type param struct {
		str *cpi.StringParameter
		bin *cpi.BinaryParameter
	}
	resolved := map[string]param{}
	for pid := range byPID {
		strs, err := local.ReadStringParameters(pid)
		if err != nil {
			return nil, output.Usagef("cannot read local parameters of %s: %v", pid, err)
		}
		bins, err := local.ReadBinaryFiles(pid)
		if err != nil {
			return nil, output.Usagef("cannot read local parameters of %s: %v", pid, err)
		}
		for i := range strs {
			resolved[pid+":"+strs[i].ID] = param{str: &strs[i]}
		}
		for i := range bins {
			b := bins[i].BinaryParameter
			if !bins[i].FromMetadata {
				ext := strings.ToLower(filepath.Ext(bins[i].File))
				ct, ok := PDContentTypeForExtension(ext)
				if !ok {
					if slices.Contains(byPID[pid], b.ID) {
						return nil, output.Usagef("%s: no content type in _metadata.json and unknown extension %q (known: %s)", bins[i].File, ext, pdExtensions())
					}
					continue
				}
				b.ContentType = ct
			}
			resolved[pid+":"+b.ID] = param{bin: &b}
		}
	}
	for _, w := range wanted {
		if _, ok := resolved[w.pid+":"+w.id]; !ok {
			return nil, output.Usagef("key %s:%s not found in the local files", w.pid, w.id)
		}
	}

	var results []PDKeyResult
	var failures []string
	for _, w := range wanted {
		key := w.pid + ":" + w.id
		p := resolved[key]
		r := PDKeyResult{Key: key}
		var err error
		if p.str != nil {
			r.Kind = "string"
			r.Action, err = upsertString(api, *p.str, dryRun)
		} else {
			r.Kind = "binary"
			r.Action, err = upsertBinary(api, *p.bin, dryRun)
		}
		if err != nil {
			r.Action, r.Error = "FAILED", err.Error()
			failures = append(failures, key+": "+err.Error())
		}
		results = append(results, r)
	}
	if len(failures) > 0 {
		return results, output.Partial(fmt.Errorf("%d of %d key(s) failed: %s", len(failures), len(results), strings.Join(failures, "; ")))
	}
	return results, nil
}

func upsertString(api *cpi.PartnerDirectory, p cpi.StringParameter, dryRun bool) (string, error) {
	existing, err := api.GetStringParameter(p.Pid, p.ID)
	switch {
	case err != nil:
		return "", err
	case existing == nil:
		if !dryRun {
			err = api.CreateStringParameter(p)
		}
		return "CREATED", err
	case existing.Value != p.Value:
		if !dryRun {
			err = api.UpdateStringParameter(p)
		}
		return "UPDATED", err
	}
	return "UNCHANGED", nil
}

func upsertBinary(api *cpi.PartnerDirectory, p cpi.BinaryParameter, dryRun bool) (string, error) {
	existing, err := api.GetBinaryParameter(p.Pid, p.ID)
	switch {
	case err != nil:
		return "", err
	case existing == nil:
		if !dryRun {
			err = api.CreateBinaryParameter(p)
		}
		return "CREATED", err
	case !sameBase64Content(existing.Value, p.Value) || !sameContentType(existing.ContentType, p.ContentType):
		if !dryRun {
			err = api.UpdateBinaryParameter(p)
		}
		return "UPDATED", err
	}
	return "UNCHANGED", nil
}

// SetBinaryParameter creates or updates one Partner Directory binary
// parameter (contentType as in PDContentTypes, e.g. "xsl").
func SetBinaryParameter(exe *httpclnt.HTTPExecuter, pid, id, contentType string, content []byte) error {
	_, err := upsertBinary(cpi.NewPartnerDirectory(exe), cpi.BinaryParameter{Pid: pid, ID: id, ContentType: contentType,
		Value: base64.StdEncoding.EncodeToString(content)}, false)
	return err
}

// DeleteBinaryParameter removes one Partner Directory binary parameter; a
// missing one is not an error.
func DeleteBinaryParameter(exe *httpclnt.HTTPExecuter, pid, id string) error {
	pd := cpi.NewPartnerDirectory(exe)
	p, err := pd.GetBinaryParameter(pid, id)
	if err != nil || p == nil {
		return err
	}
	return pd.DeleteBinaryParameter(pid, id)
}
