package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/store/pagestore"
	"github.com/sugihAF/contexo/internal/sync"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show .contexo status and local-vs-server delta",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()
			hubDir := config.ContexoDirPath(root)

			if _, err := os.Stat(hubDir); os.IsNotExist(err) {
				fmt.Fprintln(cmd.OutOrStdout(), "Not initialized (run 'ctx init')")
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Initialized: yes")

			cfg, _ := config.Load(root)
			if cfg.ServerURL != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Server: %s\n", cfg.ServerURL)
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "Server: (none — run 'ctx remote set <url>')")
			}
			if cfg.RepoID != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Repo: %s\n", cfg.RepoID)
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "Repo: (none — run 'ctx remote set-repo <id>')")
			}

			creds, _ := config.LoadCredentials(root)
			if creds != nil && creds.Bearer() != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Authenticated: yes (%s)\n", creds.Kind())
				if creds.UserName != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "User: %s <%s>\n", creds.UserName, creds.UserEmail)
				}
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "Authenticated: no")
			}

			store, err := pagestore.Open(hubDir)
			if err != nil {
				return nil
			}
			pages, _ := store.List(pagestore.Filter{})
			fmt.Fprintf(cmd.OutOrStdout(), "Local pages: %d\n", len(pages))

			state, _ := sync.LoadState(hubDir)
			if state != nil && state.LastPullSHA != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Last pull: %s\n", shortSHA(state.LastPullSHA))
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "Last pull: (never)")
			}

			unpushed := 0
			for _, p := range pages {
				if _, known := state.PageSHAs[p.Frontmatter.RelPath()]; !known {
					unpushed++
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Pages never pushed: %d\n", unpushed)

			return nil
		},
	}
}
