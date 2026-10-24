package capture_test

// WP-6H.2 (defect D2, NO-GO row E12): `ctx capture turn` must buffer a
// tool-using Claude Code turn. Current Claude Code transcripts write each
// content block as its own JSONL record (thinking, tool_use and text are
// separate assistant records; tool_result blocks are separate user records)
// and the final text record can land after the Stop hook has read the file.
// The fixtures below are synthetic but mirror those record shapes.
//
// This is an external test package because the regression drives the real
// CLI (cli.Execute), and internal/cli imports internal/capture.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sugihAF/contexo/internal/capture"
	"github.com/sugihAF/contexo/internal/cli"
	"github.com/sugihAF/contexo/internal/config"
)

const (
	wp6h2Session      = "wp6h2-session"
	wp6h2Prompt       = "How should the importer handle duplicate rows?"
	wp6h2Interim      = "Let me read the importer first."
	wp6h2Final        = "Duplicate rows are now skipped and logged once per import."
	wp6h2EarlyPrompt  = "Summarise the README."
	wp6h2EarlyAnswer  = "The README covers setup and the release flow."
	wp6h2SettleBudget = 5 * time.Second // generous bound on the capture's settle wait
)

var wp6h2Tools = []string{"Grep", "Read", "Edit"}

// wp6h2Line marshals one transcript record.
func wp6h2Line(t *testing.T, rec map[string]any) string {
	t.Helper()
	rec["sessionId"] = wp6h2Session
	rec["isSidechain"] = false
	rec["userType"] = "external"
	rec["version"] = "2.1.287"
	rec["cwd"] = "/tmp/wp6h2-project"
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal fixture record: %v", err)
	}
	return string(data)
}

func wp6h2PromptRec(t *testing.T, n int, text string) string {
	return wp6h2Line(t, map[string]any{
		"type":         "user",
		"uuid":         fmt.Sprintf("u-prompt-%d", n),
		"promptId":     fmt.Sprintf("p-%d", n),
		"origin":       map[string]any{"kind": "human"},
		"promptSource": "typed",
		"turnOrigin":   "human",
		"message":      map[string]any{"role": "user", "content": text},
	})
}

func wp6h2Assistant(t *testing.T, msgID, stop string, idx int, block map[string]any) string {
	return wp6h2Line(t, map[string]any{
		"type":          "assistant",
		"uuid":          fmt.Sprintf("a-%s-%d", msgID, idx),
		"requestId":     "req_" + msgID,
		"apiBlockIndex": idx,
		"message": map[string]any{
			"id":          msgID,
			"type":        "message",
			"role":        "assistant",
			"model":       "claude-test",
			"content":     []any{block},
			"stop_reason": stop,
		},
	})
}

func wp6h2Thinking(t *testing.T, msgID, stop string, idx int) string {
	return wp6h2Assistant(t, msgID, stop, idx, map[string]any{"type": "thinking", "thinking": "(private reasoning)", "signature": "sig"})
}

func wp6h2Text(t *testing.T, msgID, stop string, idx int, text string) string {
	return wp6h2Assistant(t, msgID, stop, idx, map[string]any{"type": "text", "text": text})
}

func wp6h2ToolUse(t *testing.T, msgID string, idx int, name string) string {
	return wp6h2Assistant(t, msgID, "tool_use", idx, map[string]any{
		"type": "tool_use", "id": "toolu_" + name, "name": name, "input": map[string]any{},
	})
}

func wp6h2ToolResult(t *testing.T, name string) string {
	return wp6h2Line(t, map[string]any{
		"type":                    "user",
		"uuid":                    "u-result-" + name,
		"promptId":                "p-2",
		"sourceToolAssistantUUID": "a-" + name,
		"toolUseResult":           map[string]any{"stdout": "ok"},
		"message": map[string]any{"role": "user", "content": []any{map[string]any{
			"type": "tool_result", "tool_use_id": "toolu_" + name, "content": "tool output for " + name,
		}}},
	})
}

func wp6h2Attachment(t *testing.T, kind string) string {
	return wp6h2Line(t, map[string]any{"type": "attachment", "uuid": "att-" + kind, "attachment": map[string]any{"type": kind}})
}

func wp6h2System(t *testing.T, subtype string) string {
	return wp6h2Line(t, map[string]any{"type": "system", "subtype": subtype, "uuid": "sys-" + subtype})
}

