package ops

import (
	"encoding/base64"
	"slices"
	"sort"
	"strings"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
)

// PDContentType is a binary content type of the Partner Directory.
type PDContentType struct {
	// Type is the value of the ContentType property (e.g. "xsl").
	Type string `json:"type"`
	// Extensions are the file extensions (the first is the canonical one).
	Extensions []string `json:"extensions"`
	MIME       string   `json:"mime"`
}

// PDContentTypes are the binary content types SAP Cloud Integration supports
// for Partner Directory parameters.
var PDContentTypes = []PDContentType{
	{Type: "xml", Extensions: []string{".xml"}, MIME: "application/xml"},
	{Type: "xsl", Extensions: []string{".xsl", ".xslt"}, MIME: "application/xslt+xml"},
	{Type: "xsd", Extensions: []string{".xsd"}, MIME: "application/xml"},
	{Type: "json", Extensions: []string{".json"}, MIME: "application/json"},
	{Type: "txt", Extensions: []string{".txt"}, MIME: "text/plain"},
	{Type: "zip", Extensions: []string{".zip"}, MIME: "application/zip"},
	{Type: "gz", Extensions: []string{".gz"}, MIME: "application/gzip"},
	{Type: "zlib", Extensions: []string{".zlib"}, MIME: "application/zlib"},
	{Type: "crt", Extensions: []string{".crt"}, MIME: "application/pkix-cert"},
}

// PDContentTypeForExtension returns the content type of a file extension
// (".xslt" -> "xsl"; case-insensitive).
func PDContentTypeForExtension(ext string) (string, bool) {
	ext = strings.ToLower(ext)
	for _, ct := range PDContentTypes {
		if slices.Contains(ct.Extensions, ext) {
			return ct.Type, true
		}
	}
	return "", false
}

// LookupPDContentType returns the table entry of a content type ("xml",
// "text/xml" and "xml; encoding=UTF-8" are all "xml").
func LookupPDContentType(contentType string) (PDContentType, bool) {
	base := baseContentType(contentType)
	for _, ct := range PDContentTypes {
		if ct.Type == base {
			return ct, true
		}
	}
	return PDContentType{}, false
}

// pdExtensions lists the known file extensions (for messages).
func pdExtensions() string {
	var exts []string
	for _, ct := range PDContentTypes {
		exts = append(exts, ct.Extensions...)
	}
	return strings.Join(exts, ", ")
}

// PDPartner summarises the parameters of a partner ID.
type PDPartner struct {
	Pid      string `json:"pid"`
	Strings  int    `json:"strings"`
	Binaries int    `json:"binaries"`
	// BinaryBytes is the decoded size of all binaries; only with sizes (the
	// API has no size field, so the values have to be read).
	BinaryBytes  int64    `json:"binaryBytes,omitempty"`
	ContentTypes []string `json:"contentTypes"`
	// AlternativePartners and AuthorizedUsers count the entries that point
	// to this partner ID.
	AlternativePartners int `json:"alternativePartners,omitempty"`
	AuthorizedUsers     int `json:"authorizedUsers,omitempty"`
}

// ListPDPartners lists the partner IDs of the Partner Directory with the
// number of string and binary parameters and the binary content types.
// Parameter values are not downloaded (only Pid, Id and ContentType are
// selected).
func ListPDPartners(api *cpi.PartnerDirectory) ([]PDPartner, error) {
	return ListPDPartnersWith(api, PDPartnersOptions{})
}

// PDPartnersOptions extend ListPDPartners.
type PDPartnersOptions struct {
	// Sizes reads the binary values to report BinaryBytes (slower: the
	// tenant pages binaries by 30).
	Sizes bool
	// Pid filters by partner ID (substring, case-insensitive).
	Pid string
}

