package cli

import (
	"bufio"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/generate"
	"github.com/sugihAF/contexo/internal/migrate"
)

// genRunner is the Graphify runner seam (tests override it).
var genRunner generate.Runner = generate.DefaultRunner

func newGenerateCmd() *cobra.Command {
	var (
		from    string
		backend string
		list    bool
		typ     string
		all     bool
		yes     bool
	)
	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Seed a Contexo knowledge base from the codebase (via Graphify)",
		Long: "For a project with no docs: runs Graphify to build a code graph, turns its subsystems and key " +
			"components into a pick-list, and stages your selection. Your coding agent then reads each slice + the " +
			"cited source and writes verified Contexo pages. Requires Graphify (`uv tool install graphifyy`).",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGenerate(cmd, from, backend, typ, list, all, yes)
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "codebase directory to analyze (default: this project)")
	cmd.Flags().StringVar(&backend, "backend", "", "optional Graphify LLM backend for richer subsystem names (gemini|claude|ollama|...)")
	cmd.Flags().BoolVar(&list, "list", false, "only list what would be generated; stage nothing")
	cmd.Flags().StringVar(&typ, "type", "", "only items of this suggested type (concept|entity|analysis)")
	cmd.Flags().BoolVar(&all, "all", false, "select every item without prompting")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

func runGenerate(cmd *cobra.Command, from, backend, typ string, list, all, yes bool) error {
	out := cmd.OutOrStdout()
	root := GetRootDir()
	contexoDir := config.ContexoDirPath(root)
	if !isDirCLI(contexoDir) {
		return fmt.Errorf("generate: no .contexo/ here — run 'ctx init' first")
	}
	scanDir := root
	if from != "" {
		scanDir = from
	}

	cands, cacheDir, err := generate.Discover(scanDir, contexoDir, backend, genRunner)
	if err != nil {
		return err
	}
	cat := migrate.NewCatalog(cands, contexoDir).Filter(typ, "")
	cat.Render(out, migrate.RenderOpts{Max: migrate.CatalogRenderCap})
	if len(cat.Items) == 0 || list {
		return nil
	}

	reader := bufio.NewReader(cmd.InOrStdin())
	var picks []int
	if all {
		picks, _ = parseSelection("all", len(cat.Items))
	} else {
		fmt.Fprint(out, "\nSelect items to import (e.g. 1,2,5-7 / all): ")
		line, _ := reader.ReadString('\n')
		picks, err = parseSelection(line, len(cat.Items))
		if err != nil {
			return fmt.Errorf("generate: %w", err)
		}
	}
	if !yes {
		fmt.Fprintf(out, "Stage %d item(s) for the agent to import? [y/N]: ", len(picks))
		ans, _ := reader.ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(ans)); a != "y" && a != "yes" {
			fmt.Fprintln(out, "Aborted; nothing staged.")
			return nil
		}
	}

	byID := map[int]migrate.Candidate{}
	for _, it := range cat.Items {
		byID[it.ID] = it
	}
	m := &migrate.Manifest{
		Version: 1, Created: time.Now().UTC().Format(time.RFC3339),
		SourceRoot: scanDir, ExternalCache: cacheDir, Label: migrate.LabelFor(scanDir), Kind: "generate",
	}
	for _, id := range picks {
		it := byID[id]
		m.Items = append(m.Items, migrate.ManifestItem{
			AbsPath: it.AbsPath, RelPath: it.RelPath, Detector: it.Detector,
			SuggestedType: it.SuggestedType, SuggestedSlug: it.SuggestedSlug,
		})
	}
	if err := migrate.SaveManifest(contexoDir, m); err != nil {
		return err
	}
	fmt.Fprintf(out, "\nStaged %d item(s). Ask your agent: \"finish the contexo generation\".\n", len(m.Items))
	return nil
}