// wp6h2Transcript returns (truncated, tail): truncated ends where the Stop
// hook read the file in D2 (the last assistant record is the final
// message's thinking block, preceded by tool_result user records); tail is
// the rest of the turn (the final text record and the post-turn records).
func wp6h2Transcript(t *testing.T) (string, string) {
	t.Helper()
	head := []string{
		`{"type":"permission-mode","permissionMode":"default","sessionId":"` + wp6h2Session + `"}`,
		// An earlier, complete turn: never the one to capture.
		wp6h2PromptRec(t, 1, wp6h2EarlyPrompt),
		wp6h2Attachment(t, "hook_additional_context"),
		wp6h2Thinking(t, "msg_a", "end_turn", 0),
		wp6h2Text(t, "msg_a", "end_turn", 1, wp6h2EarlyAnswer),
		wp6h2System(t, "stop_hook_summary"),
		wp6h2System(t, "turn_duration"),
		`{"type":"file-history-snapshot","messageId":"m2","snapshot":{},"isSnapshotUpdate":false}`,
		// The latest turn: three tool calls, one per API message.
		wp6h2PromptRec(t, 2, wp6h2Prompt),
		wp6h2Attachment(t, "total_tokens_reminder"),
		wp6h2Thinking(t, "msg_b", "tool_use", 0),
		wp6h2ToolUse(t, "msg_b", 1, "Grep"),
		wp6h2ToolResult(t, "Grep"),
		wp6h2Attachment(t, "total_tokens_reminder"),
		wp6h2Text(t, "msg_c", "tool_use", 0, wp6h2Interim),
		wp6h2ToolUse(t, "msg_c", 1, "Read"),
		wp6h2ToolResult(t, "Read"),
		wp6h2Attachment(t, "total_tokens_reminder"),
		wp6h2Thinking(t, "msg_d", "tool_use", 0),
		wp6h2ToolUse(t, "msg_d", 1, "Edit"),
		wp6h2ToolResult(t, "Edit"),
		wp6h2Attachment(t, "total_tokens_reminder"),
		`{"type":"last-prompt","lastPrompt":"(elided)","leafUuid":"x","sessionId":"` + wp6h2Session + `"}`,
		wp6h2Thinking(t, "msg_e", "end_turn", 0),
	}
	tail := []string{
		wp6h2Text(t, "msg_e", "end_turn", 1, wp6h2Final),
		wp6h2System(t, "stop_hook_summary"),
		wp6h2System(t, "turn_duration"),
	}
	return strings.Join(head, "\n") + "\n", strings.Join(tail, "\n") + "\n"
}

