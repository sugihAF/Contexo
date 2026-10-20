package syncstate

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	syncpkg "github.com/sugihAF/contexo/internal/sync"
)

// TestWP04LayoutPaths pins the .sync layout (the source of truth the
// contract cites) and checks that computing a path creates nothing.
func TestWP04LayoutPaths(t *testing.T) {
	hub := filepath.Join(t.TempDir(), ".contexo")
	const rel = "wiki/concepts/x.md"
	dotSync := filepath.Join(hub, ".sync")
	// 16:15:30.5 at +07:00 is 09:15:30Z; the fraction is dropped.
	at := time.Date(2026, 11, 3, 16, 15, 30, 500_000_000, time.FixedZone("+07:00", 7*60*60))

	cases := []struct{ name, got, want string }{
		{"SyncDir", SyncDir(hub), dotSync},
		{"BasesPath", BasesPath(hub), filepath.Join(dotSync, "bases.json")},
		{"BaseFile", BaseFile(hub, rel), filepath.Join(dotSync, "base", "wiki", "concepts", "x.md")},
		{"ConflictsPath", ConflictsPath(hub), filepath.Join(dotSync, "conflicts.json")},
		{"ConflictFile", ConflictFile(hub, rel), filepath.Join(dotSync, "conflicts", "wiki", "concepts", "x.md")},
		{"LockPath", LockPath(hub), filepath.Join(dotSync, "lock")},
		{"BackupFile", BackupFile(hub, rel, at),
			filepath.Join(dotSync, "backup", "wiki", "concepts", "x.md.20261103T091530Z")},
		{"BackupRel", BackupRel(rel, at), ".sync/backup/wiki/concepts/x.md.20261103T091530Z"},
		{"BackupRel/utc", BackupRel("raw/sessions/s.md", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)),
			".sync/backup/raw/sessions/s.md.20260102T030405Z"},
		// state.json is internal/sync's, but it lives in the same directory.
		{"StatePath", syncpkg.StatePath(hub), filepath.Join(dotSync, "state.json")},
		{"SchemaBases", SchemaBases, "ctx.bases.v1"},
		{"SchemaConflicts", SchemaConflicts, "ctx.conflicts.v1"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
	if got, want := BackupFile(hub, rel, at), filepath.Join(hub, filepath.FromSlash(BackupRel(rel, at))); got != want {
		t.Errorf("BackupFile = %q, want hub + BackupRel = %q", got, want)
	}
	if _, err := os.Lstat(hub); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("computing layout paths created %s (err %v)", hub, err)
	}
}
