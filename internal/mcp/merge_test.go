package mcp

import (
	"strings"
	"testing"
)

const (
	mergeTestPath      = "wiki/concepts/x.md"
	mergeTestServerSHA = "def567890abcdef0123456789abcdef012345678"
)

func TestBuildMergeBrief_ThreeWayLayout(t *testing.T) {
	ancestor := []byte("---\nslug: x\ntype: concept\n---\n## Decision\nWe chose Stripe Billing.\n")
	yours := []byte("---\nslug: x\ntype: concept\n---\n## Decision\nWe chose Stripe Billing with tax handling.\n")
	theirs := []byte("---\nslug: x\ntype: concept\n---\n## Decision\nWe chose Stripe Billing with platform-fee handling.\n\n## Refund handling\nIssue refunds via Stripe.\n")

	out := buildMergeBrief("push", []mergeItem{{
		Path:        mergeTestPath,
		AncestorSHA: "abc123456abcdef",
		Ancestor:    ancestor,
		Yours:       yours,
		ServerSHA:   mergeTestServerSHA,
		Server:      theirs,
	}})

	for _, want := range []string{
		"<MERGE_REQUIRED origin=push>",
		mergeTestPath,
		"ancestor (both diverged from):  abc1234",
		"server now:                     " + mergeTestServerSHA,
		"YOUR changes vs ancestor:",
		"SERVER changes vs ancestor:",
		"CONFLICTING regions (both sides changed):",
		"## Decision",          // listed in the conflicting set
		"+ ## Refund handling", // server-only addition
		"--- ANCESTOR VERSION ---",
		"We chose Stripe Billing.\n",
		"--- YOUR VERSION",
		"with tax handling",
		"--- SERVER VERSION ---",
		"with platform-fee handling",
		"</MERGE_REQUIRED>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("merge brief missing %q. Full:\n%s", want, out)
		}
	}
}

func TestBuildMergeBrief_NoOverlapNotConflicting(t *testing.T) {
	// Both sides edit different sections — should NOT be flagged as conflicting.
	ancestor := []byte("---\nslug: x\ntype: concept\n---\n## A\nA-original\n\n## B\nB-original\n")
	yours := []byte("---\nslug: x\ntype: concept\n---\n## A\nA-mine\n\n## B\nB-original\n")
	theirs := []byte("---\nslug: x\ntype: concept\n---\n## A\nA-original\n\n## B\nB-theirs\n")

	out := buildMergeBrief("push", []mergeItem{{
		Path: mergeTestPath, AncestorSHA: "abc123456", Ancestor: ancestor,
		Yours: yours, ServerSHA: mergeTestServerSHA, Server: theirs,
	}})
	if strings.Contains(out, "CONFLICTING regions") {
		t.Errorf("A is yours-only and B is theirs-only; nothing conflicts:\n%s", out)
	}
	if !strings.Contains(out, "auto-mergeable in principle") {
		t.Errorf("expected the 'auto-mergeable' note when no section overlaps:\n%s", out)
	}
}

func TestBuildMergeBrief_MissingAncestor_Degrades(t *testing.T) {
	// No base snapshot (a both_added page, or a base that could not be
	// read): the brief still renders and says so.
	yours := []byte("---\nslug: x\ntype: concept\n---\nbody\n")
	theirs := []byte("---\nslug: x\ntype: concept\n---\nserver body\n")
	out := buildMergeBrief("push", []mergeItem{{
		Path: mergeTestPath, Yours: yours, ServerSHA: mergeTestServerSHA, Server: theirs,
	}})
	for _, want := range []string{
		"<MERGE_REQUIRED origin=push>",
		mergeTestPath,
		"ancestor (both diverged from):  (none)",
		"--- ANCESTOR VERSION ---",
		"(not available",
		"server body",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("degraded brief missing %q. Full:\n%s", want, out)
		}
	}
}

// TestBuildMergeBrief_StepsUseResolved pins the agent steps: the state is
// untouched, the merged page is pushed with resolved=[...], and nothing
// tells the agent to pull or claims the sync state moved.
func TestBuildMergeBrief_StepsUseResolved(t *testing.T) {
	items := []mergeItem{
		{Path: mergeTestPath, Yours: []byte("a\n"), ServerSHA: mergeTestServerSHA, Server: []byte("b\n")},
		{Path: "wiki/entities/y.md", Yours: []byte("c\n"), ServerSHA: mergeTestServerSHA, Server: []byte("d\n")},
	}
	out := buildMergeBrief("push", items)
	for _, want := range []string{
		"Local sync state was NOT changed; the page stays a conflict until you resolve it.",
		"STEPS:",
		"ctx_write_page",
		"SERVER VERSION verbatim",
		`resolved=["wiki/concepts/x.md","wiki/entities/y.md"]`,
		"─── FILE 1/2: wiki/concepts/x.md ───",
		"─── FILE 2/2: wiki/entities/y.md ───",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("brief missing %q. Full:\n%s", want, out)
		}
	}
	for _, banned := range []string{"ctx_pull", "state has been updated", "auto-updated", "will not 409"} {
		if strings.Contains(out, banned) {
			t.Errorf("brief must not contain %q. Full:\n%s", banned, out)
		}
	}
}

func TestBuildMergeBrief_OriginPull(t *testing.T) {
	out := buildMergeBrief("pull", []mergeItem{{
		Path: mergeTestPath, Yours: []byte("a\n"), ServerSHA: mergeTestServerSHA, Server: []byte("b\n"),
	}})
	if !strings.HasPrefix(out, "<MERGE_REQUIRED origin=pull>\n") {
		t.Errorf("pull brief must open with its origin. Full:\n%s", out)
	}
	if !strings.HasSuffix(out, "</MERGE_REQUIRED>") {
		t.Errorf("brief must close the block. Full:\n%s", out)
	}
	if !strings.Contains(out, `resolved=["wiki/concepts/x.md"]`) {
		t.Errorf("pull brief must also end in a resolved push. Full:\n%s", out)
	}
}
