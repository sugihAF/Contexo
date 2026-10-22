package cli

import (
	"errors"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/syncops"
	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/ui"
)

// Owner: WP-1.4.

func init() {
	registerCapability(contract.CapResolve)
}

// newResolveCmd is `ctx resolve <relpath> (--ours|--theirs)
// [--if-server-sha <sha>] [--json]`, the way out of a conflict. --ours keeps
// the working file and re-bases it onto the current server version, so the
// next push supersedes the teammate's edit; --theirs replaces the working
// file with the server version after backing it up. The engine is
// syncops.Resolve (shared with MCP ctx_push resolved=[...]); this command
// only orders the checks, builds the Env and renders.
//
// Check order: cobra's args, exactly one strategy, the argument rules
// (ResolvePreflight: path, --if-server-sha form), the hub, then the remote.
// A bad argument is therefore usage or invalid_path (exit 3) in any root and
// whatever the credentials. The envelope and contract exits apply only with
// --json (E21); without it every failure is human text and exit 1.
func newResolveCmd() *cobra.Command {
	var (
		ours, theirs bool
		ifServerSHA  string
		asJSON       bool
	)
	cmd := &cobra.Command{
		Use:   "resolve <relpath> (--ours|--theirs)",
		Short: "Resolve a conflict by keeping the local or the server version",
		Long: "Resolve a conflicted page.\n\n" +
			"--ours keeps your working file and re-bases it onto the current server version;\n" +
			"run ctx push afterwards to publish it over the teammate's edit.\n" +
			"--theirs replaces your working file with the server version. A file that differs\n" +
			"is first saved under .contexo/.sync/backup/.\n\n" +
			"--if-server-sha refuses (stale) unless the server is still at that sha.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Checked here, not with cobra flag groups: group errors bypass
			// the flag-error hook and would not be coded as usage.
			if ours == theirs {
				return ui.Coded(errors.New("resolve: pass exactly one of --ours or --theirs"), contract.CodeUsage)
			}
			strategy := contract.ResolveTheirs
			if ours {
				strategy = contract.ResolveOurs
			}
			env, doc, err := resolveRun(cmd, syncops.ResolveOptions{
				Path: args[0], Strategy: strategy, IfServerSHA: ifServerSHA,
			})
			if err != nil {
				if !asJSON {
					err = resolveHumanError(err)
				}
				return err
			}
			if asJSON {
				return contract.WriteDoc(cmd.OutOrStdout(), doc)
			}
			resolveRender(ui.From(cmd), env.HubDir, doc)
			return nil
		},
	}
	cmd.Flags().BoolVar(&ours, "ours", false, "keep the local content, re-based onto the server version")
	cmd.Flags().BoolVar(&theirs, "theirs", false, "replace the local file with the server version")
	cmd.Flags().StringVar(&ifServerSHA, "if-server-sha", "", "fail with stale unless the server is still at this sha")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit a JSON document")
	return cmd
}

// resolveRun runs the checks in contract order and then the engine. The
// returned Env is the online one the engine ran with (its HubDir names the
// backup's directory).
func resolveRun(cmd *cobra.Command, o syncops.ResolveOptions) (syncops.Env, *contract.ResolveDoc, error) {
	root := GetRootDir()
	env := syncops.NewEnv(root, "", "", syncops.Author{}, nil)
	rel, err := syncops.ResolvePreflight(env, o)
	if err != nil {
		return env, nil, err
	}
	if _, err := syncops.RequireHub(root); err != nil {
		return env, nil, err
	}
	rr, client, err := openRemote(root, "resolve", true)
	if err != nil {
		return env, nil, err
	}
	env = syncops.NewEnv(root, rr.RepoID, rr.ServerURL, syncops.Author{}, client)
	o.Path = rel
	doc, err := syncops.Resolve(cmd.Context(), env, o)
	return env, doc, err
}

// resolveHumanError makes a coded error's advice visible in human mode.
// RenderError prints advice only from a *ui.CLIError, so an engine error (a
// *contract.Error, or the duck-coded sync and syncstate errors) is wrapped
// with ui.Suggest when its contract advice is non-empty. openRemote's errors
// already are *ui.CLIError and keep theirs.
func resolveHumanError(err error) error {
	var cli *ui.CLIError
	if errors.As(err, &cli) {
		return err
	}
	if advice := ui.ToContractError(err).Advice; len(advice) > 0 {
		return ui.Suggest(err, advice...)
	}
	return err
}

// resolveRender prints the human result: what happened and the next step
// (push after --ours, the backup's absolute path after --theirs), then any
// warnings.
func resolveRender(u *ui.UI, hub string, doc *contract.ResolveDoc) {
	short := resolveShortSHA(doc.BaseSHA)
	switch {
	case doc.Strategy == contract.ResolveOurs && doc.State == contract.StateUnchanged:
		u.Success("Kept %s: it already matches server %s; nothing to push", doc.Path, short)
	case doc.Strategy == contract.ResolveOurs:
		u.Success("Kept your version of %s on top of server %s; now run ctx push", doc.Path, short)
	case doc.BackupFile != "":
		u.Success("Took the server version of %s (%s); your previous file is saved at %s",
			doc.Path, short, filepath.Join(hub, filepath.FromSlash(doc.BackupFile)))
	case doc.FileWritten:
		u.Success("Took the server version of %s (%s)", doc.Path, short)
	default:
		u.Success("%s already matches the server version (%s); nothing to do", doc.Path, short)
	}
	for _, w := range doc.Warnings {
		u.Warn("%s", w.Message)
	}
}

// resolveShortSHA is the 7-character form of a sha for human text.
func resolveShortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
