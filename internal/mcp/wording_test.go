package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/sugihAF/contexo/internal/migrate"
	"github.com/sugihAF/contexo/internal/schema"
	syncpkg "github.com/sugihAF/contexo/internal/sync"
)

// wordingForbidden are phrases that ask the model for its own reasoning.
// The API safety classifier blocks an agent whose tool text asks for them
// ("safeguards flagged this message", reasoning_extraction): with the
// v0.6.6 wording, Sonnet 5.5 and Opus 5.5 were blocked at ctx_write_page.
// Agent-facing text asks for a documented decision rationale instead.
var wordingForbidden = []string{
	"agent reasoning",
	"reasoning trail",
	"reasoning-trail",
	"chain of thought",
	"chain-of-thought",
	"your thinking",
	"your reasoning",
	"internal monologue",
	"metacognitive",
}

// wordingCheck fails for each forbidden phrase in text, case-insensitively.
func wordingCheck(t *testing.T, label, text string) {
	t.Helper()
	if strings.TrimSpace(text) == "" {
		t.Errorf("%s: no text to check", label)
		return
	}
	lower := strings.ToLower(text)
	for _, phrase := range wordingForbidden {
		if i := strings.Index(lower, phrase); i >= 0 {
			from, to := max(0, i-60), min(len(text), i+len(phrase)+60)
			t.Errorf("%s asks for the model's reasoning (%q): ...%s...", label, phrase, text[from:to])
		}
	}
}

// wordingStrings collects every string value in a decoded JSON document,
// keyed by its JSON path.
func wordingStrings(path string, v interface{}, out map[string]string) {
	switch x := v.(type) {
	case string:
		out[path] = x
	case []interface{}:
		for i, e := range x {
			wordingStrings(path+"["+strconv.Itoa(i)+"]", e, out)
		}
	case map[string]interface{}:
		for k, e := range x {
			wordingStrings(path+"."+k, e, out)
		}
	}
}

// wordingCheckToolsList checks every string of a tools/list result: tool
// names and descriptions and every input-schema description.
func wordingCheckToolsList(t *testing.T, label string, result json.RawMessage) {
	t.Helper()
	var doc struct {
		Tools []map[string]interface{} `json:"tools"`
	}
	if err := json.Unmarshal(result, &doc); err != nil {
		t.Fatalf("%s: decode tools/list result: %v\n%s", label, err, result)
	}
	if len(doc.Tools) < 10 {
		t.Fatalf("%s: tools/list returned %d tools, want the full set", label, len(doc.Tools))
	}
	var writePage string
	for _, tool := range doc.Tools {
		name, _ := tool["name"].(string)
		strs := map[string]string{}
		wordingStrings(name, tool, strs)
		for path, s := range strs {
			wordingCheck(t, label+" "+path, s)
		}
		if name == "ctx_write_page" {
			writePage, _ = tool["description"].(string)
		}
	}
	// The rationale guidance must survive the rewording, not be dropped.
	for _, want := range []string{"reasoning_summary", "Rationale section"} {
		if !strings.Contains(writePage, want) {
			t.Errorf("%s: ctx_write_page description lost %q: %q", label, want, writePage)
		}
	}
}

