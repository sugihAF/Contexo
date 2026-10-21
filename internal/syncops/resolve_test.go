package syncops

// Owner: WP-1.4. This file holds the wave-0 stub assertion for Resolve; the
// owner replaces it when Resolve is implemented. Tests here may
// use syncopstest.FakeRemote together with unexported seams (syncopstest
// never imports syncops).

import (
	"context"
	"testing"

	"github.com/sugihAF/contexo/internal/syncops/contract"
)

func TestResolveStubNotImplemented(t *testing.T) {
	env := wp01StubEnv(t)
	for _, s := range []contract.ResolveStrategy{contract.ResolveOurs, contract.ResolveTheirs} {
		doc, err := Resolve(context.Background(), env, ResolveOptions{
			Path:        "wiki/concepts/a.md",
			Strategy:    s,
			IfServerSHA: "2222222222222222222222222222222222222222",
		})
		wp01AssertNotImplemented(t, err, "resolve")
		if doc != nil {
			t.Fatalf("Resolve(%s) returned a document with the stub error: %+v", s, doc)
		}
	}
}
