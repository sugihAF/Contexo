package pagestore

import (
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
	return schema.ParsePage(data)
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

// Walk traverses every page in the store and calls fn with the parsed Page
// and its forward-slash relative path. Malformed pages and the top-level
// index.md / tags.md files are skipped silently.
func (s *Store) Walk(fn func(p *schema.Page, relPath string) error) error {
	return filepath.WalkDir(s.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}
		rel, _ := filepath.Rel(s.Root, path)
		rel = filepath.ToSlash(rel)
		if rel == "index.md" || rel == "tags.md" {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("pagestore: read %s: %w", rel, err)
		}
		p, parseErr := schema.ParsePage(data)
		if parseErr != nil {
			return nil
		}
		return fn(p, rel)
	})
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
