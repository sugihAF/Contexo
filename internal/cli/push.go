package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/syncops"
	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/ui"
)

func init() {
	registerCapability(contract.CapPushJSON)
	registerCapability(contract.CapPushPaths)
	registerCapability(contract.CapPushDistillGuard)
}

// pushFlags are the human-mode presentation flags of `ctx push`.
type pushFlags struct {
	dryRun, yes, showDiff, noPreview bool
}

// newPushCmd is `ctx push [paths...]`: a thin layer over syncops.Push. With
// --json it writes the ctx.push.v1 document (also on a conflict, exit 6) and
// never prompts; without it the batch is previewed and confirmed through
// PushOptions.Confirm and failures exit 1.
func newPushCmd() *cobra.Command {
	var (
		featureFilter  string
		tagFilter      string
		typeFilter     string
		message        string
		fallbackServer bool
		noDistill      bool
		asJSON         bool
		fl             pushFlags
	)
	cmd := &cobra.Command{
		Use:   "push [paths...]",
		Short: "Push changed local .contexo pages to the server",
		Long: "Push sends the pages you changed or created since the last sync (all of them, or only the named paths).\n" +
			"A page a teammate changed in the meantime is never overwritten: it is reported as a conflict to resolve with\n" +
			"'ctx resolve <path> --ours|--theirs'.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if fallbackServer {
				return fmt.Errorf("push: --fallback-server: server-side distillation not implemented yet (planned for Phase 4 of agent-reasoning-capture); use the agent-as-distiller flow via ctx_push MCP for now")
			}
			root := GetRootDir()
			if _, err := syncops.RequireHub(root); err != nil {
				return err
			}
			// Returned unreplaced: the *config.RemoteError stays in the chain
			// and nothing has been sent yet.
			rr, client, err := openRemote(root, "push", true)
			if err != nil {
				return err
			}
			var author syncops.Author
			if rr.Creds != nil {
				author = syncops.Author{Name: rr.Creds.UserName, Email: rr.Creds.UserEmail}
			}
			env := syncops.NewEnv(root, rr.RepoID, rr.ServerURL, author, client)
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			opts := syncops.PushOptions{
				Paths:     args,
				Message:   message,
				DryRun:    fl.dryRun,
				NoDistill: noDistill,
				Feature:   featureFilter,
				Tag:       tagFilter,
				Type:      typeFilter,
			}
			if asJSON {
				return runPushJSON(ctx, cmd, env, opts)
			}
			return runPushHuman(ctx, cmd, env, client, opts, fl)
		},
	}

	cmd.Flags().StringVar(&featureFilter, "feature", "", "push only pages tagged with this feature/tag")
	cmd.Flags().StringVar(&tagFilter, "tag", "", "push only pages with this tag (alias of --feature)")
	cmd.Flags().StringVar(&typeFilter, "type", "", "push only pages of this type (concept|entity|source|analysis)")
	cmd.Flags().StringVarP(&message, "message", "m", "", "commit message (default: 'ctx push (N pages)')")
	cmd.Flags().BoolVar(&fl.dryRun, "dry-run", false, "show what would be pushed without sending or writing anything")
	cmd.Flags().BoolVar(&fallbackServer, "fallback-server", false, "(planned) route reasoning-trail distillation to the Contexo server; currently errors")
	cmd.Flags().BoolVarP(&fl.yes, "yes", "y", false, "skip the pre-push confirmation prompt (required for non-interactive use when editing existing pages)")
	cmd.Flags().BoolVar(&fl.showDiff, "show-diff", false, "print the full per-section diff in the preview, not just a one-line summary")
	cmd.Flags().BoolVar(&fl.noPreview, "no-preview", false, "skip the pre-push preview entirely (faster; loses the heads-up about what changes)")
	cmd.Flags().BoolVar(&noDistill, "no-distill", false, "push even when an un-distilled agent session is pending (bypasses the reasoning-trail guard; use deliberately)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the ctx.push.v1 JSON document; never prompts or previews (implies --yes and --no-preview)")
	return cmd
}

// runPushJSON is the machine flow: no Confirm, so no prompt and no preview
// requests. The document is written whenever the engine returns one (on
// exit 6 too); the error goes back unchanged for Execute's envelope.
func runPushJSON(ctx context.Context, cmd *cobra.Command, env syncops.Env, opts syncops.PushOptions) error {
	doc, err := syncops.Push(ctx, env, opts)
	if doc != nil {
		if werr := contract.WriteDoc(cmd.OutOrStdout(), doc); werr != nil && err == nil {
			return fmt.Errorf("push: write document: %w", werr)
		}
	}
	return err
}

