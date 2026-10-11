package ops

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/cpars-innovation/cpicli/internal/file"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/httpclnt"
	"github.com/cpars-innovation/cpicli/pkg/manifest"
)

// IFlowCopyOptions describe a copy of an integration flow under a new ID.
type IFlowCopyOptions struct {
	// FromID copies the integration flow with this ID from the tenant
	// (FromVersion, default "active"); FromDir copies a local flow directory.
	// Exactly one is required.
	FromID      string
	FromVersion string
	FromDir     string
	// TargetDir receives the copy; it must not exist or be empty.
	TargetDir string
	// ID is the new flow ID (Bundle-SymbolicName). Name is the display name
	// (Bundle-Name, default ID), Description goes to metainfo.prop (the
	// source's description is removed when empty). Version is the new
	// Bundle-Version (default 1.0.0).
	ID, Name, Description, Version string
	// Addresses are the new sender addresses: "NEW" when the flow has one
	// sender address, else "OLD=NEW" per address (OLD as in the source,
	// e.g. "/orders/in" or "{{Orders_Path}}"). A value containing "=" is
	// always read as OLD=NEW.
	Addresses []string
	// KeepAddresses allows sender addresses that are not changed. The copy
	// then listens on the same address as the source, which fails the
	// deployment (HTTP paths, ProcessDirect) or makes both flows poll the
	// same source (SFTP, mail, JMS) while both are deployed.
	KeepAddresses bool
}

// IFlowCopyResult reports what a copy changed.
type IFlowCopyResult struct {
	From      string          `json:"from"`
	SourceID  string          `json:"sourceId"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Version   string          `json:"version"`
	Dir       string          `json:"dir"`
	Files     int             `json:"files"`
	Changes   []string        `json:"changes"`
	Addresses []AddressChange `json:"addresses"`
	// Remaining lists files that still contain the source ID (process
	// names, scripts, log texts): check whether they must change.
	Remaining []string `json:"remaining,omitempty"`
}

// AddressChange is a sender address of the copy.
type AddressChange struct {
	Adapter string `json:"adapter"`
	Old     string `json:"old"`
	New     string `json:"new,omitempty"`
	// Where is "model" (the .iflw), "parameter <key>" (parameters.prop) or
	// "kept" (unchanged, KeepAddresses).
	Where string `json:"where"`
}

// reArtifactID are the IDs cpictl accepts for new flows; the tenant may be
// stricter and reports it on upload.
var reArtifactID = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,99}$`)

// CopyIFlow copies an integration flow into TargetDir and renames it:
// Bundle-SymbolicName, Bundle-Name and Bundle-Version in MANIFEST.MF, the
// description in metainfo.prop, the model file <OldID>.iflw, the project
// name in .project, and the sender addresses (in the model, or in
// parameters.prop when the address is a {{parameter}}). Nothing is written to
// the tenant; upload the result with upload_artifact / 'cpictl update
// artifact'. On an error the target directory is left as it was.
func CopyIFlow(exe *httpclnt.HTTPExecuter, o IFlowCopyOptions) (res *IFlowCopyResult, err error) {
	if (o.FromID == "") == (o.FromDir == "") {
		return nil, output.Usagef("give either a source flow ID on the tenant or a local source directory")
	}
	if !reArtifactID.MatchString(o.ID) {
		return nil, output.Usagef("invalid flow ID %q: letters, digits, '_', '.', '-', at most 100 characters", o.ID)
	}
	if o.TargetDir == "" {
		return nil, output.Usagef("target directory is required")
	}
	if o.Name == "" {
		o.Name = o.ID
	}
	if o.Version == "" {
		o.Version = "1.0.0"
	}
	if strings.ContainsAny(o.Name+o.Version, "\r\n") {
		return nil, output.Usagef("name and version must be single lines")
	}
	created, err := prepareTarget(o.TargetDir)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			cleanTarget(o.TargetDir, created)
		}
	}()

	res = &IFlowCopyResult{ID: o.ID, Name: o.Name, Version: o.Version, Dir: o.TargetDir, Changes: []string{}, Addresses: []AddressChange{}}
	if o.FromDir != "" {
		res.From = o.FromDir
		if same, _ := sameDir(o.FromDir, o.TargetDir); same || isInside(o.TargetDir, o.FromDir) {
			return nil, output.Usagef("the target directory must not be the source directory or inside it")
		}
		if err = copyTree(o.FromDir, o.TargetDir); err != nil {
			return nil, err
		}
	} else {
		res.From = "tenant:" + o.FromID
		if _, err = DownloadArtifactToDir(exe, "Integration", o.FromID, o.FromVersion, o.TargetDir, true); err != nil {
			return nil, err
		}
	}
	err = renameIFlow(o, res)
	return res, err
}

