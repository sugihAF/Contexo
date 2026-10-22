package syncops

import "github.com/sugihAF/contexo/internal/syncops/contract"

// Owner: WP-1.2. Fields may be added, never renamed or removed.

// GuardResult is the D4 distill-guard verdict for a batch. Bypassed is true
// when CONTEXO_DISTILL_DISABLE=1 is set (E24); it never clears Required.
type GuardResult struct {
	Required bool
	Details  *contract.DistillRequiredDetails
	Bypassed bool
}

// CheckGuard evaluates the D4 distill guard over batch (the modified ∪ new
// paths about to be pushed). The wave-0 stub never requires distillation.
func CheckGuard(env Env, batch []string) (*GuardResult, error) {
	return &GuardResult{Required: false}, nil
}
