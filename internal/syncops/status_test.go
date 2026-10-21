package syncops

// Owner: WP-1.1. This file holds the wave-0 stub assertion for Status; the
// owner replaces it when Status is implemented. Tests here may
// use syncopstest.FakeRemote together with unexported seams (syncopstest
// never imports syncops).

import (
	"context"
	"testing"
)

func TestStatusStubNotImplemented(t *testing.T) {
	env := wp01StubEnv(t)
	for _, o := range []StatusOptions{{}, {Offline: true}} {
		doc, err := Status(context.Background(), env, o)
		wp01AssertNotImplemented(t, err, "status")
		if doc != nil {
			t.Fatalf("Status(%+v) returned a document with the stub error: %+v", o, doc)
		}
	}
}
