package schema

import (
	"fmt"
	"strings"
	"time"
)

const PageSchemaV1 = "ctx.page.v1"

type PageType string

const (
	TypeConcept  PageType = "concept"
	TypeEntity   PageType = "entity"
	TypeSource   PageType = "source"
	TypeAnalysis PageType = "analysis"
)

type Page struct {
	Frontmatter PageFrontmatter
	Body        string
}

type PageFrontmatter struct {
	Schema           string    `yaml:"schema"`
	Slug             string    `yaml:"slug"`
	Type             PageType  `yaml:"type"`
	Author           string    `yaml:"author"`
	Agent            string    `yaml:"agent,omitempty"`
	Created          time.Time `yaml:"created"`
	Updated          time.Time `yaml:"updated"`
	ParentSHA        string    `yaml:"parent_sha"`
	Sources          []string  `yaml:"sources,omitempty"`
	Related          []string  `yaml:"related,omitempty"`
	Tags             []string  `yaml:"tags,omitempty"`
	ReasoningSummary string    `yaml:"reasoning_summary,omitempty"`
}

func (fm *PageFrontmatter) Validate() error {
	if fm.Schema != PageSchemaV1 {
		return fmt.Errorf("page: invalid schema %q, expected %q", fm.Schema, PageSchemaV1)
	}
	if fm.Slug == "" {
		return fmt.Errorf("page: slug required")
	}
	if !validSlug(fm.Slug) {
		return fmt.Errorf("page: invalid slug %q (use letters, digits, '-', '_', '.'; no '/', '\\', '..', or leading '-'/'.')", fm.Slug)
	}
	switch fm.Type {
	case TypeConcept, TypeEntity, TypeSource, TypeAnalysis:
	case "":
		return fmt.Errorf("page: type required")
	default:
		return fmt.Errorf("page: invalid type %q (want concept|entity|source|analysis)", fm.Type)
	}
	if fm.Author == "" {
		return fmt.Errorf("page: author required")
	}
	if fm.Created.IsZero() {
		return fmt.Errorf("page: created timestamp required")
	}
	return nil
}

// validSlug reports whether a page slug is safe to embed in a filesystem path.
// The slug becomes part of RelPath() ("wiki/concepts/<slug>.md"), so it must be
// a single clean path component: no separators, no "..", no leading dash/dot,
// no control characters. This stops a crafted ctx_write_page slug from escaping
// the local .contexo store and from constructing a traversal push path.
func validSlug(s string) bool {
	if s == "" || len(s) > 200 {
		return false
	}
	if s[0] == '-' || s[0] == '.' {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return !strings.Contains(s, "..")
}

// RelPath returns the path under .contexo/ where a page of this type and slug lives.
func (fm *PageFrontmatter) RelPath() string {
	switch fm.Type {
	case TypeSource:
		return "raw/sessions/" + fm.Slug + ".md"
	case TypeConcept:
		return "wiki/concepts/" + fm.Slug + ".md"
	case TypeEntity:
		return "wiki/entities/" + fm.Slug + ".md"
	case TypeAnalysis:
		return "wiki/analyses/" + fm.Slug + ".md"
	default:
		return "wiki/" + fm.Slug + ".md"
	}
}
