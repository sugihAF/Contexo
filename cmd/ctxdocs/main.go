// Command ctxdocs prints Contexo's command surface as JSON, for the docs site
// to render its flag and schema tables from.
//
// It is deliberately NOT part of the ctx binary: .goreleaser.yaml builds
// ./cmd/ctx only, so this never ships to users. It reads static descriptors —
// no network, no auth, no .contexo state — and writes only to stdout.
//
// Regenerate the committed snapshot after changing any command or MCP tool:
//
//	go run ./cmd/ctxdocs > ../contexo-landing/apps/docs/src/data/contexo-cli.json
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/sugihAF/contexo/internal/docsdump"
)

func main() {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(docsdump.Build()); err != nil {
		fmt.Fprintln(os.Stderr, "ctxdocs:", err)
		os.Exit(1)
	}
}
