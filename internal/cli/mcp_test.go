package cli

import (
	"encoding/json"
	"testing"
)

func TestParseClientName(t *testing.T) {
	params := json.RawMessage(`{"protocolVersion":"2024-11-05","clientInfo":{"name":"claude-code","version":"1.0"}}`)
	if got := parseClientName(params); got != "claude-code" {
		t.Errorf("got %q, want claude-code", got)
	}
}

func TestParseClientNameMissing(t *testing.T) {
	if got := parseClientName(json.RawMessage(`{"protocolVersion":"x"}`)); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
	if got := parseClientName(nil); got != "" {
		t.Errorf("expected empty for nil params, got %q", got)
	}
}
