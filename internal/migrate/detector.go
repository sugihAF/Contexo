// internal/migrate/detector.go
package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/sugihAF/contexo/internal/schema"
)

// Candidate is one discovered knowledge item. Detectors fill everything
// except ID (assigned by Catalog.Build after ordering) and AlreadyImported
// (set by Build against the local .contexo/ store).
type Candidate struct {
	ID              int
	Detector        string
	SourceRoot      string
	AbsPath         string
	RelPath         string
	Title           string
	Preview         string
	EstTokens       int
	SuggestedType   schema.PageType
	SuggestedSlug   string
	AlreadyImported bool
}

// Detector recognizes a knowledge format under root and enumerates its items.
// A Detector that does not recognize root returns (nil, nil), not an error.
type Detector interface {
	Name() string
	Detect(root string) ([]Candidate, error)
}

const previewMax = 200

// mkCandidate reads absPath and builds a Candidate with derived
// Title/Preview/EstTokens/SuggestedSlug. RelPath is relative to sourceRoot.
func mkCandidate(detector, sourceRoot, absPath string, typ schema.PageType) (Candidate, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return Candidate{}, err
	}
	md := string(data)
	rel, err := filepath.Rel(sourceRoot, absPath)
	if err != nil {
		rel = filepath.Base(absPath)
	}
	title := firstHeading(md)
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(absPath), filepath.Ext(absPath))
	}
	return Candidate{
		Detector:      detector,
		SourceRoot:    sourceRoot,
		AbsPath:       absPath,
		RelPath:       rel,
		Title:         title,
		Preview:       makePreview(md, previewMax),
		EstTokens:     (len(data) + 3) / 4,
		SuggestedType: typ,
		SuggestedSlug: slugify(title),
	}, nil
}

// firstHeading returns the text of the first Markdown ATX heading, or "".
// It strips frontmatter first so a YAML '#' comment is never mistaken for a title.
func firstHeading(md string) string {
	for _, line := range strings.Split(stripFrontmatter(md), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "#") {
			return strings.TrimSpace(strings.TrimLeft(t, "#"))
		}
	}
	return ""
}

// stripFrontmatter drops a leading '---\n ... \n---\n' YAML block.
func stripFrontmatter(md string) string {
	md = strings.ReplaceAll(md, "\r\n", "\n")
	if !strings.HasPrefix(md, "---\n") {
		return md
	}
	if i := strings.Index(md[4:], "\n---\n"); i >= 0 {
		return md[4+i+len("\n---\n"):]
	}
	return md
}

// makePreview returns the first non-empty, non-heading paragraph, collapsed
// to one line and truncated to max runes (with an ellipsis when cut).
func makePreview(md string, max int) string {
	body := stripFrontmatter(md)
	var para []string
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		if len(para) == 0 {
			// Hunting for the paragraph start: skip blanks and headings.
			if t == "" || strings.HasPrefix(t, "#") {
				continue
			}
			para = append(para, t)
			continue
		}
		// Inside the paragraph: a blank line or heading ends it.
		if t == "" || strings.HasPrefix(t, "#") {
			break
		}
		para = append(para, t)
	}
	joined := strings.Join(strings.Fields(strings.Join(para, " ")), " ")
	r := []rune(joined)
	if len(r) > max {
		if max > 1 {
			return string(r[:max-1]) + "…"
		}
		return string(r[:max])
	}
	return joined
}

// slugify produces a validSlug-safe kebab-case identifier: it emits only
// [a-z0-9] and '-', so it always satisfies schema.validSlug.
func slugify(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r) && r < 128, unicode.IsDigit(r) && r < 128:
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 80 {
		out = strings.Trim(out[:80], "-")
	}
	if out == "" {
		return "untitled"
	}
	return out
}

// Slugify is the exported wrapper for slugify.
func Slugify(s string) string { return slugify(s) }

// agentInstructionFiles are root files owned by the agentfiles detector. The
// docs detector skips them so they are never misclassified as generic docs.
var agentInstructionFiles = []string{"CLAUDE.md", "AGENTS.md", "GEMINI.md"}

func isAgentInstructionFile(name string) bool {
	for _, f := range agentInstructionFiles {
		if strings.EqualFold(name, f) {
			return true
		}
	}
	return false
}
