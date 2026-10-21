package syncops

// Owner: WP-1.4. This file holds the wave-0 stub assertion for Show; the
// owner replaces it when Show is implemented. Tests here may
// use syncopstest.FakeRemote together with unexported seams (syncopstest
// never imports syncops).

import (
	"context"
	"testing"
)

func TestShowStubNotImplemented(t *testing.T) {
	env := wp01StubEnv(t)
	for _, at := range []string{"base", "server", "2222222222222222222222222222222222222222"} {
		b, err := Show(context.Background(), env, ShowOptions{Path: "wiki/concepts/a.md", At: at})
		wp01AssertNotImplemented(t, err, "show")
		if b != nil {
			t.Fatalf("Show(--at %s) returned %d bytes with the stub error", at, len(b))
		}
	}
}
