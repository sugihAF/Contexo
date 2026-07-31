// internal/migrate/catalog.go
package migrate

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sugihAF/contexo/internal/ui"
)

// Catalog is an ordered, numbered set of discovered candidates.
type Catalog struct{ Items []Candidate }

// DefaultDetectors returns the v1 detector set in registration order. Order is
// load-bearing: it decides dedup precedence and render grouping.
func DefaultDetectors() []Detector {
	return []Detector{
		newKBTreeDetector(),
		newDocsDetector(),
		newAgentFilesDetector(),
	}
}

// Build runs each detector over scanDir, dedups by AbsPath (earlier detector
// wins), orders deterministically, assigns 1-based IDs, and flags candidates
// whose SuggestedSlug already exists under contexoDir.
func Build(scanDir, contexoDir string, dets []Detector) (Catalog, error) {
	if info, err := os.Stat(scanDir); err != nil || !info.IsDir() {
		return Catalog{}, fmt.Errorf("migrate: scan dir %q is not a readable directory", scanDir)
	}

	order := map[string]int{}
	for i, d := range dets {
		order[d.Name()] = i
	}

	seen := map[string]bool{}
	var items []Candidate
	for _, d := range dets {
		cs, err := d.Detect(scanDir)
		if err != nil {
			// Degrade: one bad detector must not sink the whole catalog.
			//
			// UNROUTABLE: this goes to the process's real stderr, so
			// cmd.SetErr cannot capture or redirect it. Left as-is
			// deliberately — Build is called from both the CLI and the MCP
			// server, and neither hands it a writer to thread through.
			fmt.Fprintf(os.Stderr, "migrate: detector %s: %v\n", d.Name(), err)
			continue
		}
		for _, c := range cs {
			if seen[c.AbsPath] {
				continue
			}
			seen[c.AbsPath] = true
			items = append(items, c)
		}
	}

	sort.SliceStable(items, func(i, j int) bool {
		oi, oj := order[items[i].Detector], order[items[j].Detector]
		if oi != oj {
			return oi < oj
		}
		return items[i].RelPath < items[j].RelPath
	})

	return NewCatalog(items, contexoDir), nil
}

// NewCatalog finalizes a pre-ordered candidate slice into a Catalog: it assigns
// 1-based IDs in the given order (no reordering) and flags AlreadyImported.
// Build uses it after its own dedup+sort; generate uses it directly on
// adapter-produced candidates.
func NewCatalog(items []Candidate, contexoDir string) Catalog {
	for i := range items {
		items[i].ID = i + 1
		items[i].AlreadyImported = slugExists(contexoDir, items[i].SuggestedSlug)
	}
	return Catalog{Items: items}
}

// Filter keeps items matching typ (SuggestedType) and/or detector name; empty
// strings match everything. IDs are renumbered 1..n so selection stays contiguous.
func (c Catalog) Filter(typ, detector string) Catalog {
	var out []Candidate
	for _, it := range c.Items {
		if typ != "" && string(it.SuggestedType) != typ {
			continue
		}
		if detector != "" && it.Detector != detector {
			continue
		}
		it.ID = len(out) + 1
		out = append(out, it)
	}
	return Catalog{Items: out}
}

// CatalogRenderCap bounds how many rows a rendered catalog / directive shows
// before summarizing the remainder, so a huge KB never floods the terminal or
// the MCP tool output.
const CatalogRenderCap = 100

// RenderOpts controls Render. Max caps how many rows print (0 = all).
type RenderOpts struct{ Max int }

// Render writes the catalog to w.
//
// The signature stays (io.Writer, RenderOpts) on purpose: cli/migrate.go and
// cli/generate.go call it, and catalog_test.go hands it a bare *bytes.Buffer,
// so widening it would break a test in another package. Styling is therefore
// resolved INSIDE, from w, via ui.Plain — which yields the plain text again for
// any non-terminal sink, so the golden test output is unchanged.
//
// Note this is CLI-only. The MCP tool path does NOT come through here; it
// builds its own catalog text in mcp/migrate.go and mcp/generate.go. The two
// renderers are duplicated and drift independently.
func (c Catalog) Render(w io.Writer, opts RenderOpts) {
	if len(c.Items) == 0 {
		fmt.Fprintln(w, "No existing knowledge found to migrate.")
		return
	}
	u := ui.Plain(w)
	limit := len(c.Items)
	if opts.Max > 0 && opts.Max < limit {
		limit = opts.Max
	}
	lastDetector := ""
	for _, it := range c.Items[:limit] {
		if it.Detector != lastDetector {
			fmt.Fprintln(w)
			u.Eyebrow(strings.ToUpper(it.Detector))
			lastDetector = it.Detector
		}
		// Only the two non-data annotations carry tone: the token estimate is
		// dim, and "already imported" is warm because it is the one thing on
		// the row that can cost the user work. The row's literal layout is
		// otherwise byte-for-byte what it was.
		flag := ""
		if it.AlreadyImported {
			flag = u.Style(ui.Warm, "  (already imported — will overwrite)")
		}
		fmt.Fprintf(w, "  [%d] %-8s %s  %s%s\n", it.ID, it.SuggestedType, it.RelPath,
			u.Style(ui.FG4, fmt.Sprintf("~%dt", it.EstTokens)), flag)
	}
	if limit < len(c.Items) {
		fmt.Fprintf(w, "\n%s\n", u.Style(ui.FG3,
			fmt.Sprintf("… %d more; narrow with --type or --detector, or use --all.", len(c.Items)-limit)))
	}
}

// slugExists reports whether slug already has a page under any of the four
// .contexo/ subdirectories.
func slugExists(contexoDir, slug string) bool {
	for _, rel := range []string{
		filepath.Join("wiki", "concepts", slug+".md"),
		filepath.Join("wiki", "entities", slug+".md"),
		filepath.Join("wiki", "analyses", slug+".md"),
		filepath.Join("raw", "sessions", slug+".md"),
	} {
		if fileExists(filepath.Join(contexoDir, rel)) {
			return true
		}
	}
	return false
}
