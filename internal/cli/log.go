package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/ui"
)

func newLogCmd() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "log",
		Short: "Show the server's commit timeline (who changed what when)",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()
			rr, client, err := openRemote(root, "log", true)
			if err != nil {
				return err
			}

			commits, err := client.Timeline(rr.RepoID, limit)
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
