package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/sync"
)

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Authentication management",
	}
	cmd.AddCommand(newAuthLoginCmd())
	cmd.AddCommand(newAuthStatusCmd())
	return cmd
}

// newLoginCmd is the top-level `ctx login` alias for `ctx auth login`.
func newLoginCmd() *cobra.Command {
	c := newAuthLoginCmd()
	c.Use = "login"
	c.Short = "Authenticate with a Contexo server (alias for `ctx auth login`)"
	return c
}

func newAuthLoginCmd() *cobra.Command {
	var (
		token        string
		apiKey       string
		serverURL    string
		dashboardURL string
		repoID       string
		userName     string
		userEmail    string
		forceBrowser bool
		noBrowser    bool
	)
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate with a Contexo server",
		Long: `Authenticate with a Contexo server.

With no flags on an interactive terminal, opens the dashboard in your browser,
signs you in, and copies a freshly-minted personal access token back to the CLI
over a loopback redirect. Use --no-browser to fall back to a paste-the-token
prompt, or pass --token directly to skip the browser entirely.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()

			// Resolve which token value to use. --token wins; --api-key is
			// deprecated but accepted.
			chosen := strings.TrimSpace(token)
			if chosen == "" && apiKey != "" {
				fmt.Fprintln(cmd.ErrOrStderr(),
					"warning: --api-key is deprecated; use --token instead (PATs are minted from the dashboard's Settings page).")
				chosen = strings.TrimSpace(apiKey)
			}
			if chosen == "" {
				// Decide between browser flow and paste-prompt:
				//   --browser forces browser even off-TTY (rare but supported)
				//   --no-browser forces paste prompt
				//   otherwise: TTY → browser, no-TTY → error
				useBrowser := forceBrowser || (!noBrowser && stdinIsTTY())
				if useBrowser {
					dash := strings.TrimRight(strings.TrimSpace(dashboardURL), "/")
					if dash == "" {
						cfgEarly, _ := config.Load(root)
						if cfgEarly != nil && cfgEarly.DashboardURL != "" {
							dash = strings.TrimRight(cfgEarly.DashboardURL, "/")
						}
					}
					if dash == "" {
						dash = config.DefaultDashboardURL
					}
					var err error
					chosen, err = runBrowserLogin(context.Background(), dash, cmd.OutOrStdout())
					if err != nil {
						return err
					}
				} else if !stdinIsTTY() {
					return fmt.Errorf("a token is required (--token, or run interactively for the browser flow)")
				} else {
					fmt.Fprint(cmd.OutOrStdout(), "Token: ")
					reader := bufio.NewReader(os.Stdin)
					input, err := reader.ReadString('\n')
					if err != nil {
						return fmt.Errorf("auth: read input: %w", err)
					}
					chosen = strings.TrimSpace(input)
				}
			}
			if chosen == "" {
				return fmt.Errorf("a token is required (--token, browser flow, or interactive prompt)")
			}
			if strings.HasPrefix(chosen, "ctxi_") {
				return fmt.Errorf("that looks like a repo invite key (ctxi_…), not a CLI token. Use 'ctx join' or paste it into the dashboard's Join with key dialog")
			}

			cfg, err := config.Load(root)
			if err != nil {
				return err
			}
			if serverURL != "" {
				cfg.ServerURL = serverURL
			}
			// Default to the hosted Contexo server when the user hasn't
			// configured one yet — saves a separate `ctx remote set` step
			// for the common case. Self-hosted users still pass --server.
			if cfg.ServerURL == "" {
				cfg.ServerURL = config.DefaultServerURL
			}
			if dashboardURL != "" {
				cfg.DashboardURL = strings.TrimRight(dashboardURL, "/")
			}
			if repoID != "" {
				cfg.RepoID = repoID
			}
			if err := config.Save(root, cfg); err != nil {
				return err
			}

			creds := &config.Credentials{
				Token:     chosen,
				ServerURL: cfg.ServerURL,
				UserName:  userName,
				UserEmail: userEmail,
			}
			if err := config.SaveCredentials(root, creds); err != nil {
				return err
			}

			// Offer an interactive repo picker if the user didn't pass --repo
			// and we have a TTY + server URL. Skipping the picker (or it
			// erroring) is non-fatal: the user can always run `ctx remote
			// set-repo` later.
			if cfg.RepoID == "" && cfg.ServerURL != "" && stdinIsTTY() {
				client := sync.NewClient(cfg.ServerURL, creds.Bearer())
				chosenRepo, err := selectRepoInteractive(client, cmd.OutOrStdout())
				if err == nil && chosenRepo != "" {
					cfg.RepoID = chosenRepo
					if saveErr := config.Save(root, cfg); saveErr != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "warning: saved auth but could not persist repo selection: %v\n", saveErr)
					}
				}
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Authenticated (%s)", creds.Kind())
			if cfg.ServerURL != "" {
				fmt.Fprintf(cmd.OutOrStdout(), " · server: %s", cfg.ServerURL)
			}
			if cfg.RepoID != "" {
				fmt.Fprintf(cmd.OutOrStdout(), " · repo: %s", cfg.RepoID)
			}
			fmt.Fprintln(cmd.OutOrStdout())
			return nil
		},
	}
	cmd.Flags().StringVar(&token, "token", "", "personal access token (skips the browser flow if set)")
	cmd.Flags().StringVar(&apiKey, "api-key", "", "DEPRECATED: legacy shared API key. Use --token instead")
	cmd.Flags().StringVar(&serverURL, "server", "", "server URL (default: https://api.contexo.live)")
	cmd.Flags().StringVar(&dashboardURL, "dashboard", "", "dashboard URL used by the browser flow (default: https://contexo-web.pages.dev)")
	cmd.Flags().StringVar(&repoID, "repo", "", "repo_id on the server")
	cmd.Flags().StringVar(&userName, "name", "", "your display name (used as commit author)")
	cmd.Flags().StringVar(&userEmail, "email", "", "your email (used as commit author)")
	cmd.Flags().BoolVar(&forceBrowser, "browser", false, "force the browser flow even when stdin is not a terminal")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "skip the browser flow; paste the token interactively")
	return cmd
}

func newAuthStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show authentication status",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()
			creds, err := config.LoadCredentials(root)
			if err != nil || creds == nil {
				fmt.Fprintln(cmd.OutOrStdout(), "Not authenticated")
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Authenticated: yes")
			if creds.ServerURL != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Server: %s\n", creds.ServerURL)
			}
			if creds.UserName != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "User: %s <%s>\n", creds.UserName, creds.UserEmail)
			}
			tok := creds.Bearer()
			masked := tok
			if len(masked) > 8 {
				masked = masked[:4] + "..." + masked[len(masked)-4:]
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Token: %s (%s)\n", masked, creds.Kind())
			return nil
		},
	}
}
