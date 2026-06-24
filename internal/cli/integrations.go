package cli

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/sugihAF/contexo/internal/cli/agentwire"
)

// renderIntegrationTable prints, per known agent, whether the Contexo MCP
// server and the capture (Stop) hook are integrated for this project. deps
// lets callers inject the codex probe/runner (and tests stub them).
func renderIntegrationTable(out io.Writer, root string, deps mcpWireDeps) {
	hookOn, _ := hookInstalled(root)

	codexMCP := "not installed"
	if deps.codexInstalled() {
		if wired, _ := agentwire.CodexWired(deps.runner); wired {
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

	fmt.Fprintln(out, "Agent integrations for this project:")
	fmt.Fprintln(out)
	w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "  AGENT\tMCP SERVER\tCAPTURE (STOP HOOK)")
	fmt.Fprintf(w, "  Claude Code\t%s\t%s\n", mcpLabel(agentwire.ClaudeMCPPath(root), ".mcp.json"), hookStatus(hookOn))
	fmt.Fprintf(w, "  Cursor\t%s\t%s\n", mcpLabel(agentwire.CursorMCPPath(root), ".cursor/mcp.json"), cursorCapture)
	fmt.Fprintf(w, "  Codex\t%s\t%s\n", codexMCP, codexCapture)
	_ = w.Flush()
}

// renderAgentGuide explains how to add Contexo to agents/harnesses beyond the
// three Contexo wires directly — covering each tool's own MCP config format,
// plus the honest state of per-turn capture. Config locations can change;
// each row points at the agent's own config so its docs remain the source of
// truth.
func renderAgentGuide(out io.Writer) {
	fmt.Fprintln(out, "Add Contexo to another agent or harness")
	fmt.Fprintln(out, "---------------------------------------")
	fmt.Fprintln(out, `Contexo's MCP server is a standard stdio server:  command "ctx", args ["mcp"].`)
	fmt.Fprintln(out, "Add that to the agent's own MCP config — same server everywhere; only the")
	fmt.Fprintln(out, "file and format differ:")
	fmt.Fprintln(out)

	w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "  AGENT\tCONFIG\tFORMAT / HOW")
	fmt.Fprintln(w, "  Claude Code\t./.mcp.json\tJSON mcpServers   — ctx mcp install --tool=claude")
	fmt.Fprintln(w, "  Cursor\t./.cursor/mcp.json\tJSON mcpServers   — ctx mcp install --tool=cursor")
	fmt.Fprintln(w, "  Codex\t~/.codex/config.toml\tTOML [mcp_servers.contexo]   — ctx mcp install --tool=codex")
	fmt.Fprintln(w, "  Windsurf\t~/.codeium/windsurf/mcp_config.json\tJSON mcpServers")
	fmt.Fprintln(w, "  OpenCode\t./opencode.json\tJSON  mcp: { contexo: { type: \"local\", command: [\"ctx\",\"mcp\"] } }")
	fmt.Fprintln(w, "  Hermes\tconfig.yaml\tYAML  mcp_servers: { contexo: { command: ctx, args: [mcp] } }")
	fmt.Fprintln(w, "  OpenClaw\t(CLI-managed)\trun the `openclaw mcp` command")
	_ = w.Flush()

	fmt.Fprintln(out)
	fmt.Fprintln(out, `  JSON mcpServers entry:   "contexo": { "command": "ctx", "args": ["mcp"] }`)
	fmt.Fprintln(out, "  Any other MCP client: add the same server; see its docs for the exact path.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Capture (the Stop hook): Claude Code, Codex, and Cursor are supported —")
	fmt.Fprintln(out, "  ctx hooks install --tool=claude|codex|cursor|all")
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

func hookStatus(on bool) string {
	if on {
		return "installed"
	}
	return "not installed"
}
