package capture

import (
	"os"
	"path/filepath"
	"testing"
)

const sampleTranscript = `{"type":"user","message":{"role":"user","content":"hello"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"hidden"},{"type":"text","text":"hi there"},{"type":"tool_use","name":"Read","input":{}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"file body"}]}}
{"type":"user","message":{"role":"user","content":"what now?"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"let me check"},{"type":"tool_use","name":"Grep","input":{}},{"type":"tool_use","name":"Glob","input":{}}]}}
`

func TestLatestExchange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, []byte(sampleTranscript), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	ex, err := LatestExchange(path)
	if err != nil {
		t.Fatalf("LatestExchange: %v", err)
	}
	if ex.User != "what now?" {
		t.Errorf("user: got %q, want %q", ex.User, "what now?")
	}
	if ex.Assistant != "let me check" {
		t.Errorf("assistant: got %q, want %q", ex.Assistant, "let me check")
	}
	if len(ex.Tools) != 2 || ex.Tools[0] != "Grep" || ex.Tools[1] != "Glob" {
		t.Errorf("tools: got %v", ex.Tools)
	}
}

func TestLatestExchangeIgnoresThinking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	const onlyThinking = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"secret"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(onlyThinking), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	ex, err := LatestExchange(path)
	if err != nil {
		t.Fatalf("LatestExchange: %v", err)
	}
	if ex.Assistant != "" {
		t.Errorf("thinking blocks must not leak into assistant text: got %q", ex.Assistant)
	}
}

func TestLatestExchangeNoAssistant(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	const onlyUser = `{"type":"user","message":{"role":"user","content":"hi"}}` + "\n"
	if err := os.WriteFile(path, []byte(onlyUser), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	ex, err := LatestExchange(path)
	if err != nil {
		t.Fatalf("LatestExchange: %v", err)
	}
	if ex.User != "" || ex.Assistant != "" {
		t.Errorf("no-assistant transcript should yield empty exchange, got %+v", ex)
	}
}

func TestLatestExchangeMalformedLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	const mixed = `{not json
{"type":"user","message":{"role":"user","content":"hi"}}
broken
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"yo"}]}}
`
	if err := os.WriteFile(path, []byte(mixed), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	ex, err := LatestExchange(path)
	if err != nil {
		t.Fatalf("LatestExchange: %v", err)
	}
	if ex.User != "hi" || ex.Assistant != "yo" {
		t.Errorf("got %+v", ex)
	}
}

func TestLatestExchangeMissingFile(t *testing.T) {
	_, err := LatestExchange(filepath.Join(t.TempDir(), "nope.jsonl"))
	if err == nil {
		t.Errorf("expected error for missing file")
	}
}

func TestLatestExchangeEmptyPath(t *testing.T) {
	if _, err := LatestExchange(""); err == nil {
		t.Errorf("expected error for empty path")
	}
}
