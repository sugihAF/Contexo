package diff

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ToJSON marshals d for transport over HTTP or MCP. Indent is two spaces.
func (d SectionDiff) ToJSON() ([]byte, error) {
	return json.MarshalIndent(d, "", "  ")
}

// ToText renders d as a terminal-friendly summary. Designed for `ctx diff`
// output: leads with the SHA range, then frontmatter changes, then section
// changes. Modified sections show a count of changed lines rather than the
// full body to keep typical output short; users can pass --json for the raw
// data.
func (d SectionDiff) ToText(slug string) string {
	var sb strings.Builder

	if slug != "" {
		fmt.Fprintf(&sb, "diff: %s   %s..%s\n", slug, shortSHA(d.FromSHA), shortSHA(d.ToSHA))
	} else {
		fmt.Fprintf(&sb, "diff: %s..%s\n", shortSHA(d.FromSHA), shortSHA(d.ToSHA))
	}

	if d.ParseFallback {
		sb.WriteString("\n(frontmatter unparseable on at least one side; whole-file line diff below)\n")
		if d.Preamble != nil {
			writePreamble(&sb, d.Preamble)
		}
		return sb.String()
	}

	if hasFrontmatterChanges(d.Frontmatter) {
		sb.WriteString("\nFrontmatter\n")
		writeFrontmatter(&sb, d.Frontmatter)
	}

	if d.Preamble != nil && d.Preamble.Status != StatusUnchanged {
		sb.WriteString("\nPreamble\n")
		writePreamble(&sb, d.Preamble)
	}

	if len(d.Sections) > 0 {
		sb.WriteString("\nSections\n")
		for _, s := range d.Sections {
			writeSectionSummary(&sb, s)
		}
	}

	return sb.String()
}

func hasFrontmatterChanges(fm FrontmatterDiff) bool {
	return len(fm.Changed)+len(fm.Added)+len(fm.Removed) > 0
}

func writeFrontmatter(sb *strings.Builder, fm FrontmatterDiff) {
	changed := append([]FrontmatterFieldChange{}, fm.Changed...)
	sort.Slice(changed, func(i, j int) bool { return changed[i].Field < changed[j].Field })
	for _, c := range changed {
		fmt.Fprintf(sb, "  ~ %s\n", c.Field)
		fmt.Fprintf(sb, "      - %v\n", c.From)
		fmt.Fprintf(sb, "      + %v\n", c.To)
	}

	added := append([]FrontmatterFieldChange{}, fm.Added...)
	sort.Slice(added, func(i, j int) bool {
		if added[i].Field != added[j].Field {
			return added[i].Field < added[j].Field
		}
		return fmt.Sprintf("%v", added[i].To) < fmt.Sprintf("%v", added[j].To)
	})
	for _, a := range added {
		fmt.Fprintf(sb, "  + %s: %v\n", a.Field, a.To)
	}

	removed := append([]FrontmatterFieldChange{}, fm.Removed...)
	sort.Slice(removed, func(i, j int) bool {
		if removed[i].Field != removed[j].Field {
			return removed[i].Field < removed[j].Field
		}
		return fmt.Sprintf("%v", removed[i].From) < fmt.Sprintf("%v", removed[j].From)
	})
	for _, r := range removed {
		fmt.Fprintf(sb, "  - %s: %v\n", r.Field, r.From)
	}
}

func writeSectionSummary(sb *strings.Builder, s SectionChange) {
	switch s.Status {
	case StatusUnchanged:
		fmt.Fprintf(sb, "  = %s\n", s.Heading)
	case StatusAdded:
		fmt.Fprintf(sb, "  + %s (%d lines added)\n", s.Heading, countLines(s.To))
	case StatusRemoved:
		fmt.Fprintf(sb, "  - %s (%d lines removed)\n", s.Heading, countLines(s.From))
	case StatusModified:
		n := countChangedLines(s.LineDiff)
		fmt.Fprintf(sb, "  ~ %s (%d line%s changed)\n", s.Heading, n, pluralS(n))
	case StatusRenamed:
		if s.LineDiff != "" {
			n := countChangedLines(s.LineDiff)
			fmt.Fprintf(sb, "  ~> %s  (renamed from %q, %d line%s changed)\n",
				s.Heading, s.OldHeading, n, pluralS(n))
		} else {
			fmt.Fprintf(sb, "  ~> %s  (renamed from %q)\n", s.Heading, s.OldHeading)
		}
	}
}

func writePreamble(sb *strings.Builder, p *SectionChange) {
	switch p.Status {
	case StatusAdded:
		fmt.Fprintf(sb, "  + preamble (%d lines added)\n", countLines(p.To))
	case StatusRemoved:
		fmt.Fprintf(sb, "  - preamble (%d lines removed)\n", countLines(p.From))
	case StatusModified:
		fmt.Fprintf(sb, "  ~ preamble (%d line%s changed)\n", countChangedLines(p.LineDiff), pluralS(countChangedLines(p.LineDiff)))
	}
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// countChangedLines counts the +/- markers in a lineDiff output. Each marker
// is the first character of a line, followed by a space, then the line text.
func countChangedLines(diffText string) int {
	if diffText == "" {
		return 0
	}
	n := 0
	for _, line := range strings.Split(diffText, "\n") {
		if strings.HasPrefix(line, "+ ") || strings.HasPrefix(line, "- ") {
			n++
		}
	}
	return n
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func shortSHA(s string) string {
	if len(s) < 7 {
		return s
	}
	return s[:7]
}
