package cli

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/syncops"
	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/ui"
)

func init() {
	registerCapability(contract.CapRepair)
}

// repairEngine is the engine behind `ctx repair`. It is a variable so tests
// can drive the command (flags, env, output) without a server.
var repairEngine = syncops.Repair

// repairBudgetSkip is the skip reason a plain re-run makes progress on: the
// per-invocation request budget ran out before the path was tried.
const repairBudgetSkip = "budget_exhausted"

// newRepairCmd builds `ctx repair [--dry-run] [--json]` (E11/E12/E18).
func newRepairCmd() *cobra.Command {
	var dryRun, asJSON bool
	cmd := &cobra.Command{
		Use:   "repair",
		Short: "Backfill missing base snapshots and fix wrong page shas",
		Long: "Repair finds every synced page whose base snapshot under .contexo/.sync is missing or\n" +
			"no longer matches, fetches the base from the server and records it. It also fixes page\n" +
			"shas that older servers or ctx builds recorded wrongly (the repo HEAD instead of the\n" +
			"page's own commit). Working files are never changed. Pages it cannot verify are\n" +
			"reported as skipped; with --dry-run it makes the same reads and writes nothing.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			err := repairRun(cmd, dryRun, asJSON)
			if err != nil && !asJSON {
				return repairHumanError(err)
			}
			return err
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report what would be repaired; write nothing")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the ctx.repair.v1 JSON document")
	return cmd
}

// repairRun resolves the project and its remote, runs the engine and prints
// the result. Skipped pages are not a failure: they exit 0 with a
// base_repair_skipped warning in the document.
func repairRun(cmd *cobra.Command, dryRun, asJSON bool) error {
	root := GetRootDir()
	if _, err := syncops.RequireHub(root); err != nil {
		return err
	}
	rr, client, err := openRemote(root, "repair", true)
	if err != nil {
		return err
	}
	// Repair never commits, so it has no author.
	env := syncops.NewEnv(root, rr.RepoID, rr.ServerURL, syncops.Author{}, client)
	res, err := repairEngine(cmd.Context(), env, syncops.RepairOptions{DryRun: dryRun})
	if err != nil {
		return err
	}
	if res == nil {
		res = syncops.NewRepairResult(dryRun)
	}
	if asJSON {
		return contract.WriteDoc(cmd.OutOrStdout(), res)
	}
	repairPrintSummary(cmd, res)
	return nil
}

// repairHumanError hands a contract error's advice to RenderError, which
// prints advice only from a *ui.CLIError. The --json envelope reads code and
// advice from the chain already, so only human mode needs this.
func repairHumanError(err error) error {
	var cli *ui.CLIError
	var ce *contract.Error
	if errors.As(err, &cli) || !errors.As(err, &ce) {
		return err
	}
	return ui.Suggest(err, ce.Advice...)
}

// repairPrintSummary prints the counts, each repaired path with its reason,
// and the skips grouped by reason.
func repairPrintSummary(cmd *cobra.Command, res *syncops.RepairResult) {
	u := ui.From(cmd)
	out := cmd.OutOrStdout()
	nb, nr, ns := len(res.Backfilled), len(res.Repaired), len(res.Skipped)
	if nb+nr+ns == 0 {
		u.Success("Nothing to repair: every synced page has a valid base")
		return
	}

	summary := fmt.Sprintf("Base repair: %d backfilled, %d repaired, %d skipped", nb, nr, ns)
	if res.DryRun {
		summary = fmt.Sprintf("Dry run: would backfill %d, repair %d, skip %d (nothing written)", nb, nr, ns)
	}
	if nb+nr == 0 {
		u.Warn("%s", summary)
	} else {
		u.Success("%s", summary)
	}
	for _, r := range res.Repaired {
		fmt.Fprintf(out, "    %s   %s → %s   %s\n", r.Path, shortSHA(r.OldSHA), shortSHA(r.NewSHA), r.Reason)
	}
	if ns == 0 {
		return
	}

	counts := map[string]int{}
	for _, s := range res.Skipped {
		counts[s.Reason]++
	}
	reasons := make([]string, 0, len(counts))
	for r := range counts {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	groups := make([]string, len(reasons))
	for i, r := range reasons {
		groups[i] = fmt.Sprintf("%d %s", counts[r], r)
	}
	u.Warn("%d page(s) left unverified: %s", ns, strings.Join(groups, ", "))
	if counts[repairBudgetSkip] > 0 {
		u.Hint("run again", "ctx repair")
	}
}