func renameIFlow(o IFlowCopyOptions, res *IFlowCopyResult) error {
	dir := o.TargetDir
	mfPath := filepath.Join(dir, "META-INF", "MANIFEST.MF")
	mf, err := os.ReadFile(mfPath)
	if err != nil {
		return output.Usagef("%s is not an integration flow: META-INF/MANIFEST.MF missing", res.From)
	}
	h := parseManifest(mf)
	if h["SAP-BundleType"] != "IntegrationFlow" {
		return output.Usagef("%s is not an integration flow (SAP-BundleType %q)", res.From, h["SAP-BundleType"])
	}
	oldSym := h["Bundle-SymbolicName"]
	oldID, suffix, _ := strings.Cut(oldSym, ";")
	oldID = strings.TrimSpace(oldID)
	res.SourceID = oldID
	if oldID == o.ID {
		return output.Usagef("the new ID equals the source ID %s", oldID)
	}

	// sender addresses first: they can refuse the copy
	models, _ := filepath.Glob(filepath.Join(dir, filepath.FromSlash(ResourcesDir), "scenarioflows", "integrationflow", "*.iflw"))
	if len(models) == 0 {
		return output.Usagef("%s has no .iflw model", res.From)
	}
	contents := map[string]string{}
	var senders []senderAddress
	for _, m := range models {
		data, err := os.ReadFile(m)
		if err != nil {
			return err
		}
		contents[m] = string(data)
		for _, s := range findSenderAddresses(string(data)) {
			s.file = m
			senders = append(senders, s)
		}
	}
	changes, err := planAddressChanges(senders, o.Addresses, o.KeepAddresses)
	if err != nil {
		return err
	}
	params := map[string]string{}
	for _, s := range senders {
		newAddr, ok := changes[s.value]
		if !ok {
			res.Addresses = append(res.Addresses, AddressChange{Adapter: s.adapter, Old: s.value, Where: "kept"})
			continue
		}
		if m := reWholeParameter.FindStringSubmatch(s.value); m != nil {
			params[m[1]] = newAddr
			res.Addresses = append(res.Addresses, AddressChange{Adapter: s.adapter, Old: s.value, New: newAddr, Where: "parameter " + m[1]})
			continue
		}
		res.Addresses = append(res.Addresses, AddressChange{Adapter: s.adapter, Old: s.value, New: newAddr, Where: "model"})
	}
	// replace in the models from the end, so offsets stay valid
	sort.Slice(senders, func(i, j int) bool { return senders[i].start > senders[j].start })
	for _, s := range senders {
		newAddr, ok := changes[s.value]
		if !ok || reWholeParameter.MatchString(s.value) {
			continue
		}
		c := contents[s.file]
		contents[s.file] = c[:s.start] + xmlEscape(newAddr) + c[s.end:]
	}

	// MANIFEST.MF
	newSym := o.ID
	if suffix != "" {
		newSym += ";" + suffix
	}
	mf = setManifestHeaders(mf, map[string]string{"Bundle-SymbolicName": newSym, "Bundle-Name": o.Name, "Bundle-Version": o.Version})
	if err := os.WriteFile(mfPath, mf, 0o644); err != nil {
		return err
	}
	res.Changes = append(res.Changes, fmt.Sprintf("MANIFEST.MF: Bundle-SymbolicName %s -> %s, Bundle-Name %q -> %q, Bundle-Version %s -> %s",
		oldID, o.ID, h["Bundle-Name"], o.Name, h["Bundle-Version"], o.Version))

	// models: addresses, then the file name
	for _, m := range models {
		if contents[m] != "" {
			if err := os.WriteFile(m, []byte(contents[m]), 0o644); err != nil {
				return err
			}
		}
		if filepath.Base(m) == oldID+".iflw" {
			renamed := filepath.Join(filepath.Dir(m), o.ID+".iflw")
			if err := os.Rename(m, renamed); err != nil {
				return err
			}
			res.Changes = append(res.Changes, fmt.Sprintf("model %s.iflw -> %s.iflw", oldID, o.ID))
		}
	}
	for _, a := range res.Addresses {
		switch {
		case a.Where == "kept":
			res.Changes = append(res.Changes, fmt.Sprintf("%s sender address %s kept (same as the source flow)", a.Adapter, a.Old))
		default:
			res.Changes = append(res.Changes, fmt.Sprintf("%s sender address %s -> %s (%s)", a.Adapter, a.Old, a.New, a.Where))
		}
	}

	// parameters.prop
	if len(params) > 0 {
		p := filepath.Join(dir, filepath.FromSlash(ResourcesDir), "parameters.prop")
		data, _ := os.ReadFile(p)
		if err := os.WriteFile(p, setProperties(data, params, true), 0o644); err != nil {
			return err
		}
	}

	// metainfo.prop
	meta := filepath.Join(dir, "metainfo.prop")
	if data, err := os.ReadFile(meta); err == nil || o.Description != "" {
		if err := os.WriteFile(meta, setProperties(data, map[string]string{"description": o.Description}, false), 0o644); err != nil {
			return err
		}
		if o.Description == "" {
			res.Changes = append(res.Changes, "metainfo.prop: description of the source removed (set one with the description option)")
		} else {
			res.Changes = append(res.Changes, "metainfo.prop: description set")
		}
	}

	// .project (Eclipse project name)
	project := filepath.Join(dir, ".project")
	if data, err := os.ReadFile(project); err == nil {
		old := "<name>" + xmlEscape(oldID) + "</name>"
		if bytes.Contains(data, []byte(old)) {
			data = bytes.Replace(data, []byte(old), []byte("<name>"+xmlEscape(o.ID)+"</name>"), 1)
			if err := os.WriteFile(project, data, 0o644); err != nil {
				return err
			}
			res.Changes = append(res.Changes, ".project: name "+oldID+" -> "+o.ID)
		}
	}

	// what still mentions the source
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		res.Files++
		if !textResource(p) {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		if n := bytes.Count(data, []byte(oldID)); n > 0 {
			rel, _ := filepath.Rel(dir, p)
			res.Remaining = append(res.Remaining, fmt.Sprintf("%s (%d)", filepath.ToSlash(rel), n))
		}
		return nil
	})
	return nil
}

