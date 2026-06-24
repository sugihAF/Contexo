package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestIntegrationTableListsAllAgents(t *testing.T) {
	project := tmpContexoProject(t)
	out := &bytes.Buffer{}
	renderIntegrationTable(out, project, testDeps(false, nil))
	s := out.String()
	for _, want := range []string{
		"Claude Code", "Cursor", "Codex",
		"not installed",         // codex, when its CLI isn't present
		"not wired (.mcp.json)", // fresh project
	} {
		if !strings.Contains(s, want) {
			t.Errorf("integration table missing %q; got:\n%s", want, s)
		}
	}
}

func TestIntegrationTableReflectsClaudeWiring(t *testing.T) {
	project := tmpContexoProject(t)
	cmd, _ := bufCmd()
	if err := runMCPInstall(cmd, project, "claude", true, testDeps(false, nil)); err != nil {
		t.Fatalf("install claude: %v", err)
	}
	if err := installHook(cmd, project); err != nil {
		t.Fatalf("install hook: %v", err)
	}
	out := &bytes.Buffer{}
	renderIntegrationTable(out, project, testDeps(false, nil))
	s := out.String()
	if !strings.Contains(s, "wired (.mcp.json)") {
		t.Errorf("Claude MCP should show wired; got:\n%s", s)
	}
	if !strings.Contains(s, "installed") {
		t.Errorf("capture hook should show installed; got:\n%s", s)
	}
}

func TestIntegrationTableCodexWiredWhenInstalled(t *testing.T) {
	project := tmpContexoProject(t)
	out := &bytes.Buffer{}
	renderIntegrationTable(out, project, testDeps(true, nil)) // installed + get succeeds ⇒ wired
	s := out.String()
	if !strings.Contains(s, "wired (~/.codex, global)") || strings.Contains(s, "not wired (~/.codex, global)") {
		t.Errorf("codex should show wired (not 'not wired') when installed+wired; got:\n%s", s)
	}
}

func TestAgentGuideCoversKnownAgentsAndCapture(t *testing.T) {
	out := &bytes.Buffer{}
	renderAgentGuide(out)
	s := out.String()
	for _, want := range []string{
		// the named agents/harnesses + their config locations/formats
		"Windsurf", "mcp_config.json",
		"OpenCode", "opencode.json",
		"Hermes", "config.yaml",
		"OpenClaw", "openclaw mcp",
		"~/.codex/config.toml",
		// the universal server entry + how-to
		`"command": "ctx", "args": ["mcp"]`,
		"ctx mcp install",
		// capture state: Claude + Codex supported, Cursor planned
		"ctx hooks install", "Claude Code, Codex, and Cursor are supported",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("agent guide missing %q; got:\n%s", want, s)
		}
	}
}
