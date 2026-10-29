package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/ops"
)

// Payloads (message bodies, attachments, persisted messages, variables,
// data store entries) are stored whole in a content store and results carry
// a reference with the first bytes inline. get_content reads a reference:
// by offset, or into a file. Nothing is lost by the inline cut, and the
// store survives a restart of the server.
const (
	inlinePayloadBytes = 16 * 1024
	// fetchBytes is how much of a payload the server reads from the tenant
	// (the inline limit applies afterwards)
	fetchBytes = 256 << 20
	// maxInlineBytes is the largest max_bytes an agent may ask for inline
	maxInlineBytes = 1 << 20
	saveToProp     = "save_to"
	responseOfProp = "response_of"
	// content store cleanup: entries older than this, or beyond this total
	contentMaxAge   = 7 * 24 * time.Hour
	contentMaxTotal = 500 << 20
)

// fullInline are tools with max_bytes whose content an agent usually needs
// whole (scripts, mappings, parameters): 64 KiB inline.
var fullInline = map[string]bool{"get_resource": true, "get_pd_parameters": true}

var reContentRef = regexp.MustCompile(`^c:[0-9a-f]{20}$`)

// ContentStore keeps payloads by content hash below a directory
// (<root>/.cpi/cache/content). by-message/<guid> names the test response of
// a message (response_of).
type ContentStore struct {
	dir string
	// root, when set, must contain dir once symbolic links are resolved
	root    string
	once    sync.Once
	initErr error
}

// NewContentStore returns the store in dir.
func NewContentStore(dir string) *ContentStore { return &ContentStore{dir: dir} }

// newRootStore returns the store of a server root: .cpi/cache/content below
// it, which must stay inside the root; without a root, the user's own
// cache directory (not a shared temp folder).
func newRootStore(root string) *ContentStore {
	userDir := func() string {
		dir, err := os.UserCacheDir()
		if err != nil {
			dir = filepath.Join(os.TempDir(), fmt.Sprintf("cpictl-%d", os.Getuid()))
		}
		return filepath.Join(dir, "cpictl", "content")
	}
	if root == "" {
		return &ContentStore{dir: userDir()}
	}
	dir := filepath.Join(root, ".cpi", "cache", "content")
	// a root (or store) of another user is shared: keep this user's payloads
	// in their own cache, one store per root (cpicli#64)
	if ownedByOther(root) || ownedByOther(dir) {
		abs, _ := filepath.Abs(root)
		sum := sha256.Sum256([]byte(abs))
		return &ContentStore{dir: filepath.Join(userDir(), "roots", hex.EncodeToString(sum[:8]))}
	}
	return &ContentStore{dir: dir, root: root}
}

func (c *ContentStore) init() error {
	c.once.Do(func() {
		if c.initErr = os.MkdirAll(filepath.Join(c.dir, "by-message"), 0o700); c.initErr != nil {
			return
		}
		if c.root != "" {
			// a .cpi or cache folder that links elsewhere would put payloads
			// (and reads) outside the root
			if c.initErr = insideRoot(c.root, c.dir); c.initErr != nil {
				return
			}
		}
		// keep the cache out of Git: .cpi/cache/.gitignore
		if filepath.Base(filepath.Dir(c.dir)) == "cache" {
			ignore := filepath.Join(filepath.Dir(c.dir), ".gitignore")
			if _, err := os.Stat(ignore); os.IsNotExist(err) {
				_ = os.WriteFile(ignore, []byte("# cpictl content cache (payloads of tool results)\n*\n"), 0o600)
			}
		}
		c.cleanup(time.Now())
	})
	return c.initErr
}

