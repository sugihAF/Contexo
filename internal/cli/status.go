package cli

import (
	"fmt"
	"os"
	goSync "sync"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/ui"

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
			u := ui.From(cmd)
			out := cmd.OutOrStdout()

			if _, err := os.Stat(hubDir); os.IsNotExist(err) {
				fmt.Fprintln(out, "Not initialized (run 'ctx init')")
				return nil
			}

			cfg, _ := config.Load(root)
			creds, _ := config.LoadCredentials(root)

			authed := creds != nil && creds.Bearer() != ""
			state := "connected"
			if !authed {
				state = "not authenticated"
			} else if cfg.ServerURL == "" {
				state = "no server configured"
			}
			u.Brand(state, authed && cfg.ServerURL != "")
			fmt.Fprintln(out)

			u.Eyebrow("REMOTE")
			if cfg.ServerURL != "" {
				u.Field("server", cfg.ServerURL)
			} else {
				u.Field("server", "(none — run 'ctx remote set <url>')")
			}
			if cfg.RepoID != "" {
				u.Field("repo", cfg.RepoID)
			} else {
				u.Field("repo", "(none — run 'ctx remote set-repo <id>')")
			}
			if authed {
				if creds.UserName != "" {
					u.Field("user", fmt.Sprintf("%s <%s>", creds.UserName, creds.UserEmail))
				}
				u.Field("auth", fmt.Sprintf("yes (%s)", creds.Kind()))
			} else {
				u.Field("auth", "no")
			}
			fmt.Fprintln(out)

			store, err := pagestore.Open(hubDir)
			if err != nil {
				return nil
			}
			pages, _ := store.List(pagestore.Filter{})

			// LoadState returns nil only when the state file exists but is
			// unreadable or corrupt, and the error is deliberately discarded
			// here (a bad state file should not break `ctx status`). Substitute
			// an empty state so the loop below cannot nil-deref -- every page
			// then reads as never-pushed, which is the honest answer when the
			// sync state is unusable.
			syncState, _ := sync.LoadState(hubDir)
			if syncState == nil {
				syncState = &sync.State{PageSHAs: map[string]string{}}
			}

			unpushed := 0
			for _, p := range pages {
				if _, known := syncState.PageSHAs[p.Frontmatter.RelPath()]; !known {
					unpushed++
				}
			}

			u.Eyebrow("KNOWLEDGE")
			u.Field("pages", fmt.Sprintf("%d local · %d never pushed", len(pages), unpushed))
			if syncState.LastPullSHA != "" {
				u.Field("last pull", shortSHA(syncState.LastPullSHA))
			} else {
				u.Field("last pull", "(never)")
			}

			if !noDrift && authed && cfg.ServerURL != "" && cfg.RepoID != "" {
				t := u.Task("Checking server drift")
				drifted := computeDriftedPages(cfg, creds, pages, syncState)
				t.Done("Checked %d pages", len(pages))

				if len(drifted) > 0 {
					fmt.Fprintln(out)
					u.Warn("%d page(s) drifted on server", len(drifted))
					for _, p := range drifted {
						fmt.Fprintf(out, "    %s   %s → %s\n",
							p.Path, shortSHA(p.LocalSHA), shortSHA(p.ServerSHA))
					}
					u.Hint("run", "ctx pull")
				}
			}

			return nil
		},
	}
	cmd.Flags().BoolVar(&noDrift, "no-drift", false, "skip the per-page server-drift check (faster; offline-friendly)")
	return cmd
}

// driftConcurrency bounds in-flight server reads during the drift check.
const driftConcurrency = 8

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
// Checks run concurrently: this used to be one blocking round trip per page,
// so a 40-page repo waited on 40 sequential requests. Each goroutine writes to
// its own index, so the reported order is the page order and does not shuffle
// between runs.
func computeDriftedPages(cfg *config.Config, creds *config.Credentials, pages []*schema.Page, state *sync.State) []driftedPage {
	client := sync.NewClient(cfg.ServerURL, creds.Bearer())

	found := make([]*driftedPage, len(pages))
	sem := make(chan struct{}, driftConcurrency)
	var wg goSync.WaitGroup

	for i, p := range pages {
		path := p.Frontmatter.RelPath()
		localSHA, ok := state.PageSHAs[path]
		if !ok || localSHA == "" {
			continue
		}
		wg.Add(1)
		go func(i int, path, localSHA string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			_, serverSHA, err := client.ReadPage(cfg.RepoID, path)
			if err != nil || serverSHA == "" || serverSHA == localSHA {
				return
			}
			found[i] = &driftedPage{Path: path, LocalSHA: localSHA, ServerSHA: serverSHA}
		}(i, path, localSHA)
	}
	wg.Wait()

	var drifted []driftedPage
	for _, d := range found {
		if d != nil {
			drifted = append(drifted, *d)
		}
	}
	return drifted
}
