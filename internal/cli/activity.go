package cli

import (
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/ui"
)

func newActivityCmd() *cobra.Command {
	var limit int
	c := &cobra.Command{
		Use:   "activity",
		Short: "Show recent push/pull activity on the current repo",
		Long: `List who has pushed or pulled the current repo, newest first.

Pulls are recorded only when pages were actually received, so routine
"already up to date" syncs don't flood the feed.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, repoID, err := repoClient("activity")
			if err != nil {
				return err
			}
			events, _, err := client.ListActivity(repoID, limit, 0)
			if err != nil {
				return err
			}
			renderActivity(cmd.OutOrStdout(), events)
			return nil
		},
	}
	c.Flags().IntVar(&limit, "limit", 50, "Maximum number of events to show")
	return c
}

// renderActivity writes one aligned row per event: email, action, timestamp.
func renderActivity(w io.Writer, events []sync.ActivityEvent) {
	u := ui.Plain(w)
	if len(events) == 0 {
		fmt.Fprintln(u.Out(), u.Style(ui.FG3, "no activity yet"))
		return
	}
	t := u.Table("WHO", "WHAT", "WHEN")
	for _, e := range events {
		when := time.Unix(e.CreatedAt, 0).UTC().Format("2006-01-02 15:04 UTC")
		t.Row(e.Email, activityVerb(e.Action), when)
	}
	t.Render()
}

// activityVerb renders a stored action as a past-tense verb for display.
func activityVerb(action string) string {
	switch action {
	case "push":
		return "pushed"
	case "pull":
		return "pulled"
	default:
		return action
	}
}
