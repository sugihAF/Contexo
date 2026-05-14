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

func newAuthLoginCmd() *cobra.Command {
	var (
		apiKey    string
		serverURL string
		repoID    string
		userName  string
		userEmail string
	)
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate with a CtxHub server",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()

			if apiKey == "" {
				fmt.Fprint(cmd.OutOrStdout(), "API Key: ")
				reader := bufio.NewReader(os.Stdin)
				input, err := reader.ReadString('\n')
				if err != nil {
					return fmt.Errorf("auth: read input: %w", err)
				}
				apiKey = strings.TrimSpace(input)
			}
			if apiKey == "" {
				return fmt.Errorf("API key is required")
			}

			cfg, err := config.LoadHub(root)
			if err != nil {
				return err
			}
			if serverURL != "" {
				cfg.ServerURL = serverURL
			}
			if repoID != "" {
				cfg.RepoID = repoID
			}
			if err := config.SaveHub(root, cfg); err != nil {
				return err
			}

			creds := &config.Credentials{
				APIKey:    apiKey,
				ServerURL: cfg.ServerURL,
				UserName:  userName,
				UserEmail: userEmail,
			}
			if err := config.SaveCredentialsHub(root, creds); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Authenticated")
			if cfg.ServerURL != "" {
				fmt.Fprintf(cmd.OutOrStdout(), " (server: %s)", cfg.ServerURL)
			}
			if cfg.RepoID != "" {
				fmt.Fprintf(cmd.OutOrStdout(), " (repo: %s)", cfg.RepoID)
			}
			fmt.Fprintln(cmd.OutOrStdout())
			return nil
		},
	}
	cmd.Flags().StringVar(&apiKey, "api-key", "", "API key (will prompt if not provided)")
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
			creds, err := config.LoadCredentialsHub(root)
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
			masked := creds.APIKey
			if len(masked) > 8 {
				masked = masked[:4] + "..." + masked[len(masked)-4:]
			}
			fmt.Fprintf(cmd.OutOrStdout(), "API Key: %s\n", masked)
			return nil
		},
	}
}
