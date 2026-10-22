package syncops

// Owner: WP-1.2. Tests of the D4 distill guard (CheckGuard). The p12
// helpers declared here are shared with push_test.go.

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/syncops/contract"
)

// p12Now is the fixed clock of every p12 Env: a non-UTC zone with
// nanoseconds, so a contract time that skips TimeOf shows up.
var p12Now = time.Date(2026, 10, 22, 16, 30, 15, 123456789, time.FixedZone("WIB", 7*60*60))

// p12GuardEnv is an offline Env over an initialised temp root, with HOME
// isolated, the distill env var cleared and Now fixed at p12Now.
func p12GuardEnv(t *testing.T) Env {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("CONTEXO_DISTILL_DISABLE", "")
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".contexo"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := NewEnv(root, "acme-kb", "https://kb.acme.dev", Author{Name: "Ana", Email: "ana@acme.dev"}, nil)
	env.Now = func() time.Time { return p12Now }
	return env
}

// p12Page returns a valid ctx.page.v1 page of type typ and slug with the
// given body and tags.
func p12Page(t *testing.T, typ schema.PageType, slug, body string, tags ...string) []byte {
	t.Helper()
	b, err := schema.SerializePage(&schema.Page{
		Frontmatter: schema.PageFrontmatter{
			Schema:  schema.PageSchemaV1,
			Slug:    slug,
			Type:    typ,
			Author:  "tester",
			Created: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
			Updated: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
			Tags:    tags,
		},
		Body: body,
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// p12WriteFile writes data at hub/rel, creating the parent directories.
func p12WriteFile(t *testing.T, hub, rel string, data []byte) {
	t.Helper()
	p := filepath.Join(hub, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// p12WriteTypedPage writes a valid page of type typ at hub/rel (slug = the
// file's base name) and returns its bytes.
func p12WriteTypedPage(t *testing.T, hub, rel string, typ schema.PageType) []byte {
	t.Helper()
	slug := strings.TrimSuffix(path.Base(rel), ".md")
	data := p12Page(t, typ, slug, "# "+slug+"\n\nBody of "+slug+".\n")
	p12WriteFile(t, hub, rel, data)
	return data
}

// p12WriteBufferRaw writes a capture buffer for session sid with content
// and sets its mtime.
func p12WriteBufferRaw(t *testing.T, hub, sid string, content []byte, mtime time.Time) {
	t.Helper()
	p := filepath.Join(hub, "raw", "sessions", "_pending", sid+".jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

// p12WriteBuffer writes a capture buffer of lines turn records.
func p12WriteBuffer(t *testing.T, hub, sid string, lines int, mtime time.Time) {
	t.Helper()
	var b strings.Builder
	for i := 1; i <= lines; i++ {
		b.WriteString(`{"ts":"2026-10-22T09:00:00Z","turn":` + strconv.Itoa(i) +
			`,"user":"q","assistant":"a"}` + "\n")
	}
	p12WriteBufferRaw(t, hub, sid, []byte(b.String()), mtime)
}

// p12Tree returns every regular file under hub (rel path -> content),
// except the lock files and .sync/.gitignore, which taking the lock may
// create.
func p12Tree(t *testing.T, hub string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(hub, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(hub, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		switch rel {
		case ".sync/lock", ".sync/lock.guard", ".sync/.gitignore":
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[rel] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// p12AssertSessions compares capture sessions, times with Equal.
func p12AssertSessions(t *testing.T, got, want []contract.CaptureSession) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("sessions = %+v, want %+v", got, want)
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.SessionID != w.SessionID || g.Turns != w.Turns || g.Fresh != w.Fresh ||
			!g.UpdatedAt.Equal(w.UpdatedAt) || g.UpdatedAt.Location() != time.UTC ||
			g.UpdatedAt.Nanosecond() != 0 {
			t.Fatalf("sessions[%d] = %+v, want %+v (UTC, whole seconds)", i, g, w)
		}
	}
}

func p12Prompt(pages ...string) string {
	return "Push my pending Contexo changes with the ctx_push tool (" + strings.Join(pages, ", ") +
		"). Distill the pending session into a source page first when ctx_push asks you to."
}

func TestGuard_BlocksConceptWithFreshBuffer(t *testing.T) {
	env := p12GuardEnv(t)
	rel := "wiki/concepts/auth-flow.md"
	p12WriteTypedPage(t, env.HubDir, rel, schema.TypeConcept)
	mtime := p12Now.Add(-time.Minute)
	p12WriteBuffer(t, env.HubDir, "sess-1", 3, mtime)

	g, err := CheckGuard(env, []string{rel})
	if err != nil {
		t.Fatalf("CheckGuard: %v", err)
	}
	if !g.Required || g.Bypassed {
		t.Fatalf("Required, Bypassed = %v, %v; want true, false", g.Required, g.Bypassed)
	}
	if g.Details == nil {
		t.Fatal("Details = nil with Required")
	}
	if !reflect.DeepEqual(g.Details.Pages, []string{rel}) {
		t.Fatalf("Pages = %#v, want [%s]", g.Details.Pages, rel)
	}
	p12AssertSessions(t, g.Details.Sessions, []contract.CaptureSession{
		{SessionID: "sess-1", Turns: 3, UpdatedAt: contract.TimeOf(mtime), Fresh: true},
	})
	if want := p12Prompt(rel); g.Details.AgentPrompt != want {
		t.Fatalf("AgentPrompt = %q, want %q", g.Details.AgentPrompt, want)
	}
}

func TestGuard_AllowsEntityOnly(t *testing.T) {
	env := p12GuardEnv(t)
	rel := "wiki/entities/billing.md"
	p12WriteTypedPage(t, env.HubDir, rel, schema.TypeEntity)
	src := "raw/sessions/notes.md"
	p12WriteTypedPage(t, env.HubDir, src, schema.TypeSource)
	p12WriteBuffer(t, env.HubDir, "sess-1", 3, p12Now.Add(-time.Minute))

	g, err := CheckGuard(env, []string{rel, src})
	if err != nil {
		t.Fatalf("CheckGuard: %v", err)
	}
	if g.Required || g.Details != nil || g.Bypassed {
		t.Fatalf("CheckGuard = %+v, want not required, no details, not bypassed", g)
	}
}

func TestGuard_IgnoresStaleBuffer(t *testing.T) {
	env := p12GuardEnv(t)
	rel := "wiki/concepts/a.md"
	p12WriteTypedPage(t, env.HubDir, rel, schema.TypeConcept)
	p12WriteBuffer(t, env.HubDir, "old", 3, p12Now.Add(-7*time.Hour))

	g, err := CheckGuard(env, []string{rel})
	if err != nil {
		t.Fatalf("CheckGuard: %v", err)
	}
	if g.Required || g.Details != nil {
		t.Fatalf("CheckGuard = %+v, want not required for a 7h-old buffer", g)
	}
}

func TestGuard_IgnoresEmptyBuffer(t *testing.T) {
	for name, content := range map[string]string{
		"zero lines":  "",
		"blank lines": "\n  \n",
	} {
		t.Run(name, func(t *testing.T) {
			env := p12GuardEnv(t)
			rel := "wiki/analyses/a.md"
			p12WriteTypedPage(t, env.HubDir, rel, schema.TypeAnalysis)
			p12WriteBufferRaw(t, env.HubDir, "empty", []byte(content), p12Now.Add(-time.Minute))

			g, err := CheckGuard(env, []string{rel})
			if err != nil {
				t.Fatalf("CheckGuard: %v", err)
			}
			if g.Required || g.Details != nil {
				t.Fatalf("CheckGuard = %+v, want not required for a buffer without turns", g)
			}
		})
	}
}

func TestGuard_ScansAllBuffers(t *testing.T) {
	env := p12GuardEnv(t)
	rel := "wiki/concepts/a.md"
	p12WriteTypedPage(t, env.HubDir, rel, schema.TypeConcept)
	p12WriteBuffer(t, env.HubDir, "newest-empty", 0, p12Now.Add(-time.Minute))
	older := p12Now.Add(-2 * time.Hour)
	p12WriteBuffer(t, env.HubDir, "older", 2, older)
	// An archived buffer is never pending.
	arch := filepath.Join(env.HubDir, "raw", "sessions", "_pending", "_archive", "archived.jsonl")
	if err := os.MkdirAll(filepath.Dir(arch), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(arch, []byte("{\"turn\":1}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	g, err := CheckGuard(env, []string{rel})
	if err != nil {
		t.Fatalf("CheckGuard: %v", err)
	}
	if !g.Required || g.Details == nil {
		t.Fatalf("CheckGuard = %+v, want required: an older fresh buffer has turns", g)
	}
	p12AssertSessions(t, g.Details.Sessions, []contract.CaptureSession{
		{SessionID: "older", Turns: 2, UpdatedAt: contract.TimeOf(older), Fresh: true},
	})
}

func TestGuard_DisabledByEnvSetsBypassed(t *testing.T) {
	for _, c := range []struct {
		val      string
		bypassed bool
	}{{"1", true}, {"0", false}, {"", false}, {"true", false}} {
		t.Run("value "+c.val, func(t *testing.T) {
			env := p12GuardEnv(t)
			t.Setenv("CONTEXO_DISTILL_DISABLE", c.val)
			concept := "wiki/concepts/a.md"
			entity := "wiki/entities/e.md"
			p12WriteTypedPage(t, env.HubDir, concept, schema.TypeConcept)
			p12WriteTypedPage(t, env.HubDir, entity, schema.TypeEntity)
			p12WriteBuffer(t, env.HubDir, "s", 1, p12Now.Add(-time.Minute))

			g, err := CheckGuard(env, []string{concept})
			if err != nil {
				t.Fatalf("CheckGuard: %v", err)
			}
			if !g.Required || g.Details == nil || g.Bypassed != c.bypassed {
				t.Fatalf("CheckGuard = %+v, want Required true with details and Bypassed %v", g, c.bypassed)
			}
			// Bypassed is set from the env var whether or not the guard is required.
			g, err = CheckGuard(env, []string{entity})
			if err != nil {
				t.Fatalf("CheckGuard(entity): %v", err)
			}
			if g.Required || g.Details != nil || g.Bypassed != c.bypassed {
				t.Fatalf("CheckGuard(entity) = %+v, want not required and Bypassed %v", g, c.bypassed)
			}
		})
	}
}

func TestGuard_DetailsAgentPromptExact(t *testing.T) {
	env := p12GuardEnv(t)
	p12WriteTypedPage(t, env.HubDir, "wiki/concepts/b.md", schema.TypeConcept)
	p12WriteTypedPage(t, env.HubDir, "wiki/analyses/a.md", schema.TypeAnalysis)
	p12WriteTypedPage(t, env.HubDir, "wiki/entities/e.md", schema.TypeEntity)
	p12WriteBuffer(t, env.HubDir, "s", 1, p12Now.Add(-time.Minute))

	g, err := CheckGuard(env, []string{"wiki/concepts/b.md", "wiki/analyses/a.md", "wiki/entities/e.md", "wiki/concepts/b.md"})
	if err != nil {
		t.Fatalf("CheckGuard: %v", err)
	}
	if !g.Required || g.Details == nil {
		t.Fatalf("CheckGuard = %+v, want required", g)
	}
	if want := []string{"wiki/analyses/a.md", "wiki/concepts/b.md"}; !reflect.DeepEqual(g.Details.Pages, want) {
		t.Fatalf("Pages = %#v, want %#v", g.Details.Pages, want)
	}
	const want = "Push my pending Contexo changes with the ctx_push tool (wiki/analyses/a.md, wiki/concepts/b.md). Distill the pending session into a source page first when ctx_push asks you to."
	if g.Details.AgentPrompt != want {
		t.Fatalf("AgentPrompt = %q, want %q", g.Details.AgentPrompt, want)
	}
}

func TestGuard_NoWrites(t *testing.T) {
	env := p12GuardEnv(t)

	// No pending directory at all: not required, and none is created.
	concept := "wiki/concepts/a.md"
	p12WriteTypedPage(t, env.HubDir, concept, schema.TypeConcept)
	before := p12Tree(t, env.HubDir)
	g, err := CheckGuard(env, []string{concept})
	if err != nil || g.Required {
		t.Fatalf("CheckGuard without buffers = %+v, %v; want not required", g, err)
	}
	if _, err := os.Stat(filepath.Join(env.HubDir, "raw")); !os.IsNotExist(err) {
		t.Fatalf("CheckGuard created raw/: %v", err)
	}
	if after := p12Tree(t, env.HubDir); !reflect.DeepEqual(before, after) {
		t.Fatalf("CheckGuard changed the hub:\nbefore %v\nafter  %v", before, after)
	}

	broken := "wiki/concepts/broken.md"
	p12WriteFile(t, env.HubDir, broken, []byte("not a page\n"))
	missing := "wiki/concepts/missing.md"
	p12WriteBuffer(t, env.HubDir, "s", 2, p12Now.Add(-time.Minute))
	before = p12Tree(t, env.HubDir)
	g, err = CheckGuard(env, []string{missing, broken, concept})
	if err != nil {
		t.Fatalf("CheckGuard: %v", err)
	}
	if !g.Required || g.Details == nil || !reflect.DeepEqual(g.Details.Pages, []string{concept}) {
		t.Fatalf("CheckGuard = %+v, want required for the concept page only", g)
	}
	if after := p12Tree(t, env.HubDir); !reflect.DeepEqual(before, after) {
		t.Fatalf("CheckGuard changed the hub:\nbefore %v\nafter  %v", before, after)
	}

	// A missing file or an unparsable page is not knowledge.
	g, err = CheckGuard(env, []string{missing, broken})
	if err != nil {
		t.Fatalf("CheckGuard(missing, broken): %v", err)
	}
	if g.Required || g.Details != nil {
		t.Fatalf("CheckGuard(missing, broken) = %+v, want not required", g)
	}

	// An invalid path is an error.
	if g, err := CheckGuard(env, []string{"../x.md"}); err == nil {
		t.Fatalf("CheckGuard(../x.md) = %+v, want an error", g)
	}
	if after := p12Tree(t, env.HubDir); !reflect.DeepEqual(before, after) {
		t.Fatalf("CheckGuard changed the hub:\nbefore %v\nafter  %v", before, after)
	}
}

func TestGuard_SessionsNewestFirst(t *testing.T) {
	env := p12GuardEnv(t)
	rel := "wiki/concepts/a.md"
	p12WriteTypedPage(t, env.HubDir, rel, schema.TypeConcept)
	older := p12Now.Add(-30 * time.Minute)
	newer := p12Now.Add(-time.Minute)
	p12WriteBuffer(t, env.HubDir, "a-older", 1, older)
	p12WriteBuffer(t, env.HubDir, "z-newer", 4, newer)
	p12WriteBuffer(t, env.HubDir, "stale", 5, p12Now.Add(-6*time.Hour-time.Second))
	p12WriteBuffer(t, env.HubDir, "empty", 0, p12Now.Add(-2*time.Minute))

	g, err := CheckGuard(env, []string{rel})
	if err != nil {
		t.Fatalf("CheckGuard: %v", err)
	}
	if !g.Required || g.Details == nil {
		t.Fatalf("CheckGuard = %+v, want required", g)
	}
	p12AssertSessions(t, g.Details.Sessions, []contract.CaptureSession{
		{SessionID: "z-newer", Turns: 4, UpdatedAt: contract.TimeOf(newer), Fresh: true},
		{SessionID: "a-older", Turns: 1, UpdatedAt: contract.TimeOf(older), Fresh: true},
	})
}
