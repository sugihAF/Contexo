package testkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/schema"
)

// PageSpec describes one test page for PageBytes and NewProject.
type PageSpec struct {
	Type   schema.PageType
	Slug   string
	Title  string
	Body   string
	Author string
	Tags   []string
}

// wp04PageTime is the fixed Created/Updated stamp of every PageBytes page.
var wp04PageTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// wp04Page fills the PageSpec defaults: Type concept, Author "testkit",
// Title = Slug, and a body of "# <Title>\n" followed by a blank line and
// Body (newline-terminated) when Body is set.
func wp04Page(spec PageSpec) *schema.Page {
	typ := spec.Type
	if typ == "" {
		typ = schema.TypeConcept
	}
	author := spec.Author
	if author == "" {
		author = "testkit"
	}
	title := spec.Title
	if title == "" {
		title = spec.Slug
	}
	body := "# " + title + "\n"
	if spec.Body != "" {
		body += "\n" + spec.Body
		if !strings.HasSuffix(spec.Body, "\n") {
			body += "\n"
		}
	}
	var tags []string
	if len(spec.Tags) > 0 {
		tags = append(tags, spec.Tags...)
	}
	return &schema.Page{
		Frontmatter: schema.PageFrontmatter{
			Schema:  schema.PageSchemaV1,
			Slug:    spec.Slug,
			Type:    typ,
			Author:  author,
			Created: wp04PageTime,
			Updated: wp04PageTime,
			Tags:    tags,
		},
		Body: body,
	}
}

// PageBytes returns a valid ctx.page.v1 page for spec, serialized with
// schema.SerializePage and fixed Created/Updated times so the output is
// deterministic.
func PageBytes(spec PageSpec) []byte {
	data, err := schema.SerializePage(wp04Page(spec))
	if err != nil {
		panic("testkit: PageBytes: " + err.Error())
	}
	return data
}

// ProjectOptions configures NewProject. Key becomes the credentials token.
// GlobalCreds writes the credentials to the isolated HOME instead of the
// project; NoCreds writes none.
type ProjectOptions struct {
	ServerURL   string
	Key         string
	RepoID      string
	Pages       []PageSpec
	GlobalCreds bool
	NoCreds     bool
}

// Project is a temp Contexo project: Root holds .contexo, HubDir is
// Root/.contexo.
type Project struct {
	Root   string
	HubDir string

	t testing.TB
}

// NewProject builds a temp project. It isolates HOME and USERPROFILE with
// t.Setenv, so it panics in a test that calls t.Parallel.
func NewProject(t testing.TB, o ProjectOptions) *Project {
	t.Helper()

	// HOME moves first so global credentials can never reach the real home.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	// Resolve symlinks (macOS /var -> /private/var) so Root matches what a
	// child process sees from os.Getwd.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("testkit: resolve project root: %v", err)
	}
	p := &Project{Root: root, HubDir: config.ContexoDirPath(root), t: t}

	for _, d := range []string{"wiki/concepts", "wiki/entities", "wiki/analyses", "raw/sessions"} {
		if err := os.MkdirAll(filepath.Join(p.HubDir, filepath.FromSlash(d)), 0o755); err != nil {
			t.Fatalf("testkit: create %s: %v", d, err)
		}
	}
	cfg := &config.Config{Version: 1, RepoID: o.RepoID, ServerURL: o.ServerURL}
	if err := config.Save(root, cfg); err != nil {
		t.Fatalf("testkit: save config: %v", err)
	}
	p.WriteRaw("index.md", []byte("# Knowledge Index\n"))
	p.WriteRaw("tags.md", []byte("# Tags\n"))

	if !o.NoCreds {
		creds := &config.Credentials{Token: o.Key, ServerURL: o.ServerURL}
		if o.GlobalCreds {
			err = config.SaveGlobalCredentials(creds)
		} else {
			err = config.SaveCredentials(root, creds)
		}
		if err != nil {
			t.Fatalf("testkit: save credentials: %v", err)
		}
	}

	for _, spec := range o.Pages {
		p.WritePage(spec)
	}
	return p
}

// WritePage writes spec at its frontmatter RelPath and returns that path.
func (p *Project) WritePage(spec PageSpec) string {
	p.t.Helper()
	fm := wp04Page(spec).Frontmatter
	if err := fm.Validate(); err != nil {
		p.t.Fatalf("testkit: WritePage %q: %v", spec.Slug, err)
	}
	rel := fm.RelPath()
	p.WriteRaw(rel, PageBytes(spec))
	return rel
}

// WriteRaw writes data at rel ('/'-separated, relative to HubDir).
func (p *Project) WriteRaw(rel string, data []byte) {
	p.t.Helper()
	path := p.wp04Path(rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		p.t.Fatalf("testkit: WriteRaw %q: %v", rel, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		p.t.Fatalf("testkit: WriteRaw %q: %v", rel, err)
	}
}

// Read returns the bytes at rel, failing the test when it is missing.
func (p *Project) Read(rel string) []byte {
	p.t.Helper()
	data, err := os.ReadFile(p.wp04Path(rel))
	if err != nil {
		p.t.Fatalf("testkit: Read %q: %v", rel, err)
	}
	return data
}

// Remove deletes rel, failing the test on any error.
func (p *Project) Remove(rel string) {
	p.t.Helper()
	if err := os.Remove(p.wp04Path(rel)); err != nil {
		p.t.Fatalf("testkit: Remove %q: %v", rel, err)
	}
}

// wp04Path maps rel to its native path under HubDir, failing the test when
// rel is empty, absolute or climbs out of HubDir.
func (p *Project) wp04Path(rel string) string {
	p.t.Helper()
	native := filepath.FromSlash(rel)
	if !filepath.IsLocal(native) {
		p.t.Fatalf("testkit: path %q is not inside the hub dir", rel)
	}
	return filepath.Join(p.HubDir, native)
}
