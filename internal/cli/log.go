package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/store"
)

func newLogCmd() *cobra.Command {
	var feature string

	cmd := &cobra.Command{
		Use:   "log",
		Short: "List context commits",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()
			db, err := openDB(root)
			if err != nil {
				return err
			}
			defer db.Close()

			filter := store.CommitFilter{Feature: feature}
			commits, err := db.ListCommits(context.Background(), filter)
			if err != nil {
				return err
			}

			if len(commits) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No commits found")
				return nil
			}

			for _, c := range commits {
				featureStr := ""
				if c.Feature != "" {
					featureStr = fmt.Sprintf(" [%s]", c.Feature)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s %s%s (%s)\n",
					shortID(c.CommitID), c.Title, featureStr,
					c.CreatedAt.Format("2006-01-02 15:04"))
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&feature, "feature", "", "filter by feature")
	return cmd
}