// senderAddress is the address property of a sender adapter in a model.
type senderAddress struct {
	file       string
	adapter    string
	value      string // unescaped
	start, end int    // byte range of the escaped value in the model
}

var (
	reMessageFlow    = regexp.MustCompile(`(?s)<(?:\w+:)?messageFlow\b.*?</(?:\w+:)?messageFlow>`)
	reModelProperty  = regexp.MustCompile(`(?s)<key>([^<]*)</key>\s*<value>([^<]*)</value>`)
	reWholeParameter = regexp.MustCompile(`^\{\{([^{}]+)\}\}$`)
)

// findSenderAddresses returns the address of every sender adapter that has
// one (the property chosen as in AnalyzeIFlow).
func findSenderAddresses(model string) []senderAddress {
	var res []senderAddress
	for _, mf := range reMessageFlow.FindAllStringIndex(model, -1) {
		block := model[mf[0]:mf[1]]
		props := map[string][]int{}
		values := map[string]string{}
		for _, m := range reModelProperty.FindAllStringSubmatchIndex(block, -1) {
			key := block[m[2]:m[3]]
			props[key] = []int{mf[0] + m[4], mf[0] + m[5]}
			values[key] = xmlUnescape(block[m[4]:m[5]])
		}
		if values["direction"] != "Sender" {
			continue
		}
		adapter := values["ComponentType"]
		if adapter == "" {
			_, cname := variant(values["cmdVariantUri"])
			adapter = strings.TrimPrefix(cname, "sap:")
		}
		for _, k := range addressKeys {
			if strings.TrimSpace(values[k]) != "" {
				res = append(res, senderAddress{adapter: adapter, value: values[k], start: props[k][0], end: props[k][1]})
				break
			}
		}
	}
	return res
}

// planAddressChanges maps old sender addresses to new ones.
func planAddressChanges(senders []senderAddress, flags []string, keep bool) (map[string]string, error) {
	var olds []string
	for _, s := range senders {
		if !slices.Contains(olds, s.value) {
			olds = append(olds, s.value)
		}
	}
	list := func() string {
		if len(olds) == 0 {
			return "none"
		}
		return strings.Join(olds, ", ")
	}
	changes := map[string]string{}
	for _, f := range flags {
		f = strings.TrimSpace(f)
		// a value with "=" is always OLD=NEW, so a typo in OLD never
		// becomes the new address
		if old, newAddr, ok := strings.Cut(f, "="); ok {
			old = strings.TrimSpace(old)
			if !slices.Contains(olds, old) {
				return nil, output.Usagef("address %q: the source has no sender address %q (sender addresses: %s)", f, old, list())
			}
			changes[old] = strings.TrimSpace(newAddr)
			continue
		}
		if len(flags) != 1 || len(olds) != 1 {
			return nil, output.Usagef("address %q: give OLD=NEW when the flow has %d sender addresses (%s)", f, len(olds), list())
		}
		changes[olds[0]] = f
	}
	for old, newAddr := range changes {
		if newAddr == "" {
			return nil, output.Usagef("new address for %s is empty", old)
		}
		if newAddr == old {
			delete(changes, old)
		}
	}
	if !keep {
		var missing []string
		for _, old := range olds {
			if _, ok := changes[old]; !ok {
				missing = append(missing, old)
			}
		}
		if len(missing) > 0 {
			return nil, output.Usagef("sender address(es) %s would be the same as in the source flow, which fails the deployment "+
				"(HTTP paths, ProcessDirect) or makes both flows poll the same source: give a new address (OLD=NEW), or keep them explicitly",
				strings.Join(missing, ", "))
		}
	}
	return changes, nil
}

