package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/syncops"
	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/ui"
)

// Owner: WP-1.3.

func init() { registerCapability(contract.CapPullJSON) }

// newPullCmd is `ctx pull [--json] [--full]`: the safe pull. The work is
// syncops.Pull; this command resolves the remote, runs it and prints the
// ctx.pull.v1 document (--json) or a human summary.
func newPullCmd() *cobra.Command {
	var asJSON, full bool
	cmd := &cobra.Command{
		Use:   "pull",
		Short: "Safely pull teammates' page changes into .contexo/",
		Long: "ctx pull brings the server's page changes into .contexo/ without ever " +
			"overwriting a local edit. Pages you have not touched are updated, new " +
			"server pages are created, and pages whose content already matches the " +
			"server are adopted as is.\n\n" +
			"A page with local edits, a conflict, an invalid or locally deleted page, " +
			"or a page that changed while the pull ran is left alone and listed as " +
			"skipped, with the command that resolves it. Skips are normal results: " +
			"the exit code is still 0.\n\n" +
			"--json prints the ctx.pull.v1 document on stdout and nothing else.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()
			// R11: validate the hub before resolving the remote, so an
			// uninitialised root reports not_initialized whatever its
			// credentials say.
			if _, err := syncops.RequireHub(root); err != nil {
				return pullError(err, asJSON)
			}
			// openRemote's error is returned unchanged: it already carries
			// its advice and keeps the *config.RemoteError reachable.
			rr, client, err := openRemote(root, "pull", true)
			if err != nil {
				return err
			}
			env := syncops.NewEnv(root, rr.RepoID, rr.ServerURL, syncops.Author{}, client)

			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			doc, err := syncops.Pull(ctx, env, syncops.PullOptions{})
			if err != nil {
				return pullError(err, asJSON)
			}
			if rr.Mismatch {
				w := contract.Warning{
					Code: contract.WarnServerURLMismatch,
					Message: fmt.Sprintf("config server_url %s differs from the credentials' server %s; using %s",
						rr.ConfiguredServerURL, rr.ServerURL, rr.ServerURL),
					Paths: []string{},
				}
				doc.Warnings = append([]contract.Warning{w}, doc.Warnings...)
			}

			out := cmd.OutOrStdout()
			if asJSON {
				return contract.WriteDoc(out, doc)
			}
			pullPrintHuman(out, doc, full)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the ctx.pull.v1 JSON document")
	cmd.Flags().BoolVar(&full, "full", false, "accepted for compatibility; pull always reconciles every page")
	return cmd
}

// pullError prepares a failure for Execute. In human mode a *contract.Error
// that carries advice is wrapped with ui.Suggest, because the human renderer
// prints only a *ui.CLIError's advice (R11). In --json mode the error is
// returned unchanged: the envelope reads the contract advice itself.
func pullError(err error, asJSON bool) error {
	if asJSON {
		return err
	}
	var ce *contract.Error
	if errors.As(err, &ce) && len(ce.Advice) > 0 {
		return ui.Suggest(err, ce.Advice...)
	}
	return err
}

// pullPrintHuman writes the human summary of doc to w: the --full note,
// the summary line, one line per skipped page with the command that
// resolves it, then the warnings.
func pullPrintHuman(w io.Writer, doc *contract.PullDoc, full bool) {
	if full {
		fmt.Fprintln(w, "note: --full is no longer needed; ctx pull always reconciles every page with the server")
	}
	if len(doc.Written) == 0 && len(doc.Adopted) == 0 && len(doc.Skipped) == 0 {
		fmt.Fprintln(w, "Already up to date")
	} else {
		fmt.Fprintf(w, "Pulled %d page(s), adopted %d; HEAD=%s\n",
			len(doc.Written), len(doc.Adopted), shortSHA(doc.Head))
	}
	if len(doc.Skipped) > 0 {
		fmt.Fprintf(w, "Skipped %d page(s):\n", len(doc.Skipped))
		for _, s := range doc.Skipped {
			fmt.Fprintf(w, "  %s: %s\n", s.Path, pullSkipAdvice(s))
		}
	}
	for _, wn := range doc.Warnings {
		fmt.Fprintf(w, "warning: %s\n", wn.Message)
	}
}

// pullSkipAdvice is the human explanation of one skipped page.
func pullSkipAdvice(s contract.SkippedPage) string {
	switch s.Reason {
	case contract.SkipModified:
		return "kept your local edits; run ctx push"
	case contract.SkipConflict:
		return "conflicts with the server version; run ctx resolve " + s.Path + " --theirs|--ours"
	case contract.SkipUnverified:
		return "base not verified; run ctx repair"
	case contract.SkipInvalid:
		return "invalid page; fix it, then pull again"
	case contract.SkipDeletedLocal:
		return "deleted locally; ctx resolve " + s.Path + " --theirs restores it"
	case contract.SkipCaseCollision:
		return "differs only by letter case from another page; rename one of them"
	case contract.SkipChangedDuringPull:
		return "changed during the pull; run ctx pull again"
	}
	return string(s.Reason)
}
