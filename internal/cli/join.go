package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/sync"
)

func newJoinCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "join [invite-key]",
		Short: "Join an existing Contexo repo using an invite key",
		Long: `Join an existing Contexo repo using an invite key minted from the
dashboard. The current project's .contexo/config.json is updated to point
at the joined repo, so 'ctx push' / 'ctx pull' work immediately.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()
			cfg, _ := config.Load(root)
			creds, _ := config.LoadCredentials(root)
			if creds == nil || creds.Bearer() == "" {
				return fmt.Errorf("join: not authenticated — run 'ctx login --token <token>' first")
			}
			if cfg.ServerURL == "" {
				return fmt.Errorf("join: server URL not set — run 'ctx remote set <url>' first")
			}

			var key string
			if len(args) == 1 {
				key = args[0]
			} else {
				fmt.Fprint(cmd.OutOrStdout(), "Invite key: ")
				input, err := bufio.NewReader(os.Stdin).ReadString('\n')
				if err != nil {
					return fmt.Errorf("join: read input: %w", err)
				}
				key = strings.TrimSpace(input)
			}
			if key == "" {
				return fmt.Errorf("an invite key is required")
			}

			client := sync.NewClient(cfg.ServerURL, creds.Bearer())
			repoID, role, err := client.JoinRepo(key)
			if err != nil {
				return err
			}

			cfg.RepoID = repoID
			if err := config.Save(root, cfg); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Joined %s as %s. 'ctx pull' to fetch existing pages.\n", repoID, role)
			return nil
		},
	}
}
