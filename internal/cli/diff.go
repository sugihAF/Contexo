package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/sync"
)

func newDiffCmd() *cobra.Command {
	var typ, from, to string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "diff <slug>",
		Short: "Show the structured diff between two versions of a page",
		Long: "Compares two versions of the page identified by <slug>. When --from / --to are\n" +
			"omitted, defaults to the most recent change (parent..head for the page).\n\n" +
			"Output is section-aware: frontmatter changes show as old→new per field, and each\n" +
			"## section is reported as added / removed / modified / unchanged. Pass --json to\n" +
			"emit the raw structured diff for scripting or for an agent to consume.",
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
			d, err := client.PageDiff(cfg.RepoID, path, from, to)
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
	return cmd
}
