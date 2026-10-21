package docsdump

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestBuildCoversTheWholeCommandTree(t *testing.T) {
	d := Build()

	byPath := map[string]Command{}
	for _, c := range d.Commands {
		byPath[c.Path] = c
	}

	// Every top-level command registered in cli.NewRootCmd.
	for _, want := range []string{
		"ctx init", "ctx detach", "ctx push", "ctx pull", "ctx mcp", "ctx remote",
		"ctx auth", "ctx login", "ctx join", "ctx invite", "ctx members",
		"ctx activity", "ctx status", "ctx log", "ctx history", "ctx diff",
		"ctx evolution", "ctx capture", "ctx hooks", "ctx migrate", "ctx generate",
		"ctx version", "ctx update",
		// Hidden wave-0 stubs (WP-0.1); wave 1 unhides them. Presence, not
		// an exact list, so unhiding keeps this valid.
		"ctx show", "ctx resolve", "ctx repair",
	} {
		if _, ok := byPath[want]; !ok {
			t.Errorf("missing command %q", want)
		}
	}

	// Nested commands must be reached too.
	for _, want := range []string{
		"ctx mcp install", "ctx mcp uninstall", "ctx mcp status", "ctx mcp guide",
		"ctx remote set", "ctx remote set-repo", "ctx remote get",
		"ctx auth login", "ctx auth status",
		"ctx invite mint", "ctx invite list", "ctx invite revoke",
		"ctx members remove", "ctx capture turn", "ctx capture status",
		"ctx hooks install", "ctx hooks uninstall", "ctx hooks status",
	} {
		if _, ok := byPath[want]; !ok {
			t.Errorf("missing subcommand %q", want)
		}
	}

	push := byPath["ctx push"]
	if push.Short == "" {
		t.Error("ctx push has no Short description")
	}
	var names []string
	for _, f := range push.Flags {
		names = append(names, f.Name)
	}
	for _, want := range []string{"feature", "tag", "type", "dry-run", "yes"} {
		if !contains(names, want) {
			t.Errorf("ctx push missing flag --%s (got %v)", want, names)
		}
	}

	if got := len(d.MCPTools); got != 10 {
		t.Errorf("want 10 MCP tools, got %d", got)
	}
	if got := len(d.Resources); got != 5 {
		t.Errorf("want 5 MCP resources, got %d", got)
	}

	// The dump must round-trip as JSON — it is committed into the docs site.
	if _, err := json.Marshal(d); err != nil {
		t.Fatalf("dump is not JSON-serialisable: %v", err)
	}
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

func TestRootPersistentFlagIsRecordedAsInherited(t *testing.T) {
	d := Build()
	for _, c := range d.Commands {
		if c.Path != "ctx push" {
			continue
		}
		for _, f := range c.InheritedFlags {
			if f.Name == "root" {
				return
			}
		}
		t.Fatalf("ctx push should inherit --root, got %+v", c.InheritedFlags)
	}
	t.Fatal("ctx push not found")
}

func TestParentPathsAndSubcommandsAreLinked(t *testing.T) {
	d := Build()
	byPath := map[string]Command{}
	for _, c := range d.Commands {
		byPath[c.Path] = c
	}

	mcp, ok := byPath["ctx mcp"]
	if !ok {
		t.Fatal("ctx mcp missing")
	}
	if !contains(mcp.Subcommands, "ctx mcp install") {
		t.Errorf("ctx mcp should list its subcommands, got %v", mcp.Subcommands)
	}

	install := byPath["ctx mcp install"]
	if install.Parent != "ctx mcp" {
		t.Errorf("ctx mcp install parent = %q, want %q", install.Parent, "ctx mcp")
	}
	if install.Name != "install" {
		t.Errorf("ctx mcp install name = %q, want %q", install.Name, "install")
	}
}

func TestCobraBuiltinsAreExcluded(t *testing.T) {
	d := Build()
	for _, c := range d.Commands {
		if strings.HasSuffix(c.Path, " help") || strings.HasSuffix(c.Path, " completion") {
			t.Errorf("cobra builtin leaked into the dump: %s", c.Path)
		}
		for _, f := range c.Flags {
			if f.Name == "help" {
				t.Errorf("%s: --help should not be documented as a flag", c.Path)
			}
		}
	}
}

func TestMCPToolDescriptionsAreComplete(t *testing.T) {
	d := Build()
	if len(d.MCPTools) == 0 {
		t.Fatal("no MCP tools")
	}
	for _, tool := range d.MCPTools {
		if tool.Description == "" {
			t.Errorf("tool %s has no description", tool.Name)
		}
		if strings.HasSuffix(tool.Description, "…") {
			t.Errorf("tool %s description looks truncated", tool.Name)
		}
		if tool.InputSchema == nil {
			t.Errorf("tool %s has no input schema", tool.Name)
		}
	}
	// The three tools README.md still omits — the reason this generator exists.
	names := map[string]bool{}
	for _, tool := range d.MCPTools {
		names[tool.Name] = true
	}
	for _, want := range []string{"ctx_capture_session", "ctx_migrate", "ctx_generate"} {
		if !names[want] {
			t.Errorf("missing MCP tool %q", want)
		}
	}
}

func TestDumpContainsNothingMachineSpecific(t *testing.T) {
	d := Build()
	blob, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	text := string(blob)

	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	user := os.Getenv("USERNAME")
	if user == "" {
		user = os.Getenv("USER")
	}

	for _, bad := range []string{home, cwd, user} {
		if len(bad) < 4 {
			continue
		}
		if strings.Contains(text, bad) {
			t.Errorf("dump leaks machine-specific string %q", bad)
		}
	}

	// No absolute paths of any flavour.
	for _, pat := range []string{`C:\\Users`, "C:/Users", "/home/", "/Users/", "/var/folders"} {
		if strings.Contains(text, pat) {
			t.Errorf("dump contains an absolute path fragment %q", pat)
		}
	}

	// No timestamp: a per-run value would churn the committed snapshot on
	// every regeneration and add nothing.
	if d.Generated != "" {
		t.Errorf("Generated must stay empty, got %q", d.Generated)
	}
}
