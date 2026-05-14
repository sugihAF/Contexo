package schema

import (
	"strings"
	"testing"
	"time"
)

func TestPageRoundtrip(t *testing.T) {
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	original := &Page{
		Frontmatter: PageFrontmatter{
			Schema:           PageSchemaV1,
			Slug:             "stripe-subscription",
			Type:             TypeConcept,
			Author:           "sugihAF",
			Agent:            "claude-opus-4-7",
			Created:          now,
			Updated:          now,
			ParentSHA:        "",
			Sources:          []string{"2026-05-14-stripe-research"},
			Related:          []string{"chompchat-saas-subscription"},
			Tags:             []string{"stripe", "billing"},
			ReasoningSummary: "Rejected Connect; chose Billing + metered",
		},
		Body: "## Decision\nStripe Billing.\n\n## Agent Reasoning\n- Considered Connect, rejected.\n",
	}

	data, err := SerializePage(original)
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}

	parsed, err := ParsePage(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if parsed.Frontmatter.Slug != original.Frontmatter.Slug {
		t.Errorf("slug mismatch: got %q, want %q", parsed.Frontmatter.Slug, original.Frontmatter.Slug)
	}
	if parsed.Frontmatter.Type != original.Frontmatter.Type {
		t.Errorf("type mismatch: got %q, want %q", parsed.Frontmatter.Type, original.Frontmatter.Type)
	}
	if parsed.Frontmatter.Author != original.Frontmatter.Author {
		t.Errorf("author mismatch")
	}
	if !parsed.Frontmatter.Created.Equal(original.Frontmatter.Created) {
		t.Errorf("created mismatch: got %v, want %v", parsed.Frontmatter.Created, original.Frontmatter.Created)
	}
	if len(parsed.Frontmatter.Tags) != 2 || parsed.Frontmatter.Tags[0] != "stripe" {
		t.Errorf("tags mismatch: %v", parsed.Frontmatter.Tags)
	}
	if parsed.Body != original.Body {
		t.Errorf("body mismatch:\ngot:  %q\nwant: %q", parsed.Body, original.Body)
	}
	if err := parsed.Frontmatter.Validate(); err != nil {
		t.Errorf("validate after roundtrip: %v", err)
	}
}

func TestParseHandlesCRLF(t *testing.T) {
	src := "---\r\nschema: ctx.page.v1\r\nslug: x\r\ntype: concept\r\nauthor: me\r\ncreated: 2026-01-01T00:00:00Z\r\nupdated: 2026-01-01T00:00:00Z\r\nparent_sha: \"\"\r\n---\r\nbody here\r\n"
	p, err := ParsePage([]byte(src))
	if err != nil {
		t.Fatalf("parse CRLF: %v", err)
	}
	if p.Frontmatter.Slug != "x" {
		t.Errorf("slug: %q", p.Frontmatter.Slug)
	}
	if !strings.Contains(p.Body, "body here") {
		t.Errorf("body lost: %q", p.Body)
	}
}

func TestValidateRequiredFields(t *testing.T) {
	cases := []struct {
		name string
		fm   PageFrontmatter
		ok   bool
	}{
		{"missing schema", PageFrontmatter{Slug: "x", Type: TypeConcept, Author: "a", Created: time.Now()}, false},
		{"wrong schema", PageFrontmatter{Schema: "ctx.page.v2", Slug: "x", Type: TypeConcept, Author: "a", Created: time.Now()}, false},
		{"missing slug", PageFrontmatter{Schema: PageSchemaV1, Type: TypeConcept, Author: "a", Created: time.Now()}, false},
		{"missing type", PageFrontmatter{Schema: PageSchemaV1, Slug: "x", Author: "a", Created: time.Now()}, false},
		{"invalid type", PageFrontmatter{Schema: PageSchemaV1, Slug: "x", Type: "weird", Author: "a", Created: time.Now()}, false},
		{"missing author", PageFrontmatter{Schema: PageSchemaV1, Slug: "x", Type: TypeConcept, Created: time.Now()}, false},
		{"missing created", PageFrontmatter{Schema: PageSchemaV1, Slug: "x", Type: TypeConcept, Author: "a"}, false},
		{"valid", PageFrontmatter{Schema: PageSchemaV1, Slug: "x", Type: TypeConcept, Author: "a", Created: time.Now()}, true},
	}
	for _, c := range cases {
		err := c.fm.Validate()
		if c.ok && err != nil {
			t.Errorf("%s: unexpected error: %v", c.name, err)
		}
		if !c.ok && err == nil {
			t.Errorf("%s: expected error, got nil", c.name)
		}
	}
}

func TestRelPath(t *testing.T) {
	cases := []struct {
		typ  PageType
		slug string
		want string
	}{
		{TypeConcept, "stripe-subscription", "wiki/concepts/stripe-subscription.md"},
		{TypeEntity, "stripe", "wiki/entities/stripe.md"},
		{TypeAnalysis, "billing-comparison", "wiki/analyses/billing-comparison.md"},
		{TypeSource, "2026-05-14-stripe", "raw/sessions/2026-05-14-stripe.md"},
	}
	for _, c := range cases {
		fm := PageFrontmatter{Type: c.typ, Slug: c.slug}
		if got := fm.RelPath(); got != c.want {
			t.Errorf("type=%s slug=%s: got %q, want %q", c.typ, c.slug, got, c.want)
		}
	}
}
