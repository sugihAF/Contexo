package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/ui"

	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/sync"
)

func newLogCmd() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "log",
		Short: "Show the server's commit timeline (who changed what when)",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()
			cfg, err := config.Load(root)
			if err != nil {
				return err
			}
			creds, err := config.LoadCredentials(root)
			if err != nil || creds == nil {
				return ui.Suggest(errors.New("log: not authenticated"), "ctx auth login")
			}
			serverURL := chooseServerURL(creds, cfg)
			if serverURL == "" || cfg.RepoID == "" {
				return fmt.Errorf("log: server URL or repo_id not configured")
			}

			client := sync.NewClient(serverURL, creds.Bearer())
			commits, err := client.Timeline(cfg.RepoID, limit)
			if err != nil {
				return err
			}
			if len(commits) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No commits yet")
				return nil
			}
			// A table so author names of different lengths stop shifting the
			// message column. Cells stay untoned: tabwriter measures a cell in
			// runes, so a pre-styled body cell under a styled header pads the
			// column by the difference in escape length and visibly misaligns
			// the next column.
			t := ui.From(cmd).Table("COMMIT", "WHEN", "WHO", "MESSAGE")
			for _, c := range commits {
				t.Row(
					shortSHA(c.SHA),
					c.Time.Format("2006-01-02 15:04"),
					c.Author,
					c.Message,
				)
			}
			t.Render()
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "max commits to show")
	return cmd
}
