package schema

import (
	"testing"
	"time"
)

// TestValidateRejectsUnsafeSlug locks in the slug guard that stops a crafted
// ctx_write_page slug from escaping the local .contexo store or building a
// traversal push path (RelPath embeds the slug into the file path).
func TestValidateRejectsUnsafeSlug(t *testing.T) {
	base := func(slug string) *PageFrontmatter {
		return &PageFrontmatter{
			Schema:  PageSchemaV1,
			Slug:    slug,
			Type:    TypeConcept,
			Author:  "a",
			Created: time.Now().UTC(),
		}
	}
	bad := []string{
		"../../etc/passwd",
		"a/b",
		`a\b`,
		"..",
		"-rf",
		".hidden",
		"foo/../bar",
		"a b",   // space
		"slug!", // punctuation
	}
	for _, slug := range bad {
		if err := base(slug).Validate(); err == nil {
			t.Errorf("Validate() slug=%q = nil, want error", slug)
		}
	}
	good := []string{"contexo", "2026-06-13-topic", "my_page.v2", "a-b-c"}
	for _, slug := range good {
		if err := base(slug).Validate(); err != nil {
			t.Errorf("Validate() slug=%q = %v, want nil", slug, err)
		}
	}
}
