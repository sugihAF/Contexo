package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/store/pagestore"
	"github.com/sugihAF/contexo/internal/sync"
)

func newStatusCmd() *cobra.Command {
	var noDrift bool
	cmd := &cobra.Command{
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

			if !noDrift && creds != nil && creds.Bearer() != "" && cfg.ServerURL != "" && cfg.RepoID != "" {
				drifted := computeDriftedPages(cfg, creds, pages, state)
				if len(drifted) == 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "Pages drifted on server: 0")
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "Pages drifted on server (run `ctx pull` to refresh): %d\n", len(drifted))
					for _, p := range drifted {
						fmt.Fprintf(cmd.OutOrStdout(), "  %s   (local %s → server %s)\n",
							p.Path, shortSHA(p.LocalSHA), shortSHA(p.ServerSHA))
					}
				}
			}

			return nil
		},
	}
	cmd.Flags().BoolVar(&noDrift, "no-drift", false, "skip the per-page server-drift check (faster; offline-friendly)")
	return cmd
}

// driftedPage describes a page that has changed on the server since the local
// last-pulled sha. Used by `ctx status` to list a heads-up of pages that
// would 409 on push (or surprise the user on read).
type driftedPage struct {
	Path      string
	LocalSHA  string
	ServerSHA string
}

// computeDriftedPages walks every locally-tracked page, asks the server for
// its current sha, and returns the subset that has moved. Pages never pulled
// (no entry in state.PageSHAs) are skipped — drift implies a baseline. Errors
// per page are silently skipped; failure to detect drift should never break
// the status output.
func computeDriftedPages(cfg *config.Config, creds *config.Credentials, pages []*schema.Page, state *sync.State) []driftedPage {
	client := sync.NewClient(cfg.ServerURL, creds.Bearer())
	var drifted []driftedPage
	for _, p := range pages {
		path := p.Frontmatter.RelPath()
		localSHA, ok := state.PageSHAs[path]
		if !ok || localSHA == "" {
			continue
		}
		_, serverSHA, err := client.ReadPage(cfg.RepoID, path)
		if err != nil || serverSHA == "" || serverSHA == localSHA {
			continue
		}
		drifted = append(drifted, driftedPage{Path: path, LocalSHA: localSHA, ServerSHA: serverSHA})
	}
	return drifted
}
