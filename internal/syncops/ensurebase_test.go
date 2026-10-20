// External test package: syncopstest imports syncops, so a package-syncops
// test cannot import it (import cycle).
package syncops_test

// Owner: WP-1.5. This file holds the wave-0 stub assertion for EnsureBase;
// the owner replaces it when base repair is implemented.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/syncops"
	"github.com/sugihAF/contexo/internal/syncops/syncopstest"
)

func TestEnsureBaseStubNoop(t *testing.T) {
	root := t.TempDir()
	hub := filepath.Join(root, ".contexo")
	if err := os.Mkdir(hub, 0o755); err != nil {
		t.Fatal(err)
	}
	fake := syncopstest.NewFakeRemote(syncopstest.FakeOptions{})
	sha := fake.Seed("wiki/concepts/a.md", []byte("---\ntitle: A\n---\nbody\n"))
	env := syncops.NewEnv(root, "acme-kb", "https://kb.acme.dev",
		syncops.Author{Name: "Ana", Email: "ana@acme.dev"}, fake)

	listing := []sync.PageMeta{{
		Path: "wiki/concepts/a.md", SHA: sha, Author: "Seed", Email: "seed@acme.dev",
		Time: time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC), Message: "seed",
	}}
	cases := []struct {
		name string
		o    syncops.EnsureBaseOptions
	}{
		{"offline", syncops.EnsureBaseOptions{}},
		{"online unlocked", syncops.EnsureBaseOptions{Listing: listing}},
		{"online locked with paths", syncops.EnsureBaseOptions{
			Listing: listing, Paths: []string{"wiki/concepts/a.md"}, Locked: true,
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := syncops.EnsureBase(context.Background(), env, c.o)
			if err != nil {
				t.Fatalf("EnsureBase: %v", err)
			}
			if res == nil {
				t.Fatal("EnsureBase returned a nil result")
			}
			if res.Repaired == nil || res.Unverified == nil || res.Warnings == nil {
				t.Fatalf("result has a nil slice: %+v", res)
			}
			if len(res.Repaired)+len(res.Unverified)+len(res.Warnings) != 0 {
				t.Fatalf("stub result is not empty: %+v", res)
			}
			if got := fake.Requests(); len(got) != 0 {
				t.Fatalf("EnsureBase made remote calls: %v", got)
			}
			entries, err := os.ReadDir(hub)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("EnsureBase wrote under .contexo: %v", entries)
			}
		})
	}
}
