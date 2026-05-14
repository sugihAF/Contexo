package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/store"
)

func newLinkCmd() *cobra.Command {
	var commitFlag string

	cmd := &cobra.Command{
		Use:   "link <git-sha>",
		Short: "Link a context commit to a git SHA",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()
			db, err := openDB(root)
			if err != nil {
				return err
			}
			defer db.Close()

			gitSHA := args[0]
			commitID := commitFlag

			if commitID == "" {
				// Find most recent commit
				commits, err := db.ListCommits(context.Background(), store.CommitFilter{Limit: 1})
				if err != nil {
					return err
				}
				if len(commits) == 0 {
					return fmt.Errorf("no context commits to link")
				}
				commitID = commits[0].CommitID
			}

			if err := db.LinkGit(context.Background(), gitSHA, commitID); err != nil {
				return fmt.Errorf("link: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Linked git %s -> context commit %s\n", shortID(gitSHA), shortID(commitID))
			return nil
		},
	}

	cmd.Flags().StringVar(&commitFlag, "commit", "", "specific commit ID to link (default: latest)")

	return cmd
}
