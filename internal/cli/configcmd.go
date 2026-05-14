package cli

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/config"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage project configuration",
	}

	cmd.AddCommand(newConfigSetCmd())
	cmd.AddCommand(newConfigGetCmd())

	return cmd
}

func newConfigSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a configuration value",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()
			key := args[0]
			value := args[1]

			cfg, err := config.Load(root)
			if err != nil {
				return err
			}

			switch key {
			case "server_url":
				cfg.ServerURL = value
			case "repo_id":
				cfg.RepoID = value
			case "default_client":
				cfg.DefaultClient = value
			case "redaction_level":
				cfg.RedactionLevel = value
			case "remote_name":
				cfg.RemoteName = value
			case "recorder_port":
				port, err := strconv.Atoi(value)
				if err != nil {
					return fmt.Errorf("invalid port number: %s", value)
				}
				cfg.RecorderPort = port
			default:
				return fmt.Errorf("unknown config key: %s\nValid keys: server_url, repo_id, default_client, redaction_level, remote_name, recorder_port", key)
			}

			if err := config.Save(root, cfg); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Set %s = %s\n", key, value)
			return nil
		},
	}
}

func newConfigGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get [key]",
		Short: "Get a configuration value (or all if no key given)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()

			cfg, err := config.Load(root)
			if err != nil {
				return err
			}

			if len(args) == 0 {
				// Show all
				data, _ := json.MarshalIndent(cfg, "", "  ")
				fmt.Fprintln(cmd.OutOrStdout(), string(data))
				return nil
			}

			key := args[0]
			var value interface{}

			switch key {
			case "server_url":
				value = cfg.ServerURL
			case "repo_id":
				value = cfg.RepoID
			case "default_client":
				value = cfg.DefaultClient
			case "redaction_level":
				value = cfg.RedactionLevel
			case "remote_name":
				value = cfg.RemoteName
			case "recorder_port":
				value = cfg.RecorderPort
			case "version":
				value = cfg.Version
			default:
				return fmt.Errorf("unknown config key: %s", key)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "%v\n", value)
			return nil
		},
	}
}
