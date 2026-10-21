package cli

import "testing"

func TestPushUsesResolvedServer(t *testing.T) {
	tr := wp03TrapServers(t)
	root := wp03Project(t, tr)

	// A's generic bodies may not satisfy push; only the host matters.
	_, _, _ = wp03Run(t, root, "push", "--yes")
	tr.AssertOnlyA(t, "push", wp03Token)
}

func TestPushGlobalCredsWithoutServerIsUnresolved(t *testing.T) {
	tr := wp03TrapServers(t)
	root := wp03UnresolvedProject(t, tr)

	_, _, err := wp03Run(t, root, "push", "--yes")
	if code := wp03RemoteCode(err); code != "server_unresolved" {
		t.Errorf("push: error code = %q (err: %v), want server_unresolved", code, err)
	}
	wp03AssertNothingSent(t, tr, "push")
}
