package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/sugihAF/contexo/internal/version"
)

func runRoot(t *testing.T, args ...string) string {
	t.Helper()
	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		t.Fatalf("execute %v: %v", args, err)
	}
	return buf.String()
}

func TestVersionCmd(t *testing.T) {
	old := version.Version
	defer func() { version.Version = old }()
	version.Version = "9.9.9"

	out := runRoot(t, "version")
	if !strings.Contains(out, "ctx 9.9.9") {
		t.Errorf("version output = %q, want it to contain 'ctx 9.9.9'", out)
	}

	short := runRoot(t, "version", "--short")
	if strings.TrimSpace(short) != "9.9.9" {
		t.Errorf("version --short = %q, want '9.9.9'", strings.TrimSpace(short))
	}
}

func TestUpdateCmdDevBuild(t *testing.T) {
	old := version.Version
	defer func() { version.Version = old }()
	version.Version = "dev"

	out := runRoot(t, "update")
	if !strings.Contains(strings.ToLower(out), "dev build") {
		t.Errorf("update (dev) output = %q, want it to mention a dev build", out)
	}
}
