package pagestore

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sugihAF/contexo/internal/schema"
)

// Store wraps a .contexo/ directory tree on local disk.
type Store struct {
	Root string
}

var ErrNotFound = errors.New("pagestore: page not found")

func Open(root string) (*Store, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("pagestore: open %s: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("pagestore: %s is not a directory", root)
	}
	return &Store{Root: root}, nil
}

// Write persists a page to disk at its computed RelPath, creating any
// needed parent directories. Sets Updated to now if zero.
func (s *Store) Write(p *schema.Page) error {
	if err := p.Frontmatter.Validate(); err != nil {
		return err
	}
	if p.Frontmatter.Updated.IsZero() {
		p.Frontmatter.Updated = time.Now().UTC()
	}
	relPath := p.Frontmatter.RelPath()
	absPath := filepath.Join(s.Root, filepath.FromSlash(relPath))

	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return fmt.Errorf("pagestore: mkdir: %w", err)
	}

	data, err := schema.SerializePage(p)
	if err != nil {
		return err
	}
	return os.WriteFile(absPath, data, 0o644)
}

// Read loads a page by its relative path under the store root.
// relPath uses forward slashes regardless of platform.
func (s *Store) Read(relPath string) (*schema.Page, error) {
	absPath := filepath.Join(s.Root, filepath.FromSlash(relPath))
	data, err := os.ReadFile(absPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("pagestore: read %s: %w", relPath, err)
	}
	// Strip a BOM like WalkAll does, so a page Walk lists can be read.
	return schema.ParsePage(stripBOM(data))
}

// FindBySlug searches all known type directories for a page with the given slug.
func (s *Store) FindBySlug(slug string) (*schema.Page, error) {
	types := []schema.PageType{
		schema.TypeConcept, schema.TypeEntity, schema.TypeAnalysis, schema.TypeSource,
	}
	for _, t := range types {
		fm := schema.PageFrontmatter{Type: t, Slug: slug}
		p, err := s.Read(fm.RelPath())
		if err == nil {
			return p, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
	}
	return nil, ErrNotFound
}

// Filter narrows page listings. Empty fields match everything.
type Filter struct {
	Types []schema.PageType
	Tags  []string
}

// List returns all pages matching filter.
func (s *Store) List(filter Filter) ([]*schema.Page, error) {
	var out []*schema.Page
	err := s.Walk(func(p *schema.Page, _ string) error {
		if matchFilter(p, filter) {
			out = append(out, p)
		}
		return nil
	})
	return out, err
}

// Seams for WalkAll's root resolution; tests use them to stand in for a
// Windows junction.
var (
	walkRootLstat        = os.Lstat
	walkRootEvalSymlinks = filepath.EvalSymlinks
)

// walkRootIsDir reports whether Lstat sees path itself as a directory.
func walkRootIsDir(path string) bool {
	fi, err := walkRootLstat(path)
	return err == nil && fi.IsDir()
}

// WalkedFile is one page file found by WalkAll. Page and ParseErr come from
// schema.ParsePage on Data with a leading UTF-8 BOM stripped; exactly one of
// them is non-nil.
type WalkedFile struct {
	Rel      string
	Data     []byte
	Page     *schema.Page
	ParseErr error
}

// WalkAll calls fn for every *.md file under the store root, including files
// that do not parse, with the same filter as Walk: dot-directories below the
// root and the top-level index.md / tags.md are skipped. A root that is
// itself a link to a directory (a symlink, or a Windows junction) is
// followed; links below it are not.
func (s *Store) WalkAll(fn func(f WalkedFile) error) error {
	root := s.Root
	if fi, err := walkRootLstat(root); err == nil && !fi.IsDir() {
		// A root that links to a directory: a symlink, or a Windows
		// junction, which Lstat reports as neither a directory nor a
		// symlink and EvalSymlinks leaves alone. With a trailing separator
		// Lstat, and so WalkDir, follows either kind of link.
		if resolved, err := walkRootEvalSymlinks(root); err == nil && walkRootIsDir(resolved) {
			root = resolved
		} else if st, err := os.Stat(root); err == nil && st.IsDir() {
			root = filepath.Clean(root) + string(filepath.Separator)
		}
	}
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path != root && errors.Is(err, fs.ErrNotExist) {
				return nil // removed while walking
			}
			return err
		}
		if d.IsDir() {
			// Skip dot-directories (e.g. .migrate-cache holding an external
			// clone, a stray .git, or .sync holding base and conflict copies)
			// so their files never masquerade as store pages and get
			// listed/indexed/pushed. Never skip the store root itself, whose
			// base name may start with '.' (e.g. ".contexo").
			if path != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if rel == "index.md" || rel == "tags.md" {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				// Removed (or a dangling symlink) since the directory was
				// listed: not a page any more.
				return nil
			}
			return fmt.Errorf("pagestore: read %s: %w", rel, err)
		}
		p, parseErr := schema.ParsePage(stripBOM(data))
		return fn(WalkedFile{Rel: rel, Data: data, Page: p, ParseErr: parseErr})
	})
}

// Walk traverses every page in the store and calls fn with the parsed Page
// and its forward-slash relative path. Malformed pages and the top-level
// index.md / tags.md files are skipped silently. It is WalkAll restricted to
// the files that parse, so a page with a leading UTF-8 BOM is visited too.
func (s *Store) Walk(fn func(p *schema.Page, relPath string) error) error {
	return s.WalkAll(func(f WalkedFile) error {
		if f.ParseErr != nil {
			return nil
		}
		return fn(f.Page, f.Rel)
	})
}

// stripBOM drops one leading UTF-8 byte-order mark. It deliberately
// duplicates syncstate.StripBOM, which this package cannot import (syncstate
// imports pagestore).
func stripBOM(b []byte) []byte {
	return bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
}

func matchFilter(p *schema.Page, f Filter) bool {
	if len(f.Types) > 0 {
		ok := false
		for _, t := range f.Types {
			if p.Frontmatter.Type == t {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if len(f.Tags) > 0 {
		ok := false
		for _, want := range f.Tags {
			for _, have := range p.Frontmatter.Tags {
				if have == want {
					ok = true
					break
				}
			}
			if ok {
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}
