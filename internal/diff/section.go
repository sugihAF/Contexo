// Package diff produces structured, prose-aware diffs between two versions of
// a Contexo page. Pages are markdown with optional YAML frontmatter; the
// differ splits each version into {frontmatter, preamble, ## sections} and
// reports changes per-field and per-section so prose changes are legible in a
// way `git diff` is not.
package diff

import (
	"bufio"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Section status values used in SectionChange.Status and on Preamble.
const (
	StatusUnchanged = "unchanged"
	StatusAdded     = "added"
	StatusRemoved   = "removed"
	StatusModified  = "modified"
)

// SectionDiff is the structured diff between two page versions. It is the
// single value the differ produces; HTTP, CLI, and MCP each render it
// differently (see format.go).
type SectionDiff struct {
	FromSHA       string          `json:"from_sha"`
	ToSHA         string          `json:"to_sha"`
	Frontmatter   FrontmatterDiff `json:"frontmatter"`
	Preamble      *SectionChange  `json:"preamble,omitempty"`
	Sections      []SectionChange `json:"sections"`
	ParseFallback bool            `json:"parse_fallback,omitempty"`
}

// FrontmatterDiff captures structured changes to YAML frontmatter fields.
// Scalar changes appear in Changed; list-valued fields use set semantics and
// surface their adds/removes in Added/Removed with Field+To or Field+From.
type FrontmatterDiff struct {
	Changed []FrontmatterFieldChange `json:"changed,omitempty"`
	Added   []FrontmatterFieldChange `json:"added,omitempty"`
	Removed []FrontmatterFieldChange `json:"removed,omitempty"`
}

// FrontmatterFieldChange is one entry in a FrontmatterDiff.
type FrontmatterFieldChange struct {
	Field string `json:"field"`
	From  any    `json:"from,omitempty"`
	To    any    `json:"to,omitempty"`
}

// SectionChange is one ## section's diff entry. Status determines which of
// From/To/LineDiff are populated.
type SectionChange struct {
	Heading  string `json:"heading"`
	Status   string `json:"status"`
	From     string `json:"from,omitempty"`
	To       string `json:"to,omitempty"`
	LineDiff string `json:"line_diff,omitempty"`
}

// PageSections computes the structured diff between two page byte slices.
// fromSHA/toSHA are passed through verbatim onto the result; they are not
// inspected. Returns a SectionDiff with ParseFallback=true and a single
// preamble entry when frontmatter is malformed on either side.
func PageSections(from, to []byte, fromSHA, toSHA string) SectionDiff {
	fromFM, fromBody, fromOK := splitFrontmatter(from)
	toFM, toBody, toOK := splitFrontmatter(to)

	if !fromOK || !toOK {
		return SectionDiff{
			FromSHA:       fromSHA,
			ToSHA:         toSHA,
			ParseFallback: true,
			Preamble:      preambleDiff(normalize(string(from)), normalize(string(to))),
		}
	}

	d := SectionDiff{
		FromSHA:     fromSHA,
		ToSHA:       toSHA,
		Frontmatter: diffFrontmatter(fromFM, toFM),
	}

	fromPre, fromSecs := parseSections(fromBody)
	toPre, toSecs := parseSections(toBody)

	if fromPre != "" || toPre != "" {
		d.Preamble = preambleDiff(fromPre, toPre)
	}
	d.Sections = diffSections(fromSecs, toSecs)
	return d
}

// splitFrontmatter returns (frontmatterMap, body, ok). ok=false signals that
// the input does not have a recognizable YAML frontmatter block. Frontmatter
// values are kept as generic Go values so that any field (not just schema
// fields) can be diffed.
//
// An empty input is treated as an empty-but-valid page (zero frontmatter,
// zero body) so that diffing nil/[] against a real page emits clean section
// adds instead of triggering the malformed-frontmatter fallback path.
func splitFrontmatter(data []byte) (map[string]any, string, bool) {
	if len(data) == 0 {
		return map[string]any{}, "", true
	}
	s := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(s, "---\n") {
		return nil, s, false
	}
	rest := s[len("---\n"):]
	end := strings.Index(rest, "\n---\n")
	var yamlBlock, body string
	switch {
	case end >= 0:
		yamlBlock = rest[:end]
		body = rest[end+len("\n---\n"):]
	case strings.HasSuffix(rest, "\n---"):
		yamlBlock = rest[:len(rest)-len("\n---")]
		body = ""
	default:
		return nil, s, false
	}
	fm := map[string]any{}
	if err := yaml.Unmarshal([]byte(yamlBlock), &fm); err != nil {
		return nil, s, false
	}
	return fm, body, true
}

// parseSections splits a body into (preamble, [sections...]). A section starts
// at any line matching `## ` at column 0 and runs until the next such line or
// end of input. Lines before the first `## ` heading form the preamble.
// Trailing blank lines within a section (or the preamble) are stripped so a
// reformatting that adds or removes a separator line between sections doesn't
// register as a content change.
func parseSections(body string) (string, []section) {
	body = normalize(body)
	if body == "" {
		return "", nil
	}
	var preamble strings.Builder
	var sections []section
	var curHeading string
	var curBody strings.Builder
	inSection := false

	closeCurrent := func() {
		if !inSection {
			return
		}
		sections = append(sections, section{
			heading: curHeading,
			body:    strings.TrimRight(curBody.String(), " \t\n"),
		})
		curHeading = ""
		curBody.Reset()
		inSection = false
	}

	scanner := bufio.NewScanner(strings.NewReader(body))
	// Allow long lines (default bufio limit is 64 KB; pages should be much
	// smaller but a single long line shouldn't crash the differ).
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if isH2(line) {
			closeCurrent()
			curHeading = strings.TrimRight(line, " \t")
			inSection = true
			continue
		}
		if inSection {
			if curBody.Len() > 0 {
				curBody.WriteByte('\n')
			}
			curBody.WriteString(line)
		} else {
			if preamble.Len() > 0 {
				preamble.WriteByte('\n')
			}
			preamble.WriteString(line)
		}
	}
	closeCurrent()
	return strings.TrimRight(preamble.String(), " \t\n"), sections
}

