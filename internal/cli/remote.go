package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/config"
)

func newRemoteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remote",
		Short: "Manage remote servers",
	}

	cmd.AddCommand(newRemoteAddCmd())
	cmd.AddCommand(newRemoteLsCmd())

	return cmd
}

func newRemoteAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add <name> <url>",
		Short: "Add a remote server",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()
			name := args[0]
			url := args[1]

			cfg, err := config.Load(root)
			if err != nil {
				return err
			}

			// Check for duplicate
			for _, r := range cfg.Remotes {
				if r.Name == name {
					return fmt.Errorf("remote '%s' already exists", name)
				}
			}

			cfg.Remotes = append(cfg.Remotes, config.Remote{Name: name, URL: url})

			// If this is the first remote, set it as default and update ServerURL
			if cfg.RemoteName == "" {
				cfg.RemoteName = name
				cfg.ServerURL = url
			}

			if err := config.Save(root, cfg); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Remote '%s' added: %s\n", name, url)
			return nil
		},
	}
}

func newRemoteLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List remotes",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()
			cfg, err := config.Load(root)
			if err != nil {
				return err
			}

			if len(cfg.Remotes) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No remotes configured")
				return nil
			}

			for _, r := range cfg.Remotes {
				marker := " "
				if r.Name == cfg.RemoteName {
					marker = "*"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s %s\t%s\n", marker, r.Name, r.URL)
			}
			return nil
		},
	}
}
