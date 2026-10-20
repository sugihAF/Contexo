package testkit

import (
	"testing"

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

// PageBytes returns a valid ctx.page.v1 page for spec, serialized with
// schema.SerializePage and fixed Created/Updated times so the output is
// deterministic.
func PageBytes(spec PageSpec) []byte {
	return nil
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
	t.Fatal("testkit: NewProject not implemented")
	return nil
}

// WritePage writes spec at its frontmatter RelPath and returns that path.
func (p *Project) WritePage(spec PageSpec) string {
	p.t.Helper()
	p.t.Fatal("testkit: WritePage not implemented")
	return ""
}

// WriteRaw writes data at rel ('/'-separated, relative to HubDir).
func (p *Project) WriteRaw(rel string, data []byte) {
	p.t.Helper()
	p.t.Fatal("testkit: WriteRaw not implemented")
}

// Read returns the bytes at rel, failing the test when it is missing.
func (p *Project) Read(rel string) []byte {
	p.t.Helper()
	p.t.Fatal("testkit: Read not implemented")
	return nil
}

// Remove deletes rel, failing the test on any error.
func (p *Project) Remove(rel string) {
	p.t.Helper()
	p.t.Fatal("testkit: Remove not implemented")
}
