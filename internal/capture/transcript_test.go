package capture

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
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

func TestLatestExchangeNoAssistantKeepsPrompt(t *testing.T) {
	// A prompted turn is never dropped (WP-6H.2): with no reply in the
	// transcript yet, the prompt comes back with an empty reply.
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	const onlyUser = `{"type":"user","message":{"role":"user","content":"hi"}}` + "\n"
	if err := os.WriteFile(path, []byte(onlyUser), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	ex, err := LatestExchange(path)
	if err != nil {
		t.Fatalf("LatestExchange: %v", err)
	}
	if ex.User != "hi" || ex.Assistant != "" || len(ex.Tools) != 0 {
		t.Errorf("prompt-only transcript should yield the prompt alone, got %+v", ex)
	}
}

func writeTranscriptFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestLatestExchangeAggregatesWholeTurn(t *testing.T) {
	// Older shape: several blocks per assistant record, a tool_result user
	// record between the turn's two API messages.
	path := writeTranscriptFile(t, `{"type":"user","message":{"role":"user","content":"old prompt"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"old reply"}]}}
{"type":"user","message":{"role":"user","content":[{"type":"text","text":"rename the flag"}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Looking."},{"type":"tool_use","name":"Grep","input":{}},{"type":"tool_use","name":"Edit","input":{}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"x"},{"type":"tool_result","content":"y"}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"hidden"},{"type":"text","text":"Renamed."}]}}
`)
	ex, err := LatestExchange(path)
	if err != nil {
		t.Fatalf("LatestExchange: %v", err)
	}
	if ex.User != "rename the flag" {
		t.Errorf("user: got %q, want the prompt, not the tool_result record", ex.User)
	}
	if ex.Assistant != "Looking.\nRenamed." {
		t.Errorf("assistant: got %q", ex.Assistant)
	}
	if !reflect.DeepEqual(ex.Tools, []string{"Grep", "Edit"}) {
		t.Errorf("tools: got %v", ex.Tools)
	}
}

func TestLatestExchangeSkipsMetaSidechainAndCompactRecords(t *testing.T) {
	path := writeTranscriptFile(t, `{"type":"user","message":{"role":"user","content":"<command-name>/review</command-name>"}}
{"type":"user","isMeta":true,"message":{"role":"user","content":[{"type":"text","text":"expanded skill body"}]}}
{"type":"user","isSidechain":true,"message":{"role":"user","content":"subagent task"}}
{"type":"assistant","isSidechain":true,"message":{"role":"assistant","content":[{"type":"text","text":"subagent reply"}]}}
{"type":"user","isCompactSummary":true,"message":{"role":"user","content":"summary of earlier turns"}}
{"type":"attachment","attachment":{"type":"hook_success"}}
{"type":"system","subtype":"stop_hook_summary"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"review done"}]}}
`)
	ex, err := LatestExchange(path)
	if err != nil {
		t.Fatalf("LatestExchange: %v", err)
	}
	if ex.User != "<command-name>/review</command-name>" || ex.Assistant != "review done" {
		t.Errorf("got %+v", ex)
	}
}

func TestLatestExchangeImageOnlyPromptStartsTurn(t *testing.T) {
	path := writeTranscriptFile(t, `{"type":"user","message":{"role":"user","content":"old prompt"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"old reply"}]}}
{"type":"user","message":{"role":"user","content":[{"type":"image","source":{}}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"a diagram"}]}}
`)
	ex, err := LatestExchange(path)
	if err != nil {
		t.Fatalf("LatestExchange: %v", err)
	}
	if ex.User != "" || ex.Assistant != "a diagram" {
		t.Errorf("an image-only prompt starts a new turn, got %+v", ex)
	}
}

func TestLatestExchangeKeepsFinalTextWithinCap(t *testing.T) {
	long := strings.Repeat("a", 3000)
	final := "final " + strings.Repeat("b", 1000)
	path := writeTranscriptFile(t, `{"type":"user","message":{"role":"user","content":"go"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"`+long+`"},{"type":"tool_use","name":"Bash","input":{}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"`+long+`"},{"type":"tool_use","name":"Bash","input":{}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"`+final+`"}]}}
`)
	ex, err := LatestExchange(path)
	if err != nil {
		t.Fatalf("LatestExchange: %v", err)
	}
	if len(ex.Assistant) > MaxAssistantBytes {
		t.Errorf("assistant is %d bytes, want <= %d", len(ex.Assistant), MaxAssistantBytes)
	}
	if !strings.HasSuffix(ex.Assistant, final) || !strings.HasPrefix(ex.Assistant, "...\n") {
		t.Errorf("oldest text must be dropped first, keeping the final text; got prefix %q", ex.Assistant[:10])
	}
	if want := "...\n" + long + "\n" + final; ex.Assistant != want {
		t.Errorf("got %d bytes, want the newest parts that fit (%d bytes)", len(ex.Assistant), len(want))
	}
}

func TestLatestTurnWaitsOnlyWhileUnsettled(t *testing.T) {
	path := writeTranscriptFile(t, sampleTranscript)
	start := time.Now()
	ex, err := LatestTurn(path, "", 500*time.Millisecond)
	if err != nil {
		t.Fatalf("LatestTurn: %v", err)
	}
	// sampleTranscript ends on a tool_use, so it is unsettled: it waits for
	// the bound, then returns what it has.
	if took := time.Since(start); took < 500*time.Millisecond || took > 2*time.Second {
		t.Errorf("a trailing tool_use must wait for the bound (500ms), returned after %v", took)
	}
	if ex.User != "what now?" {
		t.Errorf("user: got %q", ex.User)
	}

	settled := writeTranscriptFile(t, `{"type":"user","message":{"role":"user","content":"hi"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Read","input":{}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"x"}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}
`)
	start = time.Now()
	if _, err := LatestTurn(settled, "", 3*time.Second); err != nil {
		t.Fatalf("LatestTurn: %v", err)
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("a settled transcript must not wait, took %v", took)
	}
}

func TestLatestTurnWithoutPromptDoesNotWait(t *testing.T) {
	path := writeTranscriptFile(t, `{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"x"}]}}`+"\n")
	start := time.Now()
	ex, err := LatestTurn(path, "", 3*time.Second)
	if err != nil {
		t.Fatalf("LatestTurn: %v", err)
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("nothing to capture must not wait, took %v", took)
	}
	if ex.User != "" || ex.Assistant != "" || len(ex.Tools) != 0 {
		t.Errorf("got %+v", ex)
	}
}

func TestLatestTurnFinalTextIsNotDuplicated(t *testing.T) {
	path := writeTranscriptFile(t, `{"type":"user","message":{"role":"user","content":"hi"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}
`)
	ex, err := LatestTurn(path, " done\n", time.Second)
	if err != nil {
		t.Fatalf("LatestTurn: %v", err)
	}
	if ex.Assistant != "done" {
		t.Errorf("assistant: got %q, want the final text once", ex.Assistant)
	}
}

func TestContentTextForms(t *testing.T) {
	cases := map[string]string{
		`"plain"`:                          "plain",
		`{"type":"text","text":" block "}`: "block",
		`[{"type":"text","text":"a"},{"type":"tool_use","name":"X"}]`:   "a",
		`{"role":"assistant","content":[{"type":"text","text":"msg"}]}`: "msg",
		`{"type":"thinking","thinking":"hidden"}`:                       "",
		`42`: "",
	}
	for in, want := range cases {
		if got := ContentText(json.RawMessage(in)); got != want {
			t.Errorf("ContentText(%s) = %q, want %q", in, got, want)
		}
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
