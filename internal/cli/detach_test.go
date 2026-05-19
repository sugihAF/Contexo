package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetachPurgesByDefault(t *testing.T) {
	project := tmpContexoProject(t)
	writeMCP(t, project, `{"mcpServers":{"contexo":{"command":"ctx","args":["mcp"]}}}`)
	writeFile(t, filepath.Join(project, ".gitignore"),
		"# Contexo local knowledge (synced via ctx push/pull, not git)\n.contexo/\nnode_modules/\n")

	cmd := newDetachCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	if err := runDetach(cmd, project, true, false); err != nil {
		t.Fatalf("detach: %v", err)
	}

	if _, err := os.Stat(filepath.Join(project, ".contexo")); !os.IsNotExist(err) {
		t.Errorf(".contexo/ should be gone; stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, ".mcp.json")); !os.IsNotExist(err) {
		t.Errorf(".mcp.json should be gone (it only had the contexo entry); stat err = %v", err)
	}
	gi, _ := os.ReadFile(filepath.Join(project, ".gitignore"))
	if strings.Contains(string(gi), ".contexo") {
		t.Errorf(".gitignore should no longer mention .contexo, got:\n%s", gi)
	}
	if !strings.Contains(string(gi), "node_modules/") {
		t.Errorf(".gitignore should still contain other entries, got:\n%s", gi)
	}
}

func TestDetachKeepKnowledge(t *testing.T) {
	project := tmpContexoProject(t)
	writeMCP(t, project, `{"mcpServers":{"contexo":{"command":"ctx","args":["mcp"]}}}`)

	cmd := newDetachCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	if err := runDetach(cmd, project, true, true); err != nil {
		t.Fatalf("detach: %v", err)
	}

	if _, err := os.Stat(filepath.Join(project, ".contexo")); err != nil {
		t.Errorf(".contexo/ should still exist with --keep-knowledge: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, ".mcp.json")); !os.IsNotExist(err) {
		t.Errorf(".mcp.json should be gone regardless of --keep-knowledge")
	}
}

func TestDetachPreservesOtherMCPServers(t *testing.T) {
	project := tmpContexoProject(t)
	writeMCP(t, project, `{"mcpServers":{"contexo":{"command":"ctx","args":["mcp"]},"other":{"command":"foo"}}}`)

	cmd := newDetachCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	if err := runDetach(cmd, project, true, false); err != nil {
		t.Fatalf("detach: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(project, ".mcp.json"))
	if err != nil {
		t.Fatalf(".mcp.json should still exist (other server present): %v", err)
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		t.Fatalf("parse .mcp.json: %v", err)
	}
	servers := obj["mcpServers"].(map[string]interface{})
	if _, ok := servers["contexo"]; ok {
		t.Errorf("contexo entry should be removed; got %s", data)
	}
	if _, ok := servers["other"]; !ok {
		t.Errorf("other server entry should be preserved; got %s", data)
	}
}

func TestDetachNoOpWhenNothingPresent(t *testing.T) {
	project := t.TempDir() // bare dir — no .contexo/, no .mcp.json, no hook
	cmd := newDetachCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})

	if err := runDetach(cmd, project, true, false); err != nil {
		t.Fatalf("detach: %v", err)
	}
	if !strings.Contains(out.String(), "Nothing to detach") {
		t.Errorf("expected 'Nothing to detach' message, got: %s", out.String())
	}
}

func TestDetachWithoutConfirmationAborts(t *testing.T) {
	project := tmpContexoProject(t)
	cmd := newDetachCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetIn(strings.NewReader("n\n"))

	if err := runDetach(cmd, project, false, false); err != nil {
		t.Fatalf("detach: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, ".contexo")); err != nil {
		t.Errorf(".contexo/ should still exist after 'n' confirmation: %v", err)
	}
}

func TestDetachReportsUnpushedPages(t *testing.T) {
	project := tmpContexoProject(t)
	// Drop a page into the store so the unpushed counter trips. Use a
	// minimal-but-valid frontmatter; pagestore needs a parseable doc.
	pagePath := filepath.Join(project, ".contexo", "wiki", "concepts", "demo.md")
	if err := os.MkdirAll(filepath.Dir(pagePath), 0o755); err != nil {
		t.Fatal(err)
	}
	page := "---\ntitle: Demo\ntype: concept\ntags: [demo]\ncreated: 2026-05-19\nupdated: 2026-05-19\n---\n\nbody\n"
	if err := os.WriteFile(pagePath, []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newDetachCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})

	if err := runDetach(cmd, project, true, false); err != nil {
		t.Fatalf("detach: %v", err)
	}
	if !strings.Contains(out.String(), "never been pushed") {
		t.Errorf("expected unpushed-pages warning, got:\n%s", out.String())
	}
}

func writeMCP(t *testing.T, project, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(project, ".mcp.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
