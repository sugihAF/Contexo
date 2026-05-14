package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/config"
)

func newRemoteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remote",
		Short: "Configure the CtxHub server URL and repo ID",
	}
	cmd.AddCommand(newRemoteSetCmd())
	cmd.AddCommand(newRemoteGetCmd())
	cmd.AddCommand(newRemoteSetRepoCmd())
	return cmd
}

func newRemoteSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <url>",
		Short: "Set the CtxHub server URL",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()
			cfg, err := config.LoadHub(root)
			if err != nil {
				return err
			}
			cfg.ServerURL = args[0]
			if err := config.SaveHub(root, cfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Server: %s\n", cfg.ServerURL)
			return nil
		},
	}
}

func newRemoteSetRepoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set-repo <repo_id>",
		Short: "Set the repo_id for this project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()
			cfg, err := config.LoadHub(root)
			if err != nil {
				return err
			}
			cfg.RepoID = args[0]
			if err := config.SaveHub(root, cfg); err != nil {
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
			cfg, err := config.LoadHub(root)
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
