package pagestore

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// A frontmatter'd .md hiding inside a dot-directory (e.g. an external clone
// under .contexo/.migrate-cache) must NOT be enumerated as a store page —
// otherwise raw imported content would be listed, indexed, and pushed.
func TestWalkSkipsDotDirs(t *testing.T) {
	root := t.TempDir()
	write := func(rel, slug string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		body := fmt.Sprintf("---\nschema: ctx.page.v1\nslug: %s\ntype: concept\nauthor: x\ncreated: 2026-01-01T00:00:00Z\nupdated: 2026-01-01T00:00:00Z\n---\nbody\n", slug)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("wiki/concepts/real.md", "real")
	write(".migrate-cache/abc/wiki/concepts/phantom.md", "phantom")

	store := &Store{Root: root}
	pages, err := store.List(Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 || pages[0].Frontmatter.Slug != "real" {
		t.Fatalf("want only [real] (dot-dir excluded); got %d pages", len(pages))
	}
}
