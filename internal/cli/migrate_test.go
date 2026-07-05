package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sugihAF/contexo/internal/migrate"
)

func TestParseSelection(t *testing.T) {
	ok := []struct {
		in   string
		max  int
		want []int
	}{
		{"all", 3, []int{1, 2, 3}},
		{"*", 2, []int{1, 2}},
		{"1,2,5-7", 10, []int{1, 2, 5, 6, 7}},
		{" 3 , 1 ", 3, []int{1, 3}},
		{"2-2", 5, []int{2}},
	}
	for _, c := range ok {
		got, err := parseSelection(c.in, c.max)
		if err != nil {
			t.Errorf("parseSelection(%q): unexpected err %v", c.in, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("parseSelection(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	bad := []struct {
		in  string
		max int
	}{
		{"", 3}, {"0", 3}, {"4", 3}, {"5-3", 10}, {"a", 3}, {"1-", 3},
	}
	for _, c := range bad {
		if _, err := parseSelection(c.in, c.max); err == nil {
			t.Errorf("parseSelection(%q, max=%d) expected error", c.in, c.max)
		}
	}
}

// execMigrate runs the standalone migrate command against root. It sets the
// package-level rootDir directly (what GetRootDir reads) rather than passing a
// --root flag, and restores it after. Named execMigrate, NOT runMigrate — the
// production runMigrate lives in migrate.go in this same package.
func execMigrate(t *testing.T, root, stdin string, args ...string) string {
	t.Helper()
	prev := rootDir
	rootDir = root
	t.Cleanup(func() { rootDir = prev })

	cmd := newMigrateCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("migrate execute: %v\n%s", err, out.String())
	}
	return out.String()
}

func TestMigrateListDoesNotStage(t *testing.T) {
	root := t.TempDir()
	hub := filepath.Join(root, ".contexo")
	if err := os.MkdirAll(hub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("# Rules\n\nx"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := execMigrate(t, root, "", "--list")
	if !strings.Contains(out, "CLAUDE.md") {
		t.Errorf("--list should show candidates:\n%s", out)
	}
	if m, _ := migrate.LoadManifest(hub); m != nil {
		t.Errorf("--list must not write a manifest")
	}
}

func TestMigrateStagesManifest(t *testing.T) {
	root := t.TempDir()
	hub := filepath.Join(root, ".contexo")
	if err := os.MkdirAll(hub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("# Rules\n\nx"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Select "all", then confirm "y".
	out := execMigrate(t, root, "all\ny\n")
	if !strings.Contains(out, "finish the contexo migration") {
		t.Errorf("expected handoff message:\n%s", out)
	}
	m, err := migrate.LoadManifest(hub)
	if err != nil || m == nil || len(m.Items) != 1 {
		t.Fatalf("manifest not staged: %v %+v", err, m)
	}
	if m.Items[0].SuggestedSlug != "rules" {
		t.Errorf("staged slug = %q", m.Items[0].SuggestedSlug)
	}
}
