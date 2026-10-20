package testkit

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/schema"
)

// wp04IsolateHome points HOME and USERPROFILE at a fresh sentinel dir so a
// test can tell whether NewProject replaced them (and wrote nothing there).
func wp04IsolateHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	return dir
}

// wp04Under reports whether child lies inside parent once symlinks (macOS
// /var -> /private/var) are resolved.
func wp04Under(t *testing.T, child, parent string) bool {
	t.Helper()
	c, err := filepath.EvalSymlinks(child)
	if err != nil {
		t.Fatalf("resolve %s: %v", child, err)
	}
	p, err := filepath.EvalSymlinks(parent)
	if err != nil {
		t.Fatalf("resolve %s: %v", parent, err)
	}
	rel, err := filepath.Rel(p, c)
	if err != nil {
		return false
	}
	return rel != "." && filepath.IsLocal(rel)
}

func wp04AssertMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%s should not exist (stat err = %v)", path, err)
	}
}

func wp04ParseValid(t *testing.T, data []byte) *schema.Page {
	t.Helper()
	page, err := schema.ParsePage(data)
	if err != nil {
		t.Fatalf("ParsePage: %v\n%s", err, data)
	}
	if err := page.Frontmatter.Validate(); err != nil {
		t.Fatalf("Validate: %v\n%s", err, data)
	}
	return page
}

// wp04FakeTB records a fatal failure instead of failing the real test, so
// the failure paths of Project's methods can be asserted.
type wp04FakeTB struct {
	testing.TB

	mu     sync.Mutex
	failed bool
	msg    string
}

func (f *wp04FakeTB) Helper() {}

func (f *wp04FakeTB) Fail()                          { f.stop("Fail") }
func (f *wp04FakeTB) FailNow()                       { f.stop("FailNow") }
func (f *wp04FakeTB) Error(args ...any)              { f.stop(fmt.Sprint(args...)) }
func (f *wp04FakeTB) Errorf(format string, a ...any) { f.stop(fmt.Sprintf(format, a...)) }
func (f *wp04FakeTB) Fatal(args ...any)              { f.stop(fmt.Sprint(args...)) }
func (f *wp04FakeTB) Fatalf(format string, a ...any) { f.stop(fmt.Sprintf(format, a...)) }

func (f *wp04FakeTB) stop(msg string) {
	f.mu.Lock()
	f.failed = true
	f.msg = msg
	f.mu.Unlock()
	runtime.Goexit()
}

// wp04ExpectFatal runs fn on its own goroutine with a fake TB and returns
// the failure message, failing t when fn finishes without failing.
func wp04ExpectFatal(t *testing.T, fn func(tb testing.TB)) string {
	t.Helper()
	fake := &wp04FakeTB{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(fake)
	}()
	<-done
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if !fake.failed {
		t.Fatal("expected a fatal test failure, got none")
	}
	return fake.msg
}

