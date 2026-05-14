package schema

import (
	"fmt"
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
