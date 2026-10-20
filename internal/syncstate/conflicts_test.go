package syncstate

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sugihAF/contexo/internal/store/pagestore"
)

// wp04baseOpenConflicts opens the conflicts of hub and fails the test on
// error.
func wp04baseOpenConflicts(t *testing.T, hub string) *Conflicts {
	t.Helper()
	c, err := OpenConflicts(hub)
	if err != nil {
		t.Fatalf("OpenConflicts: %v", err)
	}
	if c == nil {
		t.Fatal("OpenConflicts returned a nil *Conflicts")
	}
	return c
}

// wp04baseConflict returns a valid both_modified pull record for rel.
func wp04baseConflict(rel string) ConflictRecord {
	return ConflictRecord{
		Path:       rel,
		ServerSHA:  "s2",
		BaseSHA:    "s1",
		Kind:       KindBothModified,
		Source:     SourcePull,
		DetectedAt: time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC),
	}
}

func TestConflictStoreRoundTripAndClear(t *testing.T) {
	hub := wp04baseHub(t)
	const (
		relA = "wiki/concepts/alpha.md"
		relB = "wiki/concepts/beta.md"
	)
	// Stored verbatim: the caller passes the sanitized server bytes.
	content := append([]byte{0xEF, 0xBB, 0xBF}, wp04basePage("alpha", "# Theirs\r\n\x1b[1m\n")...)
	c := wp04baseOpenConflicts(t, hub)
	if l := c.List(); l == nil || len(l) != 0 {
		t.Errorf("empty List = %#v, want non-nil empty", l)
	}

	recA := ConflictRecord{
		Path:       relA,
		ServerSHA:  "s2",
		BaseSHA:    "s1",
		Kind:       KindBothModified,
		Source:     SourcePull,
		DetectedAt: time.Date(2026, 2, 3, 11, 5, 6, 999999999, time.FixedZone("+07", 7*3600)),
		HasContent: false, // derived from the content, not the caller
	}
	if err := c.Put(recA, content); err != nil {
		t.Fatalf("Put(A): %v", err)
	}
	// B first gets content, then a Put without content drops the old file.
	recB := ConflictRecord{
		Path:       relB,
		ServerSHA:  "s9",
		Kind:       KindBothAdded,
		Source:     SourcePush,
		DetectedAt: time.Date(2026, 2, 3, 4, 0, 0, 0, time.UTC),
		HasContent: true,
	}
	if err := c.Put(recB, []byte("old theirs\n")); err != nil {
		t.Fatalf("Put(B, content): %v", err)
	}
	if err := c.Put(recB, nil); err != nil {
		t.Fatalf("Put(B, nil): %v", err)
	}
	if _, err := os.Stat(ConflictFile(hub, relB)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("conflicts/%s after a Put without content: stat err = %v, want not-exist", relB, err)
	}
	if got, err := os.ReadFile(ConflictFile(hub, relA)); err != nil || !bytes.Equal(got, content) {
		t.Errorf("conflicts/%s = %q, %v; want the verbatim content", relA, got, err)
	}
	if gi, err := os.ReadFile(filepath.Join(SyncDir(hub), ".gitignore")); err != nil || string(gi) != "*\n" {
		t.Errorf(".sync/.gitignore = %q, %v; want \"*\\n\"", gi, err)
	}
	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	wantA := recA
	wantA.DetectedAt = time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	wantA.HasContent = true
	wantB := recB
	wantB.HasContent = false

	re := wp04baseOpenConflicts(t, hub)
	if re.Corrupt() {
		t.Error("reopened conflicts are Corrupt")
	}
	for _, want := range []ConflictRecord{wantA, wantB} {
		got, ok := re.Get(want.Path)
		if !ok {
			t.Errorf("Get(%s): no record", want.Path)
			continue
		}
		if !got.DetectedAt.Equal(want.DetectedAt) || got.DetectedAt.Location() != time.UTC {
			t.Errorf("Get(%s).DetectedAt = %v, want %v", want.Path, got.DetectedAt, want.DetectedAt)
		}
		got.DetectedAt, want.DetectedAt = time.Time{}, time.Time{}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Get(%s) = %+v\nwant %+v", want.Path, got, want)
		}
	}
	if got, err := re.Content(relA); err != nil || !bytes.Equal(got, content) {
		t.Errorf("Content(A) = %q, %v; want the verbatim content", got, err)
	}
	if _, err := re.Content(relB); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Content(B without content) err = %v, want fs.ErrNotExist", err)
	}
	var paths []string
	for _, r := range re.List() {
		paths = append(paths, r.Path)
	}
	if !reflect.DeepEqual(paths, []string{relA, relB}) {
		t.Errorf("List paths = %q, want [%q %q]", paths, relA, relB)
	}

	if err := re.Clear(relA); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, ok := re.Get(relA); ok {
		t.Error("Get after Clear: record still present")
	}
	if _, err := os.Stat(ConflictFile(hub, relA)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("conflicts/%s after Clear: stat err = %v, want not-exist", relA, err)
	}
	if err := re.Clear(relA); err != nil {
		t.Errorf("second Clear: %v", err)
	}
	if err := re.Clear("wiki/concepts/never.md"); err != nil {
		t.Errorf("Clear(never stored): %v", err)
	}
	if err := re.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	final := wp04baseOpenConflicts(t, hub).List()
	if len(final) != 1 || final[0].Path != relB {
		t.Errorf("after Clear+Save: List = %+v, want only %s", final, relB)
	}
	wp04baseNoTemp(t, SyncDir(hub))
}

