package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/config"
	mcpserver "github.com/sugihAF/contexo/internal/mcp"
	sqlitestore "github.com/sugihAF/contexo/internal/store/sqlite"
)

func newMCPCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run MCP server over stdio",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()
			ctxDir := config.CtxDirPath(root)

			db, err := sqlitestore.Open(filepath.Join(ctxDir, "index.sqlite"))
			if err != nil {
				return err
			}
			defer db.Close()

			sessionsDir := filepath.Join(ctxDir, "sessions")
			srv := mcpserver.NewServer(db, db, sessionsDir)

			return runMCPStdio(srv)
		},
	}
}

// MCPRequest represents a JSON-RPC request.
type MCPRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// MCPResponse represents a JSON-RPC response.
type MCPResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *MCPError   `json:"error,omitempty"`
}

// MCPError represents a JSON-RPC error.
type MCPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func runMCPStdio(srv *mcpserver.Server) error {
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

func handleMCPRequest(srv *mcpserver.Server, req *MCPRequest) *MCPResponse {
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
					"name":    "ctx",
					"version": "0.1.0",
				},
			},
		}

	case "resources/list":
		templates := srv.ListResources()
		var resources []interface{}
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
			Result: map[string]interface{}{
				"resources": resources,
			},
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