// runPushHuman previews and confirms the batch under the lock (through
// Confirm, unless --no-preview), then reports what happened. Failures exit
// 1 with their advice; nothing here prints JSON.
func runPushHuman(ctx context.Context, cmd *cobra.Command, env syncops.Env, client *sync.Client, opts syncops.PushOptions, fl pushFlags) error {
	if !fl.noPreview {
		opts.Confirm = pushConfirm(cmd.OutOrStdout(), os.Stdin, stdinIsTTY, client, env.RepoID, fl)
	}
	doc, err := syncops.Push(ctx, env, opts)
	return pushFinishHuman(cmd, doc, err, fl)
}

// pushConfirm returns the human Confirm hook: it previews the plan and, when
// the batch edits existing pages and neither --dry-run nor --yes was given,
// asks on a TTY or refuses without one.
func pushConfirm(out io.Writer, in io.Reader, isTTY func() bool, client *sync.Client, repoID string, fl pushFlags) func([]syncops.PushPlanFile) (bool, error) {
	return func(plan []syncops.PushPlanFile) (bool, error) {
		previews := computePushPreview(client, repoID, plan)
		hasEdits := renderPreview(out, repoID, previews, fl.showDiff)
		if fl.dryRun || !hasEdits || fl.yes {
			return true, nil
		}
		if !isTTY() {
			return false, ui.Suggest(errors.New("push: refusing to alter existing pages in non-interactive mode; pass --yes to confirm"), "ctx push --yes")
		}
		return confirm(in, out, "Proceed?")
	}
}

// pushFinishHuman reports a human push from the engine's result and returns
// the error Execute renders (exit 1). A declined prompt is not a failure.
func pushFinishHuman(cmd *cobra.Command, doc *contract.PushDoc, err error, fl pushFlags) error {
	if errors.Is(err, syncops.ErrPushDeclined) {
		fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")
		return nil
	}
	if doc != nil {
		pushReportHuman(cmd.OutOrStdout(), cmd.ErrOrStderr(), doc, fl)
	}
	if err == nil {
		return nil
	}

	var ce *contract.Error
	if !errors.As(err, &ce) {
		return err
	}
	switch ce.Code {
	case contract.CodeConflict:
		if doc != nil {
			pushReportConflicts(cmd.OutOrStderr(), doc.Conflicts)
		}
		// RenderError prints advice only from a *ui.CLIError.
		return ui.Suggest(err, ce.Advice...)
	case contract.CodeDistillRequired:
		if prompt := pushAgentPrompt(ce.Details); prompt != "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "Share via your agent: %s\n", prompt)
		}
		fmt.Fprintln(cmd.ErrOrStderr(), "ctx push --no-distill bypasses this guard; use it only deliberately.")
	}
	return err
}

// pushReportHuman prints the outcome of a push (or dry run) that returned a
// document.
func pushReportHuman(out, errOut io.Writer, doc *contract.PushDoc, fl pushFlags) {
	for _, w := range doc.Warnings {
		fmt.Fprintf(errOut, "warning: %s\n", w.Message)
	}
	switch {
	case len(doc.Pushed) == 0 && len(doc.Conflicts) == 0:
		fmt.Fprintln(out, "Nothing to push.")
	case doc.DryRun && fl.noPreview:
		fmt.Fprintf(out, "Would push %d page(s):\n", len(doc.Pushed))
		for _, p := range doc.Pushed {
			fmt.Fprintf(out, "  %s  (%s)\n", p.Path, p.Kind)
		}
	case doc.DryRun:
		fmt.Fprintln(out, "(dry-run; no changes sent)")
	default:
		fmt.Fprintf(out, "Pushed %d page(s); HEAD=%s\n", len(doc.Pushed), shortSHA(doc.NewHead))
	}
	if len(doc.Skipped) > 0 {
		fmt.Fprintf(out, "Skipped %d page(s):\n", len(doc.Skipped))
		for _, s := range doc.Skipped {
			fmt.Fprintf(out, "  %s  (%s)\n", s.Path, s.Reason)
		}
	}
}

// pushReportConflicts lists the pages the server refused, each with the
// teammate's sha and the base the local edit started from.
func pushReportConflicts(w io.Writer, conflicts []contract.PushConflict) {
	fmt.Fprintf(w, "%d conflict(s):\n", len(conflicts))
	for _, c := range conflicts {
		base := "none"
		if c.BaseSHA != "" {
			base = shortSHA(c.BaseSHA)
		}
		fmt.Fprintf(w, "  %s: server=%s base=%s\n", c.Path, shortSHA(c.ServerSHA), base)
	}
}

// pushAgentPrompt returns the distill_required details' agent prompt, or ""
// when the details are missing.
func pushAgentPrompt(details any) string {
	switch d := details.(type) {
	case *contract.DistillRequiredDetails:
		if d != nil {
			return d.AgentPrompt
		}
	case contract.DistillRequiredDetails:
		return d.AgentPrompt
	}
	return ""
}

func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
