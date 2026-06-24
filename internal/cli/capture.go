package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/capture"
	"github.com/sugihAF/contexo/internal/config"
)

// hookPayload mirrors the hook JSON written to stdin by Claude Code and Codex.
// We tolerate missing fields and fall back to CLI flags when absent.
type hookPayload struct {
	HookEventName  string `json:"hook_event_name,omitempty"`
	SessionID      string `json:"session_id,omitempty"`
	TranscriptPath string `json:"transcript_path,omitempty"`
	CWD            string `json:"cwd,omitempty"`
	// Codex inline-capture fields: UserPromptSubmit carries the prompt, Stop
	// carries the assistant's final message (no transcript parsing needed).
	Prompt               string `json:"prompt,omitempty"`
	LastAssistantMessage string `json:"last_assistant_message,omitempty"`
	// Cursor inline-capture fields: beforeSubmitPrompt carries `prompt` (shared
	// above), afterAgentResponse carries `text`; Cursor keys sessions by
	// conversation_id (it has no session_id).
	Text           string `json:"text,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`
}

func newCaptureCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "capture",
		Short: "Capture utilities for the agent-reasoning buffer",
	}
	cmd.AddCommand(newCaptureTurnCmd())
	cmd.AddCommand(newCaptureStatusCmd())
	return cmd
}

func newCaptureTurnCmd() *cobra.Command {
	var (
		flagAgent      string
		flagSession    string
		flagTranscript string
		flagCWD        string
	)
	cmd := &cobra.Command{
		Use:   "turn",
		Short: "Append the latest assistant turn to the local capture buffer (Stop-hook target)",
		Long: "Reads a hook's stdin payload (or accepts --session/--transcript/--cwd flags) and " +
			"appends one record to .contexo/raw/sessions/_pending/<session-id>.jsonl.\n\n" +
			"--agent claude (default) parses the Stop-hook transcript. --agent codex pairs the " +
			"UserPromptSubmit prompt with the Stop hook's last_assistant_message (no transcript " +
			"parsing). No LLM call. Silently no-ops outside a .contexo project, or when " +
			"CONTEXO_CAPTURE_DISABLE=1.",
		Hidden:       true,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCaptureTurn(cmd, flagAgent, flagSession, flagTranscript, flagCWD)
		},
	}
	cmd.Flags().StringVar(&flagAgent, "agent", "claude", "source agent: claude|codex|cursor")
	cmd.Flags().StringVar(&flagSession, "session", "", "session id (falls back to stdin payload)")
	cmd.Flags().StringVar(&flagTranscript, "transcript", "", "path to the session transcript JSONL (falls back to stdin payload)")
	cmd.Flags().StringVar(&flagCWD, "cwd", "", "working directory used to locate .contexo (falls back to stdin payload, then os.Getwd)")
	return cmd
}

func runCaptureTurn(cmd *cobra.Command, flagAgent, flagSession, flagTranscript, flagCWD string) error {
	if os.Getenv("CONTEXO_CAPTURE_DISABLE") == "1" {
		return nil
	}

	payload := readStdinPayload(cmd.InOrStdin())
	sessionID := firstNonEmpty(flagSession, payload.SessionID, payload.ConversationID)
	transcriptPath := firstNonEmpty(flagTranscript, payload.TranscriptPath)
	cwd := firstNonEmpty(flagCWD, payload.CWD)

	if cwd == "" {
		got, err := os.Getwd()
		if err != nil {
			return nil // cannot recover, but don't fail the agent's turn
		}
		cwd = got
	}

	projectRoot := findContexoRoot(cwd)
	if projectRoot == "" {
		return nil // not a Contexo project — silent no-op
	}
	if sessionID == "" {
		sessionID = "unknown-" + time.Now().UTC().Format("20060102")
	}
	contexoDir := config.ContexoDirPath(projectRoot)

	ex, done, err := extractExchange(flagAgent, payload, transcriptPath, contexoDir, sessionID)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "ctx capture turn: %v\n", err)
		return nil
	}
	if done {
		return nil // event handled with nothing to append yet (e.g. Codex prompt stashed)
	}
	if ex.User == "" && ex.Assistant == "" {
		return nil
	}

	buf := capture.Open(contexoDir, sessionID)
	rec := capture.TurnRecord{
		User:      ex.User,
		Assistant: ex.Assistant,
		Tools:     ex.Tools,
	}
	if err := buf.AppendTurn(rec); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "ctx capture turn: append: %v\n", err)
		return nil
	}
	if _, err := capture.PruneOlderThan(contexoDir, 30*24*time.Hour); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "ctx capture turn: prune: %v\n", err)
	}
	return nil
}

