package mcp

import (
	"cmp"
	"encoding/json"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/cpars-innovation/cpicli/internal/skills"
)

// Resource URIs. Skills and workflows are what help returns; loop journals
// are the .cpi/loops/<id>.md files under the server root that loop_end
// writes.
const (
	resourceScheme    = "cpictl://"
	skillsResource    = resourceScheme + "skills/"
	loopsResource     = resourceScheme + "loops/"
	workflowsResource = resourceScheme + "workflows"
)

// codeResourceNotFound is the MCP error code of an unknown resource.
const codeResourceNotFound = -32002

// Resource is one entry of resources/list.
type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType"`
}

// reLoopID is what a loop ID looks like (see Ledger.start); anything else
// in a loop URI is refused before it gets near the file system.
var reLoopID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// listResources returns the skills (SKILL.md and their reference files),
// the workflows and the loop journals under the root.
func (s *Server) listResources() []Resource {
	res := []Resource{{URI: workflowsResource, Name: "workflows", Title: "Workflows",
		Description: "Common tasks with the skill and tools for each (as help topic workflows)", MIMEType: "application/json"}}
	for _, sk := range skills.List() {
		for _, f := range sk.Files {
			r := Resource{URI: skillsResource + sk.Name + "/" + f, Name: sk.Name + "/" + f, MIMEType: mimeType(f)}
			if f == "SKILL.md" {
				r.URI, r.Name, r.Title, r.Description = skillsResource+sk.Name, sk.Name, "Skill "+sk.Name, sk.Description
			}
			res = append(res, r)
		}
	}
	if s.Root == "" {
		return res
	}
	files, _ := filepath.Glob(filepath.Join(s.Root, ".cpi", "loops", "*.md"))
	slices.Sort(files)
	for _, f := range files {
		id := strings.TrimSuffix(filepath.Base(f), ".md")
		if reLoopID.MatchString(id) {
			res = append(res, Resource{URI: loopsResource + id, Name: "loop " + id, Title: "Loop journal " + id,
				Description: "Journal of an autonomous loop (loop_end): goal, outcome, counters and tool calls", MIMEType: "text/markdown"})
		}
	}
	return res
}

// readResource returns the contents of a resource; unknown URIs and URIs
// that leave the root are JSON-RPC errors.
func (s *Server) readResource(params json.RawMessage) (any, *rpcError) {
	var p struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.URI == "" {
		return nil, &rpcError{Code: codeInvalidParams, Message: "resources/read needs a uri"}
	}
	notFound := &rpcError{Code: codeResourceNotFound, Message: "resource not found: " + p.URI}
	contents := func(mime, text string) (any, *rpcError) {
		return map[string]any{"contents": []map[string]any{{"uri": p.URI, "mimeType": mime, "text": text}}}, nil
	}
	switch {
	case p.URI == workflowsResource:
		available := map[string]Tool{}
		for _, t := range s.tools {
			available[t.Name] = t
		}
		data, err := json.MarshalIndent(workflowsFor(available), "", "  ")
		if err != nil {
			return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
		}
		return contents("application/json", string(data))
	case strings.HasPrefix(p.URI, skillsResource):
		name, file, _ := strings.Cut(strings.TrimPrefix(p.URI, skillsResource), "/")
		sk, ok := skills.Find(name)
		if !ok || (file != "" && !slices.Contains(sk.Files, file)) {
			return nil, notFound
		}
		text, err := skills.Read(name, file)
		if err != nil {
			return nil, notFound
		}
		return contents(mimeType(cmp.Or(file, "SKILL.md")), text)
	case strings.HasPrefix(p.URI, loopsResource):
		id, err := url.PathUnescape(strings.TrimPrefix(p.URI, loopsResource))
		if err != nil || !reLoopID.MatchString(id) || s.Root == "" {
			return nil, &rpcError{Code: codeInvalidParams, Message: "invalid loop resource " + p.URI + ": expected cpictl://loops/<loop id>"}
		}
		file, uErr := resolvePath(s.Root, filepath.Join(".cpi", "loops", id+".md"))
		if uErr != nil {
			return nil, &rpcError{Code: codeInvalidParams, Message: uErr.Error()}
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, notFound
		}
		return contents("text/markdown", string(data))
	}
	return nil, notFound
}

func mimeType(file string) string {
	switch path.Ext(file) {
	case ".md":
		return "text/markdown"
	case ".json":
		return "application/json"
	}
	return "text/plain"
}
