package cli

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/store/jsonl"
)

func newOpenSessionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "open-session <commit-id>",
		Short: "Open the evidence session for a context commit",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()
			db, err := openDB(root)
			if err != nil {
				return err
			}
			defer db.Close()

			commitID := args[0]
			commit, err := db.GetCommit(context.Background(), commitID)
			if err != nil {
				return err
			}
			if commit == nil {
				return fmt.Errorf("commit not found: %s", commitID)
			}

			if len(commit.Evidence) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No evidence sessions linked to this commit")
				return nil
			}

			// Open each evidence session
			ctxDir := config.CtxDirPath(root)

			for _, ev := range commit.Evidence {
				fmt.Fprintf(cmd.OutOrStdout(), "Evidence: session %s", ev.SessionID)
				if ev.FromTurn > 0 || ev.ToTurn > 0 {
					fmt.Fprintf(cmd.OutOrStdout(), " (turns %d-%d)", ev.FromTurn, ev.ToTurn)
				}
				fmt.Fprintln(cmd.OutOrStdout())

				// Try to find the session to get its source
				session, _ := db.GetSession(context.Background(), ev.SessionID)
				source := "unknown"
				if session != nil {
					source = session.Source
				}
				if ev.Source != "" {
					source = ev.Source
				}

				jsonlPath := filepath.Join(ctxDir, "sessions", source, ev.SessionID+".jsonl")
				reader := jsonl.NewReader(jsonlPath)

				events, err := reader.ReadRange(ev.FromTurn, ev.ToTurn)
				if err != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "  (session file not found: %s)\n\n", jsonlPath)
					continue
				}

				if len(events) == 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "  (no events in range)")
					continue
				}

				fmt.Fprintln(cmd.OutOrStdout())
				for _, evt := range events {
					fmt.Fprintf(cmd.OutOrStdout(), "[Turn %d] %s (%s):\n", evt.Turn, evt.Type, evt.Actor.Role)
					fmt.Fprintf(cmd.OutOrStdout(), "  %s\n\n", evt.Content.Text)
				}
			}

			return nil
		},
	}
}