// wp6h2Project creates a temp Contexo project and writes the transcript.
func wp6h2Project(t *testing.T, transcript string) (project, path string) {
	t.Helper()
	t.Setenv("CONTEXO_CAPTURE_DISABLE", "")
	project = t.TempDir()
	if err := os.MkdirAll(config.ContexoDirPath(project), 0o755); err != nil {
		t.Fatalf("mkdir .contexo: %v", err)
	}
	path = filepath.Join(t.TempDir(), wp6h2Session+".jsonl")
	if err := os.WriteFile(path, []byte(transcript), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return project, path
}

// wp6h2Run runs `ctx capture turn <args>` through cli.Execute with stdin as
// the hook payload, returning the exit code and the wall time it took.
func wp6h2Run(t *testing.T, stdin string, args ...string) (int, time.Duration, string) {
	t.Helper()
	in := filepath.Join(t.TempDir(), "stdin.json")
	if err := os.WriteFile(in, []byte(stdin), 0o644); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	f, err := os.Open(in)
	if err != nil {
		t.Fatalf("open stdin: %v", err)
	}
	defer f.Close()
	old := os.Stdin
	os.Stdin = f
	defer func() { os.Stdin = old }()

	var stdout, stderr bytes.Buffer
	start := time.Now()
	code := cli.Execute(append([]string{"capture", "turn"}, args...), &stdout, &stderr)
	return code, time.Since(start), stderr.String()
}

// wp6h2Captured returns the single buffered record, failing with the D2
// symptom when nothing was written.
func wp6h2Captured(t *testing.T, project string) capture.TurnRecord {
	t.Helper()
	recs, err := capture.Open(config.ContexoDirPath(project), wp6h2Session).Records()
	if err != nil {
		t.Fatalf("read buffer: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("D2: `ctx capture turn` buffered %d records under .contexo/%s, want 1 (the turn was dropped)", len(recs), capture.PendingDirRel)
	}
	return recs[0]
}

func wp6h2StopPayload(t *testing.T, project, path string, last any) string {
	t.Helper()
	p := map[string]any{
		"session_id":      wp6h2Session,
		"transcript_path": path,
		"cwd":             project,
		"hook_event_name": "Stop",
		"permission_mode": "default",
	}
	if last != nil {
		p["last_assistant_message"] = last
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return string(data)
}

// (1) The D2 flush race: the hook reads the transcript before the final
// text record lands, and the record never arrives within the bound. The
// turn must still be buffered with the real prompt and every tool.
func TestWP6H2_CaptureTurnTruncatedTranscriptStillBuffersPrompt(t *testing.T) {
	truncated, _ := wp6h2Transcript(t)
	project, path := wp6h2Project(t, truncated)

	code, took, stderr := wp6h2Run(t, wp6h2StopPayload(t, project, path, nil))
	if code != 0 {
		t.Fatalf("exit %d, want 0 (stderr %q)", code, stderr)
	}
	if took > wp6h2SettleBudget {
		t.Errorf("capture took %v, want a bounded wait (< %v)", took, wp6h2SettleBudget)
	}
	rec := wp6h2Captured(t, project)
	if rec.User != wp6h2Prompt {
		t.Errorf("user: got %q, want the real prompt %q", rec.User, wp6h2Prompt)
	}
	if !reflect.DeepEqual(rec.Tools, wp6h2Tools) {
		t.Errorf("tools: got %v, want every tool of the turn %v", rec.Tools, wp6h2Tools)
	}
	if rec.Assistant != wp6h2Interim {
		t.Errorf("assistant: got %q, want the text flushed so far %q", rec.Assistant, wp6h2Interim)
	}
}

// (2) A fully flushed per-block transcript: the whole latest turn.
func TestWP6H2_CaptureTurnFullPerBlockTranscript(t *testing.T) {
	truncated, tail := wp6h2Transcript(t)
	project, path := wp6h2Project(t, truncated+tail)

	code, took, stderr := wp6h2Run(t, "", "--session", wp6h2Session, "--transcript", path, "--cwd", project)
	if code != 0 {
		t.Fatalf("exit %d, want 0 (stderr %q)", code, stderr)
	}
	if took > time.Second {
		t.Errorf("a settled transcript took %v; the common path must not wait", took)
	}
	rec := wp6h2Captured(t, project)
	if rec.User != wp6h2Prompt {
		t.Errorf("user: got %q, want the real prompt %q", rec.User, wp6h2Prompt)
	}
	if want := wp6h2Interim + "\n" + wp6h2Final; rec.Assistant != want {
		t.Errorf("assistant: got %q, want every text of the turn %q", rec.Assistant, want)
	}
	if !strings.HasSuffix(rec.Assistant, wp6h2Final) {
		t.Errorf("assistant must end with the final text %q, got %q", wp6h2Final, rec.Assistant)
	}
	if !reflect.DeepEqual(rec.Tools, wp6h2Tools) {
		t.Errorf("tools: got %v, want %v", rec.Tools, wp6h2Tools)
	}
}

// (3) The Stop payload's last_assistant_message is authoritative for the
// final text: no wait, and the final text is captured even though the
// transcript has not caught up. Claude Code documents it as a text block
// object; a bare string is accepted too.
func TestWP6H2_CaptureTurnUsesStopLastAssistantMessage(t *testing.T) {
	forms := map[string]any{
		"block":  map[string]any{"type": "text", "text": wp6h2Final},
		"string": wp6h2Final,
	}
	for name, last := range forms {
		t.Run(name, func(t *testing.T) {
			truncated, _ := wp6h2Transcript(t)
			project, path := wp6h2Project(t, truncated)

			code, took, stderr := wp6h2Run(t, wp6h2StopPayload(t, project, path, last))
			if code != 0 {
				t.Fatalf("exit %d, want 0 (stderr %q)", code, stderr)
			}
			if took > 1500*time.Millisecond {
				t.Errorf("took %v; with last_assistant_message the hook must not wait for the transcript", took)
			}
			rec := wp6h2Captured(t, project)
			if rec.User != wp6h2Prompt {
				t.Errorf("user: got %q, want %q", rec.User, wp6h2Prompt)
			}
			if want := wp6h2Interim + "\n" + wp6h2Final; rec.Assistant != want {
				t.Errorf("assistant: got %q, want %q", rec.Assistant, want)
			}
			if !reflect.DeepEqual(rec.Tools, wp6h2Tools) {
				t.Errorf("tools: got %v, want %v", rec.Tools, wp6h2Tools)
			}
		})
	}
}

// (4) The final text record lands shortly after the hook starts: the
// bounded re-read picks it up.
func TestWP6H2_CaptureTurnLateFlushIsPickedUp(t *testing.T) {
	truncated, tail := wp6h2Transcript(t)
	project, path := wp6h2Project(t, truncated)

	flushed := make(chan error, 1)
	go func() {
		time.Sleep(300 * time.Millisecond)
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			flushed <- err
			return
		}
		_, err = f.WriteString(tail)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		flushed <- err
	}()

	code, took, stderr := wp6h2Run(t, "", "--session", wp6h2Session, "--transcript", path, "--cwd", project)
	if err := <-flushed; err != nil {
		t.Fatalf("late flush: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit %d, want 0 (stderr %q)", code, stderr)
	}
	if took > wp6h2SettleBudget {
		t.Errorf("capture took %v, want a bounded wait", took)
	}
	rec := wp6h2Captured(t, project)
	if rec.User != wp6h2Prompt {
		t.Errorf("user: got %q, want %q", rec.User, wp6h2Prompt)
	}
	if want := wp6h2Interim + "\n" + wp6h2Final; rec.Assistant != want {
		t.Errorf("assistant: got %q, want the late final text included %q", rec.Assistant, want)
	}
}