// cleanup removes entries older than contentMaxAge, then the oldest until
// the total is below contentMaxTotal.
func (c *ContentStore) cleanup(now time.Time) {
	type entry struct {
		path string
		mod  time.Time
		size int64
	}
	var entries []entry
	var total int64
	for _, dir := range []string{c.dir, filepath.Join(c.dir, "by-message")} {
		files, _ := os.ReadDir(dir)
		for _, f := range files {
			info, err := f.Info()
			if err != nil || f.IsDir() {
				continue
			}
			p := filepath.Join(dir, f.Name())
			if now.Sub(info.ModTime()) > contentMaxAge {
				_ = os.Remove(p)
				continue
			}
			entries = append(entries, entry{p, info.ModTime(), info.Size()})
			total += info.Size()
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].mod.Before(entries[j].mod) })
	for _, e := range entries {
		if total <= contentMaxTotal {
			break
		}
		_ = os.Remove(e.path)
		total -= e.size
	}
}

// Put stores b and returns its reference ("c:" and 20 hex digits of the
// SHA-256).
func (c *ContentStore) Put(b []byte) (ref string, sum string, err error) {
	h := sha256.Sum256(b)
	sum = hex.EncodeToString(h[:])
	ref = "c:" + sum[:20]
	if err := c.init(); err != nil {
		return "", "", err
	}
	p := filepath.Join(c.dir, sum[:20])
	if _, err := os.Stat(p); err == nil {
		now := time.Now()
		_ = os.Chtimes(p, now, now)
		return ref, sum, nil
	}
	tmp, err := os.CreateTemp(c.dir, ".tmp-*")
	if err != nil {
		return "", "", err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", "", err
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		os.Remove(tmp.Name())
		// the same content stored by a call at the same time (Windows does
		// not rename over an existing file)
		if _, statErr := os.Stat(p); statErr != nil {
			return "", "", err
		}
	}
	return ref, sum, nil
}

// Get returns the content of a reference.
func (c *ContentStore) Get(ref string) ([]byte, error) {
	if !reContentRef.MatchString(ref) {
		return nil, output.Usagef("invalid ref %q (c: and 20 hex digits, from a result's ref)", ref)
	}
	if err := c.init(); err != nil {
		return nil, err
	}
	b, err := readRegular(filepath.Join(c.dir, strings.TrimPrefix(ref, "c:")))
	if os.IsNotExist(err) {
		return nil, output.Usagef("content %s is not in the store (%s; entries are removed after 7 days): read it from the tenant again", ref, c.dir)
	}
	if err != nil {
		return nil, err
	}
	// the name is the content's hash: anything else was put there by
	// someone else
	if sum := sha256.Sum256(b); "c:"+hex.EncodeToString(sum[:])[:20] != ref {
		return nil, output.Usagef("content %s does not match its reference: the store entry was changed, read it from the tenant again", ref)
	}
	return b, nil
}

// readRegular reads a regular file, never through a symbolic link.
func readRegular(p string) ([]byte, error) {
	fi, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, output.Usagef("%s is not a regular file", filepath.Base(p))
	}
	return os.ReadFile(p)
}

// insideRoot checks that dir, symbolic links resolved, is inside root.
func insideRoot(root, dir string) error {
	r, err := filepath.EvalSymlinks(cmpRoot(root))
	if err != nil {
		return err
	}
	r, _ = filepath.Abs(r)
	d, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	d, _ = filepath.Abs(d)
	rel, err := filepath.Rel(r, d)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return output.Usagef("the content store %s is outside the server root (a linked .cpi or cache folder)", dir)
	}
	return nil
}

// name records that a message's test response has a reference.
func (c *ContentStore) name(guid, ref string) {
	if guid == "" || strings.ContainsAny(guid, `/\.`) || c.init() != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(c.dir, "by-message", guid), []byte(ref), 0o600)
}

// responseOf returns the reference of a message's test response.
func (c *ContentStore) responseOf(guid string) (string, error) {
	if strings.ContainsAny(guid, `/\.`) {
		return "", output.Usagef("invalid message GUID %q", guid)
	}
	if err := c.init(); err != nil {
		return "", err
	}
	b, err := readRegular(filepath.Join(c.dir, "by-message", guid))
	if err != nil {
		return "", output.Usagef("no stored response of message %s: only responses of send_test_message from this server root are kept (7 days)", guid)
	}
	return strings.TrimSpace(string(b)), nil
}

// contentStore returns the server's store (in the server root by default).
func (s *Server) contentStore() *ContentStore {
	s.contentOnce.Do(func() {
		if s.Content == nil {
			s.Content = newRootStore(s.Root)
		}
	})
	return s.Content
}

