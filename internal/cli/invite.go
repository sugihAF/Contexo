package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/sync"
)

func newInviteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "invite",
		Short: "Manage repo invite keys",
		Long: `Mint, list, or revoke repo invite keys.

An owner mints an invite key and shares the resulting 'ctxi_…' token with a
teammate; the teammate runs 'ctx join <token>' to become a member. Keys expire
after a week (or whenever an owner revokes them).`,
	}
	cmd.AddCommand(newInviteMintCmd())
	cmd.AddCommand(newInviteListCmd())
	cmd.AddCommand(newInviteRevokeCmd())
	return cmd
}

func newInviteMintCmd() *cobra.Command {
	var label string
	c := &cobra.Command{
		Use:   "mint",
		Short: "Mint a new invite key for the current repo",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, repoID, err := inviteClient()
			if err != nil {
				return err
			}
			key, token, err := client.MintInviteKey(repoID, label)
			if err != nil {
				return err
			}
			expires := time.Unix(key.ExpiresAt, 0).UTC().Format("2006-01-02 15:04 UTC")
			labelPart := ""
			if key.Label != "" {
				labelPart = fmt.Sprintf(" · label %q", key.Label)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s\nexpires %s%s\n", token, expires, labelPart)
			fmt.Fprintf(cmd.OutOrStdout(), "share this with a teammate; they run 'ctx join %s'\n", token)
			return nil
		},
	}
	c.Flags().StringVar(&label, "label", "", "Optional label to remember what the key is for")
	return c
}

func newInviteListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List active invite keys on the current repo",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, repoID, err := inviteClient()
			if err != nil {
				return err
			}
			keys, err := client.ListInviteKeys(repoID)
			if err != nil {
				return err
			}
			if len(keys) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no invite keys")
				return nil
			}
			now := time.Now().Unix()
			for _, k := range keys {
				status := "active"
				if k.ExpiresAt <= now {
					status = "expired"
				}
				expires := time.Unix(k.ExpiresAt, 0).UTC().Format("2006-01-02 15:04 UTC")
				label := k.Label
				if label == "" {
					label = "(no label)"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  expires %s  %s\n", k.ID, status, expires, label)
			}
			return nil
		},
	}
}

func newInviteRevokeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "revoke <keyId>",
		Short: "Revoke an invite key by id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, repoID, err := inviteClient()
			if err != nil {
				return err
			}
			if err := client.DeleteInviteKey(repoID, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "revoked %s\n", args[0])
			return nil
		},
	}
}

// inviteClient is the common preflight for 'ctx invite' subcommands: read
// config + credentials, complain if the project isn't set up, and return a
// configured sync client plus the current repo id.
func inviteClient() (*sync.Client, string, error) {
	root := GetRootDir()
	cfg, _ := config.Load(root)
	creds, _ := config.LoadCredentials(root)
	if creds == nil || creds.Bearer() == "" {
		return nil, "", fmt.Errorf("invite: not authenticated — run 'ctx login' first")
	}
	if cfg.ServerURL == "" {
		return nil, "", fmt.Errorf("invite: server URL not set — run 'ctx remote set <url>' first")
	}
	if cfg.RepoID == "" {
		return nil, "", fmt.Errorf("invite: repo id not set — run 'ctx remote set-repo <id>' first")
	}
	return sync.NewClient(cfg.ServerURL, creds.Bearer()), cfg.RepoID, nil
}
