package mcp

// WP-4.1 regression tests for the agent path (GFR-2..GFR-6): the MCP
// re-brief after an agent restart and the MERGE_REQUIRED / ctx_push wording.
// The integration cases reuse the WP-2.1 fixtures of tools_sync_test.go.

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/sugihAF/contexo/internal/testkit"
)

// wp41SHA40 matches a full 40-hex sha.
var wp41SHA40 = regexp.MustCompile(`^[0-9a-f]{40}$`)

// wp41Conflict is a conflict the agent's listing already shows: the
// teammate's push landed before the agent's ctx_push (no 409).
type wp41Conflict struct {
	fx                   *mcpSyncFix
	agent                *testkit.Project
	rel                  string
	ancestorSHA, mateSHA string
}

func wp41ListingConflict(t *testing.T) *wp41Conflict {
	t.Helper()
	fx := mcpSyncServer(t)
	c := &wp41Conflict{fx: fx}
	c.agent = mcpSyncProject(t, fx)
	c.rel = c.agent.WritePage(mcpSyncEntity("wp41-shared", mcpSyncAncestorMark))
	c.ancestorSHA = mcpSyncPushedSHA(t, mcpSyncEnginePush(t, mcpSyncDirectEnv(fx, c.agent, "agent")), c.rel)
	mate := mcpSyncProject(t, fx)
	mateEnv := mcpSyncTeammateEnv(fx, mate)
	mcpSyncEnginePull(t, mateEnv)
	mate.WritePage(mcpSyncEntity("wp41-shared", mcpSyncMateMark))
	c.mateSHA = mcpSyncPushedSHA(t, mcpSyncEnginePush(t, mateEnv), c.rel)
	c.agent.WritePage(mcpSyncEntity("wp41-shared", mcpSyncAgentMark))
	t.Cleanup(c.forget)
	return c
}

// forget drops the registry entries an agent restart loses.
func (c *wp41Conflict) forget() {
	briefForget(c.agent.Root, c.rel)
	briefForget(c.agent.HubDir, c.rel)
}

// merge writes the agent's merged page and returns its bytes.
func (c *wp41Conflict) merge() []byte {
	merged := testkit.PageBytes(mcpSyncEntity("wp41-shared",
		mcpSyncAgentMark+"\n\n"+mcpSyncMateMark+"\n\nmerged by the agent"))
	c.agent.WriteRaw(c.rel, merged)
	return merged
}

// firstBrief runs ctx_push on s and asserts the MERGE_REQUIRED for rel.
func (c *wp41Conflict) firstBrief(t *testing.T, s *Server) string {
	t.Helper()
	res := mcpSyncCall(t, s, "ctx_push", nil)
	text := mcpSyncText(res)
	if res.IsError {
		t.Fatalf("ctx_push on a listing conflict is an error:\n%s", text)
	}
	mcpSyncWantBrief(t, "first ctx_push", text, "push", c.rel, c.mateSHA, mcpSyncAgentMark, mcpSyncMateMark)
	return text
}

func TestWP41_GFR2_ResolvedAfterRestartPushes(t *testing.T) {
	c := wp41ListingConflict(t)
	c.firstBrief(t, mcpSyncMCP(t, c.agent))

	// The agent restarts: a new ctx mcp process has an empty registry.
	c.forget()
	merged := c.merge()
	c.fx.px.Reset()
	res := mcpSyncCall(t, mcpSyncMCP(t, c.agent), "ctx_push", mcpSyncResolved(c.rel))
	text := mcpSyncText(res)
	if res.IsError || strings.Contains(text, "<MERGE_REQUIRED") {
		t.Fatalf("the first resolved push after a restart re-briefed (isError %v), want it pushed:\n%s", res.IsError, text)
	}
	if !strings.Contains(text, "Resolved "+c.rel) {
		t.Errorf("no 'Resolved %s' note:\n%s", c.rel, text)
	}
	body, _ := mcpSyncServerPage(t, c.fx, c.rel)
	if want := mcpSyncNorm(t, merged); !bytes.Equal(body, want) {
		t.Errorf("server %s = %q, want the merged page %q", c.rel, body, want)
	}
}
