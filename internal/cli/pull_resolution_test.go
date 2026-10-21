package cli

import "testing"

func TestPullUsesResolvedServer(t *testing.T) {
	tr := wp03TrapServers(t)
	root := wp03Project(t, tr)

	// A's generic bodies may not satisfy pull; only the host matters.
	_, _, _ = wp03Run(t, root, "pull")
	tr.AssertOnlyA(t, "pull", wp03Token)
}

func TestPullGlobalCredsWithoutServerIsUnresolved(t *testing.T) {
	tr := wp03TrapServers(t)
	root := wp03UnresolvedProject(t, tr)

	_, _, err := wp03Run(t, root, "pull")
	if code := wp03RemoteCode(err); code != "server_unresolved" {
		t.Errorf("pull: error code = %q (err: %v), want server_unresolved", code, err)
	}
	wp03AssertNothingSent(t, tr, "pull")
}
