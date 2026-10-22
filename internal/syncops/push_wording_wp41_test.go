package syncops

// WP-4.1 L22-1: the D4 refusal message is human-facing text; it says
// "decision record", never "reasoning" (lead ruling L22).

import (
	"strings"
	"testing"

	"github.com/sugihAF/contexo/internal/syncops/contract"
)

func TestWP41_L22_DistillMessageWording(t *testing.T) {
	for _, n := range []int{1, 2} {
		g := &GuardResult{Required: true, Details: &contract.DistillRequiredDetails{
			Sessions: make([]contract.CaptureSession, n),
		}}
		msg := pushDistillError(g).Message
		if strings.Contains(strings.ToLower(msg), "reasoning") {
			t.Errorf("D4 message %q says 'reasoning' (L22: use 'decision record')", msg)
		}
		if !strings.HasSuffix(msg, "; share these pages through your agent so the decision record is captured") {
			t.Errorf("D4 message = %q, want it to end in '...so the decision record is captured'", msg)
		}
	}
}
