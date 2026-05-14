package indexer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/store/pagestore"
)

func TestGenerateIndexAndTags(t *testing.T) {
	dir := t.TempDir()
	store, err := pagestore.Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	pages := []*schema.Page{
		{
			Frontmatter: schema.PageFrontmatter{
				Schema: schema.PageSchemaV1, Slug: "stripe-subscription",
				Type: schema.TypeConcept, Author: "sugihAF", Created: now, Updated: now,
				Tags: []string{"stripe", "billing"}, ReasoningSummary: "Rejected Connect; chose Billing",
			},
			Body: "## Decision\nUse Billing.\n",
		},
		{
			Frontmatter: schema.PageFrontmatter{
				Schema: schema.PageSchemaV1, Slug: "stripe",
				Type: schema.TypeEntity, Author: "sugihAF", Created: now, Updated: now,
				Tags: []string{"stripe"},
			},
			Body: "Stripe is a payment platform.\n",
		},
		{
			Frontmatter: schema.PageFrontmatter{
				Schema: schema.PageSchemaV1, Slug: "2026-05-14-stripe-research",
				Type: schema.TypeSource, Author: "sugihAF", Created: now, Updated: now,
				Tags: []string{"stripe", "research"},
			},
			Body: "## Context\nResearching Stripe billing options.\n",
		},
	}
	for _, p := range pages {
		if err := store.Write(p); err != nil {
			t.Fatalf("write %s: %v", p.Frontmatter.Slug, err)
		}
	}

	if err := Generate(store); err != nil {
		t.Fatalf("generate: %v", err)
	}

	idx, _ := os.ReadFile(filepath.Join(dir, "index.md"))
	idxStr := string(idx)

	for _, want := range []string{
		"# Knowledge Index",
		"## Concepts",
		"Stripe Subscription",
		"Rejected Connect; chose Billing",
		"tags: stripe,billing",
		"sugihAF 2026-05-14",
		"## Entities",
		"## Analyses",
		"(none yet)",
		"## Sources",
		"2026-05-14-stripe-research",
	} {
		if !strings.Contains(idxStr, want) {
			t.Errorf("index missing %q in:\n%s", want, idxStr)
		}
	}

	tags, _ := os.ReadFile(filepath.Join(dir, "tags.md"))
	tagsStr := string(tags)

	for _, want := range []string{
		"## stripe",
		"## billing",
		"## research",
		"[stripe-subscription]",
		"[stripe]",
	} {
		if !strings.Contains(tagsStr, want) {
			t.Errorf("tags missing %q in:\n%s", want, tagsStr)
		}
	}
}

func TestEmptyStoreGeneratesEmptySections(t *testing.T) {
	dir := t.TempDir()
	store, _ := pagestore.Open(dir)
	if err := Generate(store); err != nil {
		t.Fatalf("generate empty: %v", err)
	}

	idx, _ := os.ReadFile(filepath.Join(dir, "index.md"))
	if !strings.Contains(string(idx), "(none yet)") {
		t.Errorf("expected (none yet) markers in empty index")
	}

	tags, _ := os.ReadFile(filepath.Join(dir, "tags.md"))
	if !strings.Contains(string(tags), "(none yet)") {
		t.Errorf("expected (none yet) in empty tags")
	}
}

func TestTitleFromSlug(t *testing.T) {
	cases := map[string]string{
		"stripe-subscription":           "Stripe Subscription",
		"telnyx-voice-ai-architecture":  "Telnyx Voice Ai Architecture",
		"single":                        "Single",
		"":                              "",
	}
	for input, want := range cases {
		if got := titleFromSlug(input); got != want {
			t.Errorf("titleFromSlug(%q): got %q, want %q", input, got, want)
		}
	}
}