func TestWP04ConflictPutDefaultsDetectedAt(t *testing.T) {
	c := wp04baseOpenConflicts(t, wp04baseHub(t))
	rec := wp04baseConflict("wiki/concepts/alpha.md")
	rec.DetectedAt = time.Time{}
	before := time.Now().UTC().Truncate(time.Second)
	if err := c.Put(rec, nil); err != nil {
		t.Fatalf("Put: %v", err)
	}
	after := time.Now().UTC()
	got, _ := c.Get(rec.Path)
	if got.DetectedAt.Before(before) || got.DetectedAt.After(after) ||
		got.DetectedAt.Nanosecond() != 0 || got.DetectedAt.Location() != time.UTC {
		t.Errorf("zero DetectedAt became %v, want now in UTC whole seconds", got.DetectedAt)
	}
}

func TestConflictsJSONShape(t *testing.T) {
	hub := wp04baseHub(t)
	c := wp04baseOpenConflicts(t, hub)
	if err := c.Save(); err != nil {
		t.Fatalf("Save(empty): %v", err)
	}
	raw, err := os.ReadFile(ConflictsPath(hub))
	if err != nil {
		t.Fatal(err)
	}
	wantEmpty := "{\n  \"schema\": \"ctx.conflicts.v1\",\n  \"conflicts\": {}\n}\n"
	if string(raw) != wantEmpty {
		t.Errorf("empty conflicts.json = %q, want %q", raw, wantEmpty)
	}

	at := time.Date(2026, 7, 8, 9, 10, 11, 500, time.FixedZone("-03", -3*3600))
	const (
		mod   = "wiki/concepts/mod.md"
		added = "wiki/concepts/added.md"
		rep   = "wiki/concepts/rep.md"
	)
	recs := []struct {
		rec     ConflictRecord
		content []byte
	}{
		{ConflictRecord{Path: mod, ServerSHA: "s2", BaseSHA: "s1", Kind: KindBothModified,
			Source: SourcePull, DetectedAt: at}, []byte("theirs\n")},
		// both_added has no base: a caller-supplied BaseSHA is dropped.
		{ConflictRecord{Path: added, ServerSHA: "s3", BaseSHA: "ignored", Kind: KindBothAdded,
			Source: SourcePush, DetectedAt: at}, []byte("theirs\n")},
		{ConflictRecord{Path: rep, ServerSHA: "s4", BaseSHA: "s0", Kind: KindBothModified,
			Source: SourceRepair, DetectedAt: at}, nil},
	}
	for _, r := range recs {
		if err := c.Put(r.rec, r.content); err != nil {
			t.Fatalf("Put(%s): %v", r.rec.Path, err)
		}
	}
	if got, _ := c.Get(added); got.BaseSHA != "" {
		t.Errorf("both_added Get.BaseSHA = %q, want empty", got.BaseSHA)
	}
	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	raw, err = os.ReadFile(ConflictsPath(hub))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(raw, []byte("}\n")) || !bytes.Contains(raw, []byte("\n  \"conflicts\": {\n    \"")) {
		t.Errorf("conflicts.json is not 2-space indented with a trailing newline:\n%s", raw)
	}

	m := wp04baseDecode(t, ConflictsPath(hub))
	if got := wp04baseKeys(m); !reflect.DeepEqual(got, []string{"conflicts", "schema"}) {
		t.Errorf("top-level keys = %q, want [conflicts schema]", got)
	}
	if m["schema"] != "ctx.conflicts.v1" {
		t.Errorf("schema = %v, want ctx.conflicts.v1", m["schema"])
	}
	cm, ok := m["conflicts"].(map[string]any)
	if !ok || len(cm) != 3 {
		t.Fatalf("conflicts = %#v, want 3 records", m["conflicts"])
	}
	want := map[string]map[string]any{
		mod: {"server_sha": "s2", "base_sha": "s1", "kind": "both_modified", "source": "pull",
			"detected_at": "2026-07-08T12:10:11Z", "has_content": true},
		added: {"server_sha": "s3", "kind": "both_added", "source": "push",
			"detected_at": "2026-07-08T12:10:11Z", "has_content": true},
		rep: {"server_sha": "s4", "base_sha": "s0", "kind": "both_modified", "source": "repair",
			"detected_at": "2026-07-08T12:10:11Z", "has_content": false},
	}
	for rel, w := range want {
		if got, _ := cm[rel].(map[string]any); !reflect.DeepEqual(got, w) {
			t.Errorf("conflicts[%q] = %#v\nwant %#v", rel, cm[rel], w)
		}
		if at, _ := cm[rel].(map[string]any)["detected_at"].(string); !wp04baseSecondsZ.MatchString(at) {
			t.Errorf("conflicts[%q].detected_at = %q, want UTC whole seconds", rel, at)
		}
	}
	if strings.Contains(string(raw), `"path"`) {
		t.Errorf("conflicts.json marshals Path:\n%s", raw)
	}
}

