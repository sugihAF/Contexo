package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/diff"
	"github.com/sugihAF/contexo/internal/sync"
)

func newDiffCmd() *cobra.Command {
	var typ, from, to string
	var asJSON, local, blame bool
	cmd := &cobra.Command{
		Use:   "diff <slug>",
		Short: "Show the structured diff between two versions of a page",
		Long: "Compares two versions of the page identified by <slug>. When --from / --to are\n" +
			"omitted, defaults to the most recent change (parent..head for the page).\n\n" +
			"Output is section-aware: frontmatter changes show as old→new per field, and each\n" +
			"## section is reported as added / removed / modified / unchanged. Pass --json to\n" +
			"emit the raw structured diff for scripting or for an agent to consume.\n\n" +
			"Pass --local to compare your local .contexo/ copy against the server's HEAD for\n" +
			"this page — the same diff the pre-push preview computes — without pushing.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug := args[0]
			root := GetRootDir()
			if typ != "" && !contains(validTypes(), typ) {
				return fmt.Errorf("invalid --type %q (want one of %s)", typ, strings.Join(validTypes(), "|"))
			}
			path, err := resolveSlugPath(root, slug, typ)
			if err != nil {
				return err
			}
			cfg, err := config.Load(root)
			if err != nil {
				return err
			}
			creds, err := config.LoadCredentials(root)
			if err != nil || creds == nil {
				return fmt.Errorf("diff: not authenticated (run 'ctx auth login')")
			}
			serverURL := chooseServerURL(creds, cfg)
			if serverURL == "" || cfg.RepoID == "" {
				return fmt.Errorf("diff: server URL or repo_id not configured")
			}
			client := sync.NewClient(serverURL, creds.Bearer())

			var d *diff.SectionDiff
			switch {
			case local:
				if from != "" || to != "" || blame {
					return fmt.Errorf("diff: --local is mutually exclusive with --from/--to/--blame")
				}
				d, err = diffLocalVsServer(client, cfg.RepoID, root, path)
			default:
				d, err = client.PageDiff(cfg.RepoID, path, from, to, blame)
			}
			if err != nil {
				return err
			}

			if asJSON {
				js, err := d.ToJSON()
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), string(js))
				return nil
			}
			fmt.Fprint(cmd.OutOrStdout(), d.ToText(slug))
			return nil
		},
	}
	cmd.Flags().StringVar(&typ, "type", "", "page type (concept|entity|analysis|source); only needed when the slug is ambiguous")
	cmd.Flags().StringVar(&from, "from", "", "old sha (default: parent of --to)")
	cmd.Flags().StringVar(&to, "to", "", "new sha (default: HEAD-for-this-path)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit raw SectionDiff JSON instead of formatted text")
	cmd.Flags().BoolVar(&local, "local", false, "diff your local .contexo/ copy against the server's HEAD (what `ctx push` would change)")
	cmd.Flags().BoolVar(&blame, "blame", false, "annotate each section with the commit that introduced its heading (best-effort; adds latency on long histories)")
	return cmd
}

// diffLocalVsServer reads the local working copy and diffs it against the
// server's current version of the same path. Mirrors what the push preview
// does for a single file, but surfaces it as a standalone command so a dev
// can ask "what would my push change?" without actually pushing.
func diffLocalVsServer(client *sync.Client, repoID, root, path string) (*diff.SectionDiff, error) {
	abs := filepath.Join(root, ".contexo", filepath.FromSlash(path))
	localBytes, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("diff --local: read local %s: %w", path, err)
	}
	serverBytes, serverSHA, err := client.ReadPage(repoID, path)
	if err != nil {
		if errors.Is(err, sync.ErrPageNotFound) {
			// Page only exists locally — represent as "added against nothing"
			// by handing the differ empty server bytes; the differ will emit
			// every section as added.
			d := diff.PageSections(nil, localBytes, "", "local")
			return &d, nil
		}
		return nil, err
	}
	d := diff.PageSections(serverBytes, localBytes, serverSHA, "local")
	return &d, nil
}
