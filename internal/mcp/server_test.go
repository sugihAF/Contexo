package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sugihAF/contexo/internal/indexer"
	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/store/pagestore"
)

func setupHub(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	store, err := pagestore.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	pages := []*schema.Page{
		{
			Frontmatter: schema.PageFrontmatter{
				Schema: schema.PageSchemaV1, Slug: "stripe-subscription",
				Type: schema.TypeConcept, Author: "sugihAF", Created: now, Updated: now,
				Tags: []string{"stripe", "billing"}, ReasoningSummary: "Use Billing, not Connect",
			},
			Body: "## Decision\nUse Billing.\n",
		},
		{
			Frontmatter: schema.PageFrontmatter{
				Schema: schema.PageSchemaV1, Slug: "stripe",
				Type: schema.TypeEntity, Author: "sugihAF", Created: now, Updated: now,
				Tags: []string{"stripe"},
			},
			Body: "Payment platform.\n",
		},
		{
			Frontmatter: schema.PageFrontmatter{
				Schema: schema.PageSchemaV1, Slug: "2026-05-14-stripe-research",
				Type: schema.TypeSource, Author: "sugihAF", Created: now, Updated: now,
				Tags: []string{"stripe"},
			},
			Body: "## Context\nResearching Stripe.\n",
		},
	}
	for _, p := range pages {
		if err := store.Write(p); err != nil {
			t.Fatalf("write %s: %v", p.Frontmatter.Slug, err)
		}
	}
	if err := indexer.Generate(store); err != nil {
		t.Fatalf("indexer: %v", err)
	}
	_ = filepath.Join // silence unused
	return NewServer(store)
}

func TestHubReadIndex(t *testing.T) {
	srv := setupHub(t)
	data, mime, err := srv.HandleResourceRead(context.Background(), "ctx://index")
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	if mime != "text/markdown" {
		t.Errorf("mime: %q", mime)
	}
	if !strings.Contains(string(data), "Stripe Subscription") {
		t.Errorf("index missing concept entry: %s", data)
	}
}

func TestHubReadTags(t *testing.T) {
	srv := setupHub(t)
	data, _, err := srv.HandleResourceRead(context.Background(), "ctx://tags")
	if err != nil {
		t.Fatalf("read tags: %v", err)
	}
	if !strings.Contains(string(data), "## stripe") {
		t.Errorf("tags missing: %s", data)
	}
}

func TestHubReadWikiPage(t *testing.T) {
	srv := setupHub(t)
	data, mime, err := srv.HandleResourceRead(context.Background(), "ctx://wiki/stripe-subscription")
	if err != nil {
		t.Fatalf("read concept: %v", err)
	}
	if mime != "text/markdown" {
		t.Errorf("mime: %q", mime)
	}
	body := string(data)
	if !strings.Contains(body, "schema: ctx.page.v1") {
		t.Errorf("missing frontmatter: %s", body)
	}
	if !strings.Contains(body, "Use Billing") {
		t.Errorf("missing body content: %s", body)
	}
}

func TestHubReadEntity(t *testing.T) {
	srv := setupHub(t)
	data, _, err := srv.HandleResourceRead(context.Background(), "ctx://wiki/stripe")
	if err != nil {
		t.Fatalf("read entity: %v", err)
	}
	if !strings.Contains(string(data), "type: entity") {
		t.Errorf("expected entity, got: %s", data)
	}
}

func TestHubReadRawSession(t *testing.T) {
	srv := setupHub(t)
	data, _, err := srv.HandleResourceRead(context.Background(), "ctx://raw/2026-05-14-stripe-research")
	if err != nil {
		t.Fatalf("read raw: %v", err)
	}
	if !strings.Contains(string(data), "type: source") {
		t.Errorf("expected source: %s", data)
	}
}

func TestHubSearchByTag(t *testing.T) {
	srv := setupHub(t)
	data, mime, err := srv.HandleResourceRead(context.Background(), "ctx://search?tag=billing")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if mime != "application/json" {
		t.Errorf("mime: %q", mime)
	}
	var results []searchResult
	if err := json.Unmarshal(data, &results); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(results) != 1 || results[0].Slug != "stripe-subscription" {
		t.Errorf("expected stripe-subscription only, got: %+v", results)
	}
}

func TestHubSearchByQuery(t *testing.T) {
	srv := setupHub(t)
	data, _, err := srv.HandleResourceRead(context.Background(), "ctx://search?q=connect")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	var results []searchResult
	json.Unmarshal(data, &results)
	if len(results) != 1 || results[0].Slug != "stripe-subscription" {
		t.Errorf("query 'connect' should match stripe-subscription reasoning_summary: %+v", results)
	}
}

func TestHubUnknownResource(t *testing.T) {
	srv := setupHub(t)
	_, _, err := srv.HandleResourceRead(context.Background(), "ctx://bogus/whatever")
	if err == nil {
		t.Errorf("expected error for unknown resource")
	}
}

func TestHubMissingPage(t *testing.T) {
	srv := setupHub(t)
	_, _, err := srv.HandleResourceRead(context.Background(), "ctx://wiki/nonexistent")
	if err == nil {
		t.Errorf("expected error for missing page")
	}
}

func TestHubListResources(t *testing.T) {
	srv := setupHub(t)
	templates := srv.ListResources()
	if len(templates) < 5 {
		t.Errorf("expected at least 5 templates, got %d", len(templates))
	}
	found := map[string]bool{}
	for _, tmpl := range templates {
		found[tmpl.URITemplate] = true
	}
	for _, want := range []string{
		"ctx://index", "ctx://tags", "ctx://wiki/{slug}", "ctx://raw/{session-id}",
	} {
		if !found[want] {
			t.Errorf("missing template %q", want)
		}
	}
}
