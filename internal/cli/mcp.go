package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/config"
	mcpserver "github.com/sugihAF/contexo/internal/mcp"
	"github.com/sugihAF/contexo/internal/store/pagestore"
)

func newMCPCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run MCP server over stdio against the local .ctxhub/",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()
			hubDir := config.CtxhubDirPath(root)
			store, err := pagestore.Open(hubDir)
			if err != nil {
				return fmt.Errorf("mcp: open store: %w (did you run 'ctx init'?)", err)
			}
			srv := mcpserver.NewHubServer(store)
			return runMCPStdio(srv)
		},
	}
}

// MCPRequest is a JSON-RPC 2.0 request.
type MCPRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// MCPResponse is a JSON-RPC 2.0 response.
type MCPResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *MCPError   `json:"error,omitempty"`
}

// MCPError is a JSON-RPC 2.0 error object.
type MCPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func runMCPStdio(srv *mcpserver.HubServer) error {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		var req MCPRequest
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			continue
		}
		resp := handleMCPRequest(srv, &req)
		data, _ := json.Marshal(resp)
		fmt.Println(string(data))
	}
	return scanner.Err()
}

func handleMCPRequest(srv *mcpserver.HubServer, req *MCPRequest) *MCPResponse {
	switch req.Method {
	case "initialize":
		return &MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities": map[string]interface{}{
					"resources": map[string]interface{}{
						"listChanged": false,
					},
				},
				"serverInfo": map[string]interface{}{
					"name":    "ctxhub",
					"version": "0.2.0",
				},
			},
		}

	case "resources/list":
		templates := srv.ListResources()
		resources := make([]interface{}, 0, len(templates))
		for _, t := range templates {
			resources = append(resources, map[string]interface{}{
				"uri":         t.URITemplate,
				"name":        t.Name,
				"description": t.Description,
				"mimeType":    t.MimeType,
				"annotations": t.Annotations,
			})
		}
		return &MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]interface{}{"resources": resources},
		}

	case "resources/read":
		var params struct {
			URI string `json:"uri"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return &MCPResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &MCPError{Code: -32602, Message: "invalid params"},
			}
		}
		data, mimeType, err := srv.HandleResourceRead(context.Background(), params.URI)
		if err != nil {
			return &MCPResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &MCPError{Code: -32603, Message: err.Error()},
			}
		}
		return &MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"contents": []map[string]interface{}{
					{
						"uri":      params.URI,
						"mimeType": mimeType,
						"text":     string(data),
					},
				},
			},
		}

	default:
		return &MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &MCPError{Code: -32601, Message: "method not found"},
		}
	}
}
