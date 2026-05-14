package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/schema"
)

// codexOutputLine represents a line from `codex --json` output.
type codexOutputLine struct {
	Type    string `json:"type"`
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
	Message string `json:"message,omitempty"`
}

func newCodexCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "codex",
		Short: "Codex integration commands",
	}

	cmd.AddCommand(newCodexExecCmd())
	return cmd
}

func newCodexExecCmd() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "exec <task>",
		Short: "Run a Codex task and capture output",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			task := args[0]

			if dryRun {
				fmt.Fprintf(cmd.OutOrStdout(), "Would run: codex --json \"%s\"\n", task)
				return nil
			}

			return runCodexCapture(cmd, task)
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show command without running")

	return cmd
}

func runCodexCapture(cmd *cobra.Command, task string) error {
	sessionID := uuid.Must(uuid.NewV7()).String()
	now := time.Now().UTC()

	fmt.Fprintf(cmd.OutOrStdout(), "Starting Codex session: %s\n", sessionID)
	fmt.Fprintf(cmd.OutOrStdout(), "Task: %s\n\n", task)

	// Execute codex with --json flag
	codexCmd := exec.Command("codex", "--json", task)
	stdout, err := codexCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("codex: pipe stdout: %w", err)
	}

	if err := codexCmd.Start(); err != nil {
		return fmt.Errorf("codex: start: %w", err)
	}

	// Parse JSON lines from codex output
	var events []*schema.SessionEvent
	turn := 0
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Bytes()
		var out codexOutputLine
		if err := json.Unmarshal(line, &out); err != nil {
			continue
		}

		turn++
		content := out.Content
		if content == "" {
			content = out.Message
		}

		evt := &schema.SessionEvent{
			Schema:  "ctx.session_event.v1",
			EventID: uuid.Must(uuid.NewV7()).String(),
			Ts:      time.Now().UTC(),
			Session: schema.SessionRef{
				ID:     sessionID,
				Source: "codex",
			},
			Type: out.Type,
			Turn: turn,
			Actor: schema.ActorRef{
				Role: out.Role,
			},
			Content: schema.Content{
				Text: content,
			},
		}
		events = append(events, evt)
		fmt.Fprintf(cmd.OutOrStdout(), "[%s] %s\n", out.Type, content)
	}

	if err := codexCmd.Wait(); err != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "\nCodex exited with error: %v\n", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "\nSession %s: %d events captured (started: %s)\n",
		sessionID, len(events), now.Format(time.RFC3339))
	return nil
}

// ParseCodexOutput parses codex --json output lines into session events.
// Exported for testing.
func ParseCodexOutput(sessionID string, reader io.Reader) ([]*schema.SessionEvent, error) {
	var events []*schema.SessionEvent
	turn := 0
	scanner := bufio.NewScanner(reader)

	for scanner.Scan() {
		line := scanner.Bytes()
		var out codexOutputLine
		if err := json.Unmarshal(line, &out); err != nil {
			continue
		}

		turn++
		content := out.Content
		if content == "" {
			content = out.Message
		}

		evt := &schema.SessionEvent{
			Schema:  "ctx.session_event.v1",
			EventID: uuid.Must(uuid.NewV7()).String(),
			Ts:      time.Now().UTC(),
			Session: schema.SessionRef{
				ID:     sessionID,
				Source: "codex",
			},
			Type: out.Type,
			Turn: turn,
			Actor: schema.ActorRef{
				Role: out.Role,
			},
			Content: schema.Content{
				Text: content,
			},
		}
		events = append(events, evt)
	}

	return events, scanner.Err()
}
