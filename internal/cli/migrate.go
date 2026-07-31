package cli

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/migrate"
	"github.com/sugihAF/contexo/internal/ui"
)

// parseSelection turns "1,2,5-7" / "all" / "*" into sorted unique 1-based
// indices in [1, max]. Errors on empty, malformed, or out-of-range input.
func parseSelection(input string, max int) ([]int, error) {
	s := strings.TrimSpace(strings.ToLower(input))
	if s == "" {
		return nil, fmt.Errorf("empty selection")
	}
	if s == "all" || s == "*" {
		out := make([]int, max)
		for i := range out {
			out[i] = i + 1
		}
		return out, nil
	}
	set := map[int]bool{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if lo, hi, ok := strings.Cut(part, "-"); ok {
			a, err1 := strconv.Atoi(strings.TrimSpace(lo))
			b, err2 := strconv.Atoi(strings.TrimSpace(hi))
			if err1 != nil || err2 != nil || a > b {
				return nil, fmt.Errorf("bad range %q", part)
			}
			for i := a; i <= b; i++ {
				if i < 1 || i > max {
					return nil, fmt.Errorf("index %d out of range 1..%d", i, max)
				}
				set[i] = true
			}
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("bad index %q", part)
		}
		if n < 1 || n > max {
			return nil, fmt.Errorf("index %d out of range 1..%d", n, max)
		}
		set[n] = true
	}
	if len(set) == 0 {
		return nil, fmt.Errorf("empty selection")
	}
	out := make([]int, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Ints(out)
	return out, nil
}

func newMigrateCmd() *cobra.Command {
	var (
		from     string
		list     bool
		typ      string
		detector string
		all      bool
		yes      bool
	)
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Bulk-import an existing knowledge base into Contexo",
		Long: "Scans this project (or --from a path/git URL) for knowledge you already have — " +
			"llm-wiki trees, docs/, ADRs, README, CLAUDE.md/AGENTS.md, Cursor rules — lists it, and " +
			"stages the items you pick. Distillation is done by your coding agent: after staging, ask " +
			"it to 'finish the contexo migration' and it will reformat each pick into a Contexo page.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigrate(cmd, from, typ, detector, list, all, yes)
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "import from an external path or git URL instead of this project")
	cmd.Flags().BoolVar(&list, "list", false, "only list what would be migrated; stage nothing")
	cmd.Flags().StringVar(&typ, "type", "", "only items of this suggested type (concept|entity|analysis|source)")
	cmd.Flags().StringVar(&detector, "detector", "", "only items from this detector (kbtree|docs|agentfiles)")
	cmd.Flags().BoolVar(&all, "all", false, "select every item without prompting")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

func runMigrate(cmd *cobra.Command, from, typ, detector string, list, all, yes bool) error {
	out := cmd.OutOrStdout()
	root := GetRootDir()
	contexoDir := config.ContexoDirPath(root)
	if !isDirCLI(contexoDir) {
		return fmt.Errorf("migrate: no .contexo/ here — run 'ctx init' first")
	}

	// ONE UI for the whole run; the Prompter below hangs off it.
	u := ui.From(cmd)

	scanDir := root
	var cacheDir string
	if from != "" {
		// --from a git URL clones, which is a network round trip with no
		// output of its own.
		t := u.Task("Fetching " + from)
		sd, cd, err := migrate.ResolveExternal(from, contexoDir, nil)
		if err != nil {
			t.Fail("Could not fetch %s", from)
			return err
		}
		t.Done("Fetched %s", from)
		scanDir, cacheDir = sd, cd
	}

	scan := u.Task("Scanning for existing knowledge")
	cat, err := migrate.Build(scanDir, contexoDir, migrate.DefaultDetectors())
	if err != nil {
		scan.Fail("Scan failed")
		return err
	}
	cat = cat.Filter(typ, detector)
	scan.Done("Found %d item(s)", len(cat.Items))
	cat.Render(out, migrate.RenderOpts{Max: migrate.CatalogRenderCap})
	if len(cat.Items) == 0 || list {
		return nil
	}

	// ONE Prompter serves both the selection line and the y/N confirm. Two
	// readers over the same stdin would let the first buffer past its newline
	// and swallow the confirmation — a real bug in interactive use and in the
	// test (stdin "all\ny\n"). Prompter owns that invariant now.
	p := u.Prompter(cmd.InOrStdin())
	var picks []int
	if all {
		picks, _ = parseSelection("all", len(cat.Items))
	} else {
		line, _ := p.Line("\nSelect items to import (e.g. 1,2,5-7 / all)")
		picks, err = parseSelection(line, len(cat.Items))
		if err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}

	if !yes {
		ok, _ := p.Confirm(fmt.Sprintf("Stage %d item(s) for the agent to import?", len(picks)))
		if !ok {
			fmt.Fprintln(out, "Aborted; nothing staged.")
			return nil
		}
	}

	byID := map[int]migrate.Candidate{}
	for _, it := range cat.Items {
		byID[it.ID] = it
	}
	m := &migrate.Manifest{
		Version:       1,
		Created:       time.Now().UTC().Format(time.RFC3339),
		SourceRoot:    scanDir,
		ExternalCache: cacheDir,
		Label:         migrate.LabelFor(scanDir),
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

	fmt.Fprintln(out)
	u.Success("Staged %d source(s). Ask your agent: \"finish the contexo migration\".", len(m.Items))
	return nil
}

func isDirCLI(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
