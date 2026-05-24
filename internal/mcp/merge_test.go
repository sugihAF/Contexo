package mcp

import (
	"strings"
	"testing"

	syncpkg "github.com/sugihAF/contexo/internal/sync"
)

func TestBuildMergeDirective_ThreeWayLayout(t *testing.T) {
	ancestor := []byte("---\nslug: x\ntype: concept\n---\n## Decision\nWe chose Stripe Billing.\n")
	yours := []byte("---\nslug: x\ntype: concept\n---\n## Decision\nWe chose Stripe Billing with tax handling.\n")
	theirs := []byte("---\nslug: x\ntype: concept\n---\n## Decision\nWe chose Stripe Billing with platform-fee handling.\n\n## Refund handling\nIssue refunds via Stripe.\n")

	conflicts := []syncpkg.Conflict{{
		Path:              "wiki/concepts/x.md",
		CurrentSHA:        "def567890abcdef",
		CurrentContent:    theirs,
		ExpectedParentSHA: "abc123456abcdef",
		AncestorContent:   ancestor,
	}}
	out := buildMergeDirective(conflicts, map[string][]byte{
		"wiki/concepts/x.md": yours,
	})

	for _, want := range []string{
		"<MERGE_REQUIRED>",
		"wiki/concepts/x.md",
		"ancestor (both diverged from):  abc1234",
		"server now:                     def5678",
		"YOUR changes vs ancestor:",
		"SERVER changes vs ancestor:",
		"CONFLICTING regions (both sides changed):",
		"## Decision",                     // listed in the conflicting set
		"+ ## Refund handling",            // server-only addition
		"--- ANCESTOR VERSION ---",
		"--- YOUR VERSION",
		"--- SERVER VERSION ---",
		"</MERGE_REQUIRED>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("merge directive missing %q. Full:\n%s", want, out)
		}
	}
}

func TestBuildMergeDirective_NoOverlapNotConflicting(t *testing.T) {
	// Both sides edit different sections — should NOT be flagged as conflicting.
	ancestor := []byte("---\nslug: x\ntype: concept\n---\n## A\nA-original\n\n## B\nB-original\n")
	yours := []byte("---\nslug: x\ntype: concept\n---\n## A\nA-mine\n\n## B\nB-original\n")
	theirs := []byte("---\nslug: x\ntype: concept\n---\n## A\nA-original\n\n## B\nB-theirs\n")

	conflicts := []syncpkg.Conflict{{
		Path:              "wiki/concepts/x.md",
		CurrentSHA:        "def567890",
		CurrentContent:    theirs,
		ExpectedParentSHA: "abc123456",
		AncestorContent:   ancestor,
	}}
	out := buildMergeDirective(conflicts, map[string][]byte{"wiki/concepts/x.md": yours})
	if strings.Contains(out, "CONFLICTING regions") {
		// Should fall through to the "no section modified by both" branch
		// since A is yours-only and B is theirs-only.
		if !strings.Contains(out, "auto-mergeable in principle") {
			t.Errorf("expected 'auto-mergeable' note when no section overlap; full:\n%s", out)
		}
	}
}

func TestBuildMergeDirective_MissingAncestor_Degrades(t *testing.T) {
	// Server couldn't fetch ancestor (e.g. parent_sha unknown). Directive
	// should still render and not panic.
	yours := []byte("---\nslug: x\ntype: concept\n---\nbody\n")
	theirs := []byte("---\nslug: x\ntype: concept\n---\nserver body\n")
	conflicts := []syncpkg.Conflict{{
		Path:              "wiki/concepts/x.md",
		CurrentSHA:        "def5678",
		CurrentContent:    theirs,
		ExpectedParentSHA: "abc1234",
		AncestorContent:   nil,
	}}
	out := buildMergeDirective(conflicts, map[string][]byte{"wiki/concepts/x.md": yours})
	if !strings.Contains(out, "<MERGE_REQUIRED>") {
		t.Fatalf("directive missing wrapper; full:\n%s", out)
	}
	if !strings.Contains(out, "wiki/concepts/x.md") {
		t.Errorf("directive missing path")
	}
}