// Contents returns the server's content store.
func (s *Server) Contents() *ContentStore { return s.contentStore() }

// ContentTool reads stored content by reference (get_content). store
// returns the server's store; it is called on each use.
func ContentTool(store func() *ContentStore, root func() string, redacted bool) Tool {
	return Tool{
		Name: "get_content", Title: "Read stored content by reference",
		Description: "Read a payload that a result gave as ref (results show the first bytes inline and say \"cut\"): " +
			"from offset, up to max_bytes (default 16384), or the whole content into save_to (a file under the server root). " +
			"Reads the local content store only.",
		InputSchema: object(props{
			"ref":       str("Reference from a result (c:...)"),
			"offset":    map[string]any{"type": "integer", "minimum": 0, "description": "Byte offset to start at"},
			"max_bytes": map[string]any{"type": "integer", "minimum": 1, "maximum": maxInlineBytes, "description": "Bytes inline, default 16384"},
			saveToProp:  str("Write the whole content to this file under the root instead"),
		}, "ref"),
		// local: save_to writes a file
		Annotations: map[string]any{"readOnlyHint": false, "idempotentHint": true, "openWorldHint": false},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var a struct {
				Ref      string `json:"ref"`
				Offset   int    `json:"offset"`
				MaxBytes int    `json:"max_bytes"`
				SaveTo   string `json:"save_to"`
			}
			if err := decode(raw, &a); err != nil {
				return nil, err
			}
			if redacted {
				return nil, output.Usagef("get_content is not available: this server redacts message bodies (--redact-payloads)")
			}
			b, err := store().Get(a.Ref)
			if err != nil {
				return nil, err
			}
			if a.SaveTo != "" {
				return saveContent(root(), a.SaveTo, a.Ref, b)
			}
			if a.Offset < 0 || a.Offset > len(b) {
				return nil, output.Usagef("offset %d is outside the content (%d bytes)", a.Offset, len(b))
			}
			max := a.MaxBytes
			if max <= 0 {
				max = inlinePayloadBytes
			}
			if max > maxInlineBytes {
				return nil, output.Usagef("max_bytes must be at most %d (save_to for more)", maxInlineBytes)
			}
			part := b[a.Offset:]
			c := ops.NewContent(part, max)
			res := map[string]any{"ref": a.Ref, "size": len(b), "offset": a.Offset}
			n := len(c.Text)
			if c.Base64 != "" {
				res["base64"] = c.Base64
				n = min(max, len(part))
			} else {
				res["text"] = c.Text
			}
			if a.Offset+n < len(b) {
				res["next"] = a.Offset + n
			}
			return res, nil
		},
	}
}

// saveContent writes b to path (under root) and describes it.
func saveContent(root, path, ref string, b []byte) (map[string]any, error) {
	file, err := resolvePath(cmpRoot(root), path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(file, b, 0o644); err != nil {
		return nil, err
	}
	shown := file
	if rel, err := filepath.Rel(rootDir(root), file); err == nil {
		shown = filepath.ToSlash(rel)
	}
	sum := sha256.Sum256(b)
	res := map[string]any{"size": len(b), "savedTo": shown, "sha256": hex.EncodeToString(sum[:])}
	if ref != "" {
		res["ref"] = ref
	}
	return res, nil
}

func cmpRoot(root string) string {
	if root == "" {
		return "."
	}
	return root
}

// rootDir is a server root, absolute and with symbolic links resolved (as
// resolvePath returns paths).
func rootDir(root string) string {
	abs, err := filepath.Abs(cmpRoot(root))
	if err != nil {
		return cmpRoot(root)
	}
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		return r
	}
	return abs
}

// cutText describes a cut payload.
func cutText(shown, size int, ref string) string {
	if ref == "" {
		return fmt.Sprintf("%d of %d bytes shown (this server redacts payloads)", shown, size)
	}
	return fmt.Sprintf(`%d of %d bytes shown; get_content {"ref": %q} with offset or save_to for the rest`, shown, size, ref)
}
