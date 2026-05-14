package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/store"
)

func newCommitCmd() *cobra.Command {
	var (
		message     string
		feature     string
		fromSession string
		turns       string
		summaries   []string
		decisions   []string
		nextSteps   []string
		author      string
		branch      string
	)

	cmd := &cobra.Command{
		Use:   "commit",
		Short: "Create a context commit",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()
			db, err := openDB(root)
			if err != nil {
				return err
			}
			defer db.Close()

			commitID := uuid.Must(uuid.NewV7()).String()
			now := time.Now().UTC()

			commit := &schema.ContextCommit{
				Schema:    "ctx.commit.v1",
				CommitID:  commitID,
				Title:     message,
				Feature:   feature,
				CreatedAt: now,
				Summary:   summaries,
				NextSteps: nextSteps,
				Branch:    branch,
			}

			// Parse author "name:tool" format
			if author != "" {
				parts := strings.SplitN(author, ":", 2)
				commit.Author.Name = parts[0]
				if len(parts) == 2 {
					commit.Author.Tool = parts[1]
				}
			}

			// Parse decisions "description:rationale" format
			for _, d := range decisions {
				parts := strings.SplitN(d, ":", 2)
				dec := schema.Decision{Description: parts[0]}
				if len(parts) == 2 {
					dec.Rationale = parts[1]
				}
				commit.Decisions = append(commit.Decisions, dec)
			}

			// Auto-select most recent session as evidence if not specified
			if fromSession == "" {
				sessions, err := db.ListSessions(context.Background(), store.SessionFilter{Limit: 1})
				if err == nil && len(sessions) > 0 {
					fromSession = sessions[0].ID
				}
			}

			if fromSession != "" {
				fromTurn, toTurn := parseTurnRange(turns)
				commit.Evidence = []schema.Evidence{
					{
						SessionID: fromSession,
						FromTurn:  fromTurn,
						ToTurn:    toTurn,
					},
				}
			}

			if err := db.CreateCommit(context.Background(), commit); err != nil {
				return fmt.Errorf("commit: create: %w", err)
			}

			// Also write commit JSON to .ctx/commits/
			ctxDir := config.CtxDirPath(root)
			commitDir := filepath.Join(ctxDir, "commits")
			os.MkdirAll(commitDir, 0o755)

			data, _ := json.MarshalIndent(commit, "", "  ")
			commitPath := filepath.Join(commitDir, commitID+".json")
			os.WriteFile(commitPath, data, 0o644)

			fmt.Fprintf(cmd.OutOrStdout(), "Created context commit: %s\n", commitID)
			fmt.Fprintf(cmd.OutOrStdout(), "  Title: %s\n", message)
			return nil
		},
	}

	cmd.Flags().StringVarP(&message, "message", "m", "", "commit title/message (required)")
	cmd.MarkFlagRequired("message")
	cmd.Flags().StringVar(&feature, "feature", "", "feature name")
	cmd.Flags().StringVar(&fromSession, "from-session", "", "evidence session ID")
	cmd.Flags().StringVar(&turns, "turns", "", "evidence turn range (e.g. 5-10)")
	cmd.Flags().StringArrayVar(&summaries, "summary", nil, "summary bullet point (repeatable)")
	cmd.Flags().StringArrayVar(&decisions, "decision", nil, "decision as 'description:rationale' (repeatable)")
	cmd.Flags().StringArrayVar(&nextSteps, "next-step", nil, "next step (repeatable)")
	cmd.Flags().StringVar(&author, "author", "", "author as 'name:tool'")
	cmd.Flags().StringVar(&branch, "branch", "", "branch name")

	return cmd
}
