// internal/migrate/detector_test.go
package migrate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sugihAF/contexo/internal/schema"
)

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Stripe Subscription":    "stripe-subscription",
		"auth_flow.md":           "auth-flow-md",
		"  --Leading/Trailing--": "leading-trailing",
		"Café déjà":              "caf-d-j",
		"":                       "untitled",
		"...":                    "untitled",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFirstHeadingAndPreview(t *testing.T) {
	md := "---\ntitle: X\n---\n\n# Real Title\n\nFirst paragraph here.\n\nSecond."
	if got := firstHeading(md); got != "Real Title" {
		t.Errorf("firstHeading = %q", got)
	}
	if got := makePreview(md, 200); got != "First paragraph here." {
		t.Errorf("makePreview = %q", got)
	}
	if got := makePreview("no heading, just text that is fairly long", 10); got != "no headin…" {
		t.Errorf("makePreview truncate = %q", got)
	}
	if got := makePreview("# H\n\nline one\nline two\n\nnext", 200); got != "line one line two" {
		t.Errorf("multi-line paragraph = %q", got)
	}
}

func TestMkCandidate(t *testing.T) {
	dir := t.TempDir()
	abs := filepath.Join(dir, "sub", "Stripe Notes.md")
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte("# Stripe\n\nHow billing works."), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := mkCandidate("docs", dir, abs, schema.TypeConcept)
	if err != nil {
		t.Fatalf("mkCandidate: %v", err)
	}
	if c.RelPath != filepath.FromSlash("sub/Stripe Notes.md") {
		t.Errorf("RelPath = %q", c.RelPath)
	}
	if c.Title != "Stripe" || c.SuggestedSlug != "stripe" || c.SuggestedType != schema.TypeConcept {
		t.Errorf("bad candidate: %+v", c)
	}
	if c.Preview != "How billing works." || c.EstTokens <= 0 {
		t.Errorf("bad preview/tokens: %+v", c)
	}
}
