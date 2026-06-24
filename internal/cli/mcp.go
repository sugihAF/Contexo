package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	mcpserver "github.com/sugihAF/contexo/internal/mcp"
)

func newMCPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run the MCP server over stdio against the local .contexo/",
		Long: "With no subcommand, runs the MCP server over stdio (this is what an " +
			"agent's MCP config invokes). Outside a Contexo project it serves a " +
			"dormant, zero-tool server instead of failing.\n\n" +
			"Subcommands wire that server into an agent's config: see `ctx mcp install`.",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, dormant, err := openMCPStore(GetRootDir())
			if err != nil {
				return err
			}
			if dormant {
				return runMCPDormant()
			}
			srv := mcpserver.NewServer(store)
			return runMCPStdio(srv)
		},
	}
	cmd.AddCommand(newMCPInstallCmd())
	cmd.AddCommand(newMCPUninstallCmd())
	cmd.AddCommand(newMCPStatusCmd())
	cmd.AddCommand(newMCPGuideCmd())
	return cmd
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

// parseClientName extracts clientInfo.name from an MCP initialize request's
// params, or "" if absent. Used to attribute pulls to the calling agent.
func parseClientName(params json.RawMessage) string {
	if len(params) == 0 {
		return ""
	}
	var p struct {
		ClientInfo struct {
			Name string `json:"name"`
		} `json:"clientInfo"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return ""
	}
	return p.ClientInfo.Name
}

func handleMCPRequest(srv *mcpserver.Server, req *MCPRequest) *MCPResponse {
	switch req.Method {
	case "initialize":
		srv.SetClientName(parseClientName(req.Params))
		return &MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities": map[string]interface{}{
					"resources": map[string]interface{}{"listChanged": false},
					"tools":     map[string]interface{}{"listChanged": false},
				},
				"serverInfo": map[string]interface{}{
					"name":    "contexo",
					"version": "0.2.0",
				},
			},
		}

	case "tools/list":
		tools := srv.ListTools()
		return &MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]interface{}{"tools": tools},
		}

	case "tools/call":
		var params struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return &MCPResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &MCPError{Code: -32602, Message: "invalid params"},
			}
		}
		result := srv.HandleToolCall(context.Background(), params.Name, params.Arguments)
		return &MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  result,
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
