// Package docsdump serialises Contexo's user-facing command surface — the
// cobra command tree and the MCP tool/resource registry — into a JSON-ready
// structure. It is consumed by cmd/ctxdocs to produce the snapshot that
// docs.contexo.live renders its flag and schema tables from, so the reference
// documentation cannot silently drift from the code.
//
// This package is build-time tooling. It is never linked into the released ctx
// binary (.goreleaser.yaml builds ./cmd/ctx only) and performs no I/O: it walks
// static descriptors and returns them.
package docsdump

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/sugihAF/contexo/internal/cli"
	"github.com/sugihAF/contexo/internal/mcp"
	"github.com/sugihAF/contexo/internal/store/pagestore"
	"github.com/sugihAF/contexo/internal/version"
)

// Dump is the whole documented surface: one CLI tree, one MCP registry.
type Dump struct {
	Version string `json:"version"`
	// Generated is intentionally always empty. A per-run timestamp would churn
	// the committed snapshot on every regeneration without telling a reader
	// anything the git history doesn't. The field exists so consumers have a
	// stable shape if that ever changes.
	Generated string     `json:"generated"`
	Commands  []Command  `json:"commands"`
	MCPTools  []MCPTool  `json:"mcpTools"`
	Resources []Resource `json:"resources"`
}

// Command is one node of the cobra tree, identified by its full invocation
// path ("ctx mcp install") rather than its bare name, so subcommands that
// share a name across parents ("status") stay distinct.
type Command struct {
	Path           string   `json:"path"`
	Name           string   `json:"name"`
	Parent         string   `json:"parent,omitempty"`
	Use            string   `json:"use"`
	Short          string   `json:"short"`
	Long           string   `json:"long,omitempty"`
	Example        string   `json:"example,omitempty"`
	Aliases        []string `json:"aliases,omitempty"`
	Hidden         bool     `json:"hidden,omitempty"`
	Runnable       bool     `json:"runnable"`
	Subcommands    []string `json:"subcommands,omitempty"`
	Flags          []Flag   `json:"flags"`
	InheritedFlags []Flag   `json:"inheritedFlags,omitempty"`
}

// Flag is one pflag definition, flattened for rendering.
type Flag struct {
	Name      string `json:"name"`
	Shorthand string `json:"shorthand,omitempty"`
	Type      string `json:"type"`
	Default   string `json:"default,omitempty"`
	Usage     string `json:"usage"`
}

// MCPTool mirrors mcp.Tool.
type MCPTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

// Resource mirrors the published parts of mcp.ResourceTemplate. Annotations
// are omitted: they steer agent context loading and mean nothing to a reader.
type Resource struct {
	URITemplate string `json:"uriTemplate"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MimeType    string `json:"mimeType,omitempty"`
}

// Build walks the live command tree and MCP registry.
func Build() Dump {
	d := Dump{Version: version.Version}

	walk(cli.NewRootCmd(), "", &d)

	// ListTools and ListResources return static descriptors and never touch
	// the store, so an empty one is safe and keeps this function I/O-free.
	srv := mcp.NewServer(&pagestore.Store{})
	for _, t := range srv.ListTools() {
		d.MCPTools = append(d.MCPTools, MCPTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}
	for _, r := range srv.ListResources() {
		d.Resources = append(d.Resources, Resource{
			URITemplate: r.URITemplate,
			Name:        r.Name,
			Description: r.Description,
			MimeType:    r.MimeType,
		})
	}

	return d
}

// walk appends cmd and every descendant to d, depth-first.
func walk(cmd *cobra.Command, parentPath string, d *Dump) {
	path := cmd.Name()
	if parentPath != "" {
		path = parentPath + " " + cmd.Name()
	}

	entry := Command{
		Path:     path,
		Name:     cmd.Name(),
		Parent:   parentPath,
		Use:      cmd.Use,
		Short:    cmd.Short,
		Long:     cmd.Long,
		Example:  cmd.Example,
		Aliases:  cmd.Aliases,
		Hidden:   cmd.Hidden,
		Runnable: cmd.Runnable(),
		Flags:    collect(cmd.LocalFlags()),
	}
	if parentPath != "" {
		entry.InheritedFlags = collect(cmd.InheritedFlags())
	}

	children := documented(cmd)
	for _, sub := range children {
		entry.Subcommands = append(entry.Subcommands, path+" "+sub.Name())
	}
	d.Commands = append(d.Commands, entry)

	for _, sub := range children {
		walk(sub, path, d)
	}
}

// documented filters out the commands cobra adds for us — they are part of
// every CLI and documenting them says nothing about Contexo.
func documented(cmd *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	for _, sub := range cmd.Commands() {
		switch sub.Name() {
		case "help", "completion":
			continue
		}
		out = append(out, sub)
	}
	return out
}

func collect(set *pflag.FlagSet) []Flag {
	out := []Flag{}
	set.VisitAll(func(f *pflag.Flag) {
		if f.Name == "help" {
			return
		}
		out = append(out, Flag{
			Name:      f.Name,
			Shorthand: f.Shorthand,
			Type:      f.Value.Type(),
			Default:   strings.TrimSpace(f.DefValue),
			Usage:     f.Usage,
		})
	})
	return out
}
