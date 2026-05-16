package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/config"
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
		token     string
		apiKey    string
		serverURL string
		repoID    string
		userName  string
		userEmail string
	)
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate with a Contexo server",
		Long: `Authenticate with a Contexo server.

The preferred path is --token: paste a personal access token minted from the
web dashboard at Settings → New token. The legacy --api-key flag still works
for the shared CONTEXO_API_KEY but is deprecated.`,
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
				fmt.Fprint(cmd.OutOrStdout(), "Token: ")
				reader := bufio.NewReader(os.Stdin)
				input, err := reader.ReadString('\n')
				if err != nil {
					return fmt.Errorf("auth: read input: %w", err)
				}
				chosen = strings.TrimSpace(input)
			}
			if chosen == "" {
				return fmt.Errorf("a token is required (--token or interactive prompt)")
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
	cmd.Flags().StringVar(&token, "token", "", "personal access token (paste from dashboard → Settings)")
	cmd.Flags().StringVar(&apiKey, "api-key", "", "DEPRECATED: legacy shared API key. Use --token instead")
	cmd.Flags().StringVar(&serverURL, "server", "", "server URL")
	cmd.Flags().StringVar(&repoID, "repo", "", "repo_id on the server")
	cmd.Flags().StringVar(&userName, "name", "", "your display name (used as commit author)")
	cmd.Flags().StringVar(&userEmail, "email", "", "your email (used as commit author)")
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
