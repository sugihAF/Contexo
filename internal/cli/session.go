package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/store"
	"github.com/sugihAF/contexo/internal/store/jsonl"
)

func newSessionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Browse captured sessions",
	}

	cmd.AddCommand(newSessionLsCmd())
	cmd.AddCommand(newSessionShowCmd())
	cmd.AddCommand(newSessionTailCmd())

	return cmd
}

func newSessionLsCmd() *cobra.Command {
	var feature string

	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List sessions",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()
			db, err := openDB(root)
			if err != nil {
				return err
			}
			defer db.Close()

			filter := store.SessionFilter{Feature: feature}
			sessions, err := db.ListSessions(context.Background(), filter)
			if err != nil {
				return err
			}

			if len(sessions) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No sessions found")
				return nil
			}

			fmt.Fprintf(cmd.OutOrStdout(), "%-38s %-15s %-24s %s\n", "ID", "SOURCE", "STARTED", "EVENTS")
			for _, s := range sessions {
				fmt.Fprintf(cmd.OutOrStdout(), "%-38s %-15s %-24s %d\n",
					s.ID, s.Source, s.StartedAt.Format("2006-01-02 15:04:05"), s.EventCount)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&feature, "feature", "", "filter by feature")
	return cmd
}

func newSessionShowCmd() *cobra.Command {
	var turns string

	cmd := &cobra.Command{
		Use:   "show <session-id>",
		Short: "Show session events",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()
			sessionID := args[0]

			db, err := openDB(root)
			if err != nil {
				return err
			}
			defer db.Close()

			session, err := db.GetSession(context.Background(), sessionID)
			if err != nil || session == nil {
				return fmt.Errorf("session not found: %s", sessionID)
			}

			// Find JSONL file
			ctxDir := config.CtxDirPath(root)
			jsonlPath := filepath.Join(ctxDir, "sessions", session.Source, sessionID+".jsonl")

			reader := jsonl.NewReader(jsonlPath)

			fromTurn, toTurn := parseTurnRange(turns)
			events, err := reader.ReadRange(fromTurn, toTurn)
			if err != nil {
				return fmt.Errorf("read session: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Session: %s (source: %s)\n", session.ID, session.Source)
			fmt.Fprintf(cmd.OutOrStdout(), "Started: %s\n\n", session.StartedAt.Format("2006-01-02 15:04:05"))

			for _, evt := range events {
				fmt.Fprintf(cmd.OutOrStdout(), "[Turn %d] %s (%s):\n", evt.Turn, evt.Type, evt.Actor.Role)
				fmt.Fprintf(cmd.OutOrStdout(), "  %s\n\n", evt.Content.Text)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&turns, "turns", "", "turn range (e.g. 5-10)")
	return cmd
}

func newSessionTailCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tail <session-id>",
		Short: "Show last events of a session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()
			sessionID := args[0]

			db, err := openDB(root)
			if err != nil {
				return err
			}
			defer db.Close()

			session, err := db.GetSession(context.Background(), sessionID)
			if err != nil || session == nil {
				return fmt.Errorf("session not found: %s", sessionID)
			}

			ctxDir := config.CtxDirPath(root)
			jsonlPath := filepath.Join(ctxDir, "sessions", session.Source, sessionID+".jsonl")
			reader := jsonl.NewReader(jsonlPath)

			events, err := reader.ReadAll()
			if err != nil {
				return fmt.Errorf("read session: %w", err)
			}

			// Show last 10 events
			start := 0
			if len(events) > 10 {
				start = len(events) - 10
			}

			for _, evt := range events[start:] {
				fmt.Fprintf(cmd.OutOrStdout(), "[Turn %d] %s: %s\n", evt.Turn, evt.Type, evt.Content.Text)
			}
			return nil
		},
	}
}

func parseTurnRange(s string) (int, int) {
	if s == "" {
		return 0, 0
	}
	parts := strings.SplitN(s, "-", 2)
	from, _ := strconv.Atoi(parts[0])
	to := 0
	if len(parts) > 1 {
		to, _ = strconv.Atoi(parts[1])
	}
	return from, to
}
