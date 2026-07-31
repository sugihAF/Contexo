package ui

import (
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// Mode is resolved ONCE per process, by the root command, before any
// subcommand runs. Resolving it per call site is how machine output gets
// corrupted: every new print site would have to remember the rule.
type Mode struct {
	// Machine means the output is consumed by a program, not a human:
	// JSON-RPC, hook payloads, shell completion, --json, version --short.
	// In this mode the UI emits nothing and spinners/forms are disabled.
	Machine bool
	// TTY reports whether the DESTINATION STREAM is a terminal. Probe the fd
	// you actually write to -- ctx's update nudge probes stdout but writes to
	// stderr, and flipping that silently turns the nudge on for
	// `ctx completion bash > file`.
	TTY bool
	// Dark is explicit, never queried. See the package doc.
	Dark bool
}

// machineCommand reports whether a resolved cobra command path is a
// machine-consumed surface.
//
// Matching is on the FULL path, not the first word. Matching the group name
// would sweep in human subcommands that merely live under a machine-sounding
// parent: `ctx mcp status`, `ctx mcp install`, `ctx mcp guide`, and
// `ctx capture status` are all reports a person reads, and classifying them as
// machine silences their output entirely.
//
// The genuinely machine surfaces are:
//   - `ctx mcp` with NO subcommand -- the stdio JSON-RPC server
//   - `ctx capture turn` -- the agent hook target
//   - shell completion, in both its script and RPC forms
func machineCommand(path string) bool {
	fields := strings.Fields(path)
	if len(fields) < 2 {
		return false
	}
	// Shell completion, whatever the shell argument.
	switch fields[1] {
	case "completion", "__complete", "__completeNoDesc":
		return true
	}
	// The bare stdio server: `ctx mcp` and nothing more.
	if fields[1] == "mcp" && len(fields) == 2 {
		return true
	}
	// The hook target: `ctx capture turn`.
	if fields[1] == "capture" && len(fields) >= 3 && fields[2] == "turn" {
		return true
	}
	return false
}

// DetectMode resolves the mode for cmd writing to w.
func DetectMode(cmd *cobra.Command, w io.Writer) Mode {
	m := Mode{Dark: darkBackground()}

	if cmd != nil {
		if machineCommand(cmd.CommandPath()) {
			m.Machine = true
		}
		// --json is a hard styling kill-switch on both streams.
		if f := cmd.Flags().Lookup("json"); f != nil && f.Value.String() == "true" {
			m.Machine = true
		}
		// `version --short` is asserted by exact whole-output equality.
		if f := cmd.Flags().Lookup("short"); f != nil && f.Value.String() == "true" {
			m.Machine = true
		}
	}
	if os.Getenv("NO_COLOR") != "" {
		m.TTY = false
		return m
	}
	m.TTY = writerIsTTY(w)
	return m
}

// writerIsTTY probes the actual destination stream.
func writerIsTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// darkBackground decides the theme WITHOUT querying the terminal.
// CTX_THEME wins; otherwise COLORFGBG is read as a passive hint; otherwise
// dark, which is the overwhelmingly common developer default.
func darkBackground() bool {
	switch strings.ToLower(os.Getenv("CTX_THEME")) {
	case "light":
		return false
	case "dark":
		return true
	}
	// COLORFGBG is "fg;bg"; backgrounds 7 and 15 are the light ones.
	if v := os.Getenv("COLORFGBG"); v != "" {
		parts := strings.Split(v, ";")
		switch parts[len(parts)-1] {
		case "7", "15":
			return false
		}
	}
	return true
}