func TestWP04ConflictPutRejectsBadRecord(t *testing.T) {
	hub := wp04baseHub(t)
	c := wp04baseOpenConflicts(t, hub)
	const rel = "wiki/concepts/alpha.md"
	bad := map[string]func(*ConflictRecord){
		"kind x":          func(r *ConflictRecord) { r.Kind = "x" },
		"kind empty":      func(r *ConflictRecord) { r.Kind = "" },
		"kind modified":   func(r *ConflictRecord) { r.Kind = "modified" },
		"source y":        func(r *ConflictRecord) { r.Source = "y" },
		"source empty":    func(r *ConflictRecord) { r.Source = "" },
		"source resolve":  func(r *ConflictRecord) { r.Source = "resolve" },
		"server_sha void": func(r *ConflictRecord) { r.ServerSHA = "" },
	}
	for name, mutate := range bad {
		rec := wp04baseConflict(rel)
		mutate(&rec)
		err := c.Put(rec, []byte("theirs\n"))
		if err == nil {
			t.Errorf("Put(%s): want error, got nil", name)
		}
		var pe *PathError
		if errors.As(err, &pe) {
			t.Errorf("Put(%s): got a *PathError for a valid path: %v", name, err)
		}
	}
	if l := c.List(); len(l) != 0 {
		t.Errorf("List after rejected Puts = %+v, want empty", l)
	}
	if _, err := os.Stat(ConflictFile(hub, rel)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("content written by a rejected Put: stat err = %v", err)
	}
}

