package ui

import "testing"

// REGRESSION: matching only the first word swept human subcommands into
// machine mode, silencing their output entirely. `ctx mcp status` renders a
// report a person reads; only the bare `ctx mcp` is the JSON-RPC server.
func TestMachineCommandDoesNotSwallowHumanSubcommands(t *testing.T) {
	human := []string{
		"ctx mcp status", "ctx mcp install", "ctx mcp uninstall", "ctx mcp guide",
		"ctx capture status",
	}
	for _, p := range human {
		if machineCommand(p) {
			t.Errorf("machineCommand(%q) = true; that silences a human report", p)
		}
	}
	machine := []string{
		"ctx mcp", "ctx capture turn", "ctx completion bash", "ctx __complete",
	}
	for _, p := range machine {
		if !machineCommand(p) {
			t.Errorf("machineCommand(%q) = false; machine output would get chrome", p)
		}
	}
}
