package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/sync"
)

func newHistoryCmd() *cobra.Command {
	var typ string
	var limit int
	cmd := &cobra.Command{
		Use:   "history <slug>",
		Short: "Show the commit timeline for a single page",
		Long: "Lists every commit that touched the page identified by <slug>, newest first.\n" +
			"The page is resolved locally by scanning .contexo/wiki/{concepts,entities,analyses}/\n" +
			"and .contexo/raw/sessions/; pass --type to disambiguate when the same slug exists\n" +
			"under more than one type.",
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
				return fmt.Errorf("history: not authenticated (run 'ctx auth login')")
			}
			serverURL := chooseServerURL(creds, cfg)
			if serverURL == "" || cfg.RepoID == "" {
				return fmt.Errorf("history: server URL or repo_id not configured")
			}
			client := sync.NewClient(serverURL, creds.Bearer())
			commits, err := client.PageHistory(cfg.RepoID, path, limit)
			if err != nil {
				return err
			}
			if len(commits) == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "No commits found for %s\n", path)
				return nil
			}
			for _, c := range commits {
				fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  %s  %s\n",
					shortSHA(c.SHA),
					c.Time.Format("2006-01-02"),
					padAuthor(c.Author),
					c.Message,
				)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&typ, "type", "", "page type (concept|entity|analysis|source); only needed when the slug is ambiguous")
	cmd.Flags().IntVar(&limit, "limit", 50, "max commits to show (server caps at a reasonable limit)")
	return cmd
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// padAuthor right-pads short author names so columns line up at ~12 chars,
// which fits the common "First Last" / "First" / GitHub-handle widths without
// dominating the line when names are long.
func padAuthor(name string) string {
	const w = 12
	if len(name) >= w {
		return name
	}
	return name + strings.Repeat(" ", w-len(name))
}
