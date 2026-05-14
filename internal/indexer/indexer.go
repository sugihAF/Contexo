package indexer

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/store/pagestore"
)

// Generate rebuilds index.md and tags.md at the root of the store from the
// current page contents.
func Generate(store *pagestore.Store) error {
	pages, err := store.List(pagestore.Filter{})
	if err != nil {
		return fmt.Errorf("indexer: list pages: %w", err)
	}

	if err := os.WriteFile(filepath.Join(store.Root, "index.md"), buildIndex(pages), 0o644); err != nil {
		return fmt.Errorf("indexer: write index.md: %w", err)
	}
	if err := os.WriteFile(filepath.Join(store.Root, "tags.md"), buildTags(pages), 0o644); err != nil {
		return fmt.Errorf("indexer: write tags.md: %w", err)
	}
	return nil
}

func buildIndex(pages []*schema.Page) []byte {
	var buf bytes.Buffer
	buf.WriteString("# Knowledge Index\n\n")
	buf.WriteString("Always-loaded index for this project's Contexo knowledge base. ")
	buf.WriteString("Find what's relevant here, then read individual pages on demand.\n\n")

	sections := []struct {
		title string
		typ   schema.PageType
	}{
		{"Concepts", schema.TypeConcept},
		{"Entities", schema.TypeEntity},
		{"Analyses", schema.TypeAnalysis},
		{"Sources", schema.TypeSource},
	}

	for _, sec := range sections {
		buf.WriteString("## " + sec.title + "\n")
		matching := pagesOfType(pages, sec.typ)
		if len(matching) == 0 {
			buf.WriteString("(none yet)\n\n")
			continue
		}
		sort.Slice(matching, func(i, j int) bool {
			return matching[i].Frontmatter.Slug < matching[j].Frontmatter.Slug
		})
		for _, p := range matching {
			buf.WriteString(formatEntry(p))
		}
		buf.WriteString("\n")
	}

	return buf.Bytes()
}

func pagesOfType(pages []*schema.Page, t schema.PageType) []*schema.Page {
	var out []*schema.Page
	for _, p := range pages {
		if p.Frontmatter.Type == t {
			out = append(out, p)
		}
	}
	return out
}

func formatEntry(p *schema.Page) string {
	fm := p.Frontmatter
	desc := fm.ReasoningSummary
	if desc == "" {
		desc = firstSentence(p.Body)
	}
	if desc == "" {
		desc = "(no summary)"
	}

	tags := "—"
	if len(fm.Tags) > 0 {
		tags = strings.Join(fm.Tags, ",")
	}

	return fmt.Sprintf("- [%s](%s) — %s | tags: %s | %s %s\n",
		titleFromSlug(fm.Slug),
		fm.RelPath(),
		desc,
		tags,
		fm.Author,
		fm.Updated.Format("2006-01-02"),
	)
}

func titleFromSlug(s string) string {
	parts := strings.Split(s, "-")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

// firstSentence returns up to the first sentence of the body, skipping leading
// markdown headers. Returns "" if no usable text is found.
func firstSentence(body string) string {
	body = strings.TrimSpace(body)
	for strings.HasPrefix(body, "#") {
		idx := strings.Index(body, "\n")
		if idx < 0 {
			return ""
		}
		body = strings.TrimSpace(body[idx+1:])
	}
	for _, term := range []string{". ", "\n\n", "\n"} {
		if idx := strings.Index(body, term); idx > 0 {
			return strings.TrimSpace(body[:idx])
		}
	}
	if len(body) > 80 {
		return body[:80] + "..."
	}
	return body
}

func buildTags(pages []*schema.Page) []byte {
	tagMap := make(map[string][]*schema.Page)
	for _, p := range pages {
		for _, t := range p.Frontmatter.Tags {
			tagMap[t] = append(tagMap[t], p)
		}
	}

	var buf bytes.Buffer
	buf.WriteString("# Tags\n\n")

	if len(tagMap) == 0 {
		buf.WriteString("(none yet)\n")
		return buf.Bytes()
	}

	tagNames := make([]string, 0, len(tagMap))
	for t := range tagMap {
		tagNames = append(tagNames, t)
	}
	sort.Strings(tagNames)

	for _, t := range tagNames {
		buf.WriteString("## " + t + "\n")
		pgs := tagMap[t]
		sort.Slice(pgs, func(i, j int) bool {
			return pgs[i].Frontmatter.Slug < pgs[j].Frontmatter.Slug
		})
		for _, p := range pgs {
			buf.WriteString(fmt.Sprintf("- [%s](%s)\n", p.Frontmatter.Slug, p.Frontmatter.RelPath()))
		}
		buf.WriteString("\n")
	}
	return buf.Bytes()
}
