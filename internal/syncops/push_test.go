package syncops

// Owner: WP-1.2. This file holds the wave-0 stub assertion for Push; the
// owner replaces it when Push is implemented. Tests here may
// use syncopstest.FakeRemote together with unexported seams (syncopstest
// never imports syncops).

import (
	"context"
	"testing"
)

func TestPushStubNotImplemented(t *testing.T) {
	env := wp01StubEnv(t)
	doc, err := Push(context.Background(), env, PushOptions{
		Paths:   []string{"wiki/concepts/a.md"},
		Message: "update a",
		DryRun:  true,
	})
	wp01AssertNotImplemented(t, err, "push")
	if doc != nil {
		t.Fatalf("Push returned a document with the stub error: %+v", doc)
	}
}
