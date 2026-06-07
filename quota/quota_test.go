package quota

import (
	"errors"
	"fmt"
	"testing"
)

// The OSS/self-host build injects no policy and falls back to Unlimited, which
// must never deny — caps are a hosted-only concern.
func TestUnlimited_NeverDenies(t *testing.T) {
	var p Policy = Unlimited{}
	if err := p.AllowRepoCreate("user-1", 9999); err != nil {
		t.Errorf("Unlimited.AllowRepoCreate should never deny, got %v", err)
	}
	if err := p.AllowMemberAdd("repo-1", []string{"owner-1"}, 9999); err != nil {
		t.Errorf("Unlimited.AllowMemberAdd should never deny, got %v", err)
	}
}

func TestLimitError_MessageAndExtraction(t *testing.T) {
	base := &LimitError{
		Kind:       "repos",
		Limit:      5,
		Message:    "Free tier is limited to 5 repos.",
		UpgradeURL: "https://contexo.live/#pricing",
	}
	if base.Error() != "Free tier is limited to 5 repos." {
		t.Errorf("Error() = %q, want the Message", base.Error())
	}

	// Handlers wrap the policy error; errors.As must still extract the typed
	// value so the 402 mapping can read Kind/Limit/UpgradeURL.
	wrapped := fmt.Errorf("create repo: %w", base)
	var le *LimitError
	if !errors.As(wrapped, &le) {
		t.Fatal("errors.As should extract *LimitError from a wrapped error")
	}
	if le.Kind != "repos" || le.Limit != 5 || le.UpgradeURL == "" {
		t.Errorf("extracted LimitError mismatch: %+v", le)
	}
}
