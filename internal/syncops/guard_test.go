package syncops

// Owner: WP-1.2. This file holds the wave-0 stub assertion for CheckGuard;
// the owner replaces it when the D4 distill guard is implemented.

import "testing"

func TestCheckGuardStubNotRequired(t *testing.T) {
	env := wp01StubEnv(t)
	for _, batch := range [][]string{nil, {}, {"wiki/concepts/a.md", "wiki/analyses/b.md"}} {
		res, err := CheckGuard(env, batch)
		if err != nil {
			t.Fatalf("CheckGuard(%q): %v", batch, err)
		}
		if res == nil {
			t.Fatalf("CheckGuard(%q) returned a nil result", batch)
		}
		if res.Required {
			t.Fatalf("CheckGuard(%q).Required = true, want false for the stub", batch)
		}
		if res.Details != nil {
			t.Fatalf("CheckGuard(%q).Details = %+v, want nil", batch, res.Details)
		}
	}
}
