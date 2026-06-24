package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/store/pagestore"
)

// openMCPStore opens the local .contexo pagestore for the MCP server. When
// no .contexo project exists at root it returns dormant=true (and a nil
// store) instead of an error: the global Codex MCP entry launches `ctx mcp`
// in every directory, so outside a Contexo project we serve a dormant,
// zero-tool server rather than crashing.
func openMCPStore(root string) (store *pagestore.Store, dormant bool, err error) {
	hubDir := config.ContexoDirPath(root)
	store, err = pagestore.Open(hubDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("mcp: open store: %w (did you run 'ctx init'?)", err)
	}
	return store, false, nil
}

// runMCPDormant runs a minimal stdio MCP server: it initializes cleanly and
// advertises zero tools/resources. Used when `ctx mcp` is launched outside a
// Contexo project (e.g. via Codex's global config in an unrelated repo).
func runMCPDormant() error {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	for scanner.Scan() {
		var req MCPRequest
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			continue
		}
		resp := handleDormantRequest(&req)
		if resp == nil {
			continue // JSON-RPC notification — no response expected
		}
		data, _ := json.Marshal(resp)
		fmt.Println(string(data))
	}
	return scanner.Err()
}

// handleDormantRequest answers MCP requests for a dormant server: it
// initializes successfully and reports zero tools/resources. Requests with
// no id are JSON-RPC notifications and get no response.
func handleDormantRequest(req *MCPRequest) *MCPResponse {
	if req.ID == nil {
		return nil
	}
	switch req.Method {
	case "initialize":
		return &MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities": map[string]interface{}{
					"resources": map[string]interface{}{"listChanged": false},
					"tools":     map[string]interface{}{"listChanged": false},
				},
				"serverInfo": map[string]interface{}{"name": "contexo", "version": "0.2.0"},
			},
		}
	case "tools/list":
		return &MCPResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]interface{}{"tools": []interface{}{}}}
	case "resources/list":
		return &MCPResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]interface{}{"resources": []interface{}{}}}
	default:
		return &MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &MCPError{Code: -32601, Message: "method not available (no .contexo project in this directory)"},
		}
	}
}
