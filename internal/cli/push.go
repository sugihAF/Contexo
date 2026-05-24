package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/store/pagestore"
	"github.com/sugihAF/contexo/internal/sync"
)

func newPushCmd() *cobra.Command {
	var (
		featureFilter  string
		tagFilter      string
		typeFilter     string
		message        string
		dryRun         bool
		fallbackServer bool
		yes            bool
		showDiff       bool
		noPreview      bool
	)
	cmd := &cobra.Command{
		Use:   "push",
		Short: "Push local .contexo pages to the server",
		RunE: func(cmd *cobra.Command, args []string) error {
			if fallbackServer {
				return fmt.Errorf("push: --fallback-server: server-side distillation not implemented yet (planned for Phase 4 of agent-reasoning-capture); use the agent-as-distiller flow via ctx_push MCP for now")
			}
			root := GetRootDir()
			hubDir := config.ContexoDirPath(root)

			cfg, err := config.Load(root)
			if err != nil {
				return err
			}
			creds, err := config.LoadCredentials(root)
			if err != nil || creds == nil {
				return fmt.Errorf("push: no credentials, run 'ctx auth login' first")
			}

			serverURL := chooseServerURL(creds, cfg)
			if serverURL == "" {
				return fmt.Errorf("push: no server URL configured (run 'ctx remote add')")
			}
			if cfg.RepoID == "" {
				return fmt.Errorf("push: no repo_id configured in .contexo/config.json")
			}

			store, err := pagestore.Open(hubDir)
			if err != nil {
				return fmt.Errorf("push: open hub: %w (did you run 'ctx init'?)", err)
			}

			pages, err := store.List(pagestore.Filter{})
			if err != nil {
				return fmt.Errorf("push: list pages: %w", err)
			}

			filtered := applyFilters(pages, featureFilter, tagFilter, typeFilter)
			if len(filtered) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "Nothing to push (no pages match filters)")
				return nil
			}

			state, err := sync.LoadState(hubDir)
			if err != nil {
				return err
			}

			files := make([]sync.PushFile, 0, len(filtered))
			for _, p := range filtered {
				data, err := schema.SerializePage(p)
				if err != nil {
					return fmt.Errorf("push: serialize %s: %w", p.Frontmatter.Slug, err)
				}
				path := p.Frontmatter.RelPath()
				files = append(files, sync.PushFile{
					Path:      path,
					Content:   string(data),
					ParentSHA: state.PageSHAs[path],
				})
			}

			if dryRun {
				fmt.Fprintf(cmd.OutOrStdout(), "Would push %d page(s):\n", len(files))
				for _, f := range files {
					fmt.Fprintf(cmd.OutOrStdout(), "  %s  (parent=%s)\n", f.Path, shortSHA(f.ParentSHA))
				}
				return nil
			}

			if message == "" {
				message = fmt.Sprintf("ctx push (%d pages)", len(files))
			}

			client := sync.NewClient(serverURL, creds.Bearer())

			if !noPreview {
				previews := computePushPreview(client, cfg.RepoID, files)
				hasEdits := renderPreview(cmd.OutOrStdout(), cfg.RepoID, previews, showDiff)
				if hasEdits && !yes {
					if !stdinIsTTY() {
						return fmt.Errorf("push: refusing to alter existing pages in non-interactive mode; pass --yes to confirm")
					}
					ok, err := confirm(os.Stdin, cmd.OutOrStdout(), "Proceed?")
					if err != nil {
						return err
					}
					if !ok {
						fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")
						return nil
					}
				}
			}

			resp, err := client.PushPages(cfg.RepoID, &sync.PushRequest{
				AuthorName:  creds.UserName,
				AuthorEmail: creds.UserEmail,
				Message:     message,
				Files:       files,
			})
			if err != nil {
				return err
			}

			if len(resp.Conflicts) > 0 {
				fmt.Fprintf(cmd.OutOrStderr(), "%d conflict(s):\n", len(resp.Conflicts))
				for _, cf := range resp.Conflicts {
					fmt.Fprintf(cmd.OutOrStderr(), "  %s: current=%s expected_parent=%s\n",
						cf.Path, shortSHA(cf.CurrentSHA), shortSHA(cf.ExpectedParentSHA))
				}
				fmt.Fprintln(cmd.OutOrStderr(),
					"Resolve by running 'ctx pull', merging the conflicting pages, then 'ctx push' again.")
			}

			for _, f := range resp.Pushed {
				state.PageSHAs[f.Path] = f.SHA
			}
			if err := sync.SaveState(hubDir, state); err != nil {
				return fmt.Errorf("push: save state: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Pushed %d page(s); HEAD=%s\n", len(resp.Pushed), shortSHA(resp.NewHead))
			if len(resp.Conflicts) > 0 {
				return fmt.Errorf("push: %d conflict(s) remain", len(resp.Conflicts))
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&featureFilter, "feature", "", "push only pages tagged with this feature/tag")
	cmd.Flags().StringVar(&tagFilter, "tag", "", "push only pages with this tag (alias of --feature)")
	cmd.Flags().StringVar(&typeFilter, "type", "", "push only pages of this type (concept|entity|source|analysis)")
	cmd.Flags().StringVarP(&message, "message", "m", "", "commit message (default: 'ctx push (N pages)')")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be pushed without sending")
	cmd.Flags().BoolVar(&fallbackServer, "fallback-server", false, "(planned) route reasoning-trail distillation to the Contexo server; currently errors")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the pre-push confirmation prompt (required for non-interactive use when editing existing pages)")
	cmd.Flags().BoolVar(&showDiff, "show-diff", false, "print the full per-section diff in the preview, not just a one-line summary")
	cmd.Flags().BoolVar(&noPreview, "no-preview", false, "skip the pre-push preview entirely (faster; loses the heads-up about what changes)")
	return cmd
}

func applyFilters(pages []*schema.Page, feature, tag, typ string) []*schema.Page {
	wanted := strings.ToLower(strings.TrimSpace(feature))
	if wanted == "" {
		wanted = strings.ToLower(strings.TrimSpace(tag))
	}
	typLow := strings.ToLower(strings.TrimSpace(typ))

	var out []*schema.Page
	for _, p := range pages {
		if typLow != "" && strings.ToLower(string(p.Frontmatter.Type)) != typLow {
			continue
		}
		if wanted != "" {
			has := false
			for _, t := range p.Frontmatter.Tags {
				if strings.ToLower(t) == wanted {
					has = true
					break
				}
			}
			if !has {
				continue
			}
		}
		out = append(out, p)
	}
	return out
}

func chooseServerURL(creds *config.Credentials, cfg *config.Config) string {
	if creds != nil && creds.ServerURL != "" {
		return creds.ServerURL
	}
	return cfg.ServerURL
}

func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