func TestNewProjectWritesValidTree(t *testing.T) {
	sentinel := wp04IsolateHome(t)
	specs := []PageSpec{
		{Type: schema.TypeConcept, Slug: "alpha", Title: "Alpha", Body: "About alpha."},
		{Type: schema.TypeEntity, Slug: "beta", Body: "About beta.", Tags: []string{"x", "y"}},
	}
	p := NewProject(t, ProjectOptions{
		ServerURL: "http://127.0.0.1:1",
		Key:       "k1",
		RepoID:    "r1",
		Pages:     specs,
	})

	// HOME and USERPROFILE are replaced by a fresh dir inside the temp dir.
	home := os.Getenv("HOME")
	if home == "" || home == sentinel {
		t.Fatalf("HOME = %q, want a fresh dir (sentinel %q)", home, sentinel)
	}
	if up := os.Getenv("USERPROFILE"); up != home {
		t.Fatalf("USERPROFILE = %q, want %q", up, home)
	}
	if !wp04Under(t, home, os.TempDir()) {
		t.Fatalf("HOME %q is not under the temp dir %q", home, os.TempDir())
	}
	if got, err := os.UserHomeDir(); err != nil || got != home {
		t.Fatalf("os.UserHomeDir() = %q, %v; want %q", got, err, home)
	}

	// Root and HubDir are temp dirs separate from HOME.
	if p.HubDir != filepath.Join(p.Root, config.ContexoDir) {
		t.Fatalf("HubDir = %q, want %q", p.HubDir, filepath.Join(p.Root, config.ContexoDir))
	}
	if !wp04Under(t, p.Root, os.TempDir()) {
		t.Fatalf("Root %q is not under the temp dir", p.Root)
	}
	if wp04Under(t, p.Root, home) || wp04Under(t, home, p.Root) {
		t.Fatalf("Root %q and HOME %q must not nest", p.Root, home)
	}

	// The init layout.
	for _, d := range []string{"wiki/concepts", "wiki/entities", "wiki/analyses", "raw/sessions"} {
		fi, err := os.Stat(filepath.Join(p.HubDir, filepath.FromSlash(d)))
		if err != nil || !fi.IsDir() {
			t.Fatalf("%s: want a dir (err = %v)", d, err)
		}
	}
	for name, head := range map[string]string{"index.md": "# Knowledge Index\n", "tags.md": "# Tags\n"} {
		data, err := os.ReadFile(filepath.Join(p.HubDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !bytes.HasPrefix(data, []byte(head)) {
			t.Fatalf("%s = %q, want prefix %q", name, data, head)
		}
	}

	// config.json carries repo_id and server_url.
	cfg, err := config.Load(p.Root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if cfg.Version != 1 || cfg.RepoID != "r1" || cfg.ServerURL != "http://127.0.0.1:1" {
		t.Fatalf("config = %+v", cfg)
	}

	// Project credentials carry the key as token plus server_url; no
	// global credentials are written.
	if _, err := os.Stat(config.CredentialsPath(p.Root)); err != nil {
		t.Fatalf("project credentials: %v", err)
	}
	creds, err := config.LoadCredentials(p.Root)
	if err != nil || creds == nil {
		t.Fatalf("LoadCredentials = %+v, %v", creds, err)
	}
	if creds.Token != "k1" || creds.ServerURL != "http://127.0.0.1:1" {
		t.Fatalf("creds = %+v", creds)
	}
	gp, err := config.GlobalCredentialsPath()
	if err != nil {
		t.Fatalf("GlobalCredentialsPath: %v", err)
	}
	wp04AssertMissing(t, gp)

	// Each page parses, validates and lives at its RelPath.
	wantRel := []string{"wiki/concepts/alpha.md", "wiki/entities/beta.md"}
	for i, spec := range specs {
		data, err := os.ReadFile(filepath.Join(p.HubDir, filepath.FromSlash(wantRel[i])))
		if err != nil {
			t.Fatalf("read %s: %v", wantRel[i], err)
		}
		if !bytes.Equal(data, PageBytes(spec)) {
			t.Fatalf("%s differs from PageBytes(spec)", wantRel[i])
		}
		page := wp04ParseValid(t, data)
		if got := page.Frontmatter.RelPath(); got != wantRel[i] {
			t.Fatalf("RelPath = %q, want %q", got, wantRel[i])
		}
		if !reflect.DeepEqual(page.Frontmatter.Tags, spec.Tags) {
			t.Fatalf("%s tags = %v, want %v", wantRel[i], page.Frontmatter.Tags, spec.Tags)
		}
	}
}

func TestWP04NewProjectIsolatesHomePerTest(t *testing.T) {
	sentinel := wp04IsolateHome(t)
	var inner string
	t.Run("project", func(t *testing.T) {
		NewProject(t, ProjectOptions{NoCreds: true})
		inner = os.Getenv("HOME")
	})
	if inner == "" || inner == sentinel {
		t.Fatalf("NewProject left HOME = %q", inner)
	}
	if got := os.Getenv("HOME"); got != sentinel {
		t.Fatalf("HOME after the subtest = %q, want %q restored", got, sentinel)
	}
	if got := os.Getenv("USERPROFILE"); got != sentinel {
		t.Fatalf("USERPROFILE after the subtest = %q, want %q restored", got, sentinel)
	}
}

func TestWP04NewProjectGlobalCreds(t *testing.T) {
	sentinel := wp04IsolateHome(t)
	p := NewProject(t, ProjectOptions{
		ServerURL:   "http://127.0.0.1:2",
		Key:         "kg",
		RepoID:      "r2",
		GlobalCreds: true,
	})

	wp04AssertMissing(t, config.CredentialsPath(p.Root))
	gp, err := config.GlobalCredentialsPath()
	if err != nil {
		t.Fatalf("GlobalCredentialsPath: %v", err)
	}
	if !wp04Under(t, gp, os.Getenv("HOME")) || wp04Under(t, gp, sentinel) {
		t.Fatalf("global credentials %q not under the isolated HOME", gp)
	}
	if _, err := os.Stat(gp); err != nil {
		t.Fatalf("global credentials: %v", err)
	}
	// Written after HOME moved: nothing landed in the previous HOME.
	wp04AssertMissing(t, filepath.Join(sentinel, config.ContexoDir, "credentials.json"))

	creds, err := config.LoadCredentials(p.Root)
	if err != nil || creds == nil {
		t.Fatalf("LoadCredentials = %+v, %v", creds, err)
	}
	if creds.Token != "kg" || creds.ServerURL != "http://127.0.0.1:2" {
		t.Fatalf("creds = %+v", creds)
	}
	cfg, err := config.Load(p.Root)
	if err != nil || cfg.RepoID != "r2" || cfg.ServerURL != "http://127.0.0.1:2" {
		t.Fatalf("config = %+v, %v", cfg, err)
	}
}

func TestWP04NewProjectNoCreds(t *testing.T) {
	for _, global := range []bool{false, true} {
		t.Run(fmt.Sprintf("global=%v", global), func(t *testing.T) {
			sentinel := wp04IsolateHome(t)
			p := NewProject(t, ProjectOptions{
				ServerURL:   "http://127.0.0.1:3",
				Key:         "kn",
				RepoID:      "r3",
				NoCreds:     true,
				GlobalCreds: global,
			})
			wp04AssertMissing(t, config.CredentialsPath(p.Root))
			gp, err := config.GlobalCredentialsPath()
			if err != nil {
				t.Fatalf("GlobalCredentialsPath: %v", err)
			}
			wp04AssertMissing(t, gp)
			wp04AssertMissing(t, filepath.Join(sentinel, config.ContexoDir, "credentials.json"))
			creds, err := config.LoadCredentials(p.Root)
			if err != nil || creds != nil {
				t.Fatalf("LoadCredentials = %+v, %v; want nil, nil", creds, err)
			}
			cfg, err := config.Load(p.Root)
			if err != nil || cfg.RepoID != "r3" || cfg.ServerURL != "http://127.0.0.1:3" {
				t.Fatalf("config = %+v, %v", cfg, err)
			}
		})
	}
}

func TestWP04NewProjectInvalidPageFails(t *testing.T) {
	wp04IsolateHome(t)
	msg := wp04ExpectFatal(t, func(tb testing.TB) {
		NewProject(tb, ProjectOptions{NoCreds: true, Pages: []PageSpec{{Slug: "../escape"}}})
	})
	if !strings.Contains(msg, "escape") {
		t.Fatalf("failure message %q does not name the bad slug", msg)
	}
}

func TestWP04PageBytesDeterministic(t *testing.T) {
	spec := PageSpec{
		Type:  schema.TypeEntity,
		Slug:  "beta",
		Title: "Beta Thing",
		Body:  "Line one.\n\nLine two.",
		Tags:  []string{"x", "y"},
	}
	a := PageBytes(spec)
	b := PageBytes(spec)
	if !bytes.Equal(a, b) {
		t.Fatalf("PageBytes is not deterministic:\n%s\n---\n%s", a, b)
	}

	// Normalize-stable: re-serializing the parsed page gives the same bytes.
	page := wp04ParseValid(t, a)
	again, err := schema.SerializePage(page)
	if err != nil {
		t.Fatalf("SerializePage: %v", err)
	}
	if !bytes.Equal(again, a) {
		t.Fatalf("PageBytes is not normalize-stable:\n%s\n---\n%s", a, again)
	}

	fm := page.Frontmatter
	fixed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if fm.Schema != schema.PageSchemaV1 || fm.Slug != "beta" || fm.Type != schema.TypeEntity ||
		fm.Author != "testkit" || !fm.Created.Equal(fixed) || !fm.Updated.Equal(fixed) ||
		!reflect.DeepEqual(fm.Tags, []string{"x", "y"}) {
		t.Fatalf("frontmatter = %+v", fm)
	}
	if want := "# Beta Thing\n\nLine one.\n\nLine two.\n"; page.Body != want {
		t.Fatalf("body = %q, want %q", page.Body, want)
	}
}

func TestWP04PageBytesDefaults(t *testing.T) {
	got := PageBytes(PageSpec{Slug: "alpha"})
	want := "---\n" +
		"schema: ctx.page.v1\n" +
		"slug: alpha\n" +
		"type: concept\n" +
		"author: testkit\n" +
		"created: 2026-01-01T00:00:00Z\n" +
		"updated: 2026-01-01T00:00:00Z\n" +
		"parent_sha: \"\"\n" +
		"---\n" +
		"# alpha\n"
	if string(got) != want {
		t.Fatalf("PageBytes defaults:\n%s\nwant:\n%s", got, want)
	}

	page := wp04ParseValid(t, PageBytes(PageSpec{
		Type:   schema.TypeSource,
		Slug:   "s1",
		Author: "alice",
		Body:   "crlf\r\nbody\r\n",
	}))
	if page.Frontmatter.Author != "alice" || page.Frontmatter.RelPath() != "raw/sessions/s1.md" {
		t.Fatalf("frontmatter = %+v", page.Frontmatter)
	}
	if want := "# s1\n\ncrlf\nbody\n"; page.Body != want {
		t.Fatalf("body = %q, want %q", page.Body, want)
	}
}

func TestWP04ProjectFileMethods(t *testing.T) {
	wp04IsolateHome(t)
	p := NewProject(t, ProjectOptions{NoCreds: true})

	spec := PageSpec{Type: schema.TypeAnalysis, Slug: "gamma", Body: "g"}
	rel := p.WritePage(spec)
	if rel != "wiki/analyses/gamma.md" {
		t.Fatalf("WritePage = %q", rel)
	}
	if _, err := os.Stat(filepath.Join(p.HubDir, "wiki", "analyses", "gamma.md")); err != nil {
		t.Fatalf("written page: %v", err)
	}
	if got := p.Read(rel); !bytes.Equal(got, PageBytes(spec)) {
		t.Fatalf("Read(%s) = %q", rel, got)
	}

	// WriteRaw creates parent dirs (dot dirs included) and overwrites.
	for _, r := range []string{"wiki/concepts/broken.md", ".sync/state.json", "new/dir/x.md"} {
		p.WriteRaw(r, []byte("first"))
		p.WriteRaw(r, []byte("second"))
		if got := p.Read(r); string(got) != "second" {
			t.Fatalf("Read(%s) = %q, want %q", r, got, "second")
		}
		data, err := os.ReadFile(filepath.Join(p.HubDir, filepath.FromSlash(r)))
		if err != nil || string(data) != "second" {
			t.Fatalf("on-disk %s = %q, %v", r, data, err)
		}
		p.Remove(r)
		wp04AssertMissing(t, filepath.Join(p.HubDir, filepath.FromSlash(r)))
	}

	p.Remove(rel)
	wp04AssertMissing(t, filepath.Join(p.HubDir, "wiki", "analyses", "gamma.md"))
}

func TestWP04ProjectMethodsFailLoudly(t *testing.T) {
	wp04IsolateHome(t)
	p := NewProject(t, ProjectOptions{NoCreds: true})
	fake := func(tb testing.TB) *Project {
		return &Project{Root: p.Root, HubDir: p.HubDir, t: tb}
	}

	cases := map[string]func(tb testing.TB){
		"read missing":   func(tb testing.TB) { fake(tb).Read("wiki/concepts/nope.md") },
		"remove missing": func(tb testing.TB) { fake(tb).Remove("wiki/concepts/nope.md") },
		"write parent":   func(tb testing.TB) { fake(tb).WriteRaw("../escape.md", []byte("x")) },
		"write absolute": func(tb testing.TB) {
			fake(tb).WriteRaw(filepath.ToSlash(filepath.Join(p.Root, "abs.md")), []byte("x"))
		},
		"write empty":   func(tb testing.TB) { fake(tb).WriteRaw("", []byte("x")) },
		"read parent":   func(tb testing.TB) { fake(tb).Read("../.contexo/index.md") },
		"remove parent": func(tb testing.TB) { fake(tb).Remove("../.contexo/config.json") },
		"bad slug":      func(tb testing.TB) { fake(tb).WritePage(PageSpec{Slug: "../../escape"}) },
		"no slug":       func(tb testing.TB) { fake(tb).WritePage(PageSpec{}) },
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			wp04ExpectFatal(t, fn)
		})
	}
	wp04AssertMissing(t, filepath.Join(p.Root, "escape.md"))
	wp04AssertMissing(t, filepath.Join(p.Root, "abs.md"))
	if _, err := os.Stat(config.ContexoConfigPath(p.Root)); err != nil {
		t.Fatalf("config.json: %v", err)
	}
}
