package transport

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
)

type JSONRPCRequest struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method"`
	Params  json.RawMessage  `json:"params,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id"`
	Result  any              `json:"result,omitempty"`
	Error   any              `json:"error,omitempty"`
}

func ValidateJSONRPCID(rawID *json.RawMessage) error {
	if rawID == nil {
		return nil
	}
	s := strings.TrimSpace(string(*rawID))
	if s == "null" {
		return nil
	}
	if s == "true" || s == "false" {
		return fmt.Errorf("id cannot be a boolean")
	}
	if strings.HasPrefix(s, "\"") && strings.HasSuffix(s, "\"") {
		return nil
	}
	var n json.Number
	if err := json.Unmarshal([]byte(s), &n); err == nil {
		return nil
	}
	return fmt.Errorf("id must be a string, number, or null")
}

type ToolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type ToolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type ToolResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// RunServer runs the JSON-RPC server with standard configuration
func RunServer(in io.Reader, out io.Writer, db, dbRO *sql.DB, logger *log.Logger, flags ...bool) {
	strict := len(flags) > 0 && flags[0]
	RunServerWithConfig(in, out, db, dbRO, logger, strict)
}

// RunServerWithConfig runs the MCP JSON-RPC 2.0 server event loop
func RunServerWithConfig(in io.Reader, out io.Writer, db, dbRO *sql.DB, logger *log.Logger, isStrict bool, flags ...bool) {
	logMsg := func(format string, v ...any) {
		if logger != nil {
			logger.Printf(format, v...)
		}
	}

	reader := bufio.NewReader(in)
	writer := bufio.NewWriter(out)

	sendResponse := func(resp JSONRPCResponse) {
		if resp.ID == nil && resp.Error == nil {
			// Do not send responses to notifications
			return
		}
		b, err := json.Marshal(resp)
		if err != nil {
			logMsg("Failed to marshal response: %v", err)
			return
		}
		logMsg("OUT: %s", string(b))
		writer.Write(b)
		writer.WriteString("\n")
		writer.Flush()
	}

	for {
		line, err := reader.ReadBytes('\n')
		trimmed := strings.TrimSpace(string(line))
		if len(trimmed) > 0 {
			logMsg("IN: %s", trimmed)

			if strings.HasPrefix(trimmed, "[") {
				sendResponse(JSONRPCResponse{
					JSONRPC: "2.0",
					ID:      nil,
					Error: map[string]any{
						"code":    -32600,
						"message": "Invalid Request: batch requests are not supported",
					},
				})
				continue
			}

			var req JSONRPCRequest
			if errUnmarshal := json.Unmarshal([]byte(trimmed), &req); errUnmarshal != nil {
				logMsg("JSON unmarshal error: %v", errUnmarshal)
				sendResponse(JSONRPCResponse{
					JSONRPC: "2.0",
					ID:      nil,
					Error: map[string]any{
						"code":    -32700,
						"message": "Parse error",
					},
				})
				continue
			}

			if errID := ValidateJSONRPCID(req.ID); errID != nil {
				sendResponse(JSONRPCResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Error: map[string]any{
						"code":    -32600,
						"message": fmt.Sprintf("Invalid Request: %v", errID),
					},
				})
				continue
			}

			if req.JSONRPC != "2.0" {
				sendResponse(JSONRPCResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Error: map[string]any{
						"code":    -32600,
						"message": "Invalid Request: jsonrpc must be '2.0'",
					},
				})
				continue
			}

			switch req.Method {
			case "initialize":
				sendResponse(JSONRPCResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Result: map[string]any{
						"protocolVersion": "2024-11-05",
						"capabilities": map[string]any{
							"tools":     map[string]any{},
							"resources": map[string]any{},
							"prompts":   map[string]any{},
						},
						"serverInfo": map[string]any{
							"name":    "cypher-graph-mcp",
							"version": "0.3.3",
						},
					},
				})

			case "notifications/initialized":
				logMsg("Client initialized notification received")

			case "ping":
				sendResponse(JSONRPCResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Result:  map[string]any{},
				})

			case "resources/list":
				sendResponse(JSONRPCResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Result: map[string]any{
						"resources": []any{},
					},
				})

			case "prompts/list":
				sendResponse(JSONRPCResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Result: map[string]any{
						"prompts": []any{},
					},
				})

			case "logging/setLevel":
				sendResponse(JSONRPCResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Result:  map[string]any{},
				})

			case "tools/list":
				sendResponse(JSONRPCResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Result: map[string]any{
						"tools": ServerTools,
					},
				})

			case "tools/call":
				var params ToolCallParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					sendResponse(JSONRPCResponse{
						JSONRPC: "2.0",
						ID:      req.ID,
						Error: map[string]any{
							"code":    -32602,
							"message": "Invalid params",
						},
					})
					continue
				}

				outText, callErr := ExecuteToolCall(db, dbRO, params.Name, params.Arguments, isStrict)

				if callErr != nil {
					sendResponse(JSONRPCResponse{
						JSONRPC: "2.0",
						ID:      req.ID,
						Result: ToolResult{
							Content: []ToolContent{
								{
									Type: "text",
									Text: fmt.Sprintf("Error executing tool %s: %v", params.Name, callErr),
								},
							},
							IsError: true,
						},
					})
				} else {
					sendResponse(JSONRPCResponse{
						JSONRPC: "2.0",
						ID:      req.ID,
						Result: ToolResult{
							Content: []ToolContent{
								{
									Type: "text",
									Text: outText,
								},
							},
						},
					})
				}

			default:
				sendResponse(JSONRPCResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Error: map[string]any{
						"code":    -32601,
						"message": fmt.Sprintf("Method not found: %s", req.Method),
					},
				})
			}
		}

		if err != nil {
			if err != io.EOF {
				logMsg("Server read loop error: %v", err)
			}
			break
		}
	}
}
