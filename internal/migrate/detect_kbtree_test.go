// internal/migrate/detect_kbtree_test.go
package migrate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sugihAF/contexo/internal/schema"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestKBTreeDetectorLLMWiki(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "wiki", "entities", "stripe.md"), "# Stripe\n\nBilling.")
	writeFile(t, filepath.Join(root, "wiki", "concepts", "auth.md"), "# Auth\n\nHow login works.")
	writeFile(t, filepath.Join(root, "raw", "sessions", "2026-01-01-x.md"), "# Session\n\nnotes")

	got, err := newKBTreeDetector().Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	byType := map[string]schema.PageType{}
	for _, c := range got {
		byType[filepath.Base(c.AbsPath)] = c.SuggestedType
	}
	if byType["stripe.md"] != schema.TypeEntity {
		t.Errorf("stripe should be entity, got %q", byType["stripe.md"])
	}
	if byType["auth.md"] != schema.TypeConcept {
		t.Errorf("auth should be concept, got %q", byType["auth.md"])
	}
	if byType["2026-01-01-x.md"] != schema.TypeSource {
		t.Errorf("raw session should be source, got %q", byType["2026-01-01-x.md"])
	}
}

func TestKBTreeDetectorObsidian(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".obsidian", "config"), "{}")
	writeFile(t, filepath.Join(root, "Note A.md"), "# A\n\ntext")
	writeFile(t, filepath.Join(root, "folder", "Note B.md"), "# B\n\ntext")

	got, err := newKBTreeDetector().Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 obsidian notes, got %d", len(got))
	}
	for _, c := range got {
		if c.SuggestedType != schema.TypeConcept {
			t.Errorf("obsidian note should be concept, got %q", c.SuggestedType)
		}
	}
}

func TestKBTreeDetectorNotAKB(t *testing.T) {
	got, err := newKBTreeDetector().Detect(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("empty dir should yield 0 candidates, got %d", len(got))
	}
}
