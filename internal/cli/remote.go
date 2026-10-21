package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/config"
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
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Server: %s\n", cfg.ServerURL)
			// Best-effort: credentials never follow a workspace URL, so say
			// where they will (not) go. A resolution error is ignored here;
			// the next networked command reports it.
			if rr, err := config.ResolveRemote(root); err == nil && rr.Token != "" {
				switch {
				case rr.Mismatch:
					fmt.Fprintf(out, "note: your credentials belong to %s; ctx keeps sending them there, not to %s. To use this server, log in with its own dashboard: 'ctx auth login --server %s --dashboard <its dashboard URL>' (this replaces your global credentials).\n",
						rr.ServerURL, cfg.ServerURL, cfg.ServerURL)
				case rr.ServerURL == "":
					fmt.Fprintf(out, "note: your %s credentials have no server URL, so ctx will not send them to %s. To use this server, log in with its own dashboard: 'ctx auth login --server %s --dashboard <its dashboard URL>' (this replaces your global credentials).\n",
						rr.CredsSource, cfg.ServerURL, cfg.ServerURL)
				}
			}
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
				chosen, err = pickRepo(root, "set-repo", cmd.InOrStdin(), cmd.OutOrStdout())
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

// pickRepo fetches the repos the user belongs to from the resolved server and
// presents the interactive picker. Shared by 'ctx remote set-repo' and the
// login flow; name prefixes the resolution errors.
func pickRepo(root, name string, in io.Reader, out io.Writer) (string, error) {
	_, client, err := openRemote(root, name, false)
	if err != nil {
		return "", err
	}
	return selectRepoInteractive(client, in, out)
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
