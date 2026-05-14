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

func newPushCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "push",
		Short: "Push unsynced commits to server",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()
			ctxDir := config.CtxDirPath(root)

			creds, err := config.LoadCredentials(root)
			if err != nil || creds == nil {
				return fmt.Errorf("push: no credentials, run 'ctx auth login' first")
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
				return fmt.Errorf("push: no server URL configured")
			}

			db, err := sqlitestore.Open(filepath.Join(ctxDir, "index.sqlite"))
			if err != nil {
				return err
			}
			defer db.Close()

			ctx := context.Background()
			client := sync.NewClient(serverURL, creds.APIKey)

			// Get unsynced commits
			unsyncedIDs, err := db.GetUnsyncedCommits(ctx)
			if err != nil {
				return err
			}

			if len(unsyncedIDs) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "Everything up to date")
				return nil
			}

			repoID := cfg.RepoID
			pushed := 0

			for _, id := range unsyncedIDs {
				commit, err := db.GetCommit(ctx, id)
				if err != nil || commit == nil {
					continue
				}

				if err := client.PushCommit(repoID, commit); err != nil {
					fmt.Fprintf(cmd.OutOrStderr(), "warning: push commit %s: %v\n", shortID(id), err)
					continue
				}

				db.MarkSynced(ctx, "commit", id)
				pushed++
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Pushed %d commits\n", pushed)
			return nil
		},
	}
}
