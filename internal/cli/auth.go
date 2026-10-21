package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/ui"
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

// browserLogin is runBrowserLogin behind a seam, so tests can drive the login
// command without opening a browser.
var browserLogin = runBrowserLogin

// loginDashboard is the dashboard the browser flow mints the token on:
// --dashboard, else config.dashboard_url, else the hosted dashboard.
func loginDashboard(dashboardFlag string, cfg *config.Config) string {
	if dash := strings.TrimRight(strings.TrimSpace(dashboardFlag), "/"); dash != "" {
		return dash
	}
	if cfg != nil && cfg.DashboardURL != "" {
		return strings.TrimRight(cfg.DashboardURL, "/")
	}
	return config.DefaultDashboardURL
}

// browserLoginRefusal stops the browser flow before it mints anything when
// the hosted dashboard would issue the token but the server_url comes from
// the workspace config and is not the hosted API (lead ruling L8). Login
// would save that hosted PAT, project and global, with the workspace URL and
// send it there, so a cloned repo's config could collect it. An explicit
// --server, --token, the paste prompt and a non-hosted dashboard are
// unaffected.
func browserLoginRefusal(serverFlag, dashboardFlag string, cfg *config.Config) error {
	if serverFlag != "" || cfg == nil || cfg.ServerURL == "" ||
		config.SameServer(cfg.ServerURL, config.DefaultServerURL) {
		return nil
	}
	dash := loginDashboard(dashboardFlag, cfg)
	if !config.SameServer(dash, config.DefaultDashboardURL) {
		return nil
	}
	return suggestRemote("login", &config.RemoteError{
		Code: "server_unresolved",
		Msg: fmt.Sprintf("this project's config points at %s, but the browser login mints tokens on %s for %s; refusing to send a hosted token to %s",
			cfg.ServerURL, dash, config.DefaultServerURL, cfg.ServerURL),
		Advice: []string{"ctx auth login --server <url> --dashboard <dashboard-url>", "ctx auth login --server <url> --token <token issued by that server>"},
	})
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
			u := ui.From(cmd)

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
					cfgEarly, _ := config.Load(root)
					if err := browserLoginRefusal(serverURL, dashboardURL, cfgEarly); err != nil {
						return err
					}
					dash := loginDashboard(dashboardURL, cfgEarly)
					res, err := browserLogin(context.Background(), dash, cmd.OutOrStdout())
					if err != nil {
						return err
					}
					chosen = res.Token
					// Identity from the dashboard's Google session fills in
					// any --name / --email the user didn't pass explicitly.
					// CLI flags still win when provided.
					if userName == "" {
						userName = res.Name
					}
					if userEmail == "" {
						userEmail = res.Email
					}
				} else if !stdinIsTTY() {
					return fmt.Errorf("a token is required (--token, or run interactively for the browser flow)")
				} else {
					// Reads cmd.InOrStdin(), not os.Stdin: the old direct read
					// could not be redirected or tested.
					input, err := u.Prompter(cmd.InOrStdin()).Line("Token")
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
			// Also write the global credentials file so the CLI + MCP server
			// are authenticated in every project, not just this directory.
			// Best-effort: a missing home dir shouldn't fail an otherwise-good login.
			if err := config.SaveGlobalCredentials(creds); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: saved project auth but could not write global credentials: %v\n", err)
			}

			// Offer an interactive repo picker if the user didn't pass --repo
			// and we have a TTY; it asks the server the credentials were just
			// saved for. Skipping the picker (or it erroring) is non-fatal:
			// the user can always run `ctx remote set-repo` later.
			if cfg.RepoID == "" && stdinIsTTY() {
				chosenRepo, err := pickRepo(root, "login", cmd.InOrStdin(), cmd.OutOrStdout())
				if err == nil && chosenRepo != "" {
					cfg.RepoID = chosenRepo
					if saveErr := config.Save(root, cfg); saveErr != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "warning: saved auth but could not persist repo selection: %v\n", saveErr)
					}
				}
			}

			// Built up then emitted in ONE write: four separate Fprintf calls
			// could tear across a partial write and leave a half-line.
			line := fmt.Sprintf("Authenticated (%s)", creds.Kind())
			if creds.ServerURL != "" {
				line += fmt.Sprintf(" · server: %s", creds.ServerURL)
			}
			if cfg.RepoID != "" {
				line += fmt.Sprintf(" · repo: %s", cfg.RepoID)
			}
			u.Success("%s", line)
			return nil
		},
	}
	cmd.Flags().StringVar(&token, "token", "", "personal access token (skips the browser flow if set)")
	cmd.Flags().StringVar(&apiKey, "api-key", "", "DEPRECATED: legacy shared API key. Use --token instead")
	cmd.Flags().StringVar(&serverURL, "server", "", "server URL (default: https://api.contexo.live)")
	cmd.Flags().StringVar(&dashboardURL, "dashboard", "", "dashboard URL used by the browser flow (default: https://app.contexo.live)")
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
			u := ui.From(cmd)
			creds, err := config.LoadCredentials(root)
			if err != nil || creds == nil {
				u.Warn("Not authenticated")
				u.Hint("try", "ctx auth login")
				return nil
			}
			tok := creds.Bearer()
			masked := tok
			if len(masked) > 8 {
				masked = masked[:4] + "..." + masked[len(masked)-4:]
			}

			u.Eyebrow("AUTH")
			u.Field("status", "authenticated")
			if creds.ServerURL != "" {
				u.Field("server", creds.ServerURL)
			}
			if creds.UserName != "" {
				u.Field("user", fmt.Sprintf("%s <%s>", creds.UserName, creds.UserEmail))
			}
			u.Field("token", fmt.Sprintf("%s (%s)", masked, creds.Kind()))
			return nil
		},
	}
}
