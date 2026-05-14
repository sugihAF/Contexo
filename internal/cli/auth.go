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
	)

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate with a CtxHub server",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()

			// If API key not provided via flag, prompt
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

			// Resolve server URL from flag, config, or remotes
			if serverURL == "" {
				cfg, err := config.Load(root)
				if err == nil {
					if cfg.ServerURL != "" {
						serverURL = cfg.ServerURL
					} else if len(cfg.Remotes) > 0 {
						serverURL = cfg.Remotes[0].URL
					}
				}
			}

			creds := &config.Credentials{
				APIKey:    apiKey,
				ServerURL: serverURL,
			}

			if err := config.SaveCredentials(root, creds); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Authenticated successfully")
			if serverURL != "" {
				fmt.Fprintf(cmd.OutOrStdout(), " (server: %s)", serverURL)
			}
			fmt.Fprintln(cmd.OutOrStdout())
			return nil
		},
	}

	cmd.Flags().StringVar(&apiKey, "api-key", "", "API key (will prompt if not provided)")
	cmd.Flags().StringVar(&serverURL, "server", "", "server URL")

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
			// Mask API key
			masked := creds.APIKey
			if len(masked) > 8 {
				masked = masked[:4] + "..." + masked[len(masked)-4:]
			}
			fmt.Fprintf(cmd.OutOrStdout(), "API Key: %s\n", masked)
			return nil
		},
	}
}
