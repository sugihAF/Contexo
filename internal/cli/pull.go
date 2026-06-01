package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/indexer"
	"github.com/sugihAF/contexo/internal/store/pagestore"
	"github.com/sugihAF/contexo/internal/sync"
)

func newPullCmd() *cobra.Command {
	var forceFull bool
	cmd := &cobra.Command{
		Use:   "pull",
		Short: "Pull new pages from the server into .contexo/",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()
			hubDir := config.ContexoDirPath(root)

			cfg, err := config.Load(root)
			if err != nil {
				return err
			}
			creds, err := config.LoadCredentials(root)
			if err != nil || creds == nil {
				return fmt.Errorf("pull: no credentials, run 'ctx auth login' first")
			}
			serverURL := chooseServerURL(creds, cfg)
			if serverURL == "" {
				return fmt.Errorf("pull: no server URL configured (run 'ctx remote add')")
			}
			if cfg.RepoID == "" {
				return fmt.Errorf("pull: no repo_id configured in .contexo/config.json")
			}

			store, err := pagestore.Open(hubDir)
			if err != nil {
				return fmt.Errorf("pull: open hub: %w (did you run 'ctx init'?)", err)
			}
			_ = store

			state, err := sync.LoadState(hubDir)
			if err != nil {
				return err
			}

			since := state.LastPullSHA
			if forceFull {
				since = ""
			}

			client := sync.NewClient(serverURL, creds.Bearer())
			client.SetClientName("ctx-cli")
			resp, err := client.PullPages(cfg.RepoID, since)
			if err != nil {
				return err
			}

			if len(resp.Files) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "Already up to date")
				if resp.NewHead != "" && resp.NewHead != state.LastPullSHA {
					state.LastPullSHA = resp.NewHead
					_ = sync.SaveState(hubDir, state)
				}
				return nil
			}

			written := 0
			for _, f := range resp.Files {
				abs := filepath.Join(hubDir, filepath.FromSlash(f.Path))
				if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
					return fmt.Errorf("pull: mkdir %s: %w", f.Path, err)
				}
				if err := os.WriteFile(abs, []byte(f.Content), 0o644); err != nil {
					return fmt.Errorf("pull: write %s: %w", f.Path, err)
				}
				state.PageSHAs[f.Path] = f.SHA
				written++
			}

			state.LastPullSHA = resp.NewHead
			if err := sync.SaveState(hubDir, state); err != nil {
				return fmt.Errorf("pull: save state: %w", err)
			}

			// Regenerate index after pulling new pages so the local index reflects
			// the latest team knowledge.
			if err := indexer.Generate(store); err != nil {
				fmt.Fprintf(cmd.OutOrStderr(), "warning: reindex: %v\n", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Pulled %d page(s); HEAD=%s\n", written, shortSHA(resp.NewHead))
			return nil
		},
	}

	cmd.Flags().BoolVar(&forceFull, "full", false, "ignore last_pull_sha and fetch everything")
	return cmd
}