type section struct {
	heading string
	body    string
}

func isH2(line string) bool {
	if !strings.HasPrefix(line, "## ") {
		return false
	}
	// Reject `### ` and deeper.
	if strings.HasPrefix(line, "### ") {
		return false
	}
	return true
}

// preambleDiff classifies the preamble change; nil result means both sides
// were empty (caller should not emit a Preamble).
func preambleDiff(from, to string) *SectionChange {
	if from == "" && to == "" {
		return nil
	}
	switch {
	case from == "":
		return &SectionChange{Heading: "_preamble", Status: StatusAdded, To: to}
	case to == "":
		return &SectionChange{Heading: "_preamble", Status: StatusRemoved, From: from}
	case from == to:
		return &SectionChange{Heading: "_preamble", Status: StatusUnchanged}
	default:
		return &SectionChange{
			Heading:  "_preamble",
			Status:   StatusModified,
			From:     from,
			To:       to,
			LineDiff: lineDiff(from, to),
		}
	}
}

// diffFrontmatter compares two parsed frontmatter maps. Lists are treated as
// sets; their order is ignored, and changes surface as Added/Removed entries
// with Field set on each.
func diffFrontmatter(from, to map[string]any) FrontmatterDiff {
	d := FrontmatterDiff{}
	keys := unionKeys(from, to)
	for _, k := range keys {
		fv, fok := from[k]
		tv, tok := to[k]
		switch {
		case !fok && tok:
			d.Added = append(d.Added, FrontmatterFieldChange{Field: k, To: tv})
		case fok && !tok:
			d.Removed = append(d.Removed, FrontmatterFieldChange{Field: k, From: fv})
		default:
			if fl, ok := asStringList(fv); ok {
				if tl, ok := asStringList(tv); ok {
					added, removed := stringSetDiff(fl, tl)
					for _, v := range added {
						d.Added = append(d.Added, FrontmatterFieldChange{Field: k, To: v})
					}
					for _, v := range removed {
						d.Removed = append(d.Removed, FrontmatterFieldChange{Field: k, From: v})
					}
					continue
				}
			}
			if !scalarsEqual(fv, tv) {
				d.Changed = append(d.Changed, FrontmatterFieldChange{Field: k, From: fv, To: tv})
			}
		}
	}
	return d
}

