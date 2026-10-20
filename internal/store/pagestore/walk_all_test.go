package pagestore

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/sugihAF/contexo/internal/schema"
)

var wp04cmpBOM = []byte{0xEF, 0xBB, 0xBF}

// wp04cmpPageBytes returns a valid concept page with the given slug.
func wp04cmpPageBytes(slug string) []byte {
	return []byte("---\nschema: ctx.page.v1\nslug: " + slug + "\ntype: concept\n" +
		"author: x\ncreated: 2026-01-01T00:00:00Z\nupdated: 2026-01-01T00:00:00Z\n---\nbody\n")
}

// wp04cmpTree writes files (rel -> bytes) under a fresh root and returns it.
func wp04cmpTree(t *testing.T, files map[string][]byte) string {
	t.Helper()
	root := t.TempDir()
	for rel, data := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func wp04cmpWithBOM(b []byte) []byte {
	return append(append([]byte{}, wp04cmpBOM...), b...)
}

func TestWalkAllReportsInvalid(t *testing.T) {
	broken := []byte("no frontmatter here\n")
	bom := wp04cmpWithBOM(wp04cmpPageBytes("bom"))
	root := wp04cmpTree(t, map[string][]byte{
		"wiki/concepts/good.md":            wp04cmpPageBytes("good"),
		"wiki/concepts/broken.md":          broken,
		"wiki/concepts/bom.md":             bom,
		"index.md":                         []byte("# Knowledge Index\n"),
		"tags.md":                          []byte("# Tags\n"),
		".sync/base/wiki/concepts/good.md": wp04cmpPageBytes("good"),
		"wiki/concepts/notes.txt":          []byte("text\n"),
		"wiki/concepts/.x.md.tmp-1":        wp04cmpPageBytes("x"),
	})

	got := map[string]WalkedFile{}
	var order []string
	err := (&Store{Root: root}).WalkAll(func(f WalkedFile) error {
		got[f.Rel] = f
		order = append(order, f.Rel)
		return nil
	})
	if err != nil {
		t.Fatalf("WalkAll: %v", err)
	}
	want := []string{"wiki/concepts/bom.md", "wiki/concepts/broken.md", "wiki/concepts/good.md"}
	sorted := append([]string(nil), order...)
	sort.Strings(sorted)
	if len(sorted) != len(want) {
		t.Fatalf("walked %q, want %q", sorted, want)
	}
	for i := range want {
		if sorted[i] != want[i] {
			t.Fatalf("walked %q, want %q", sorted, want)
		}
	}

	for rel, f := range got {
		if (f.Page == nil) == (f.ParseErr == nil) {
			t.Errorf("%s: want exactly one of Page/ParseErr non-nil, got Page=%v ParseErr=%v", rel, f.Page, f.ParseErr)
		}
	}
	b := got["wiki/concepts/broken.md"]
	if b.ParseErr == nil || b.Page != nil {
		t.Errorf("broken: ParseErr=%v Page=%v, want an error and nil Page", b.ParseErr, b.Page)
	}
	if !bytes.Equal(b.Data, broken) {
		t.Errorf("broken: Data = %q, want the raw bytes %q", b.Data, broken)
	}
	g := got["wiki/concepts/good.md"]
	if g.ParseErr != nil || g.Page == nil || g.Page.Frontmatter.Slug != "good" {
		t.Errorf("good: Page=%v ParseErr=%v", g.Page, g.ParseErr)
	}
	m := got["wiki/concepts/bom.md"]
	if m.ParseErr != nil || m.Page == nil || m.Page.Frontmatter.Slug != "bom" {
		t.Errorf("bom: want a parsed page, got Page=%v ParseErr=%v", m.Page, m.ParseErr)
	}
	if !bytes.Equal(m.Data, bom) {
		t.Errorf("bom: Data must keep the BOM verbatim, got %q", m.Data)
	}
}

func TestWalkStillSkipsInvalid(t *testing.T) {
	root := wp04cmpTree(t, map[string][]byte{
		"wiki/concepts/good.md":   wp04cmpPageBytes("good"),
		"wiki/concepts/broken.md": []byte("---\nslug: [\n---\nbody\n"),
		"wiki/concepts/bom.md":    wp04cmpWithBOM(wp04cmpPageBytes("bom")),
		"index.md":                []byte("# Knowledge Index\n"),
	})
	s := &Store{Root: root}

	var rels []string
	err := s.Walk(func(p *schema.Page, rel string) error {
		rels = append(rels, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	sort.Strings(rels)
	if len(rels) != 2 || rels[0] != "wiki/concepts/bom.md" || rels[1] != "wiki/concepts/good.md" {
		t.Errorf("Walk visited %q, want [wiki/concepts/bom.md wiki/concepts/good.md] (BOM page now parses)", rels)
	}

	pages, err := s.List(Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var slugs []string
	for _, p := range pages {
		slugs = append(slugs, p.Frontmatter.Slug)
	}
	sort.Strings(slugs)
	if len(slugs) != 2 || slugs[0] != "bom" || slugs[1] != "good" {
		t.Errorf("List slugs = %q, want [bom good]", slugs)
	}
}

func TestWP04WalkAllPropagatesFnError(t *testing.T) {
	root := wp04cmpTree(t, map[string][]byte{
		"wiki/concepts/a.md": wp04cmpPageBytes("a"),
		"wiki/concepts/b.md": wp04cmpPageBytes("b"),
	})
	stop := errors.New("stop")
	calls := 0
	err := (&Store{Root: root}).WalkAll(func(WalkedFile) error {
		calls++
		return stop
	})
	if !errors.Is(err, stop) || calls != 1 {
		t.Errorf("WalkAll = %v after %d calls, want the fn error after 1 call", err, calls)
	}
}

// A page that vanishes between the directory listing and the read (here a
// dangling symlink) is not a working file: the walk skips it instead of
// failing.
func TestWP04WalkAllSkipsVanishedFile(t *testing.T) {
	root := wp04cmpTree(t, map[string][]byte{
		"wiki/concepts/good.md": wp04cmpPageBytes("good"),
	})
	dangling := filepath.Join(root, "wiki", "concepts", "gone.md")
	if err := os.Symlink(filepath.Join(root, "nowhere.md"), dangling); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	var rels []string
	err := (&Store{Root: root}).WalkAll(func(f WalkedFile) error {
		rels = append(rels, f.Rel)
		return nil
	})
	if err != nil {
		t.Fatalf("WalkAll: %v", err)
	}
	if len(rels) != 1 || rels[0] != "wiki/concepts/good.md" {
		t.Errorf("walked %q, want only wiki/concepts/good.md", rels)
	}
}

func TestWP04WalkAllMissingRoot(t *testing.T) {
	err := (&Store{Root: filepath.Join(t.TempDir(), "missing")}).WalkAll(func(WalkedFile) error {
		return nil
	})
	if err == nil {
		t.Error("WalkAll on a missing root: want error, got nil")
	}
}

// A store root that is itself a symlink (e.g. .contexo linked into another
// checkout) is followed; without that, WalkDir sees one non-directory entry
// and every page would look deleted.
func TestWP04WalkAllFollowsSymlinkedRoot(t *testing.T) {
	realRoot := wp04cmpTree(t, map[string][]byte{
		"wiki/concepts/good.md":            wp04cmpPageBytes("good"),
		".sync/base/wiki/concepts/good.md": wp04cmpPageBytes("good"),
	})
	link := filepath.Join(t.TempDir(), ".contexo")
	if err := os.Symlink(realRoot, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	var rels []string
	err := (&Store{Root: link}).WalkAll(func(f WalkedFile) error {
		rels = append(rels, f.Rel)
		return nil
	})
	if err != nil {
		t.Fatalf("WalkAll: %v", err)
	}
	if len(rels) != 1 || rels[0] != "wiki/concepts/good.md" {
		t.Errorf("walked %q, want only wiki/concepts/good.md", rels)
	}
}