// ListPDPartnersWith is ListPDPartners with options.
func ListPDPartnersWith(api *cpi.PartnerDirectory, opts PDPartnersOptions) ([]PDPartner, error) {
	pids, err := api.Partners()
	if err != nil {
		return nil, err
	}
	byPid := map[string]*PDPartner{}
	get := func(pid string) *PDPartner {
		p := byPid[pid]
		if p == nil {
			p = &PDPartner{Pid: pid, ContentTypes: []string{}}
			byPid[pid] = p
		}
		return p
	}
	for _, pid := range pids {
		get(pid)
	}
	strs, err := api.GetStringParameters("Pid,Id")
	if err != nil {
		return nil, err
	}
	for _, s := range strs {
		get(s.Pid).Strings++
	}
	sel := "Pid,Id,ContentType"
	if opts.Sizes {
		sel = ""
	}
	bins, err := api.GetBinaryParameters(sel)
	if err != nil {
		return nil, err
	}
	for _, b := range bins {
		p := get(b.Pid)
		p.Binaries++
		if ct := baseContentType(b.ContentType); ct != "" && !slices.Contains(p.ContentTypes, ct) {
			p.ContentTypes = append(p.ContentTypes, ct)
		}
		if opts.Sizes {
			p.BinaryBytes += int64(base64.StdEncoding.DecodedLen(len(b.Value)) - strings.Count(b.Value, "="))
		}
	}
	if alts, err := api.AlternativePartners(); err == nil {
		for _, a := range alts {
			get(a.Pid).AlternativePartners++
		}
	}
	if users, err := api.AuthorizedUsers(); err == nil {
		for _, u := range users {
			get(u.Pid).AuthorizedUsers++
		}
	}
	out := make([]PDPartner, 0, len(byPid))
	for _, p := range byPid {
		if p.Pid == "" || (opts.Pid != "" && !strings.Contains(strings.ToLower(p.Pid), strings.ToLower(opts.Pid))) {
			continue
		}
		sort.Strings(p.ContentTypes)
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Pid < out[j].Pid })
	return out, nil
}

// ListAlternativePartners lists alternative partners, optionally of one PID.
func ListAlternativePartners(api *cpi.PartnerDirectory, pid string) ([]cpi.AlternativePartner, error) {
	all, err := api.AlternativePartners()
	if err != nil {
		return nil, err
	}
	out := []cpi.AlternativePartner{}
	for _, a := range all {
		if pid == "" || a.Pid == pid {
			out = append(out, a)
		}
	}
	return out, nil
}

// PDEntryResult is the outcome of an alternative partner or authorized
// user change.
type PDEntryResult struct {
	Action string `json:"action"`
	Pid    string `json:"pid"`
	Key    string `json:"key"`
}

// CreateAlternativePartner maps agency/scheme/id to a partner ID.
func CreateAlternativePartner(api *cpi.PartnerDirectory, a cpi.AlternativePartner) (*PDEntryResult, error) {
	if a.Agency == "" || a.Scheme == "" || a.ID == "" || a.Pid == "" {
		return nil, output.Usagef("agency, scheme, id and pid are required")
	}
	if err := api.CreateAlternativePartner(a); err != nil {
		return nil, err
	}
	return &PDEntryResult{Action: "CREATED", Pid: a.Pid, Key: a.Agency + "/" + a.Scheme + "/" + a.ID}, nil
}

// DeleteAlternativePartner deletes an alternative partner (a missing one is
// a usage error).
func DeleteAlternativePartner(api *cpi.PartnerDirectory, agency, scheme, id string) (*PDEntryResult, error) {
	all, err := api.AlternativePartners()
	if err != nil {
		return nil, err
	}
	for _, a := range all {
		if a.Agency == agency && a.Scheme == scheme && a.ID == id {
			if err := api.DeleteAlternativePartner(agency, scheme, id); err != nil {
				return nil, err
			}
			return &PDEntryResult{Action: "DELETED", Pid: a.Pid, Key: agency + "/" + scheme + "/" + id}, nil
		}
	}
	return nil, output.Usagef("alternative partner %s/%s/%s not found", agency, scheme, id)
}

// ListAuthorizedUsers lists authorized users, optionally of one PID.
func ListAuthorizedUsers(api *cpi.PartnerDirectory, pid string) ([]cpi.AuthorizedUser, error) {
	all, err := api.AuthorizedUsers()
	if err != nil {
		return nil, err
	}
	out := []cpi.AuthorizedUser{}
	for _, u := range all {
		if pid == "" || u.Pid == pid {
			out = append(out, u)
		}
	}
	return out, nil
}

// CreateAuthorizedUser authorizes a user for a partner ID.
func CreateAuthorizedUser(api *cpi.PartnerDirectory, user, pid string) (*PDEntryResult, error) {
	if user == "" || pid == "" {
		return nil, output.Usagef("user and pid are required")
	}
	if err := api.CreateAuthorizedUser(user, pid); err != nil {
		return nil, err
	}
	return &PDEntryResult{Action: "CREATED", Pid: pid, Key: user}, nil
}

// DeleteAuthorizedUser removes an authorized user (a missing one is a usage
// error).
func DeleteAuthorizedUser(api *cpi.PartnerDirectory, user string) (*PDEntryResult, error) {
	all, err := api.AuthorizedUsers()
	if err != nil {
		return nil, err
	}
	for _, u := range all {
		if u.User == user {
			if err := api.DeleteAuthorizedUser(user); err != nil {
				return nil, err
			}
			return &PDEntryResult{Action: "DELETED", Pid: u.Pid, Key: user}, nil
		}
	}
	return nil, output.Usagef("authorized user %s not found", user)
}
