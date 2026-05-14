package pagestore

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sugihAF/contexo/internal/schema"
)

func newPage(t *testing.T, typ schema.PageType, slug string, tags []string) *schema.Page {
	t.Helper()
	now := time.Now().UTC()
	return &schema.Page{
		Frontmatter: schema.PageFrontmatter{
			Schema:  schema.PageSchemaV1,
			Slug:    slug,
			Type:    typ,
			Author:  "tester",
			Created: now,
			Updated: now,
			Tags:    tags,
		},
		Body: "## Body for " + slug + "\n",
	}
}

func TestWriteThenReadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	p := newPage(t, schema.TypeConcept, "stripe-subscription", []string{"stripe", "billing"})
	if err := s.Write(p); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := s.Read("wiki/concepts/stripe-subscription.md")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Frontmatter.Slug != "stripe-subscription" {
		t.Errorf("slug: %q", got.Frontmatter.Slug)
	}
}

func TestFindBySlugAcrossTypes(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)

	if err := s.Write(newPage(t, schema.TypeEntity, "stripe", nil)); err != nil {
		t.Fatalf("write entity: %v", err)
	}
	if err := s.Write(newPage(t, schema.TypeConcept, "stripe-billing", nil)); err != nil {
		t.Fatalf("write concept: %v", err)
	}

	p, err := s.FindBySlug("stripe")
	if err != nil {
		t.Fatalf("find stripe: %v", err)
	}
	if p.Frontmatter.Type != schema.TypeEntity {
		t.Errorf("expected entity, got %s", p.Frontmatter.Type)
	}

	_, err = s.FindBySlug("nonexistent")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestListFilter(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)

	pages := []*schema.Page{
		newPage(t, schema.TypeConcept, "a", []string{"stripe", "billing"}),
		newPage(t, schema.TypeConcept, "b", []string{"telnyx"}),
		newPage(t, schema.TypeEntity, "c", []string{"stripe"}),
		newPage(t, schema.TypeSource, "d", []string{"stripe"}),
	}
	for _, p := range pages {
		if err := s.Write(p); err != nil {
			t.Fatalf("write %s: %v", p.Frontmatter.Slug, err)
		}
	}

	// All
	all, err := s.List(Filter{})
	if err != nil || len(all) != 4 {
		t.Errorf("list all: count=%d err=%v", len(all), err)
	}

	// By type
	concepts, _ := s.List(Filter{Types: []schema.PageType{schema.TypeConcept}})
	if len(concepts) != 2 {
		t.Errorf("concepts count=%d", len(concepts))
	}

	// By tag
	stripePages, _ := s.List(Filter{Tags: []string{"stripe"}})
	if len(stripePages) != 3 {
		t.Errorf("stripe-tagged count=%d", len(stripePages))
	}

	// By type AND tag
	stripeConcepts, _ := s.List(Filter{
		Types: []schema.PageType{schema.TypeConcept},
		Tags:  []string{"stripe"},
	})
	if len(stripeConcepts) != 1 || stripeConcepts[0].Frontmatter.Slug != "a" {
		t.Errorf("stripe concepts: %+v", stripeConcepts)
	}
}

func TestWalkSkipsIndexAndTags(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)

	if err := s.Write(newPage(t, schema.TypeConcept, "a", nil)); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Write dummy index.md and tags.md at root
	for _, name := range []string{"index.md", "tags.md"} {
		f := filepath.Join(dir, name)
		if err := writeFile(f, "# placeholder\n"); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	count := 0
	err := s.Walk(func(p *schema.Page, _ string) error {
		count++
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if count != 1 {
		t.Errorf("walk count: got %d, want 1", count)
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}
