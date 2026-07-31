package main

import (
	"os"

	"github.com/sugihAF/contexo/internal/cli"
	"github.com/sugihAF/contexo/internal/ui"
)

func main() {
	root := cli.NewRootCmd()

	// ExecuteC returns the command that actually ran, not the root. That
	// distinction is load-bearing: mode detection keys off the resolved
	// command path and the invoked command's flags, so passing the root here
	// would classify `ctx diff --json` and `ctx version --short` as human
	// surfaces and decorate their failures with chrome a parser cannot read.
	executed, err := root.ExecuteC()
	if err != nil {
		if executed == nil {
			executed = root
		}
		// The root sets SilenceErrors, so cobra's bare "Error: <msg>" is
		// replaced by a rendered failure plus the commands that fix it.
		// Chrome goes to stderr; stdout stays data.
		ui.ForStderr(executed).RenderError(err)
		os.Exit(1)
	}
}
