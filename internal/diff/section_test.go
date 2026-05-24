package diff

import (
	"strings"
	"testing"
)

func TestPageSections_EmptyEqual(t *testing.T) {
	page := mustPage(t, map[string]string{
		"slug": "x", "type": "concept",
	}, "")
	d := PageSections(page, page, "a", "b")
	if d.ParseFallback {
		t.Fatalf("unexpected ParseFallback")
	}
	if len(d.Frontmatter.Changed)+len(d.Frontmatter.Added)+len(d.Frontmatter.Removed) != 0 {
		t.Fatalf("expected no frontmatter changes, got %+v", d.Frontmatter)
	}
	if len(d.Sections) != 0 {
		t.Fatalf("expected no sections, got %d", len(d.Sections))
	}
}

func TestPageSections_FrontmatterScalarChange(t *testing.T) {
	from := mustPage(t, map[string]string{
		"slug": "x", "type": "concept", "reasoning_summary": "old",
	}, "")
	to := mustPage(t, map[string]string{
		"slug": "x", "type": "concept", "reasoning_summary": "new",
	}, "")
	d := PageSections(from, to, "a", "b")
	if len(d.Frontmatter.Changed) != 1 {
		t.Fatalf("want 1 changed scalar, got %d (%+v)", len(d.Frontmatter.Changed), d.Frontmatter)
	}
	c := d.Frontmatter.Changed[0]
	if c.Field != "reasoning_summary" || c.From != "old" || c.To != "new" {
		t.Fatalf("unexpected change: %+v", c)
	}
}

func TestPageSections_FrontmatterListAddRemove(t *testing.T) {
	from := []byte("---\nslug: x\ntype: concept\ntags: [a, b]\n---\n")
	to := []byte("---\nslug: x\ntype: concept\ntags: [b, c]\n---\n")
	d := PageSections(from, to, "f", "t")
	if len(d.Frontmatter.Added) != 1 || d.Frontmatter.Added[0].To != "c" {
		t.Fatalf("want one tag added 'c', got %+v", d.Frontmatter.Added)
	}
	if len(d.Frontmatter.Removed) != 1 || d.Frontmatter.Removed[0].From != "a" {
		t.Fatalf("want one tag removed 'a', got %+v", d.Frontmatter.Removed)
	}
}

func TestPageSections_FrontmatterFieldAdded(t *testing.T) {
	from := []byte("---\nslug: x\ntype: concept\n---\n")
	to := []byte("---\nslug: x\ntype: concept\nreasoning_summary: hi\n---\n")
	d := PageSections(from, to, "f", "t")
	if len(d.Frontmatter.Added) != 1 || d.Frontmatter.Added[0].Field != "reasoning_summary" {
		t.Fatalf("want reasoning_summary added, got %+v", d.Frontmatter.Added)
	}
}

func TestPageSections_SectionAdded(t *testing.T) {
	from := mustPage(t, fmBasic(), "## Decision\nfoo\n")
	to := mustPage(t, fmBasic(), "## Decision\nfoo\n\n## Why\nbar\n")
	d := PageSections(from, to, "f", "t")
	if len(d.Sections) != 2 {
		t.Fatalf("want 2 sections, got %d", len(d.Sections))
	}
	if d.Sections[0].Status != StatusUnchanged || d.Sections[0].Heading != "## Decision" {
		t.Fatalf("section[0]: %+v", d.Sections[0])
	}
	if d.Sections[1].Status != StatusAdded || d.Sections[1].Heading != "## Why" {
		t.Fatalf("section[1]: %+v", d.Sections[1])
	}
}

func TestPageSections_SectionRemoved(t *testing.T) {
	from := mustPage(t, fmBasic(), "## Decision\nfoo\n\n## Old\nbye\n")
	to := mustPage(t, fmBasic(), "## Decision\nfoo\n")
	d := PageSections(from, to, "f", "t")
	if len(d.Sections) != 2 {
		t.Fatalf("want 2 entries, got %d (%+v)", len(d.Sections), d.Sections)
	}
	var removed *SectionChange
	for i := range d.Sections {
		if d.Sections[i].Status == StatusRemoved {
			removed = &d.Sections[i]
		}
	}
	if removed == nil || removed.Heading != "## Old" {
		t.Fatalf("want section ## Old removed, got %+v", d.Sections)
	}
}

