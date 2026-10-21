package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/ui"

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
			out := cmd.OutOrStdout()
			// The token is a secret the user copy-pastes: it is written raw on
			// its own line, never styled, truncated, or wrapped. Only the
			// surrounding metadata gets any tone.
			fmt.Fprintln(out, token)
			fmt.Fprintln(out, ui.From(cmd).Style(ui.FG4,
				fmt.Sprintf("expires %s%s", expires, labelPart)))
			fmt.Fprintf(out, "share this with a teammate; they run 'ctx join %s'\n", token)
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
			u := ui.From(cmd)
			if len(keys) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no invite keys")
				return nil
			}
			now := time.Now().Unix()
			// Cells stay untoned on purpose. Pre-styled cells reach tabwriter
			// with their escape bytes counted as width, and two tones of
			// different escape length in one column misalign the next.
			t := u.Table("KEY", "STATUS", "EXPIRES", "LABEL")
			for _, k := range keys {
				status := "active"
				if k.ExpiresAt <= now {
					status = "expired"
				}
				label := k.Label
				if label == "" {
					label = "(no label)"
				}
				t.Row(k.ID, status,
					time.Unix(k.ExpiresAt, 0).UTC().Format("2006-01-02 15:04 UTC"), label)
			}
			t.Render()
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
			ui.From(cmd).Success("revoked %s", args[0])
			return nil
		},
	}
}

// inviteClient is the common preflight for 'ctx invite' subcommands: resolve
// the remote, complain if the project isn't set up, and return a configured
// sync client plus the current repo id.
func inviteClient() (*sync.Client, string, error) {
	rr, client, err := openRemote(GetRootDir(), "invite", true)
	if err != nil {
		return nil, "", err
	}
	return client, rr.RepoID, nil
}