func TestWP04ConflictsCorruptTreatedAsEmpty(t *testing.T) {
	const rec = `{"server_sha":"s2","base_sha":"s1","kind":"both_modified",` +
		`"source":"pull","detected_at":"2026-01-02T03:04:05Z","has_content":false}`
	cases := map[string]string{
		"garbage":          `{not json`,
		"empty file":       ``,
		"null":             `null`,
		"array":            `[]`,
		"wrong schema":     `{"schema":"ctx.conflicts.v2","conflicts":{"wiki/a.md":` + rec + `}}`,
		"bases schema":     `{"schema":"ctx.bases.v1","conflicts":{}}`,
		"no schema":        `{"conflicts":{"wiki/a.md":` + rec + `}}`,
		"conflicts null":   `{"schema":"ctx.conflicts.v1","conflicts":null}`,
		"conflicts absent": `{"schema":"ctx.conflicts.v1","pages":{}}`,
		"conflicts array":  `{"schema":"ctx.conflicts.v1","conflicts":[]}`,
		"record null":      `{"schema":"ctx.conflicts.v1","conflicts":{"wiki/a.md":null}}`,
		"record bad type":  `{"schema":"ctx.conflicts.v1","conflicts":{"wiki/a.md":{"has_content":"yes"}}}`,
		"bad kind":         `{"schema":"ctx.conflicts.v1","conflicts":{"wiki/a.md":` + strings.Replace(rec, "both_modified", "modified", 1) + `}}`,
		"bad source":       `{"schema":"ctx.conflicts.v1","conflicts":{"wiki/a.md":` + strings.Replace(rec, `"pull"`, `"status"`, 1) + `}}`,
		"empty server_sha": `{"schema":"ctx.conflicts.v1","conflicts":{"wiki/a.md":` + strings.Replace(rec, `"s2"`, `""`, 1) + `}}`,
		"traversal key":    `{"schema":"ctx.conflicts.v1","conflicts":{"../../config.json":` + rec + `}}`,
		"trailing garbage": `{"schema":"ctx.conflicts.v1","conflicts":{}}]`,
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			hub := wp04baseHub(t)
			if err := os.MkdirAll(SyncDir(hub), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(ConflictsPath(hub), []byte(data), 0o644); err != nil {
				t.Fatal(err)
			}
			c := wp04baseOpenConflicts(t, hub)
			if !c.Corrupt() {
				t.Errorf("Corrupt() = false for %s", data)
			}
			if l := c.List(); len(l) != 0 {
				t.Errorf("List = %+v, want empty", l)
			}
			if err := c.Save(); err != nil {
				t.Fatalf("Save: %v", err)
			}
			raw, err := os.ReadFile(ConflictsPath(hub))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(raw, []byte(`"conflicts": {}`)) || bytes.Contains(raw, []byte("null")) {
				t.Errorf("Save after corrupt wrote %s, want \"conflicts\": {}", raw)
			}
			if wp04baseOpenConflicts(t, hub).Corrupt() {
				t.Error("file written by Save is Corrupt")
			}
		})
	}
}

func TestWP04ConflictsLoadNormalizes(t *testing.T) {
	hub := wp04baseHub(t)
	if err := os.MkdirAll(SyncDir(hub), 0o755); err != nil {
		t.Fatal(err)
	}
	// Unknown fields are ignored; a both_added base_sha is dropped; times
	// become UTC whole seconds.
	data := `{"schema":"ctx.conflicts.v1","x":1,"conflicts":{"wiki/a.md":{"server_sha":"s3",` +
		`"base_sha":"s1","kind":"both_added","source":"repair",` +
		`"detected_at":"2026-01-02T10:04:05.25+07:00","has_content":true,"extra":"y"}}}`
	if err := os.WriteFile(ConflictsPath(hub), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	c := wp04baseOpenConflicts(t, hub)
	if c.Corrupt() {
		t.Fatal("valid conflicts.json with unknown fields is Corrupt")
	}
	got, ok := c.Get("wiki/a.md")
	want := ConflictRecord{Path: "wiki/a.md", ServerSHA: "s3", Kind: KindBothAdded,
		Source: SourceRepair, HasContent: true}
	wantAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if !ok || !got.DetectedAt.Equal(wantAt) || got.DetectedAt.Location() != time.UTC {
		t.Fatalf("Get = %+v, %v; want DetectedAt %v", got, ok, wantAt)
	}
	got.DetectedAt = time.Time{}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Get = %+v\nwant %+v", got, want)
	}
}

func TestWP04ConflictsOpenIOError(t *testing.T) {
	hub := wp04baseHub(t)
	if err := os.MkdirAll(ConflictsPath(hub), 0o755); err != nil {
		t.Fatal(err)
	}
	if c, err := OpenConflicts(hub); err == nil {
		t.Errorf("OpenConflicts(conflicts.json is a dir) = %+v, nil; want an error", c)
	}
	fresh := wp04baseOpenConflicts(t, wp04baseHub(t))
	if fresh.Corrupt() || len(fresh.List()) != 0 {
		t.Errorf("missing conflicts.json: Corrupt=%v List=%+v", fresh.Corrupt(), fresh.List())
	}
}