func TestPageSections_SectionModified(t *testing.T) {
	from := mustPage(t, fmBasic(), "## Decision\nold body\n")
	to := mustPage(t, fmBasic(), "## Decision\nnew body\n")
	d := PageSections(from, to, "f", "t")
	if len(d.Sections) != 1 || d.Sections[0].Status != StatusModified {
		t.Fatalf("want 1 modified section, got %+v", d.Sections)
	}
	if d.Sections[0].LineDiff == "" {
		t.Fatalf("want non-empty LineDiff")
	}
	if !strings.Contains(d.Sections[0].LineDiff, "- old body") {
		t.Fatalf("LineDiff missing removal: %q", d.Sections[0].LineDiff)
	}
	if !strings.Contains(d.Sections[0].LineDiff, "+ new body") {
		t.Fatalf("LineDiff missing addition: %q", d.Sections[0].LineDiff)
	}
}

func TestPageSections_SectionReorderedSameContent(t *testing.T) {
	from := mustPage(t, fmBasic(), "## A\naaa\n\n## B\nbbb\n")
	to := mustPage(t, fmBasic(), "## B\nbbb\n\n## A\naaa\n")
	d := PageSections(from, to, "f", "t")
	for _, s := range d.Sections {
		if s.Status != StatusUnchanged {
			t.Fatalf("section %q expected unchanged, got %s", s.Heading, s.Status)
		}
	}
}

func TestPageSections_DuplicateHeadings(t *testing.T) {
	from := mustPage(t, fmBasic(), "## Step\nfirst\n\n## Step\nsecond\n")
	to := mustPage(t, fmBasic(), "## Step\nfirst-changed\n\n## Step\nsecond\n")
	d := PageSections(from, to, "f", "t")
	if len(d.Sections) != 2 {
		t.Fatalf("want 2 sections, got %d", len(d.Sections))
	}
	if d.Sections[0].Status != StatusModified {
		t.Fatalf("first ## Step expected modified, got %+v", d.Sections[0])
	}
	if d.Sections[1].Status != StatusUnchanged {
		t.Fatalf("second ## Step expected unchanged, got %+v", d.Sections[1])
	}
}

func TestPageSections_MalformedFrontmatterFallback(t *testing.T) {
	from := []byte("---\nslug: x\nbad: : yaml\n---\nhello\n")
	to := []byte("---\nslug: x\ntype: concept\n---\nhello\n")
	d := PageSections(from, to, "f", "t")
	if !d.ParseFallback {
		t.Fatalf("expected ParseFallback=true")
	}
	if d.Preamble == nil {
		t.Fatalf("expected a preamble entry")
	}
}

func TestPageSections_EmptyFromTreatedAsBlank(t *testing.T) {
	// Empty `from` (e.g. local-vs-server diff when the page is new on the
	// server) should produce clean section adds, not a parse-fallback.
	to := mustPage(t, fmBasic(), "## Decision\nbody\n")
	d := PageSections(nil, to, "", "x")
	if d.ParseFallback {
		t.Errorf("empty `from` must not trigger ParseFallback")
	}
	if len(d.Sections) != 1 || d.Sections[0].Status != StatusAdded {
		t.Errorf("expected 1 added section, got %+v", d.Sections)
	}
	if len(d.Frontmatter.Added) == 0 {
		t.Errorf("expected frontmatter fields to surface as added")
	}
}

func TestPageSections_PreambleModified(t *testing.T) {
	from := mustPage(t, fmBasic(), "intro old\n\n## Decision\nfoo\n")
	to := mustPage(t, fmBasic(), "intro new\n\n## Decision\nfoo\n")
	d := PageSections(from, to, "f", "t")
	if d.Preamble == nil || d.Preamble.Status != StatusModified {
		t.Fatalf("want modified preamble, got %+v", d.Preamble)
	}
}

// --- helpers ---

func fmBasic() map[string]string {
	return map[string]string{"slug": "x", "type": "concept"}
}

func mustPage(t *testing.T, fm map[string]string, body string) []byte {
	t.Helper()
	var sb strings.Builder
	sb.WriteString("---\n")
	for k, v := range fm {
		sb.WriteString(k)
		sb.WriteString(": ")
		sb.WriteString(v)
		sb.WriteString("\n")
	}
	sb.WriteString("---\n")
	sb.WriteString(body)
	return []byte(sb.String())
}
