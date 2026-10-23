package cli

import (
	"strings"
	"testing"
)

// TestWP41_L22_PushFlagHelpWording: the human-facing push help and the
// --fallback-server error say "decision record", never "reasoning" (L22).
func TestWP41_L22_PushFlagHelpWording(t *testing.T) {
	cmd := newPushCmd()
	for _, name := range []string{"fallback-server", "no-distill"} {
		f := cmd.Flags().Lookup(name)
		if f == nil {
			t.Fatalf("push has no --%s flag", name)
		}
		if strings.Contains(strings.ToLower(f.Usage), "reasoning") {
			t.Errorf("--%s usage %q says 'reasoning' (L22)", name, f.Usage)
		}
		if !strings.Contains(f.Usage, "decision-record") {
			t.Errorf("--%s usage %q, want it to name the decision-record distillation", name, f.Usage)
		}
	}
	if err := cmd.Flags().Set("fallback-server", "true"); err != nil {
		t.Fatal(err)
	}
	err := cmd.RunE(cmd, nil)
	if err == nil {
		t.Fatal("--fallback-server succeeded, want its not-implemented error")
	}
	if msg := err.Error(); strings.Contains(strings.ToLower(msg), "reasoning") {
		t.Errorf("--fallback-server error %q says 'reasoning' (L22)", msg)
	}
}
