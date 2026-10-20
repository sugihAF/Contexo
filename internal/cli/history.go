package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/ui"
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
			rr, client, err := openRemote(root, "history", true)
			if err != nil {
				return err
			}
			commits, err := client.PageHistory(rr.RepoID, path, limit)
			if err != nil {
				return err
			}
			if len(commits) == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "No commits found for %s\n", path)
				return nil
			}
			// Replaces a fixed padAuthor(12), which both truncated nothing and
			// aligned nothing once a name ran past 12 chars. tabwriter measures
			// the actual data instead.
			t := ui.From(cmd).Table("COMMIT", "WHEN", "WHO", "MESSAGE")
			for _, c := range commits {
				t.Row(
					shortSHA(c.SHA),
					c.Time.Format("2006-01-02"),
					c.Author,
					c.Message,
				)
			}
			t.Render()
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