// extractExchange produces the exchange to buffer for the given agent. done=true
// means the event was handled with nothing to append yet (e.g. a Codex prompt
// was stashed to pair with a later Stop).
func extractExchange(agent string, payload hookPayload, transcriptPath, contexoDir, sessionID string) (capture.Exchange, bool, error) {
	switch agent {
	case "codex":
		// Codex gives us the turn inline across two hooks — no transcript parse.
		if payload.HookEventName == "UserPromptSubmit" {
			if err := capture.WritePendingPrompt(contexoDir, sessionID, payload.Prompt); err != nil {
				return capture.Exchange{}, true, err
			}
			return capture.Exchange{}, true, nil // stashed; pair on the next Stop
		}
		// Stop (or any terminal event): pair the stashed prompt with the reply.
		prompt, _ := capture.TakePendingPrompt(contexoDir, sessionID)
		return capture.Exchange{User: prompt, Assistant: payload.LastAssistantMessage}, false, nil
	case "cursor":
		// Cursor mirrors Codex: beforeSubmitPrompt stashes the prompt,
		// afterAgentResponse pairs it with the inline `text`.
		if payload.HookEventName == "beforeSubmitPrompt" {
			if err := capture.WritePendingPrompt(contexoDir, sessionID, payload.Prompt); err != nil {
				return capture.Exchange{}, true, err
			}
			return capture.Exchange{}, true, nil
		}
		prompt, _ := capture.TakePendingPrompt(contexoDir, sessionID)
		return capture.Exchange{User: prompt, Assistant: payload.Text}, false, nil
	default: // claude: parse the Stop-hook transcript
		ex, err := capture.LatestExchange(transcriptPath)
		return ex, false, err
	}
}

func newCaptureStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show pending capture buffers",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()
			contexoDir := config.ContexoDirPath(root)
			bs, err := capture.List(contexoDir)
			if err != nil {
				return err
			}
			if len(bs) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No pending capture buffers.")
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Pending capture buffers (most-recent first):\n")
			for _, b := range bs {
				recs, _ := b.Records()
				info, _ := os.Stat(b.Path())
				age := "unknown"
				if info != nil {
					age = time.Since(info.ModTime()).Truncate(time.Minute).String()
				}
				fmt.Fprintf(cmd.OutOrStdout(), "  %s  (%d turns, last write %s ago)\n", b.SessionID, len(recs), age)
			}
			return nil
		},
	}
}

func readStdinPayload(r io.Reader) hookPayload {
	var p hookPayload
	if r == nil {
		return p
	}
	data, err := io.ReadAll(r)
	if err != nil || len(data) == 0 {
		return p
	}
	// Best-effort parse; ignore non-JSON stdin.
	_ = json.Unmarshal(data, &p)
	return p
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// findContexoRoot walks up from start to find a directory containing a
// .contexo/ entry. Returns "" if none found before reaching the filesystem
// root.
func findContexoRoot(start string) string {
	cur, err := filepath.Abs(start)
	if err != nil {
		return ""
	}
	for {
		if info, err := os.Stat(filepath.Join(cur, config.ContexoDir)); err == nil && info.IsDir() {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}
