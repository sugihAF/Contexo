package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sugihAF/contexo/internal/capture"
	"github.com/sugihAF/contexo/internal/config"
)

func tmpContexoProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(config.ContexoDirPath(dir), 0o755); err != nil {
		t.Fatalf("mkdir contexo: %v", err)
	}
	return dir
}

func writeTranscript(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "transcript.jsonl")
	const body = `{"type":"user","message":{"role":"user","content":"how do I do X?"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"do Y because Z"},{"type":"tool_use","name":"Read","input":{}}]}}
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path
}

func TestCaptureTurnWritesBuffer(t *testing.T) {
	project := tmpContexoProject(t)
	transcript := writeTranscript(t, project)
	cmd := newCaptureTurnCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetIn(bytes.NewReader(nil))

	if err := runCaptureTurn(cmd, "claude", "sess-1", transcript, project); err != nil {
		t.Fatalf("runCaptureTurn: %v", err)
	}

	recs, err := capture.Open(config.ContexoDirPath(project), "sess-1").Records()
	if err != nil {
		t.Fatalf("records: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
	if recs[0].User != "how do I do X?" || recs[0].Assistant != "do Y because Z" {
		t.Errorf("captured wrong content: %+v", recs[0])
	}
	if len(recs[0].Tools) != 1 || recs[0].Tools[0] != "Read" {
		t.Errorf("tools missing: %v", recs[0].Tools)
	}
}

func TestCaptureTurnSilentOutsideProject(t *testing.T) {
	noProject := t.TempDir()
	transcript := writeTranscript(t, noProject)

	cmd := newCaptureTurnCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetIn(bytes.NewReader(nil))

	if err := runCaptureTurn(cmd, "claude", "sess-1", transcript, noProject); err != nil {
		t.Fatalf("runCaptureTurn outside project: %v", err)
	}
	// Should NOT have created .contexo or any buffer.
	if _, err := os.Stat(filepath.Join(noProject, ".contexo")); err == nil {
		t.Errorf("capture-turn must not create .contexo when none exists")
	}
}

func TestCaptureTurnDisableEnv(t *testing.T) {
	t.Setenv("CONTEXO_CAPTURE_DISABLE", "1")
	project := tmpContexoProject(t)
	transcript := writeTranscript(t, project)

	cmd := newCaptureTurnCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetIn(bytes.NewReader(nil))

	if err := runCaptureTurn(cmd, "claude", "sess-1", transcript, project); err != nil {
		t.Fatalf("runCaptureTurn: %v", err)
	}
	if capture.Open(config.ContexoDirPath(project), "sess-1").Exists() {
		t.Errorf("buffer must not be written when CONTEXO_CAPTURE_DISABLE=1")
	}
}

func TestCaptureTurnHandlesMissingTranscript(t *testing.T) {
	project := tmpContexoProject(t)
	cmd := newCaptureTurnCmd()
	cmd.SetOut(&bytes.Buffer{})
	stderr := &bytes.Buffer{}
	cmd.SetErr(stderr)
	cmd.SetIn(bytes.NewReader(nil))

	err := runCaptureTurn(cmd, "claude", "sess-1", "/nonexistent/transcript.jsonl", project)
	if err != nil {
		t.Errorf("runCaptureTurn must not return error on missing transcript: %v", err)
	}
	if !strings.Contains(stderr.String(), "capture turn") {
		t.Errorf("expected stderr warning, got %q", stderr.String())
	}
	if capture.Open(config.ContexoDirPath(project), "sess-1").Exists() {
		t.Errorf("buffer should not exist after failed transcript read")
	}
}

func TestCaptureTurnReadsStdinPayload(t *testing.T) {
	project := tmpContexoProject(t)
	transcript := writeTranscript(t, project)
	payload := `{"hook_event_name":"Stop","session_id":"sess-stdin","transcript_path":"` +
		strings.ReplaceAll(transcript, `\`, `\\`) + `","cwd":"` +
		strings.ReplaceAll(project, `\`, `\\`) + `"}`

	cmd := newCaptureTurnCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetIn(bytes.NewReader([]byte(payload)))

	if err := runCaptureTurn(cmd, "claude", "", "", ""); err != nil {
		t.Fatalf("runCaptureTurn: %v", err)
	}
	if !capture.Open(config.ContexoDirPath(project), "sess-stdin").Exists() {
		t.Errorf("buffer should have been written using stdin payload")
	}
}

func TestCaptureTurnCodexUserPromptSubmitStashesPrompt(t *testing.T) {
	project := tmpContexoProject(t)
	payload := `{"hook_event_name":"UserPromptSubmit","session_id":"cdx-1","prompt":"how do I add billing?"}`
	cmd := newCaptureTurnCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetIn(bytes.NewReader([]byte(payload)))

	if err := runCaptureTurn(cmd, "codex", "", "", project); err != nil {
		t.Fatalf("runCaptureTurn: %v", err)
	}
	// No buffer record yet — the prompt is only stashed for the later Stop.
	if capture.Open(config.ContexoDirPath(project), "cdx-1").Exists() {
		t.Errorf("UserPromptSubmit must not write a buffer record yet")
	}
	got, _ := capture.TakePendingPrompt(config.ContexoDirPath(project), "cdx-1")
	if got != "how do I add billing?" {
		t.Errorf("pending prompt = %q, want the submitted prompt", got)
	}
}

func TestCaptureTurnCodexStopPairsPromptAndAssistant(t *testing.T) {
	project := tmpContexoProject(t)
	cdir := config.ContexoDirPath(project)

	up := `{"hook_event_name":"UserPromptSubmit","session_id":"cdx-2","prompt":"why reject Connect?"}`
	c1 := newCaptureTurnCmd()
	c1.SetOut(&bytes.Buffer{})
	c1.SetErr(&bytes.Buffer{})
	c1.SetIn(bytes.NewReader([]byte(up)))
	if err := runCaptureTurn(c1, "codex", "", "", project); err != nil {
		t.Fatalf("UserPromptSubmit: %v", err)
	}

	stop := `{"hook_event_name":"Stop","session_id":"cdx-2","last_assistant_message":"Because Connect gives restaurants the negative balance."}`
	c2 := newCaptureTurnCmd()
	c2.SetOut(&bytes.Buffer{})
	c2.SetErr(&bytes.Buffer{})
	c2.SetIn(bytes.NewReader([]byte(stop)))
	if err := runCaptureTurn(c2, "codex", "", "", project); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	recs, err := capture.Open(cdir, "cdx-2").Records()
	if err != nil {
		t.Fatalf("records: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
	if recs[0].User != "why reject Connect?" ||
		recs[0].Assistant != "Because Connect gives restaurants the negative balance." {
		t.Errorf("paired record wrong: %+v", recs[0])
	}
}

func TestCaptureTurnCursorBeforeSubmitStashesPrompt(t *testing.T) {
	project := tmpContexoProject(t)
	payload := `{"hook_event_name":"beforeSubmitPrompt","conversation_id":"cur-1","prompt":"how does drift detection work?"}`
	cmd := newCaptureTurnCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetIn(bytes.NewReader([]byte(payload)))

	if err := runCaptureTurn(cmd, "cursor", "", "", project); err != nil {
		t.Fatalf("runCaptureTurn: %v", err)
	}
	if capture.Open(config.ContexoDirPath(project), "cur-1").Exists() {
		t.Errorf("beforeSubmitPrompt must not write a buffer record yet")
	}
	got, _ := capture.TakePendingPrompt(config.ContexoDirPath(project), "cur-1")
	if got != "how does drift detection work?" {
		t.Errorf("pending prompt = %q, want the submitted prompt (keyed by conversation_id)", got)
	}
}

func TestCaptureTurnCursorAfterResponsePairs(t *testing.T) {
	project := tmpContexoProject(t)
	cdir := config.ContexoDirPath(project)

	up := `{"hook_event_name":"beforeSubmitPrompt","conversation_id":"cur-2","prompt":"how does drift detection work?"}`
	c1 := newCaptureTurnCmd()
	c1.SetOut(&bytes.Buffer{})
	c1.SetErr(&bytes.Buffer{})
	c1.SetIn(bytes.NewReader([]byte(up)))
	if err := runCaptureTurn(c1, "cursor", "", "", project); err != nil {
		t.Fatalf("beforeSubmitPrompt: %v", err)
	}

	ar := `{"hook_event_name":"afterAgentResponse","conversation_id":"cur-2","text":"It prepends a DRIFT_NOTICE when the server page is ahead."}`
	c2 := newCaptureTurnCmd()
	c2.SetOut(&bytes.Buffer{})
	c2.SetErr(&bytes.Buffer{})
	c2.SetIn(bytes.NewReader([]byte(ar)))
	if err := runCaptureTurn(c2, "cursor", "", "", project); err != nil {
		t.Fatalf("afterAgentResponse: %v", err)
	}

	recs, err := capture.Open(cdir, "cur-2").Records()
	if err != nil {
		t.Fatalf("records: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
	if recs[0].User != "how does drift detection work?" ||
		recs[0].Assistant != "It prepends a DRIFT_NOTICE when the server page is ahead." {
		t.Errorf("paired record wrong: %+v", recs[0])
	}
}

func TestCaptureTurnCursorAfterResponseWithoutPrompt(t *testing.T) {
	project := tmpContexoProject(t)
	ar := `{"hook_event_name":"afterAgentResponse","conversation_id":"cur-3","text":"Done."}`
	cmd := newCaptureTurnCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetIn(bytes.NewReader([]byte(ar)))

	if err := runCaptureTurn(cmd, "cursor", "", "", project); err != nil {
		t.Fatalf("runCaptureTurn: %v", err)
	}
	recs, _ := capture.Open(config.ContexoDirPath(project), "cur-3").Records()
	if len(recs) != 1 || recs[0].Assistant != "Done." || recs[0].User != "" {
		t.Errorf("expected one assistant-only record, got %+v", recs)
	}
}

func TestCaptureTurnCodexStopWithoutPrompt(t *testing.T) {
	project := tmpContexoProject(t)
	stop := `{"hook_event_name":"Stop","session_id":"cdx-3","last_assistant_message":"Done."}`
	cmd := newCaptureTurnCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetIn(bytes.NewReader([]byte(stop)))

	if err := runCaptureTurn(cmd, "codex", "", "", project); err != nil {
		t.Fatalf("runCaptureTurn: %v", err)
	}
	recs, _ := capture.Open(config.ContexoDirPath(project), "cdx-3").Records()
	if len(recs) != 1 || recs[0].Assistant != "Done." || recs[0].User != "" {
		t.Errorf("expected one assistant-only record, got %+v", recs)
	}
}

func TestFindContexoRoot(t *testing.T) {
	parent := t.TempDir()
	if err := os.MkdirAll(filepath.Join(parent, ".contexo"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	deep := filepath.Join(parent, "src", "inner")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatalf("mkdir deep: %v", err)
	}

	got := findContexoRoot(deep)
	gotAbs, _ := filepath.Abs(got)
	wantAbs, _ := filepath.Abs(parent)
	if gotAbs != wantAbs {
		t.Errorf("findContexoRoot: got %q, want %q", gotAbs, wantAbs)
	}

	if findContexoRoot(t.TempDir()) != "" {
		t.Errorf("findContexoRoot in non-Contexo dir should return empty")
	}
}
