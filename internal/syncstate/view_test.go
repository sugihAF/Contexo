package syncstate

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	syncpkg "github.com/sugihAF/contexo/internal/sync"
)

// Server shas used by the view tests: the base sha, a newer server sha and
// an unrelated one (what an old binary bumped page_shas away from).
const (
	wp04viewSB = "1111111111111111111111111111111111111111"
	wp04viewSR = "2222222222222222222222222222222222222222"
	wp04viewSX = "3333333333333333333333333333333333333333"
)

// wp04viewP is the page path most view tests look at.
const wp04viewP = "wiki/concepts/x.md"

// wp04viewPage returns a valid ctx.page.v1 concept page with the given slug
// and body, its tags written as a flow list.
func wp04viewPage(slug, body string) []byte {
	return []byte("---\nschema: ctx.page.v1\nslug: " + slug +
		"\ntype: concept\nauthor: tester\ncreated: 2026-01-02T00:00:00Z\n" +
		"updated: 2026-01-02T03:04:05Z\nparent_sha: \"\"\ntags: [a, b]\n---\n" + body)
}

// wp04viewSamePage is wp04viewPage(slug, body) in another spelling that the
// comparison rule treats as equal: a leading BOM, CRLF line ends and the
// tags as a block list.
func wp04viewSamePage(slug, body string) []byte {
	s := "---\nschema: ctx.page.v1\nslug: " + slug +
		"\ntype: concept\nauthor: tester\ncreated: 2026-01-02T00:00:00Z\n" +
		"updated: 2026-01-02T03:04:05Z\nparent_sha: \"\"\ntags:\n  - a\n  - b\n---\n" + body
	return append([]byte{0xEF, 0xBB, 0xBF}, strings.ReplaceAll(s, "\n", "\r\n")...)
}

// wp04viewHub returns a fresh hub directory <tmp>/.contexo.
func wp04viewHub(t *testing.T) string {
	t.Helper()
	hub := filepath.Join(t.TempDir(), ".contexo")
	if err := os.MkdirAll(hub, 0o755); err != nil {
		t.Fatal(err)
	}
	return hub
}