// setManifestHeaders replaces or appends manifest headers (see
// manifest.SetHeaders).
func setManifestHeaders(mf []byte, headers map[string]string) []byte {
	return manifest.SetHeaders(mf, headers)
}

// wrapManifestLine splits a header into manifest lines of at most 72 bytes.
func wrapManifestLine(s string) []string { return manifest.WrapLine(s) }

// setProperties sets keys in a Java properties file, keeping all other lines;
// missing keys are appended. cpiEscapes escapes ':' and '=' in values as CPI
// writes them in parameters.prop.
func setProperties(data []byte, values map[string]string, cpiEscapes bool) []byte {
	eol := "\n"
	if bytes.Contains(data, []byte("\r\n")) {
		eol = "\r\n"
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.TrimSuffix(text, "\n")
	var lines []string
	if text != "" {
		lines = strings.Split(text, "\n")
	}
	done := map[string]bool{}
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed[0] == '#' || trimmed[0] == '!' {
			continue
		}
		for k := range PropertyValues([]byte(line)) {
			if v, ok := values[k]; ok {
				lines[i] = escapePropertyKey(k) + "=" + escapePropertyValue(v, cpiEscapes)
				done[k] = true
			}
		}
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !done[k] {
			lines = append(lines, escapePropertyKey(k)+"="+escapePropertyValue(values[k], cpiEscapes))
		}
	}
	return []byte(strings.Join(lines, eol) + eol)
}

func escapePropertyKey(k string) string {
	return strings.NewReplacer(`\`, `\\`, " ", `\ `, ":", `\:`, "=", `\=`).Replace(k)
}

func escapePropertyValue(v string, cpiEscapes bool) string {
	var b strings.Builder
	for i, r := range v {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case cpiEscapes && (r == ':' || r == '='):
			b.WriteString(`\` + string(r))
		case i == 0 && r == ' ':
			b.WriteString(`\ `)
		case r > 0x7e:
			for _, u := range utf16Units(r) {
				fmt.Fprintf(&b, `\u%04x`, u)
			}
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func utf16Units(r rune) []rune {
	if r < 0x10000 {
		return []rune{r}
	}
	r -= 0x10000
	return []rune{0xd800 + (r>>10)&0x3ff, 0xdc00 + r&0x3ff}
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func xmlUnescape(s string) string {
	return strings.NewReplacer("&lt;", "<", "&gt;", ">", "&quot;", `"`, "&apos;", "'", "&#xA;", "\n", "&#xD;", "\r", "&#x9;", "\t", "&amp;", "&").Replace(s)
}

func textResource(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".iflw", ".groovy", ".gsh", ".js", ".prop", ".propdef", ".xsl", ".xslt", ".mf", ".project", ".xml", ".json", ".mmap":
		return true
	}
	return filepath.Base(p) == ".project"
}

// prepareTarget makes sure dir does not exist or is empty; created reports
// whether it had to be created.
func prepareTarget(dir string) (created bool, err error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return true, os.MkdirAll(dir, 0o755)
	}
	if err != nil {
		return false, err
	}
	if len(entries) > 0 {
		return false, output.Usagef("target directory %s is not empty", dir)
	}
	return false, nil
}

func cleanTarget(dir string, created bool) {
	if created {
		_ = os.RemoveAll(dir)
		return
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		_ = os.RemoveAll(filepath.Join(dir, e.Name()))
	}
}

func sameDir(a, b string) (bool, error) {
	ia, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	ib, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	return os.SameFile(ia, ib), nil
}

func isInside(p, dir string) bool {
	ap, err1 := filepath.Abs(p)
	ad, err2 := filepath.Abs(dir)
	if err1 != nil || err2 != nil {
		return false
	}
	rel, err := filepath.Rel(ad, ap)
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..")
}

// copyTree copies the regular files of src into dst (.git and symlinks are
// skipped).
func copyTree(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil || !info.IsDir() {
		return output.Usagef("%s is not a directory", src)
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		return file.CopyFile(p, filepath.Join(dst, rel))
	})
}
