package cli

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/config"
	sqlitestore "github.com/sugihAF/contexo/internal/store/sqlite"
	"github.com/sugihAF/contexo/internal/sync"
)

func newPullCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pull",
		Short: "Pull new commits from server",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()
			ctxDir := config.CtxDirPath(root)

			creds, err := config.LoadCredentials(root)
			if err != nil || creds == nil {
				return fmt.Errorf("pull: no credentials, run 'ctx auth login' first")
			}

			cfg, err := config.Load(root)
			if err != nil {
				return err
			}

			serverURL := creds.ServerURL
			if serverURL == "" {
				serverURL = cfg.ServerURL
			}
			if serverURL == "" {
				return fmt.Errorf("pull: no server URL configured")
			}

			db, err := sqlitestore.Open(filepath.Join(ctxDir, "index.sqlite"))
			if err != nil {
				return err
			}
			defer db.Close()

			ctx := context.Background()
			client := sync.NewClient(serverURL, creds.APIKey)

			repoID := cfg.RepoID
			commits, err := client.PullCommits(repoID)
			if err != nil {
				return fmt.Errorf("pull: %w", err)
			}

			inserted := 0
			for _, c := range commits {
				// Check if we already have it
				existing, err := db.GetCommit(ctx, c.CommitID)
				if err == nil && existing != nil {
					continue
				}

				if err := db.CreateCommit(ctx, c); err != nil {
					fmt.Fprintf(cmd.OutOrStderr(), "warning: insert commit %s: %v\n", shortID(c.CommitID), err)
					continue
				}
				inserted++
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Pulled %d new commits\n", inserted)
			return nil
		},
	}
}