func unionKeys(a, b map[string]any) []string {
	seen := map[string]struct{}{}
	for k := range a {
		seen[k] = struct{}{}
	}
	for k := range b {
		seen[k] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func asStringList(v any) ([]string, bool) {
	switch x := v.(type) {
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			s, ok := e.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	case []string:
		return x, true
	default:
		return nil, false
	}
}

func stringSetDiff(from, to []string) (added, removed []string) {
	fset := map[string]struct{}{}
	for _, v := range from {
		fset[v] = struct{}{}
	}
	tset := map[string]struct{}{}
	for _, v := range to {
		tset[v] = struct{}{}
	}
	for _, v := range to {
		if _, ok := fset[v]; !ok {
			added = append(added, v)
		}
	}
	for _, v := range from {
		if _, ok := tset[v]; !ok {
			removed = append(removed, v)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}

func scalarsEqual(a, b any) bool {
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

// diffSections pairs sections from each side by heading identity. Duplicate
// headings are disambiguated by their occurrence index within their side, so
// the first `## Decision` on the left pairs with the first `## Decision` on
// the right.
func diffSections(from, to []section) []SectionChange {
	type key struct {
		heading string
		ord     int
	}
	indexed := func(sec []section) map[key]section {
		m := map[key]section{}
		counts := map[string]int{}
		for _, s := range sec {
			counts[s.heading]++
			m[key{s.heading, counts[s.heading]}] = s
		}
		return m
	}
	fromMap := indexed(from)

	emitted := map[key]bool{}
	var out []SectionChange

	// Iterate `to` in order first so the output preserves the latest version's
	// section order for the user.
	toCounts := map[string]int{}
	for _, s := range to {
		toCounts[s.heading]++
		k := key{s.heading, toCounts[s.heading]}
		emitted[k] = true
		fs, hasF := fromMap[k]
		if !hasF {
			out = append(out, SectionChange{Heading: s.heading, Status: StatusAdded, To: s.body})
			continue
		}
		if fs.body == s.body {
			out = append(out, SectionChange{Heading: s.heading, Status: StatusUnchanged})
			continue
		}
		out = append(out, SectionChange{
			Heading:  s.heading,
			Status:   StatusModified,
			From:     fs.body,
			To:       s.body,
			LineDiff: lineDiff(fs.body, s.body),
		})
	}

	// Anything in `from` not yet matched is a removal.
	fromCounts := map[string]int{}
	for _, s := range from {
		fromCounts[s.heading]++
		k := key{s.heading, fromCounts[s.heading]}
		if emitted[k] {
			continue
		}
		out = append(out, SectionChange{Heading: s.heading, Status: StatusRemoved, From: s.body})
	}
	return out
}

func normalize(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// lineDiff produces a minimal unified-style line diff. It is not byte-equal to
// `diff -u` output -- it omits hunk headers and file headers -- but is
// stable and human-readable. Implementation is an LCS-based diff that emits
// `+`/`-` lines for differing regions and ` ` for context lines around them.
func lineDiff(from, to string) string {
	if from == to {
		return ""
	}
	a := splitLines(from)
	b := splitLines(to)
	ops := lcsDiff(a, b)
	var sb strings.Builder
	for _, op := range ops {
		switch op.kind {
		case opEqual:
			sb.WriteString("  ")
			sb.WriteString(op.line)
			sb.WriteByte('\n')
		case opDel:
			sb.WriteString("- ")
			sb.WriteString(op.line)
			sb.WriteByte('\n')
		case opIns:
			sb.WriteString("+ ")
			sb.WriteString(op.line)
			sb.WriteByte('\n')
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

type opKind int

const (
	opEqual opKind = iota
	opDel
	opIns
)

type diffOp struct {
	kind opKind
	line string
}

// lcsDiff returns an ordered list of equal/delete/insert ops to turn a -> b,
// using a classic O(n*m) LCS table. Pages are small (1-3 KB) so this is fine.
func lcsDiff(a, b []string) []diffOp {
	n, m := len(a), len(b)
	if n == 0 {
		out := make([]diffOp, 0, m)
		for _, l := range b {
			out = append(out, diffOp{opIns, l})
		}
		return out
	}
	if m == 0 {
		out := make([]diffOp, 0, n)
		for _, l := range a {
			out = append(out, diffOp{opDel, l})
		}
		return out
	}
	table := make([][]int, n+1)
	for i := range table {
		table[i] = make([]int, m+1)
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			if a[i-1] == b[j-1] {
				table[i][j] = table[i-1][j-1] + 1
			} else if table[i-1][j] >= table[i][j-1] {
				table[i][j] = table[i-1][j]
			} else {
				table[i][j] = table[i][j-1]
			}
		}
	}
	var rev []diffOp
	i, j := n, m
	for i > 0 && j > 0 {
		if a[i-1] == b[j-1] {
			rev = append(rev, diffOp{opEqual, a[i-1]})
			i--
			j--
		} else if table[i-1][j] >= table[i][j-1] {
			rev = append(rev, diffOp{opDel, a[i-1]})
			i--
		} else {
			rev = append(rev, diffOp{opIns, b[j-1]})
			j--
		}
	}
	for i > 0 {
		rev = append(rev, diffOp{opDel, a[i-1]})
		i--
	}
	for j > 0 {
		rev = append(rev, diffOp{opIns, b[j-1]})
		j--
	}
	for left, right := 0, len(rev)-1; left < right; left, right = left+1, right-1 {
		rev[left], rev[right] = rev[right], rev[left]
	}
	return rev
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
