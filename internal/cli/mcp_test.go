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

func TestOpenMCPStoreDormantWhenNoProject(t *testing.T) {
	dir := t.TempDir() // no .contexo here
	store, dormant, err := openMCPStore(dir)
	if err != nil {
		t.Fatalf("openMCPStore: %v", err)
	}
	if !dormant {
		t.Errorf("expected dormant=true when .contexo is absent")
	}
	if store != nil {
		t.Errorf("expected a nil store in dormant mode")
	}
}

func TestOpenMCPStoreActiveWithProject(t *testing.T) {
	project := tmpContexoProject(t)
	store, dormant, err := openMCPStore(project)
	if err != nil {
		t.Fatalf("openMCPStore: %v", err)
	}
	if dormant {
		t.Errorf("expected active (non-dormant) mode with .contexo present")
	}
	if store == nil {
		t.Errorf("expected a real store with .contexo present")
	}
}

func TestHandleDormantInitialize(t *testing.T) {
	resp := handleDormantRequest(&MCPRequest{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("initialize should succeed, got %+v", resp)
	}
	result, _ := resp.Result.(map[string]interface{})
	if _, ok := result["serverInfo"]; !ok {
		t.Errorf("initialize result should include serverInfo")
	}
}

func TestHandleDormantToolsListEmpty(t *testing.T) {
	resp := handleDormantRequest(&MCPRequest{JSONRPC: "2.0", ID: 2, Method: "tools/list"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("tools/list should succeed, got %+v", resp)
	}
	result := resp.Result.(map[string]interface{})
	tools, ok := result["tools"].([]interface{})
	if !ok || len(tools) != 0 {
		t.Errorf("dormant tools/list should be an empty array, got %v", result["tools"])
	}
}

func TestHandleDormantNotificationNoResponse(t *testing.T) {
	resp := handleDormantRequest(&MCPRequest{JSONRPC: "2.0", ID: nil, Method: "notifications/initialized"})
	if resp != nil {
		t.Errorf("a notification (no id) should get no response, got %+v", resp)
	}
}