// wp04viewWrite writes data to hub/rel, creating parent directories.
func wp04viewWrite(t *testing.T, hub, rel string, data []byte) {
	t.Helper()
	p := filepath.Join(hub, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// wp04viewState saves state.json with the given page_shas.
func wp04viewState(t *testing.T, hub string, shas map[string]string) {
	t.Helper()
	if err := syncpkg.SaveState(hub, &syncpkg.State{PageSHAs: shas}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
}

// wp04viewBase stores content as the base of rel for sha and saves
// bases.json.
func wp04viewBase(t *testing.T, hub, rel string, content []byte, sha string) {
	t.Helper()
	b, err := OpenBases(hub)
	if err != nil {
		t.Fatalf("OpenBases: %v", err)
	}
	if err := b.Put(rel, content, sha, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)); err != nil {
		t.Fatalf("Bases.Put(%s): %v", rel, err)
	}
	if err := b.Save(); err != nil {
		t.Fatalf("Bases.Save: %v", err)
	}
}

// wp04viewSidecar records rec with content and saves conflicts.json.
func wp04viewSidecar(t *testing.T, hub string, rec ConflictRecord, content []byte) {
	t.Helper()
	c, err := OpenConflicts(hub)
	if err != nil {
		t.Fatalf("OpenConflicts: %v", err)
	}
	if rec.DetectedAt.IsZero() {
		rec.DetectedAt = time.Date(2026, 1, 3, 4, 5, 6, 0, time.UTC)
	}
	if err := c.Put(rec, content); err != nil {
		t.Fatalf("Conflicts.Put(%s): %v", rec.Path, err)
	}
	if err := c.Save(); err != nil {
		t.Fatalf("Conflicts.Save: %v", err)
	}
}

// wp04viewLoad runs LoadView, fails the test on error and checks the View
// invariants every caller relies on.
func wp04viewLoad(t *testing.T, hub string, remote map[string]string) *View {
	t.Helper()
	v, err := LoadView(hub, remote)
	if err != nil {
		t.Fatalf("LoadView: %v", err)
	}
	if v == nil {
		t.Fatal("LoadView returned a nil *View")
	}
	if v.State == nil || v.State.PageSHAs == nil || v.Bases == nil ||
		v.Conflicts == nil || v.Scan == nil || v.Facts == nil {
		t.Fatalf("LoadView left a nil member: %+v", v)
	}
	if v.Paths == nil || v.Corrupt == nil {
		t.Errorf("Paths and Corrupt must be non-nil (Paths=%v Corrupt=%v)", v.Paths == nil, v.Corrupt == nil)
	}
	if !sort.StringsAreSorted(v.Paths) {
		t.Errorf("Paths not sorted: %q", v.Paths)
	}
	if len(v.Facts) != len(v.Paths) {
		t.Errorf("len(Facts) = %d, len(Paths) = %d", len(v.Facts), len(v.Paths))
	}
	for i, p := range v.Paths {
		if i > 0 && v.Paths[i-1] == p {
			t.Errorf("Paths lists %q twice", p)
		}
		f, ok := v.Facts[p]
		switch {
		case !ok || f == nil:
			t.Errorf("Facts has no entry for %q", p)
		case f.Path != p:
			t.Errorf("Facts[%q].Path = %q", p, f.Path)
		}
	}
	return v
}

// wp04viewFacts returns the facts of rel and fails the test when rel is not
// an entry.
func wp04viewFacts(t *testing.T, v *View, rel string) *PathFacts {
	t.Helper()
	f, ok := v.Facts[rel]
	if !ok || f == nil {
		t.Fatalf("%s is not an entry; Paths = %q", rel, v.Paths)
	}
	return f
}

// wp04viewEntry is one path of a wp04viewSnapshot.
type wp04viewEntry struct {
	Mode    fs.FileMode
	ModTime time.Time
	Size    int64
	Data    string // regular files only
}

func (e wp04viewEntry) String() string {
	return fmt.Sprintf("mode %s mtime %s size %d", e.Mode, e.ModTime.Format(time.RFC3339Nano), e.Size)
}

// wp04viewSnapshot records every path under dir with its mode, mtime, size
// and content, so a test can prove a reader changed nothing. Each entry comes
// from os.Lstat, not the walk's DirEntry.Info: on Windows that FileInfo is the
// directory-enumeration record, whose NTFS timestamps can lag the file's own
// and change once the file has been read (windows-latest CI, WP-0.I).
func wp04viewSnapshot(t *testing.T, dir string) map[string]wp04viewEntry {
	t.Helper()
	out := map[string]wp04viewEntry{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		e := wp04viewEntry{Mode: info.Mode(), ModTime: info.ModTime(), Size: info.Size()}
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			e.Data = string(data)
		}
		out[p] = e
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// wp04viewSnapshotDiff lists every path whose snapshot entry differs between
// before and after, with its before/after mode, mtime and size; nil when
// nothing changed.
func wp04viewSnapshotDiff(before, after map[string]wp04viewEntry) []string {
	paths := make([]string, 0, len(before)+len(after))
	for p := range before {
		paths = append(paths, p)
	}
	for p := range after {
		if _, ok := before[p]; !ok {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	var diff []string
	for _, p := range paths {
		b, inBefore := before[p]
		a, inAfter := after[p]
		switch {
		case !inAfter:
			diff = append(diff, fmt.Sprintf("removed %s (before: %s)", p, b))
		case !inBefore:
			diff = append(diff, fmt.Sprintf("added %s (after: %s)", p, a))
		case b.Mode != a.Mode || !b.ModTime.Equal(a.ModTime) || b.Size != a.Size || b.Data != a.Data:
			l := fmt.Sprintf("changed %s: before %s; after %s", p, b, a)
			if b.Data != a.Data {
				l += " (content differs)"
			}
			diff = append(diff, l)
		}
	}
	return diff
}

// TestLoadViewKinds builds, for each contract decision-table row r01-r19
// (+ r03b, r12b), the on-disk inputs of that row and checks the three Side
// values LoadView gathers. Base "set" rows alternate between a valid base and
// the three ways a base goes stale.
func TestLoadViewKinds(t *testing.T) {
	baseBytes := wp04viewPage("x", "# X\n\nbase\n")
	locals := map[string][]byte{
		"same": wp04viewSamePage("x", "# X\n\nbase\n"),
		"diff": wp04viewPage("x", "# X\n\nlocal edit\n"),
		// Invalid working files, one per stage.
		"parse":    []byte("---\nslug: x\nno closing delimiter\n"),
		"validate": []byte("---\nschema: ctx.page.v1\nslug: x\ntype: concept\ncreated: 2026-01-02T00:00:00Z\n---\nno author\n"),
		"mismatch": wp04viewPage("elsewhere", "# Elsewhere\n"),
	}
	if bytes.Equal(locals["same"], baseBytes) || !Equal(locals["same"], baseBytes) {
		t.Fatal("precondition: the same-page variant must differ in bytes but be Equal to the base")
	}
	if Equal(locals["diff"], baseBytes) {
		t.Fatal("precondition: the diff variant must not be Equal to the base")
	}
	stages := map[string]string{"parse": StageParse, "validate": StageValidate, "mismatch": StagePathMismatch}
	remotes := map[string]map[string]string{
		"unknown": nil,
		"empty":   {}, // repo missing: every path absent
		"absent":  {"wiki/concepts/elsewhere.md": wp04viewSR},
		"same":    {wp04viewP: wp04viewSB},
		"other":   {wp04viewP: wp04viewSR},
	}

	rows := []struct {
		id                  string
		local, base, remote string
		want                []string // Local, Base, Remote kinds; nil when P is no entry
	}{
		{"r01", "mismatch", "valid", "same", []string{"invalid", "valid", "present"}},
		{"r01/validate", "validate", "none", "unknown", []string{"invalid", "none", "unknown"}},
		{"r02", "parse", "stale-sha", "other", []string{"invalid", "stale", "present"}},
		{"r03", "absent", "valid", "same", []string{"absent", "valid", "present"}},
		{"r03b", "absent", "stale-norec", "unknown", []string{"absent", "stale", "unknown"}},
		{"r04", "absent", "valid", "other", []string{"absent", "valid", "present"}},
		{"r05", "absent", "stale-nofile", "empty", []string{"absent", "stale", "absent"}},
		{"r06", "absent", "none", "other", []string{"absent", "none", "present"}},
		{"r07", "absent", "blank", "absent", nil},
		{"r07/unknown", "absent", "blank", "unknown", nil},
		{"r08", "same", "none", "other", []string{"valid", "none", "present"}},
		{"r09", "same", "none", "absent", []string{"valid", "none", "absent"}},
		{"r10", "diff", "none", "unknown", []string{"valid", "none", "unknown"}},
		{"r11", "same", "stale-norec", "absent", []string{"valid", "stale", "absent"}},
		{"r12", "diff", "stale-sha", "other", []string{"valid", "stale", "present"}},
		{"r12b", "same", "stale-nofile", "unknown", []string{"valid", "stale", "unknown"}},
		{"r13", "diff", "valid", "empty", []string{"valid", "valid", "absent"}},
		{"r14", "same", "valid", "same", []string{"valid", "valid", "present"}},
		{"r15", "diff", "valid", "same", []string{"valid", "valid", "present"}},
		{"r16", "same", "valid", "unknown", []string{"valid", "valid", "unknown"}},
		{"r17", "diff", "valid", "unknown", []string{"valid", "valid", "unknown"}},
		{"r18", "same", "valid", "other", []string{"valid", "valid", "present"}},
		{"r19", "diff", "valid", "other", []string{"valid", "valid", "present"}},
	}
	for _, row := range rows {
		t.Run(row.id, func(t *testing.T) {
			hub := wp04viewHub(t)
			local := locals[row.local]
			if row.local != "absent" {
				wp04viewWrite(t, hub, wp04viewP, local)
			}
			switch row.base {
			case "none":
				// No state.json and no base at all.
			case "blank": // page_shas has P with "": no base, despite a record
				wp04viewState(t, hub, map[string]string{wp04viewP: ""})
				wp04viewBase(t, hub, wp04viewP, baseBytes, wp04viewSX)
			case "valid":
				wp04viewState(t, hub, map[string]string{wp04viewP: wp04viewSB})
				wp04viewBase(t, hub, wp04viewP, baseBytes, wp04viewSB)
			case "stale-norec": // first run after the upgrade
				wp04viewState(t, hub, map[string]string{wp04viewP: wp04viewSB})
			case "stale-sha": // an old binary bumped page_shas only
				wp04viewState(t, hub, map[string]string{wp04viewP: wp04viewSB})
				wp04viewBase(t, hub, wp04viewP, baseBytes, wp04viewSX)
			case "stale-nofile": // the base file is gone
				wp04viewState(t, hub, map[string]string{wp04viewP: wp04viewSB})
				wp04viewBase(t, hub, wp04viewP, baseBytes, wp04viewSB)
				if err := os.Remove(BaseFile(hub, wp04viewP)); err != nil {
					t.Fatal(err)
				}
			default:
				t.Fatalf("unknown base setup %q", row.base)
			}
			remote, ok := remotes[row.remote]
			if !ok {
				t.Fatalf("unknown remote setup %q", row.remote)
			}

			v := wp04viewLoad(t, hub, remote)

			if row.want == nil {
				if _, ok := v.Facts[wp04viewP]; ok {
					t.Fatalf("%s is an entry, want none (Paths = %q)", wp04viewP, v.Paths)
				}
				return
			}
			f := wp04viewFacts(t, v, wp04viewP)

			// Local.
			wantLocal := Side{Kind: row.want[0]}
			if row.want[0] == "valid" {
				wantLocal.Hash = Hash(local)
			}
			if f.Local != wantLocal {
				t.Errorf("Local = %+v, want %+v", f.Local, wantLocal)
			}
			switch {
			case row.local == "absent" && f.File != nil:
				t.Errorf("File = %+v, want nil", f.File)
			case row.local != "absent" && f.File == nil:
				t.Error("File is nil for an existing working file")
			case row.local != "absent":
				if f.File != v.Scan.Files[wp04viewP] {
					t.Error("File is not the scan's LocalFile")
				}
				if want := stages[row.local]; want != "" {
					if f.File.Invalid == nil || f.File.Invalid.Stage != want {
						t.Errorf("File.Invalid = %+v, want stage %s", f.File.Invalid, want)
					}
				}
			}

			// Base.
			wantBase := Side{Kind: row.want[1]}
			if row.want[1] != "none" {
				wantBase.SHA = wp04viewSB
			}
			if row.want[1] == "valid" {
				wantBase.Hash = Hash(baseBytes)
			}
			if f.Base != wantBase {
				t.Errorf("Base = %+v, want %+v", f.Base, wantBase)
			}
			if rec, ok := v.Bases.Get(wp04viewP); ok {
				if f.BaseRecord == nil || *f.BaseRecord != rec {
					t.Errorf("BaseRecord = %+v, want %+v", f.BaseRecord, rec)
				}
			} else if f.BaseRecord != nil {
				t.Errorf("BaseRecord = %+v, want nil (no record)", f.BaseRecord)
			}
			if row.want[0] == "valid" && row.want[1] == "valid" {
				if same := f.Local.Hash == f.Base.Hash; same != (row.local == "same") {
					t.Errorf("hL == hB is %v, want %v", same, row.local == "same")
				}
			}

			// Remote.
			wantRemote := Side{Kind: row.want[2]}
			if row.want[2] == "present" {
				wantRemote.SHA = remote[wp04viewP]
			}
			if f.Remote != wantRemote {
				t.Errorf("Remote = %+v, want %+v", f.Remote, wantRemote)
			}
			if f.RemoteFromSidecar || f.Conflict != nil {
				t.Errorf("RemoteFromSidecar = %v, Conflict = %+v; no sidecar exists", f.RemoteFromSidecar, f.Conflict)
			}
			if remote == nil {
				for p, pf := range v.Facts {
					if pf.Remote != (Side{Kind: RemoteUnknown}) {
						t.Errorf("nil listing: %s Remote = %+v, want unknown", p, pf.Remote)
					}
				}
			}
			if remote != nil && len(remote) == 0 {
				for p, pf := range v.Facts {
					if pf.Remote != (Side{Kind: RemoteAbsent}) {
						t.Errorf("empty listing: %s Remote = %+v, want absent", p, pf.Remote)
					}
				}
			}
		})
	}
}

// TestWP04ViewKindLiterals pins the Side kind constants to the contract
// LocalKind/BaseKind/RemoteKind literals.
func TestWP04ViewKindLiterals(t *testing.T) {
	got := []string{LocalAbsent, LocalInvalid, LocalValid, BaseNone, BaseValid, BaseStale,
		RemoteUnknown, RemoteAbsent, RemotePresent}
	want := []string{"absent", "invalid", "valid", "none", "valid", "stale",
		"unknown", "absent", "present"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("kind constants = %q, want %q", got, want)
	}
}

func TestLoadViewOfflineSidecarRemote(t *testing.T) {
	baseBytes := wp04viewPage("x", "# X\n\nbase\n")
	localBytes := wp04viewPage("x", "# X\n\nlocal edit\n")
	theirs := wp04viewPage("x", "# X\n\ntheirs\n")

	t.Run("server_sha_differs", func(t *testing.T) {
		hub := wp04viewHub(t)
		wp04viewWrite(t, hub, wp04viewP, localBytes)
		wp04viewState(t, hub, map[string]string{wp04viewP: wp04viewSB})
		wp04viewBase(t, hub, wp04viewP, baseBytes, wp04viewSB)
		wp04viewSidecar(t, hub, ConflictRecord{Path: wp04viewP, ServerSHA: wp04viewSR, BaseSHA: wp04viewSB,
			Kind: KindBothModified, Source: SourcePush}, theirs)

		v := wp04viewLoad(t, hub, nil)
		f := wp04viewFacts(t, v, wp04viewP)
		if want := (Side{Kind: RemotePresent, SHA: wp04viewSR}); f.Remote != want {
			t.Errorf("Remote = %+v, want %+v", f.Remote, want)
		}
		if !f.RemoteFromSidecar {
			t.Error("RemoteFromSidecar = false, want true")
		}
		rec, _ := v.Conflicts.Get(wp04viewP)
		if f.Conflict == nil || *f.Conflict != rec || f.Conflict.Path != wp04viewP {
			t.Errorf("Conflict = %+v, want %+v", f.Conflict, rec)
		}
		if f.Local.Kind != LocalValid || f.Base.Kind != BaseValid || f.Base.SHA != wp04viewSB {
			t.Errorf("Local = %+v, Base = %+v; the sidecar must not change them", f.Local, f.Base)
		}
	})

	t.Run("server_sha_equals_page_sha", func(t *testing.T) {
		hub := wp04viewHub(t)
		wp04viewWrite(t, hub, wp04viewP, localBytes)
		wp04viewState(t, hub, map[string]string{wp04viewP: wp04viewSB})
		wp04viewBase(t, hub, wp04viewP, baseBytes, wp04viewSB)
		wp04viewSidecar(t, hub, ConflictRecord{Path: wp04viewP, ServerSHA: wp04viewSB, BaseSHA: wp04viewSX,
			Kind: KindBothModified, Source: SourcePull}, theirs)

		f := wp04viewFacts(t, wp04viewLoad(t, hub, nil), wp04viewP)
		if want := (Side{Kind: RemoteUnknown}); f.Remote != want {
			t.Errorf("Remote = %+v, want %+v", f.Remote, want)
		}
		if f.RemoteFromSidecar {
			t.Error("RemoteFromSidecar = true, want false")
		}
		if f.Conflict == nil || f.Conflict.ServerSHA != wp04viewSB {
			t.Errorf("Conflict = %+v, want the record (it exists)", f.Conflict)
		}
	})

	t.Run("both_added_without_page_sha", func(t *testing.T) {
		hub := wp04viewHub(t)
		wp04viewWrite(t, hub, wp04viewP, localBytes)
		wp04viewSidecar(t, hub, ConflictRecord{Path: wp04viewP, ServerSHA: wp04viewSR,
			Kind: KindBothAdded, Source: SourcePush}, nil)

		f := wp04viewFacts(t, wp04viewLoad(t, hub, nil), wp04viewP)
		if want := (Side{Kind: RemotePresent, SHA: wp04viewSR}); f.Remote != want {
			t.Errorf("Remote = %+v, want %+v", f.Remote, want)
		}
		if !f.RemoteFromSidecar {
			t.Error("RemoteFromSidecar = false, want true")
		}
		if f.Base != (Side{Kind: BaseNone}) || f.BaseRecord != nil {
			t.Errorf("Base = %+v, BaseRecord = %+v, want none/nil", f.Base, f.BaseRecord)
		}
		if f.Conflict == nil || f.Conflict.Kind != KindBothAdded || f.Conflict.HasContent {
			t.Errorf("Conflict = %+v", f.Conflict)
		}
	})

	t.Run("compares_with_page_sha_not_base_record", func(t *testing.T) {
		// The base record belongs to sx (stale); the sidecar's server sha is
		// that same sx. E9 compares with page_shas (sb), so the remote is
		// present(sx).
		hub := wp04viewHub(t)
		wp04viewWrite(t, hub, wp04viewP, localBytes)
		wp04viewState(t, hub, map[string]string{wp04viewP: wp04viewSB})
		wp04viewBase(t, hub, wp04viewP, baseBytes, wp04viewSX)
		wp04viewSidecar(t, hub, ConflictRecord{Path: wp04viewP, ServerSHA: wp04viewSX, BaseSHA: wp04viewSB,
			Kind: KindBothModified, Source: SourceRepair}, theirs)

		f := wp04viewFacts(t, wp04viewLoad(t, hub, nil), wp04viewP)
		if f.Base != (Side{Kind: BaseStale, SHA: wp04viewSB}) {
			t.Errorf("Base = %+v, want stale(sb)", f.Base)
		}
		if want := (Side{Kind: RemotePresent, SHA: wp04viewSX}); f.Remote != want || !f.RemoteFromSidecar {
			t.Errorf("Remote = %+v (from sidecar %v), want %+v from the sidecar", f.Remote, f.RemoteFromSidecar, want)
		}
	})

	t.Run("deleted_local", func(t *testing.T) {
		hub := wp04viewHub(t)
		wp04viewState(t, hub, map[string]string{wp04viewP: wp04viewSB})
		wp04viewBase(t, hub, wp04viewP, baseBytes, wp04viewSB)
		wp04viewSidecar(t, hub, ConflictRecord{Path: wp04viewP, ServerSHA: wp04viewSR, BaseSHA: wp04viewSB,
			Kind: KindBothModified, Source: SourcePull}, theirs)

		f := wp04viewFacts(t, wp04viewLoad(t, hub, nil), wp04viewP)
		if f.Local != (Side{Kind: LocalAbsent}) || f.File != nil {
			t.Errorf("Local = %+v, File = %+v, want absent/nil", f.Local, f.File)
		}
		if want := (Side{Kind: RemotePresent, SHA: wp04viewSR}); f.Remote != want || !f.RemoteFromSidecar {
			t.Errorf("Remote = %+v (from sidecar %v), want %+v from the sidecar", f.Remote, f.RemoteFromSidecar, want)
		}
	})
}

func TestLoadViewLiveIgnoresSidecar(t *testing.T) {
	cases := []struct {
		name   string
		remote map[string]string
		want   Side
	}{
		{"listed_newer", map[string]string{wp04viewP: wp04viewSX}, Side{Kind: RemotePresent, SHA: wp04viewSX}},
		{"listed_at_base", map[string]string{wp04viewP: wp04viewSB}, Side{Kind: RemotePresent, SHA: wp04viewSB}},
		{"not_listed", map[string]string{"wiki/concepts/other.md": wp04viewSX}, Side{Kind: RemoteAbsent}},
		{"repo_missing", map[string]string{}, Side{Kind: RemoteAbsent}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hub := wp04viewHub(t)
			wp04viewWrite(t, hub, wp04viewP, wp04viewPage("x", "# X\n\nlocal edit\n"))
			wp04viewState(t, hub, map[string]string{wp04viewP: wp04viewSB})
			wp04viewBase(t, hub, wp04viewP, wp04viewPage("x", "# X\n\nbase\n"), wp04viewSB)
			wp04viewSidecar(t, hub, ConflictRecord{Path: wp04viewP, ServerSHA: wp04viewSR, BaseSHA: wp04viewSB,
				Kind: KindBothModified, Source: SourcePush}, wp04viewPage("x", "# X\n\ntheirs\n"))

			f := wp04viewFacts(t, wp04viewLoad(t, hub, tc.remote), wp04viewP)
			if f.Remote != tc.want {
				t.Errorf("Remote = %+v, want %+v", f.Remote, tc.want)
			}
			if f.RemoteFromSidecar {
				t.Error("RemoteFromSidecar = true with a live listing")
			}
			if f.Conflict == nil || f.Conflict.ServerSHA != wp04viewSR {
				t.Errorf("Conflict = %+v, want the sidecar record", f.Conflict)
			}
		})
	}
}

func TestWP04LoadViewCorruptIndexes(t *testing.T) {
	garbage := []byte("{not json\n")
	cases := []struct {
		name    string
		corrupt []string // which index files hold garbage, in any order
		want    []string
	}{
		{"all", []string{"conflicts.json", "state.json", "bases.json"},
			[]string{"state.json", "bases.json", "conflicts.json"}},
		{"state_only", []string{"state.json"}, []string{"state.json"}},
		{"bases_only", []string{"bases.json"}, []string{"bases.json"}},
		{"conflicts_and_bases", []string{"conflicts.json", "bases.json"},
			[]string{"bases.json", "conflicts.json"}},
		{"none", nil, []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hub := wp04viewHub(t)
			page := wp04viewPage("x", "# X\n")
			wp04viewWrite(t, hub, wp04viewP, page)
			wp04viewState(t, hub, map[string]string{wp04viewP: wp04viewSB})
			wp04viewBase(t, hub, wp04viewP, page, wp04viewSB)
			wp04viewSidecar(t, hub, ConflictRecord{Path: wp04viewP, ServerSHA: wp04viewSR, BaseSHA: wp04viewSB,
				Kind: KindBothModified, Source: SourcePush}, nil)
			bad := map[string]bool{}
			for _, name := range tc.corrupt {
				bad[name] = true
				if err := os.WriteFile(filepath.Join(SyncDir(hub), name), garbage, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			before := wp04viewSnapshot(t, hub)

			v := wp04viewLoad(t, hub, nil)

			if !reflect.DeepEqual(v.Corrupt, tc.want) {
				t.Errorf("Corrupt = %q, want %q", v.Corrupt, tc.want)
			}
			if v.Bases.Corrupt() != bad["bases.json"] || v.Conflicts.Corrupt() != bad["conflicts.json"] {
				t.Errorf("Bases.Corrupt() = %v, Conflicts.Corrupt() = %v", v.Bases.Corrupt(), v.Conflicts.Corrupt())
			}
			f := wp04viewFacts(t, v, wp04viewP)
			if f.Local.Kind != LocalValid {
				t.Errorf("Local = %+v, want valid", f.Local)
			}
			// A corrupt index is read as empty: no page_shas means no base,
			// a lost bases.json means a stale base, lost sidecars mean no
			// offline remote.
			wantBase := BaseValid
			switch {
			case bad["state.json"]:
				wantBase = BaseNone
			case bad["bases.json"]:
				wantBase = BaseStale
			}
			if f.Base.Kind != wantBase {
				t.Errorf("Base = %+v, want kind %s", f.Base, wantBase)
			}
			wantRemote := Side{Kind: RemotePresent, SHA: wp04viewSR}
			if bad["conflicts.json"] {
				wantRemote = Side{Kind: RemoteUnknown}
			}
			if f.Remote != wantRemote {
				t.Errorf("Remote = %+v, want %+v", f.Remote, wantRemote)
			}
			if diff := wp04viewSnapshotDiff(before, wp04viewSnapshot(t, hub)); len(diff) != 0 {
				t.Errorf("LoadView changed the hub; it must only read (no repair of corrupt indexes):\n%s",
					strings.Join(diff, "\n"))
			}
		})
	}
}

func TestWP04LoadViewEntrySet(t *testing.T) {
	hub := wp04viewHub(t)
	page := func(slug string) []byte { return wp04viewPage(slug, "# "+slug+"\n") }
	// Working files: all of them are entries, valid or not, even one whose
	// path breaks the page path rules.
	files := map[string][]byte{
		"wiki/concepts/a.md":      page("a"),
		"wiki/concepts/broken.md": []byte("---\nno closing delimiter\n"),
		"wiki/concepts/-dash.md":  page("dash"),
		"wiki/tags.md":            []byte("# not the root tags.md\n"),
		"raw/sessions/s1.md":      []byte("---\nschema: ctx.page.v1\nslug: s1\ntype: source\nauthor: t\ncreated: 2026-01-02T00:00:00Z\n---\nsession\n"),
		// Never entries: root index/tags, dot-dirs, non-pages.
		"index.md":                       []byte("# Knowledge Index\n"),
		"tags.md":                        []byte("# Tags\n"),
		".sync/base/wiki/concepts/z.md":  page("z"),
		".migrate/wiki/concepts/m.md":    page("m"),
		"wiki/concepts/notes.txt":        []byte("text\n"),
		"wiki/concepts/.a.md.tmp-123456": page("a"),
	}
	for rel, data := range files {
		wp04viewWrite(t, hub, rel, data)
	}
	wp04viewState(t, hub, map[string]string{
		"wiki/concepts/b.md":     wp04viewSB, // deleted locally
		"wiki/concepts/a.md":     wp04viewSB,
		"wiki/concepts/empty.md": "", // no base: not an entry
		// Keys that are not page paths are dropped.
		"index.md":           wp04viewSB,
		"tags.md":            wp04viewSB,
		"../escape.md":       wp04viewSB,
		".sync/state.json":   wp04viewSB,
		".sync/base/b.md":    wp04viewSB,
		"wiki/concepts/x.MD": wp04viewSB,
		"config.json":        wp04viewSB,
		"":                   wp04viewSB,
	})
	// A base record and a sidecar alone make no entry.
	wp04viewBase(t, hub, "wiki/concepts/based-only.md", page("based-only"), wp04viewSX)
	wp04viewSidecar(t, hub, ConflictRecord{Path: "wiki/concepts/sidecar-only.md", ServerSHA: wp04viewSR,
		Kind: KindBothAdded, Source: SourcePush}, nil)
	remote := map[string]string{
		"wiki/concepts/a.md":       wp04viewSB,
		"wiki/concepts/c.md":       wp04viewSR, // remote only
		"wiki/entities/e.md":       wp04viewSR,
		"wiki/index.md":            wp04viewSR, // only the ROOT index.md is excluded
		"index.md":                 wp04viewSR,
		"tags.md":                  wp04viewSR,
		"wiki/../escape.md":        wp04viewSR,
		"/abs.md":                  wp04viewSR,
		`wiki\concepts\win.md`:     wp04viewSR,
		".sync/conflicts/c.md":     wp04viewSR,
		"wiki/concepts/readme.txt": wp04viewSR,
		"wiki//double.md":          wp04viewSR,
	}
	before := wp04viewSnapshot(t, hub)

	wantLocalOnly := []string{
		"raw/sessions/s1.md",
		"wiki/concepts/-dash.md",
		"wiki/concepts/a.md",
		"wiki/concepts/b.md",
		"wiki/concepts/broken.md",
		"wiki/tags.md",
	}
	v := wp04viewLoad(t, hub, nil)
	if !reflect.DeepEqual(v.Paths, wantLocalOnly) {
		t.Errorf("offline Paths = %q\nwant %q", v.Paths, wantLocalOnly)
	}

	v = wp04viewLoad(t, hub, remote)
	want := []string{
		"raw/sessions/s1.md",
		"wiki/concepts/-dash.md",
		"wiki/concepts/a.md",
		"wiki/concepts/b.md",
		"wiki/concepts/broken.md",
		"wiki/concepts/c.md",
		"wiki/entities/e.md",
		"wiki/index.md",
		"wiki/tags.md",
	}
	if !reflect.DeepEqual(v.Paths, want) {
		t.Errorf("live Paths = %q\nwant %q", v.Paths, want)
	}
	if f := wp04viewFacts(t, v, "wiki/concepts/-dash.md"); f.Local.Kind != LocalInvalid {
		t.Errorf("-dash.md Local = %+v, want invalid", f.Local)
	}
	if f := wp04viewFacts(t, v, "wiki/concepts/b.md"); f.Local.Kind != LocalAbsent || f.Base.Kind != BaseStale ||
		f.Remote.Kind != RemoteAbsent {
		t.Errorf("b.md = %+v/%+v/%+v, want absent/stale/absent", f.Local, f.Base, f.Remote)
	}
	if f := wp04viewFacts(t, v, "wiki/concepts/c.md"); f.Local.Kind != LocalAbsent || f.Base.Kind != BaseNone ||
		f.Remote != (Side{Kind: RemotePresent, SHA: wp04viewSR}) {
		t.Errorf("c.md = %+v/%+v/%+v, want absent/none/present", f.Local, f.Base, f.Remote)
	}

	if diff := wp04viewSnapshotDiff(before, wp04viewSnapshot(t, hub)); len(diff) != 0 {
		t.Errorf("LoadView changed the hub; it must only read:\n%s", strings.Join(diff, "\n"))
	}
}

// TestWP04ViewSnapshotDiffNamesChangedPaths pins the read-only checks'
// failure output: every changed, added or removed path is named with its
// before/after mode, mtime and size.
func TestWP04ViewSnapshotDiffNamesChangedPaths(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a.md", "b.md", "c.md"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	before := wp04viewSnapshot(t, dir)
	if diff := wp04viewSnapshotDiff(before, wp04viewSnapshot(t, dir)); len(diff) != 0 {
		t.Fatalf("diff of an untouched dir = %q, want none", diff)
	}

	a, b := filepath.Join(dir, "a.md"), filepath.Join(dir, "b.md")
	aTime := before[a].ModTime
	if err := os.WriteFile(a, []byte("a changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(a, aTime, aTime); err != nil { // content and size change, mtime does not
		t.Fatal(err)
	}
	later := before[b].ModTime.Add(time.Hour)
	if err := os.Chtimes(b, later, later); err != nil { // only the mtime changes
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "c.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "d.md"), []byte("d"), 0o644); err != nil {
		t.Fatal(err)
	}

	diff := wp04viewSnapshotDiff(before, wp04viewSnapshot(t, dir))
	line := func(name string) string {
		t.Helper()
		for _, l := range diff {
			if strings.Contains(l, filepath.Join(dir, name)) {
				return l
			}
		}
		t.Fatalf("diff does not name %s:\n%s", name, strings.Join(diff, "\n"))
		return ""
	}
	if l := line("a.md"); !strings.Contains(l, "size 4;") || !strings.Contains(l, "size 9") ||
		!strings.Contains(l, "content differs") {
		t.Errorf("a.md line %q: want before size 4, after size 9 and content differs", l)
	}
	if l := line("b.md"); !strings.Contains(l, later.Format(time.RFC3339Nano)) ||
		!strings.Contains(l, before[b].ModTime.Format(time.RFC3339Nano)) {
		t.Errorf("b.md line %q: want the before and after mtimes", l)
	}
	if l := line("c.md"); !strings.Contains(l, "removed") {
		t.Errorf("c.md line %q: want removed", l)
	}
	if l := line("d.md"); !strings.Contains(l, "added") {
		t.Errorf("d.md line %q: want added", l)
	}
}

func TestWP04LoadViewWritesNothingOnFreshHub(t *testing.T) {
	hub := wp04viewHub(t)
	wp04viewWrite(t, hub, wp04viewP, wp04viewPage("x", "# X\n"))
	for _, remote := range []map[string]string{nil, {}, {wp04viewP: wp04viewSR}} {
		v := wp04viewLoad(t, hub, remote)
		if len(v.Corrupt) != 0 {
			t.Errorf("Corrupt = %q on a fresh hub", v.Corrupt)
		}
		if f := wp04viewFacts(t, v, wp04viewP); f.Base != (Side{Kind: BaseNone}) {
			t.Errorf("Base = %+v, want none", f.Base)
		}
	}
	if _, err := os.Lstat(SyncDir(hub)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("LoadView created %s (err %v); it must only read", SyncDir(hub), err)
	}
}

func TestWP04LoadViewIOErrors(t *testing.T) {
	for _, name := range []string{"state.json", "bases.json", "conflicts.json"} {
		t.Run(name+"_is_a_directory", func(t *testing.T) {
			hub := wp04viewHub(t)
			if err := os.MkdirAll(filepath.Join(SyncDir(hub), name), 0o755); err != nil {
				t.Fatal(err)
			}
			if v, err := LoadView(hub, nil); err == nil {
				t.Errorf("LoadView = %+v, nil; want an I/O error", v)
			}
		})
	}
	t.Run("missing_hub", func(t *testing.T) {
		hub := filepath.Join(t.TempDir(), ".contexo")
		v, err := LoadView(hub, nil)
		if !errors.Is(err, fs.ErrNotExist) || v != nil {
			t.Errorf("LoadView = %v, %v; want nil and an error wrapping fs.ErrNotExist", v, err)
		}
	})
	t.Run("hub_is_a_file", func(t *testing.T) {
		hub := filepath.Join(t.TempDir(), ".contexo")
		if err := os.WriteFile(hub, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if v, err := LoadView(hub, nil); err == nil || v != nil {
			t.Errorf("LoadView = %v, %v; want nil and an error", v, err)
		}
	})
}

// The E6 push flow on a page that one ParsePage/SerializePage pass does not
// settle: push stores Normalize(file) as the base and never rewrites the
// file, so the page must read as unchanged (Local.Hash == Base.Hash), not
// modified forever.
func TestWP04PushFlowStaysUnchanged(t *testing.T) {
	for name, c := range wp04cmpLossyInputs(t) {
		if !c.valid {
			continue
		}
		t.Run(name, func(t *testing.T) {
			hub := wp04viewHub(t)
			wp04viewWrite(t, hub, wp04viewP, c.in)
			sent, err := Normalize(c.in)
			if err != nil {
				t.Fatalf("Normalize: %v", err)
			}
			wp04viewBase(t, hub, wp04viewP, sent, wp04viewSB)
			wp04viewState(t, hub, map[string]string{wp04viewP: wp04viewSB})

			f := wp04viewFacts(t, wp04viewLoad(t, hub, map[string]string{wp04viewP: wp04viewSB}), wp04viewP)
			if f.Local.Kind != LocalValid || f.Base.Kind != BaseValid {
				t.Fatalf("Local = %+v, Base = %+v; want a valid file and a valid base", f.Local, f.Base)
			}
			if f.Local.Hash != f.Base.Hash {
				t.Errorf("after push Local.Hash = %s, Base.Hash = %s; want equal (unchanged)", f.Local.Hash, f.Base.Hash)
			}
		})
	}
}
