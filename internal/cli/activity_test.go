package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/sugihAF/contexo/internal/sync"
)

func TestRenderActivity(t *testing.T) {
	var buf bytes.Buffer
	renderActivity(&buf, []sync.ActivityEvent{
		{Email: "bob@example.com", Action: "pull", CreatedAt: 1700100000},
		{Email: "alice@example.com", Action: "push", CreatedAt: 1700000000},
	})
	out := buf.String()
	if !strings.Contains(out, "bob@example.com") || !strings.Contains(out, "pulled") {
		t.Errorf("missing bob pull row: %s", out)
	}
	if !strings.Contains(out, "alice@example.com") || !strings.Contains(out, "pushed") {
		t.Errorf("missing alice push row: %s", out)
	}
}

func TestRenderActivityEmpty(t *testing.T) {
	var buf bytes.Buffer
	renderActivity(&buf, nil)
	if !strings.Contains(buf.String(), "no activity") {
		t.Errorf("expected 'no activity', got %q", buf.String())
	}
}

func TestActivityVerb(t *testing.T) {
	if activityVerb("push") != "pushed" || activityVerb("pull") != "pulled" {
		t.Errorf("verb mapping wrong: push=%q pull=%q", activityVerb("push"), activityVerb("pull"))
	}
	if activityVerb("weird") != "weird" {
		t.Errorf("unknown action should pass through, got %q", activityVerb("weird"))
	}
}