// TestMCPWording_ToolsListOverStdio runs the real ctx binary as an MCP
// server and checks the tools/list response it sends a client.
func TestMCPWording_ToolsListOverStdio(t *testing.T) {
	if testing.Short() {
		t.Skip("builds ctx; skipped under -short")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH")
	}
	bin := filepath.Join(t.TempDir(), "ctx")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command(goBin, "build", "-o", bin, "github.com/sugihAF/contexo/cmd/ctx")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ctx: %v\n%s", err, out)
	}

	// Isolated HOME (v0.6.7 hotfix: main has no wp03IsolateHome helper).
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	_, projectRoot := setupCaptureServer(t, false, nil)

	cmd := exec.Command(bin, "mcp", "--root", projectRoot)
	cmd.Dir = projectRoot
	cmd.Env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home, "CONTEXO_NO_UPDATE_CHECK=1")
	cmd.Stdin = strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"wording-test","version":"0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	}, "\n") + "\n")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("ctx mcp: %v\nstderr:\n%s", err, stderr.String())
	}

	var result json.RawMessage
	sc := bufio.NewScanner(&stdout)
	sc.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	for sc.Scan() {
		var resp struct {
			ID     json.RawMessage `json:"id"`
			Result json.RawMessage `json:"result"`
		}
		if json.Unmarshal(sc.Bytes(), &resp) == nil && string(resp.ID) == "2" {
			result = resp.Result
		}
	}
	if result == nil {
		t.Fatalf("no tools/list response (id 2) from ctx mcp\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	wordingCheckToolsList(t, "tools/list", result)
}

// TestMCPWording_ToolsListPayload checks the payload the stdio server
// sends for tools/list ({"tools": ListTools()}) in process, so the guard
// also runs under -short.
func TestMCPWording_ToolsListPayload(t *testing.T) {
	srv, _ := setupCaptureServer(t, false, nil)
	result, err := json.Marshal(map[string]interface{}{"tools": srv.ListTools()})
	if err != nil {
		t.Fatal(err)
	}
	wordingCheckToolsList(t, "ListTools", result)
}

// TestMCPWording_PushPausedBrief checks the PUSH_PAUSED handshake text.
func TestMCPWording_PushPausedBrief(t *testing.T) {
	srv, _ := setupCaptureServer(t, true, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("HTTP push must not be called while the handshake pauses; got %s", r.URL.Path)
	})))
	res := srv.HandleToolCall(context.Background(), "ctx_push", map[string]interface{}{"feature": "foo"})
	if res == nil || res.IsError || len(res.Content) == 0 {
		t.Fatalf("ctx_push: %+v", res)
	}
	text := res.Content[0].Text
	if !strings.HasPrefix(text, "<PUSH_PAUSED reason=distill_required>") {
		t.Fatalf("ctx_push did not pause: %s", text)
	}
	wordingCheck(t, "PUSH_PAUSED", text)
}

// TestMCPWording_CaptureTemplate checks the ctx_capture_session template.
func TestMCPWording_CaptureTemplate(t *testing.T) {
	srv, _ := setupCaptureServer(t, true, nil)
	res := srv.HandleToolCall(context.Background(), "ctx_capture_session", map[string]interface{}{})
	if res == nil || res.IsError || len(res.Content) == 0 {
		t.Fatalf("ctx_capture_session: %+v", res)
	}
	text := res.Content[0].Text
	if !strings.HasPrefix(text, "<CAPTURE_TEMPLATE>") {
		t.Fatalf("ctx_capture_session returned no template: %s", text)
	}
	wordingCheck(t, "CAPTURE_TEMPLATE", text)
}

// TestMCPWording_MergeRequiredBrief checks the MERGE_REQUIRED directive
// (v0.6.7 hotfix: main renders it with buildMergeDirective).
func TestMCPWording_MergeRequiredBrief(t *testing.T) {
	c := syncpkg.Conflict{
		Path:              "wiki/concepts/x.md",
		CurrentSHA:        "0123456789abcdef0123456789abcdef01234567",
		CurrentContent:    []byte("---\nslug: x\ntype: concept\n---\n## Decision\nC.\n"),
		ExpectedParentSHA: "abc123456abcdef",
		AncestorContent:   []byte("---\nslug: x\ntype: concept\n---\n## Decision\nA.\n"),
	}
	local := map[string][]byte{c.Path: []byte("---\nslug: x\ntype: concept\n---\n## Decision\nB.\n")}
	wordingCheck(t, "MERGE_REQUIRED", buildMergeDirective([]syncpkg.Conflict{c}, local))
}

// TestMCPWording_MigrateAndGenerateDirectives checks the ctx_migrate and
// ctx_generate catalog, resume and install directives.
func TestMCPWording_MigrateAndGenerateDirectives(t *testing.T) {
	cat := migrate.Catalog{Items: []migrate.Candidate{{
		ID: 1, Detector: "docs", RelPath: "docs/a.md", AbsPath: "/src/docs/a.md",
		Preview: "A doc.", EstTokens: 10, SuggestedType: schema.TypeConcept, SuggestedSlug: "a",
	}}}
	m := &migrate.Manifest{SourceRoot: "/src", Items: []migrate.ManifestItem{{
		AbsPath: "/src/docs/a.md", RelPath: "docs/a.md", SuggestedType: schema.TypeConcept, SuggestedSlug: "a",
	}}}
	for label, text := range map[string]string{
		"MIGRATE_CATALOG":         buildMigrateCatalog(cat, "/src", "2026-10-23-migrate-src"),
		"MIGRATE_RESUME":          buildMigrateResume(m, "2026-10-23-migrate-src"),
		"GENERATE_CATALOG":        buildGenerateCatalog(cat, "/src", "2026-10-23-generate-src"),
		"GENERATE_RESUME":         buildGenerateResume(m, "2026-10-23-generate-src"),
		"GENERATE_NEEDS_GRAPHIFY": buildNeedsGraphifyDirective(),
	} {
		wordingCheck(t, label, text)
	}
}
