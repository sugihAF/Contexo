package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/sync"
)

func newRemoteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remote",
		Short: "Configure the Contexo server URL and repo ID",
	}
	cmd.AddCommand(newRemoteSetCmd())
	cmd.AddCommand(newRemoteGetCmd())
	cmd.AddCommand(newRemoteSetRepoCmd())
	return cmd
}

func newRemoteSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <url>",
		Short: "Set the Contexo server URL",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()
			cfg, err := config.Load(root)
			if err != nil {
				return err
			}
			cfg.ServerURL = args[0]
			if err := config.Save(root, cfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Server: %s\n", cfg.ServerURL)
			return nil
		},
	}
}

func newRemoteSetRepoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set-repo [repo_id]",
		Short: "Set the repo_id for this project (interactive picker if omitted)",
		Long: "Set the repo_id this project syncs against. If no repo_id is given and stdin " +
			"is a terminal, fetches the list of repos you're a member of and presents a picker.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()
			cfg, err := config.Load(root)
			if err != nil {
				return err
			}

			var chosen string
			switch {
			case len(args) == 1:
				chosen = args[0]
			case !stdinIsTTY():
				return fmt.Errorf("set-repo requires a <repo_id> when stdin is not a terminal")
			default:
				creds, err := config.LoadCredentials(root)
				if err != nil || creds == nil || creds.Bearer() == "" {
					return fmt.Errorf("set-repo: no credentials yet — run 'ctx login --token <ctxp_...>' first, or pass a <repo_id> directly")
				}
				serverURL := chooseServerURL(creds, cfg)
				if serverURL == "" {
					return fmt.Errorf("set-repo: no server URL configured — run 'ctx remote set <url>' first, or pass a <repo_id> directly")
				}
				client := sync.NewClient(serverURL, creds.Bearer())
				chosen, err = selectRepoInteractive(client, cmd.OutOrStdout())
				if err != nil {
					return err
				}
			}

			cfg.RepoID = chosen
			if err := config.Save(root, cfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Repo: %s\n", cfg.RepoID)
			return nil
		},
	}
}

func newRemoteGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get",
		Short: "Show the configured server URL and repo",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()
			cfg, err := config.Load(root)
			if err != nil {
				return err
			}
			if cfg.ServerURL == "" {
				fmt.Fprintln(cmd.OutOrStdout(), "No server configured (run 'ctx remote set <url>')")
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Server: %s\n", cfg.ServerURL)
			}
			if cfg.RepoID == "" {
				fmt.Fprintln(cmd.OutOrStdout(), "No repo configured (run 'ctx remote set-repo <id>')")
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Repo:   %s\n", cfg.RepoID)
			}
			return nil
		},
	}
}
