package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/ui"
)

func newMembersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "members",
		Short: "List and manage who can access the current repo",
		Long: `List the members of the current repo, or remove one.

'ctx members' prints everyone with access (email, role, and when they joined).
'ctx members remove <email>' removes a member — only an owner may do this, and
the last owner cannot be removed.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, repoID, err := repoClient("members")
			if err != nil {
				return err
			}
			members, err := client.ListMembers(repoID)
			if err != nil {
				return err
			}
			renderMembers(cmd.OutOrStdout(), members)
			return nil
		},
	}
	cmd.AddCommand(newMembersRemoveCmd())
	return cmd
}

func newMembersRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <email>",
		Short: "Remove a member from the current repo (owner only)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, repoID, err := repoClient("members")
			if err != nil {
				return err
			}
			return runMembersRemove(cmd.OutOrStdout(), client, repoID, args[0])
		},
	}
}

// renderMembers writes one aligned row per member: email, role, join date.
func renderMembers(w io.Writer, members []sync.Member) {
	u := ui.Plain(w)
	if len(members) == 0 {
		fmt.Fprintln(u.Out(), u.Style(ui.FG3, "no members"))
		return
	}
	t := u.Table("MEMBER", "ROLE", "JOINED")
	for _, m := range members {
		joined := time.Unix(m.AddedAt, 0).UTC().Format("2006-01-02")
		t.Row(m.Email, m.Role, joined)
	}
	t.Render()
}

// runMembersRemove resolves email to a user id from the member list, then asks
// the server to remove them, mapping known refusals to friendly messages.
func runMembersRemove(w io.Writer, client *sync.Client, repoID, email string) error {
	members, err := client.ListMembers(repoID)
	if err != nil {
		return err
	}
	userID, err := resolveMemberID(members, email)
	if err != nil {
		return err
	}
	if err := client.RemoveMember(repoID, userID); err != nil {
		return friendlyRemoveErr(err)
	}
	ui.Plain(w).Success("removed %s", email)
	return nil
}

// resolveMemberID finds the user id for an email (case-insensitive) among the
// repo's members.
func resolveMemberID(members []sync.Member, email string) (string, error) {
	for _, m := range members {
		if strings.EqualFold(m.Email, email) {
			return m.UserID, nil
		}
	}
	return "", fmt.Errorf("no member with email %q on this repo", email)
}

// friendlyRemoveErr turns the sync sentinel errors into messages a CLI user
// can act on; anything else passes through unchanged.
func friendlyRemoveErr(err error) error {
	switch {
	case errors.Is(err, sync.ErrNotOwner):
		return fmt.Errorf("only an owner can remove members")
	case errors.Is(err, sync.ErrMemberNotFound):
		return fmt.Errorf("not a member of this repo")
	case errors.Is(err, sync.ErrLastOwner):
		return fmt.Errorf("can't remove the last owner")
	default:
		return err
	}
}
