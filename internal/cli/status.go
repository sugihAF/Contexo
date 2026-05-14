package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/store"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show project context status overview",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()
			ctxDir := config.CtxDirPath(root)

			// Check if initialized
			if _, err := os.Stat(ctxDir); os.IsNotExist(err) {
				fmt.Fprintln(cmd.OutOrStdout(), "Not initialized (run 'ctx init')")
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Initialized: yes")

			// Config
			cfg, err := config.Load(root)
			if err == nil {
				if cfg.ServerURL != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "Server: %s\n", cfg.ServerURL)
				}
				if cfg.RemoteName != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "Remote: %s\n", cfg.RemoteName)
				}
				if cfg.RepoID != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "Repo ID: %s\n", cfg.RepoID)
				}
			}

			// Credentials
			creds, credErr := config.LoadCredentials(root)
			if credErr == nil && creds != nil {
				fmt.Fprintln(cmd.OutOrStdout(), "Authenticated: yes")
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "Authenticated: no")
			}

			// Capture status
			state, stateErr := loadCaptureState(ctxDir)
			if stateErr == nil && state.Active {
				status := "active"
				if state.Paused {
					status = "paused"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Capture: %s (port %d)\n", status, state.Port)
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "Capture: inactive")
			}

			// Session and commit counts
			db, err := openDB(root)
			if err == nil {
				defer db.Close()
				ctx := context.Background()

				sessions, serr := db.ListSessions(ctx, store.SessionFilter{})
				if serr == nil {
					fmt.Fprintf(cmd.OutOrStdout(), "Sessions: %d\n", len(sessions))
				}

				commits, cerr := db.ListCommits(ctx, store.CommitFilter{})
				if cerr == nil {
					fmt.Fprintf(cmd.OutOrStdout(), "Commits: %d\n", len(commits))
				}
			}

			return nil
		},
	}
}