func TestWP04ConflictsRejectBadPath(t *testing.T) {
	root := t.TempDir()
	hub := filepath.Join(root, ".contexo")
	if err := os.MkdirAll(hub, 0o755); err != nil {
		t.Fatal(err)
	}
	decoys := map[string][]byte{
		filepath.Join(hub, "config.json"):      []byte(`{"repo_id":"r"}`),
		filepath.Join(hub, "credentials.json"): []byte(`{"token":"t"}`),
		filepath.Join(root, "x.md"):            []byte("outside\n"),
	}
	for p, data := range decoys {
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	before := wp04baseTree(t, root)

	c := wp04baseOpenConflicts(t, hub)
	for _, bad := range wp04baseBadPaths {
		rec := wp04baseConflict(bad)
		wp04baseWantPathError(t, "Put", c.Put(rec, []byte("evil\n")), bad)
		wp04baseWantPathError(t, "Put(no content)", c.Put(rec, nil), bad)
		_, err := c.Content(bad)
		wp04baseWantPathError(t, "Content", err, bad)
		wp04baseWantPathError(t, "Clear", c.Clear(bad), bad)
		if _, ok := c.Get(bad); ok {
			t.Errorf("Get(%q) found a record", bad)
		}
	}
	if l := c.List(); len(l) != 0 {
		t.Errorf("List after rejected Puts = %+v, want empty", l)
	}
	if after := wp04baseTree(t, root); !reflect.DeepEqual(after, before) {
		t.Errorf("tree changed:\nbefore %q\nafter  %q", before, after)
	}
	for p, data := range decoys {
		if got, err := os.ReadFile(p); err != nil || !bytes.Equal(got, data) {
			t.Errorf("decoy %s = %q, %v; want it untouched", p, got, err)
		}
	}
}

func TestConflictContentUnderDotDirNotWalked(t *testing.T) {
	hub := wp04baseHub(t)
	const (
		rel   = "wiki/concepts/alpha.md"
		added = "wiki/concepts/beta.md"
	)
	working := wp04basePage("alpha", "# Alpha\n\nmine\n")
	if err := os.MkdirAll(filepath.Dir(filepath.Join(hub, filepath.FromSlash(rel))), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hub, filepath.FromSlash(rel)), working, 0o644); err != nil {
		t.Fatal(err)
	}

	b := wp04baseOpen(t, hub)
	if err := b.Put(rel, wp04basePage("alpha", "# Alpha\n"), "s1", time.Now()); err != nil {
		t.Fatalf("Bases.Put: %v", err)
	}
	if err := b.Save(); err != nil {
		t.Fatalf("Bases.Save: %v", err)
	}
	c := wp04baseOpenConflicts(t, hub)
	if err := c.Put(wp04baseConflict(rel), wp04basePage("alpha", "# Alpha\n\ntheirs\n")); err != nil {
		t.Fatalf("Conflicts.Put: %v", err)
	}
	// A both_added sidecar for a page that has no working file yet.
	addRec := wp04baseConflict(added)
	addRec.Kind, addRec.BaseSHA = KindBothAdded, ""
	if err := c.Put(addRec, wp04basePage("beta", "# Beta\n")); err != nil {
		t.Fatalf("Conflicts.Put(both_added): %v", err)
	}
	if err := c.Save(); err != nil {
		t.Fatalf("Conflicts.Save: %v", err)
	}
	// The copies really are *.md pages under .sync.
	for _, p := range []string{BaseFile(hub, rel), ConflictFile(hub, rel), ConflictFile(hub, added)} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("sidecar copy missing: %v", err)
		}
	}

	pages, err := (&pagestore.Store{Root: hub}).List(pagestore.Filter{})
	if err != nil {
		t.Fatalf("pagestore List: %v", err)
	}
	if len(pages) != 1 || pages[0].Frontmatter.RelPath() != rel {
		var got []string
		for _, p := range pages {
			got = append(got, p.Frontmatter.RelPath())
		}
		t.Errorf("pagestore List = %q, want only %q", got, rel)
	}
	scan, err := ScanLocal(hub)
	if err != nil {
		t.Fatalf("ScanLocal: %v", err)
	}
	if !reflect.DeepEqual(scan.Paths, []string{rel}) {
		t.Errorf("ScanLocal Paths = %q, want [%q]", scan.Paths, rel)
	}
	if f := scan.Files[rel]; f == nil || !bytes.Equal(f.Data, working) {
		t.Errorf("ScanLocal reported the wrong bytes for %s", rel)
	}
}
