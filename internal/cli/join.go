package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/ui"

	"github.com/sugihAF/contexo/internal/config"
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
			u := ui.From(cmd)
			cfg, err := config.Load(root)
			if err != nil {
				return err
			}
			// No repo yet is the point of joining, so only auth and the
			// server are required.
			_, client, err := openRemote(root, "join", false)
			if err != nil {
				return err
			}

			var key string
			if len(args) == 1 {
				key = args[0]
			} else {
				// Reads cmd.InOrStdin(), not os.Stdin: the old direct read
				// could not be redirected or tested.
				input, err := u.Prompter(cmd.InOrStdin()).Line("Invite key")
				if err != nil {
					return fmt.Errorf("join: read input: %w", err)
				}
				key = strings.TrimSpace(input)
			}
			if key == "" {
				return fmt.Errorf("an invite key is required")
			}

			repoID, role, err := client.JoinRepo(key)
			if err != nil {
				return err
			}

			cfg.RepoID = repoID
			if err := config.Save(root, cfg); err != nil {
				return err
			}

			u.Success("Joined %s as %s.", repoID, role)
			u.Hint("run", "ctx pull")
			return nil
		},
	}
}
