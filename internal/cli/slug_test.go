package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mkPage(t *testing.T, root, rel string) {
	t.Helper()
	abs := filepath.Join(root, ".contexo", filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveSlug_SingleMatch(t *testing.T) {
	root := t.TempDir()
	mkPage(t, root, "wiki/concepts/stripe.md")
	got, err := resolveSlugPath(root, "stripe", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "wiki/concepts/stripe.md" {
		t.Errorf("got %q", got)
	}
}

func TestResolveSlug_NotFound(t *testing.T) {
	root := t.TempDir()
	_, err := resolveSlugPath(root, "missing", "")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

func TestResolveSlug_Ambiguous(t *testing.T) {
	root := t.TempDir()
	mkPage(t, root, "wiki/concepts/stripe.md")
	mkPage(t, root, "wiki/entities/stripe.md")
	_, err := resolveSlugPath(root, "stripe", "")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguous error, got %v", err)
	}
	// Pass --type to disambiguate.
	got, err := resolveSlugPath(root, "stripe", "entity")
	if err != nil {
		t.Fatalf("resolve with type: %v", err)
	}
	if got != "wiki/entities/stripe.md" {
		t.Errorf("got %q", got)
	}
}

func TestResolveSlug_AllFourTypes(t *testing.T) {
	cases := map[string]string{
		"wiki/concepts/c.md":  "concept",
		"wiki/entities/e.md":  "entity",
		"wiki/analyses/a.md":  "analysis",
		"raw/sessions/s.md":   "source",
	}
	for rel, typ := range cases {
		root := t.TempDir()
		mkPage(t, root, rel)
		slug := strings.TrimSuffix(filepath.Base(rel), ".md")
		got, err := resolveSlugPath(root, slug, "")
		if err != nil {
			t.Errorf("[type=%s] resolve: %v", typ, err)
			continue
		}
		if got != rel {
			t.Errorf("[type=%s] got %q want %q", typ, got, rel)
		}
	}
}
