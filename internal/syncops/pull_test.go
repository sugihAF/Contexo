package syncops

// Owner: WP-1.3. This file holds the wave-0 stub assertion for Pull; the
// owner replaces it when the safe pull is implemented. Tests that need
// syncopstest.FakeRemote must live in package syncops_test (syncopstest
// imports syncops).

import (
	"context"
	"testing"
)

func TestPullStubNotImplemented(t *testing.T) {
	env := wp01StubEnv(t)
	doc, err := Pull(context.Background(), env, PullOptions{})
	wp01AssertNotImplemented(t, err, "pull")
	if doc != nil {
		t.Fatalf("Pull returned a document with the stub error: %+v", doc)
	}
}
