package syncstate

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// wp04baseSecondsZ matches a UTC RFC 3339 time in whole seconds.
var wp04baseSecondsZ = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$`)

// wp04baseHashRE matches a Hash of a page that normalizes.
var wp04baseHashRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// wp04baseBadPaths are page paths that must never reach the file system:
// traversal, dot segments, absolute and Windows forms, non-pages.
var wp04baseBadPaths = []string{
	"",
	"../../config.json",
	"../../credentials.json",
	"../x.md",
	"wiki/../../x.md",
	"wiki/./x.md",
	".sync/x.md",
	".sync/base/wiki/concepts/x.md",
	".sync/state.json",
	"wiki/.hidden.md",
	"/etc/x.md",
	`wiki\..\..\x.md`,
	"C:/x.md",
	"config.json",
	"x.txt",
	"-x.md",
	"wiki//x.md",
	"wiki/x\x00.md",
}

// wp04basePage returns a valid ctx.page.v1 concept page with the given slug
// and body.
func wp04basePage(slug, body string) []byte {
	return []byte("---\nschema: ctx.page.v1\nslug: " + slug +
		"\ntype: concept\nauthor: tester\ncreated: 2026-01-02T00:00:00Z\n" +
		"updated: 2026-01-02T03:04:05Z\nparent_sha: \"\"\n---\n" + body)
}

// wp04baseHub returns a fresh hub directory <tmp>/.contexo.
func wp04baseHub(t *testing.T) string {
	t.Helper()
	hub := filepath.Join(t.TempDir(), ".contexo")
	if err := os.MkdirAll(hub, 0o755); err != nil {
		t.Fatal(err)
	}
	return hub
}

// wp04baseOpen opens the bases of hub and fails the test on error.
func wp04baseOpen(t *testing.T, hub string) *Bases {
	t.Helper()
	b, err := OpenBases(hub)
	if err != nil {
		t.Fatalf("OpenBases: %v", err)
	}
	if b == nil {
		t.Fatal("OpenBases returned a nil *Bases")
	}
	return b
}

// wp04baseTree lists every path under dir, '/'-separated and sorted.
func wp04baseTree(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != dir {
			rel, err := filepath.Rel(dir, p)
			if err != nil {
				return err
			}
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

// wp04baseNoTemp fails when any WriteFileAtomic temp file is left under dir.
func wp04baseNoTemp(t *testing.T, dir string) {
	t.Helper()
	for _, p := range wp04baseTree(t, dir) {
		if strings.Contains(wp04baseName(p), ".tmp-") {
			t.Errorf("temp file left behind: %s", p)
		}
	}
}

// wp04baseName returns the last element of a '/'-separated path.
func wp04baseName(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// wp04baseDecode decodes a JSON object file into a generic map.
func wp04baseDecode(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("%s: %v\n%s", path, err, raw)
	}
	return m
}

// wp04baseKeys returns the sorted keys of m.
func wp04baseKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// wp04baseWantPathError asserts err is (or wraps) a *PathError naming path.
func wp04baseWantPathError(t *testing.T, op string, err error, path string) {
	t.Helper()
	var pe *PathError
	if !errors.As(err, &pe) {
		t.Errorf("%s(%q): want *PathError, got %T (%v)", op, path, err, err)
		return
	}
	if pe.Path != path {
		t.Errorf("%s(%q): PathError.Path = %q", op, path, pe.Path)
	}
	if pe.ErrorCode() != "invalid_path" {
		t.Errorf("%s(%q): ErrorCode = %q, want invalid_path", op, path, pe.ErrorCode())
	}
}

func TestBaseValidOnlyWhenShaMatches(t *testing.T) {
	hub := wp04baseHub(t)
	const rel = "wiki/concepts/alpha.md"
	b := wp04baseOpen(t, hub)
	if err := b.Put(rel, wp04basePage("alpha", "# Alpha\n"), "a1", time.Now()); err != nil {
		t.Fatalf("Put: %v", err)
	}

	if !b.Valid(rel, "a1") {
		t.Error(`Valid(rel, "a1") = false, want true`)
	}
	// An old binary bumped page_shas without touching bases.json: stale.
	if b.Valid(rel, "b2") {
		t.Error(`Valid(rel, "b2") = true, want false`)
	}
	if b.Valid(rel, "") {
		t.Error(`Valid(rel, "") = true, want false`)
	}
	if b.Valid("wiki/concepts/other.md", "a1") {
		t.Error("Valid(unindexed path) = true, want false")
	}
	if b.Valid("../../config.json", "a1") {
		t.Error("Valid(bad path) = true, want false")
	}

	// The record survives a Save/reopen and is still valid.
	if err := b.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !wp04baseOpen(t, hub).Valid(rel, "a1") {
		t.Error("reopened: Valid(rel, a1) = false, want true")
	}

	// A record without its base file is not valid.
	file := BaseFile(hub, rel)
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if b.Valid(rel, "a1") {
		t.Error("base file removed: Valid = true, want false")
	}
	// Nor is one whose base path is a directory.
	if err := os.MkdirAll(file, 0o755); err != nil {
		t.Fatal(err)
	}
	if b.Valid(rel, "a1") {
		t.Error("base path is a directory: Valid = true, want false")
	}
}

func TestBasePutIsAtomicAndIndexed(t *testing.T) {
	hub := wp04baseHub(t)
	const rel = "wiki/concepts/alpha.md"
	// Verbatim: a BOM, CRLF line ends, an ESC and a zero-width space.
	content := append([]byte{0xEF, 0xBB, 0xBF}, bytes.ReplaceAll(
		wp04basePage("alpha", "# Alpha\n\x1b[31mred\x1b[0m \u200b\n"),
		[]byte("\n"), []byte("\r\n"))...)
	now := time.Date(2026, 3, 4, 12, 30, 45, 987654321, time.FixedZone("+07", 7*3600))
	want := BaseRecord{
		SHA:      "abc123",
		Hash:     Hash(content),
		SyncedAt: time.Date(2026, 3, 4, 5, 30, 45, 0, time.UTC),
	}
	if !wp04baseHashRE.MatchString(want.Hash) {
		t.Fatalf("test page does not normalize: Hash = %s", want.Hash)
	}

	b := wp04baseOpen(t, hub)
	if err := b.Put(rel, content, "abc123", now); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := os.ReadFile(BaseFile(hub, rel))
	if err != nil {
		t.Fatalf("base file: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("base file = %q\nwant verbatim %q", got, content)
	}
	if rb, err := b.Read(rel); err != nil || !bytes.Equal(rb, content) {
		t.Errorf("Read = %q, %v; want the verbatim content", rb, err)
	}
	rec, ok := b.Get(rel)
	if !ok {
		t.Fatal("Get after Put: no record")
	}
	if rec.SHA != want.SHA || rec.Hash != want.Hash || !rec.SyncedAt.Equal(want.SyncedAt) {
		t.Errorf("record = %+v, want %+v", rec, want)
	}
	if rec.SyncedAt.Location() != time.UTC {
		t.Errorf("SyncedAt location = %v, want UTC", rec.SyncedAt.Location())
	}
	if !reflect.DeepEqual(b.Paths(), []string{rel}) {
		t.Errorf("Paths = %q, want [%q]", b.Paths(), rel)
	}
	if gi, err := os.ReadFile(filepath.Join(SyncDir(hub), ".gitignore")); err != nil || string(gi) != "*\n" {
		t.Errorf(".sync/.gitignore = %q, %v; want \"*\\n\"", gi, err)
	}

	// The index is persisted only by Save.
	if _, err := os.Stat(BasesPath(hub)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("bases.json before Save: stat err = %v, want not-exist", err)
	}
	if err := b.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	re := wp04baseOpen(t, hub)
	if re.Corrupt() {
		t.Error("reopened bases are Corrupt")
	}
	rec2, ok := re.Get(rel)
	if !ok || !reflect.DeepEqual(rec2, rec) {
		t.Errorf("reopened record = %+v, %v; want %+v", rec2, ok, rec)
	}

	// A second Put replaces both the bytes and the record.
	next := wp04basePage("alpha", "# Alpha v2\n")
	later := now.Add(time.Hour)
	if err := re.Put(rel, next, "def456", later); err != nil {
		t.Fatalf("second Put: %v", err)
	}
	if rb, _ := re.Read(rel); !bytes.Equal(rb, next) {
		t.Errorf("Read after second Put = %q, want %q", rb, next)
	}
	rec3, _ := re.Get(rel)
	if rec3.SHA != "def456" || rec3.Hash != Hash(next) ||
		!rec3.SyncedAt.Equal(later.UTC().Truncate(time.Second)) {
		t.Errorf("record after second Put = %+v", rec3)
	}
	if err := re.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	wp04baseNoTemp(t, SyncDir(hub))
}

func TestWP04BasePutDefaultsAndRejects(t *testing.T) {
	hub := wp04baseHub(t)
	b := wp04baseOpen(t, hub)
	const rel = "wiki/concepts/alpha.md"

	// A zero now means the current time (UTC, whole seconds).
	before := time.Now().UTC().Truncate(time.Second)
	if err := b.Put(rel, wp04basePage("alpha", "x\n"), "s1", time.Time{}); err != nil {
		t.Fatalf("Put(zero now): %v", err)
	}
	after := time.Now().UTC()
	rec, _ := b.Get(rel)
	if rec.SyncedAt.Before(before) || rec.SyncedAt.After(after) ||
		rec.SyncedAt.Nanosecond() != 0 || rec.SyncedAt.Location() != time.UTC {
		t.Errorf("zero now: SyncedAt = %v, want now in UTC whole seconds", rec.SyncedAt)
	}

	// An empty sha can never be valid, so it is refused.
	const other = "wiki/concepts/beta.md"
	if err := b.Put(other, wp04basePage("beta", "x\n"), "", time.Now()); err == nil {
		t.Error(`Put(sha ""): want error, got nil`)
	}
	if _, ok := b.Get(other); ok {
		t.Error(`Put(sha ""): record was indexed`)
	}
	if _, err := os.Stat(BaseFile(hub, other)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf(`Put(sha ""): base file stat err = %v, want not-exist`, err)
	}

	// Raw bytes that do not parse are stored and hashed raw.
	raw := []byte("no frontmatter\n")
	if err := b.Put(other, raw, "s2", time.Now()); err != nil {
		t.Fatalf("Put(raw): %v", err)
	}
	if rec, _ := b.Get(other); rec.Hash != Hash(raw) || !strings.HasPrefix(rec.Hash, "raw-sha256:") {
		t.Errorf("raw Put: Hash = %q, want %q", rec.Hash, Hash(raw))
	}
	if got, err := b.ReadVerified(other); err != nil || !bytes.Equal(got, raw) {
		t.Errorf("ReadVerified(raw) = %q, %v", got, err)
	}
}

func TestBaseReadVerifiedDetectsTamper(t *testing.T) {
	hub := wp04baseHub(t)
	const rel = "wiki/concepts/alpha.md"
	content := wp04basePage("alpha", "# Alpha\n")
	b := wp04baseOpen(t, hub)
	if err := b.Put(rel, content, "a1", time.Now()); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if got, err := b.ReadVerified(rel); err != nil || !bytes.Equal(got, content) {
		t.Fatalf("ReadVerified = %q, %v; want the content", got, err)
	}

	tampered := append(append([]byte{}, content...), "edited by hand\n"...)
	if err := os.WriteFile(BaseFile(hub, rel), tampered, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := b.ReadVerified(rel); !errors.Is(err, ErrBaseUnavailable) {
		t.Errorf("ReadVerified(tampered) err = %v, want ErrBaseUnavailable", err)
	}
	if got, err := b.Read(rel); err != nil || !bytes.Equal(got, tampered) {
		t.Errorf("Read(tampered) = %q, %v; want the tampered bytes", got, err)
	}

	if err := os.Remove(BaseFile(hub, rel)); err != nil {
		t.Fatal(err)
	}
	_, err := b.ReadVerified(rel)
	if !errors.Is(err, ErrBaseUnavailable) {
		t.Errorf("ReadVerified(missing file) err = %v, want ErrBaseUnavailable", err)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReadVerified(missing file) err = %v, want it to wrap fs.ErrNotExist", err)
	}
	if _, err := b.Read(rel); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Read(missing file) err = %v, want fs.ErrNotExist", err)
	}

	// A base file without a record is unavailable too.
	const orphan = "wiki/concepts/orphan.md"
	if err := os.MkdirAll(filepath.Dir(BaseFile(hub, orphan)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(BaseFile(hub, orphan), content, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := b.ReadVerified(orphan); !errors.Is(err, ErrBaseUnavailable) {
		t.Errorf("ReadVerified(no record) err = %v, want ErrBaseUnavailable", err)
	}
	if _, err := b.ReadVerified("wiki/concepts/never.md"); !errors.Is(err, ErrBaseUnavailable) {
		t.Errorf("ReadVerified(nothing) err = %v, want ErrBaseUnavailable", err)
	}
}

func TestWP04BasesDelete(t *testing.T) {
	hub := wp04baseHub(t)
	const (
		rel  = "wiki/concepts/deep/alpha.md"
		keep = "wiki/concepts/beta.md"
	)
	b := wp04baseOpen(t, hub)
	for _, p := range []string{rel, keep} {
		if err := b.Put(p, wp04basePage("x", "x\n"), "s1", time.Now()); err != nil {
			t.Fatalf("Put(%s): %v", p, err)
		}
	}
	if err := b.Delete(rel); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := b.Get(rel); ok {
		t.Error("Get after Delete: record still present")
	}
	if _, err := os.Stat(BaseFile(hub, rel)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("base file after Delete: stat err = %v, want not-exist", err)
	}
	// The directory the delete emptied is pruned; its non-empty parent stays.
	if _, err := os.Stat(filepath.Dir(BaseFile(hub, rel))); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("emptied dir after Delete: stat err = %v, want not-exist", err)
	}
	if _, err := os.Stat(BaseFile(hub, keep)); err != nil {
		t.Errorf("other base file after Delete: %v", err)
	}
	// Deleting again, or a path never stored, is not an error.
	if err := b.Delete(rel); err != nil {
		t.Errorf("second Delete: %v", err)
	}
	if err := b.Delete("wiki/concepts/never.md"); err != nil {
		t.Errorf("Delete(never stored): %v", err)
	}
	if !reflect.DeepEqual(b.Paths(), []string{keep}) {
		t.Errorf("Paths = %q, want [%q]", b.Paths(), keep)
	}
	if err := b.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if got := wp04baseOpen(t, hub).Paths(); !reflect.DeepEqual(got, []string{keep}) {
		t.Errorf("reopened Paths = %q, want [%q]", got, keep)
	}
	// The base root itself is never pruned, even when it becomes empty.
	if err := b.Delete(keep); err != nil {
		t.Fatalf("Delete(keep): %v", err)
	}
	if fi, err := os.Stat(filepath.Join(SyncDir(hub), "base")); err != nil || !fi.IsDir() {
		t.Errorf(".sync/base after deleting everything: %v", err)
	}
}

func TestWP04BasesPathsSorted(t *testing.T) {
	hub := wp04baseHub(t)
	b := wp04baseOpen(t, hub)
	if p := b.Paths(); p == nil || len(p) != 0 {
		t.Errorf("empty Paths = %#v, want non-nil empty", p)
	}
	in := []string{"wiki/z.md", "raw/sessions/a.md", "wiki/B.md", "wiki/a.md"}
	for _, p := range in {
		if err := b.Put(p, []byte("x"), "s", time.Now()); err != nil {
			t.Fatalf("Put(%s): %v", p, err)
		}
	}
	want := []string{"raw/sessions/a.md", "wiki/B.md", "wiki/a.md", "wiki/z.md"}
	if got := b.Paths(); !reflect.DeepEqual(got, want) {
		t.Errorf("Paths = %q, want %q", got, want)
	}
}

func TestBasesCorruptTreatedAsEmpty(t *testing.T) {
	hub := wp04baseHub(t)
	if err := os.MkdirAll(SyncDir(hub), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(BasesPath(hub), []byte("{not json at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := wp04baseOpen(t, hub)
	if !b.Corrupt() {
		t.Error("Corrupt() = false for garbage bases.json")
	}
	if p := b.Paths(); len(p) != 0 {
		t.Errorf("Paths = %q, want empty", p)
	}

	const rel = "wiki/concepts/alpha.md"
	if err := b.Put(rel, wp04basePage("alpha", "x\n"), "s1", time.Now()); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := b.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	raw, err := os.ReadFile(BasesPath(hub))
	if err != nil || !json.Valid(raw) {
		t.Fatalf("bases.json after Save is not valid JSON: %v\n%s", err, raw)
	}
	re := wp04baseOpen(t, hub)
	if re.Corrupt() {
		t.Error("rewritten bases.json still Corrupt")
	}
	if !re.Valid(rel, "s1") {
		t.Error("rewritten bases.json lost the new record")
	}

	// A missing bases.json is an empty, healthy set.
	fresh := wp04baseOpen(t, wp04baseHub(t))
	if fresh.Corrupt() || len(fresh.Paths()) != 0 {
		t.Errorf("missing bases.json: Corrupt=%v Paths=%q", fresh.Corrupt(), fresh.Paths())
	}
}

func TestWP04BasesWrongSchemaIsCorrupt(t *testing.T) {
	const rec = `{"sha":"s1","hash":"sha256:00","synced_at":"2026-01-02T03:04:05Z"}`
	cases := map[string]string{
		"empty file":       ``,
		"null":             `null`,
		"string":           `"ctx.bases.v1"`,
		"array":            `[]`,
		"array of object":  `[{"schema":"ctx.bases.v1","pages":{}}]`,
		"wrong schema":     `{"schema":"ctx.bases.v2","pages":{"wiki/a.md":` + rec + `}}`,
		"other schema":     `{"schema":"ctx.conflicts.v1","pages":{}}`,
		"no schema":        `{"pages":{"wiki/a.md":` + rec + `}}`,
		"schema not text":  `{"schema":1,"pages":{}}`,
		"pages null":       `{"schema":"ctx.bases.v1","pages":null}`,
		"pages missing":    `{"schema":"ctx.bases.v1"}`,
		"pages array":      `{"schema":"ctx.bases.v1","pages":[]}`,
		"record null":      `{"schema":"ctx.bases.v1","pages":{"wiki/a.md":null}}`,
		"record bad type":  `{"schema":"ctx.bases.v1","pages":{"wiki/a.md":{"sha":1}}}`,
		"record bad time":  `{"schema":"ctx.bases.v1","pages":{"wiki/a.md":{"sha":"s","synced_at":"yesterday"}}}`,
		"traversal key":    `{"schema":"ctx.bases.v1","pages":{"../../config.json":` + rec + `}}`,
		"dot-dir key":      `{"schema":"ctx.bases.v1","pages":{".sync/x.md":` + rec + `}}`,
		"trailing garbage": `{"schema":"ctx.bases.v1","pages":{}} x`,
		// Records Put never writes: a base without a sha can never be
		// valid, and one without a hash would compare unequal to every
		// working file.
		"record empty sha": `{"schema":"ctx.bases.v1","pages":{"wiki/a.md":{"sha":"","hash":"sha256:00","synced_at":"2026-01-02T03:04:05Z"}}}`,
		"record no hash":   `{"schema":"ctx.bases.v1","pages":{"wiki/a.md":{"sha":"s1","synced_at":"2026-01-02T03:04:05Z"}}}`,
		"record odd hash":  `{"schema":"ctx.bases.v1","pages":{"wiki/a.md":{"sha":"s1","hash":"md5:00","synced_at":"2026-01-02T03:04:05Z"}}}`,
		"record bare hash": `{"schema":"ctx.bases.v1","pages":{"wiki/a.md":{"sha":"s1","hash":"sha256:","synced_at":"2026-01-02T03:04:05Z"}}}`,
		"record short hex": `{"schema":"ctx.bases.v1","pages":{"wiki/a.md":{"sha":"s1","hash":"raw-sha256:00","synced_at":"2026-01-02T03:04:05Z"}}}`,
		"record non-hex":   `{"schema":"ctx.bases.v1","pages":{"wiki/a.md":{"sha":"s1","hash":"sha256:` + strings.Repeat("z", 64) + `","synced_at":"2026-01-02T03:04:05Z"}}}`,
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			hub := wp04baseHub(t)
			if err := os.MkdirAll(SyncDir(hub), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(BasesPath(hub), []byte(data), 0o644); err != nil {
				t.Fatal(err)
			}
			b := wp04baseOpen(t, hub)
			if !b.Corrupt() {
				t.Errorf("Corrupt() = false for %s", data)
			}
			if p := b.Paths(); len(p) != 0 {
				t.Errorf("Paths = %q, want empty", p)
			}
			if _, ok := b.Get("wiki/a.md"); ok {
				t.Error("Get found a record in a corrupt file")
			}
			if err := b.Save(); err != nil {
				t.Fatalf("Save: %v", err)
			}
			raw, err := os.ReadFile(BasesPath(hub))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(raw, []byte(`"pages": {}`)) || bytes.Contains(raw, []byte("null")) {
				t.Errorf("Save after corrupt wrote %s, want \"pages\": {}", raw)
			}
			if wp04baseOpen(t, hub).Corrupt() {
				t.Error("file written by Save is Corrupt")
			}
		})
	}
}

func TestWP04BasesAcceptsUnknownFields(t *testing.T) {
	hub := wp04baseHub(t)
	if err := os.MkdirAll(SyncDir(hub), 0o755); err != nil {
		t.Fatal(err)
	}
	data := `{"schema":"ctx.bases.v1","extra":true,"pages":{"wiki/a.md":` +
		`{"sha":"s1","hash":"sha256:` + strings.Repeat("0", 64) + `","synced_at":"2026-01-02T10:04:05.5+07:00","note":"x"}}}`
	if err := os.WriteFile(BasesPath(hub), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	b := wp04baseOpen(t, hub)
	if b.Corrupt() {
		t.Fatal("unknown fields made bases.json Corrupt")
	}
	rec, ok := b.Get("wiki/a.md")
	if !ok || rec.SHA != "s1" || rec.Hash != "sha256:"+strings.Repeat("0", 64) {
		t.Fatalf("Get = %+v, %v", rec, ok)
	}
	// Loaded times are kept in the on-disk form: UTC, whole seconds.
	want := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if !rec.SyncedAt.Equal(want) || rec.SyncedAt.Location() != time.UTC {
		t.Errorf("SyncedAt = %v, want %v", rec.SyncedAt, want)
	}
}

func TestWP04BasesOpenIOError(t *testing.T) {
	hub := wp04baseHub(t)
	// A directory where bases.json should be is an I/O error, not corruption.
	if err := os.MkdirAll(BasesPath(hub), 0o755); err != nil {
		t.Fatal(err)
	}
	if b, err := OpenBases(hub); err == nil {
		t.Errorf("OpenBases(bases.json is a dir) = %+v, nil; want an error", b)
	}
}

func TestBasesJSONShape(t *testing.T) {
	hub := wp04baseHub(t)
	b := wp04baseOpen(t, hub)
	if err := b.Save(); err != nil {
		t.Fatalf("Save(empty): %v", err)
	}
	raw, err := os.ReadFile(BasesPath(hub))
	if err != nil {
		t.Fatal(err)
	}
	wantEmpty := "{\n  \"schema\": \"ctx.bases.v1\",\n  \"pages\": {}\n}\n"
	if string(raw) != wantEmpty {
		t.Errorf("empty bases.json = %q, want %q", raw, wantEmpty)
	}

	now := time.Date(2026, 5, 6, 7, 8, 9, 123, time.FixedZone("-05", -5*3600))
	pages := map[string][]byte{
		"wiki/concepts/alpha.md": wp04basePage("alpha", "# Alpha\n"),
		"wiki/entities/beta.md":  wp04basePage("beta", "# Beta\n"),
	}
	for rel, c := range pages {
		if err := b.Put(rel, c, "sha-"+wp04baseName(rel), now); err != nil {
			t.Fatalf("Put(%s): %v", rel, err)
		}
	}
	if err := b.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	raw, err = os.ReadFile(BasesPath(hub))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(raw, []byte("}\n")) || !bytes.Contains(raw, []byte("\n  \"pages\": {\n    \"")) {
		t.Errorf("bases.json is not 2-space indented with a trailing newline:\n%s", raw)
	}
	m := wp04baseDecode(t, BasesPath(hub))
	if got := wp04baseKeys(m); !reflect.DeepEqual(got, []string{"pages", "schema"}) {
		t.Errorf("top-level keys = %q, want [pages schema]", got)
	}
	if m["schema"] != "ctx.bases.v1" {
		t.Errorf("schema = %v, want ctx.bases.v1", m["schema"])
	}
	pm, ok := m["pages"].(map[string]any)
	if !ok || len(pm) != len(pages) {
		t.Fatalf("pages = %#v, want %d records", m["pages"], len(pages))
	}
	for rel, c := range pages {
		r, ok := pm[rel].(map[string]any)
		if !ok {
			t.Errorf("pages[%q] missing or not an object: %#v", rel, pm[rel])
			continue
		}
		if got := wp04baseKeys(r); !reflect.DeepEqual(got, []string{"hash", "sha", "synced_at"}) {
			t.Errorf("pages[%q] keys = %q, want [hash sha synced_at]", rel, got)
		}
		if r["sha"] != "sha-"+wp04baseName(rel) || r["hash"] != Hash(c) {
			t.Errorf("pages[%q] = %v", rel, r)
		}
		at, _ := r["synced_at"].(string)
		if !wp04baseSecondsZ.MatchString(at) || at != "2026-05-06T12:08:09Z" {
			t.Errorf("pages[%q].synced_at = %q, want 2026-05-06T12:08:09Z", rel, at)
		}
	}
	wp04baseNoTemp(t, SyncDir(hub))
}

func TestWP04BasesRejectBadPath(t *testing.T) {
	root := t.TempDir()
	hub := filepath.Join(root, ".contexo")
	if err := os.MkdirAll(hub, 0o755); err != nil {
		t.Fatal(err)
	}
	// Decoys where a traversal out of .sync/base would land.
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

	b := wp04baseOpen(t, hub)
	for _, bad := range wp04baseBadPaths {
		wp04baseWantPathError(t, "Put", b.Put(bad, []byte("evil\n"), "s1", time.Now()), bad)
		_, err := b.Read(bad)
		wp04baseWantPathError(t, "Read", err, bad)
		_, err = b.ReadVerified(bad)
		wp04baseWantPathError(t, "ReadVerified", err, bad)
		if !errors.Is(err, ErrBaseUnavailable) {
			t.Errorf("ReadVerified(%q) err = %v, want it to wrap ErrBaseUnavailable", bad, err)
		}
		wp04baseWantPathError(t, "Delete", b.Delete(bad), bad)
		if b.Valid(bad, "s1") {
			t.Errorf("Valid(%q) = true", bad)
		}
		if _, ok := b.Get(bad); ok {
			t.Errorf("Get(%q) found a record", bad)
		}
	}
	if p := b.Paths(); len(p) != 0 {
		t.Errorf("Paths after rejected Puts = %q, want empty", p)
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
