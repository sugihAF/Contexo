package mcp

import (
	"fmt"
	"strings"
	"time"

	"github.com/sugihAF/contexo/internal/generate"
	"github.com/sugihAF/contexo/internal/migrate"
)

func generateSourceSlug(label, date string) string {
	if strings.TrimSpace(label) == "" {
		return fmt.Sprintf("%s-generate-codebase", date)
	}
	return fmt.Sprintf("%s-generate-%s", date, migrate.Slugify(label))
}

// generateInstructions is the shared block. Unlike migrate's, it stresses
// verifying the auto-extracted scaffold against real source. Both modes end
// with done=true because generate always leaves a cache to clean.
func generateInstructions(srcSlug string, catalogMode bool) string {
	var b strings.Builder
	step := 1
	if catalogMode {
		fmt.Fprintf(&b, "%d. Show the list above to the user and ask which subsystems/components to import (numbers, ranges like 5-7, or 'all').\n", step)
		step++
	}
	fmt.Fprintf(&b, "%d. For EACH selected item: READ the slice file AND the source files it cites (path:line) to VERIFY.\n", step)
	b.WriteString("   The slice is auto-extracted from code — a scaffold, NOT ground truth. Prefer EXTRACTED facts; treat INFERRED\n")
	b.WriteString("   edges as guesses to confirm in the source. Then call ctx_write_page with the best-fit type (concept = a\n")
	b.WriteString("   subsystem, entity = a component), a slug and a NAME YOU choose from the real code (don't blindly trust the\n")
	fmt.Fprintf(&b, "   community label), tags, related links, a one-line reasoning_summary, and sources: [%q]. Write only what the\n", srcSlug)
	b.WriteString("   source confirms; note uncertainty. REDACT any API keys, tokens, passwords, or PII.\n")
	step++
	fmt.Fprintf(&b, "%d. Write the provenance page: ctx_write_page(type=\"source\", slug=%q) noting these pages were generated from\n", step, srcSlug)
	b.WriteString("   the codebase via Graphify on today's date, and which items were imported.\n")
	step++
	fmt.Fprintf(&b, "%d. Push: ctx_push with no_distill=true.\n", step)
	step++
	fmt.Fprintf(&b, "%d. Finally call ctx_generate(done=true) to clean the .generate-cache.\n", step)
	return b.String()
}

func buildGenerateCatalog(cat migrate.Catalog, sourceRoot, srcSlug string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<GENERATE_CATALOG count=%d source=%q>\n\n", len(cat.Items), sourceRoot)
	if len(cat.Items) == 0 {
		b.WriteString("Graphify produced no usable graph for this codebase. Nothing to generate.\n\n</GENERATE_CATALOG>\n")
		return b.String()
	}
	b.WriteString("Seed knowledge auto-extracted from the codebase (scaffolding — read a slice + its cited source before writing):\n")
	for _, it := range cat.Items {
		flag := ""
		if it.AlreadyImported {
			flag = "  (already imported — will overwrite)"
		}
		fmt.Fprintf(&b, "  [%d] %-8s %s  ~%dt%s\n      %s\n", it.ID, it.SuggestedType, it.RelPath, it.EstTokens, flag, it.Preview)
	}
	b.WriteString("\nInstructions:\n")
	b.WriteString(generateInstructions(srcSlug, true))
	b.WriteString("\n</GENERATE_CATALOG>\n")
	return b.String()
}

func buildGenerateResume(m *migrate.Manifest, srcSlug string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<GENERATE_RESUME count=%d source=%q>\n\n", len(m.Items), m.SourceRoot)
	b.WriteString("A CLI generate run was staged. Import exactly these auto-extracted items:\n\n")
	for i, it := range m.Items {
		fmt.Fprintf(&b, "  [%d] %-8s %s\n      (%s)\n", i+1, it.SuggestedType, it.RelPath, it.AbsPath)
	}
	b.WriteString("\nInstructions:\n")
	b.WriteString(generateInstructions(srcSlug, false))
	b.WriteString("\n</GENERATE_RESUME>\n")
	return b.String()
}

// genRunner is the Graphify runner seam (tests override it).
var genRunner generate.Runner = generate.DefaultRunner

// toolGenerate mirrors toolMigrate: done → clear; a staged generate manifest →
// resume; otherwise run Graphify + adapter, stage a generate manifest (so the
// .generate-cache gets cleaned on done), and return the catalog directive.
func (s *Server) toolGenerate(args map[string]interface{}) *ToolResult {
	contexoDir := s.store.Root
	date := time.Now().UTC().Format("2006-01-02")

	if done, _ := args["done"].(bool); done {
		if err := migrate.ClearManifest(contexoDir); err != nil {
			return errorResult("ctx_generate: clear manifest: " + err.Error())
		}
		return textResult("Generation complete; staged manifest + .generate-cache cleared.")
	}

	rescan, _ := args["rescan"].(bool)
	if !rescan {
		if m, _ := migrate.LoadManifest(contexoDir); m != nil && m.Kind == "generate" {
			return textResult(buildGenerateResume(m, generateSourceSlug(m.Label, date)))
		}
	}

	projectRoot := s.rootDir()
	if p, _ := args["path"].(string); p != "" {
		projectRoot = p
	}
	backend, _ := args["backend"].(string)

	cands, cacheDir, err := generate.Discover(projectRoot, contexoDir, backend, genRunner)
	if err != nil {
		return errorResult("ctx_generate: " + err.Error())
	}
	cat := migrate.NewCatalog(cands, contexoDir)
	label := migrate.LabelFor(projectRoot)
	srcSlug := generateSourceSlug(label, date)

	// Stage a generate manifest so ctx_generate(done=true) cleans the cache.
	m := &migrate.Manifest{
		Version: 1, Created: time.Now().UTC().Format(time.RFC3339),
		SourceRoot: projectRoot, ExternalCache: cacheDir, Label: label, Kind: "generate",
	}
	for _, it := range cat.Items {
		m.Items = append(m.Items, migrate.ManifestItem{
			AbsPath: it.AbsPath, RelPath: it.RelPath, Detector: it.Detector,
			SuggestedType: it.SuggestedType, SuggestedSlug: it.SuggestedSlug,
		})
	}
	if err := migrate.SaveManifest(contexoDir, m); err != nil {
		return errorResult("ctx_generate: stage manifest: " + err.Error())
	}
	return textResult(buildGenerateCatalog(cat, projectRoot, srcSlug))
}
