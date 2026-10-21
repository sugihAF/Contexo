package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/sugihAF/contexo/internal/cli/agentwire"
	"github.com/sugihAF/contexo/internal/ui"
)

// renderIntegrationTable prints, per known agent, whether the Contexo MCP
// server and the capture (Stop) hook are integrated for this project. deps
// lets callers inject the codex probe/runner (and tests stub them).
//
// The leading mark column carries the at-a-glance state (MCP wired or not);
// the text cells keep their exact wording so they stay greppable and so the
// Codex row's "wired (~/.codex, global)" never grows a prefix. The VS Code row
// reads .vscode/mcp.json's servers key (see vscodeMCPLabel) and shows "-" for
// capture: VS Code has no Stop hook.
func renderIntegrationTable(out io.Writer, root string, deps mcpWireDeps) {
	u := ui.Plain(out)
	hookOn, _ := hookInstalled(root)

	claudeOn, _ := agentwire.WiredJSON(agentwire.ClaudeMCPPath(root))
	cursorOn, _ := agentwire.WiredJSON(agentwire.CursorMCPPath(root))
	vscodeOn, _ := agentwire.WiredVSCode(agentwire.VSCodeMCPPath(root))

	codexOn := false
	codexMCP := "not installed"
	if deps.codexInstalled() {
		if wired, _ := agentwire.CodexWired(deps.runner); wired {
			codexOn = true
			codexMCP = "wired (~/.codex, global)"
		} else {
			codexMCP = "not wired (~/.codex, global)"
		}
	}
	codexCapture := "not installed"
	if on, _ := agentwire.CodexHooksWired(root); on {
		codexCapture = "installed"
	}
	cursorCapture := "not installed"
	if on, _ := agentwire.CursorHooksWired(root); on {
		cursorCapture = "installed"
	}

	u.Eyebrow("AGENT INTEGRATIONS")
	fmt.Fprintln(out)
	u.Table("", "AGENT", "MCP SERVER", "CAPTURE (STOP HOOK)").
		Row(u.Mark(claudeOn), "Claude Code", mcpLabel(agentwire.ClaudeMCPPath(root), ".mcp.json"), hookStatus(hookOn)).
		Row(u.Mark(cursorOn), "Cursor", mcpLabel(agentwire.CursorMCPPath(root), ".cursor/mcp.json"), cursorCapture).
		Row(u.Mark(vscodeOn), "VS Code", vscodeMCPLabel(root), "-").
		Row(u.Mark(codexOn), "Codex", codexMCP, codexCapture).
		Render()
}

// renderAgentGuide explains how to add Contexo to agents/harnesses beyond the
// four Contexo wires directly — covering each tool's own MCP config format,
// plus the honest state of per-turn capture. Config locations can change;
// each row points at the agent's own config so its docs remain the source of
// truth.
func renderAgentGuide(out io.Writer) {
	u := ui.Plain(out)
	u.Eyebrow("ADD CONTEXO TO ANOTHER AGENT OR HARNESS")
	fmt.Fprintln(out)
	fmt.Fprintln(out, `Contexo's MCP server is a standard stdio server:  command "ctx", args ["mcp"].`)
	fmt.Fprintln(out, "Add that to the agent's own MCP config — same server everywhere; only the")
	fmt.Fprintln(out, "file and format differ:")
	fmt.Fprintln(out)

	u.Table("AGENT", "CONFIG", "FORMAT / HOW").
		Row("Claude Code", "./.mcp.json", "JSON mcpServers   — ctx mcp install --tool=claude").
		Row("Cursor", "./.cursor/mcp.json", "JSON mcpServers   — ctx mcp install --tool=cursor").
		Row("VS Code", "./.vscode/mcp.json", "JSON servers   — ctx mcp install --tool=vscode").
		Row("Codex", "~/.codex/config.toml", "TOML [mcp_servers.contexo]   — ctx mcp install --tool=codex").
		Row("Windsurf", "~/.codeium/windsurf/mcp_config.json", "JSON mcpServers").
		Row("OpenCode", "./opencode.json", `JSON  mcp: { contexo: { type: "local", command: ["ctx","mcp"] } }`).
		Row("Hermes", "config.yaml", "YAML  mcp_servers: { contexo: { command: ctx, args: [mcp] } }").
		Row("OpenClaw", "(CLI-managed)", "run the `openclaw mcp` command").
		Render()

	fmt.Fprintln(out)
	fmt.Fprintln(out, `  JSON mcpServers entry:   "contexo": { "command": "ctx", "args": ["mcp"] }`)
	fmt.Fprintln(out, `  VS Code servers entry:   "contexo": { "type": "stdio", "command": "ctx", "args": ["mcp", "--root", "${workspaceFolder}"] }`)
	fmt.Fprintln(out, "  Any other MCP client: add the same server; see its docs for the exact path.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Capture (the Stop hook): Claude Code, Codex, and Cursor are supported.")
	u.Hint("try", "ctx hooks install --tool=claude|codex|cursor|all")
	fmt.Fprintln(out, "Claude parses its transcript; Codex and Cursor pair their prompt + response")
	fmt.Fprintln(out, "hooks inline. Other MCP-capable harnesses get the tools now; capture as they")
	fmt.Fprintln(out, "expose a stop/turn-end hook.")
}

func mcpLabel(path, rel string) string {
	if wired, _ := agentwire.WiredJSON(path); wired {
		return "wired (" + rel + ")"
	}
	return "not wired (" + rel + ")"
}

// vscodeMCPLabel is mcpLabel for ./.vscode/mcp.json. mcpLabel cannot be reused:
// it reads mcpServers, while VS Code's key is servers. A JSONC file is
// flagged rather than reported as "not wired", since we cannot tell.
func vscodeMCPLabel(root string) string {
	wired, err := agentwire.WiredVSCode(agentwire.VSCodeMCPPath(root))
	switch {
	case errors.Is(err, agentwire.ErrUnparseableVSCodeConfig):
		return "not strict JSON (.vscode/mcp.json)"
	case wired:
		return "wired (.vscode/mcp.json)"
	}
	return "not wired (.vscode/mcp.json)"
}

func hookStatus(on bool) string {
	if on {
		return "installed"
	}
	return "not installed"
}
