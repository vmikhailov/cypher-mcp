// Package main provides a Zero-CGO Model Context Protocol (MCP) server for cypher-sql-go on SQLite.
package main

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vmikhailov/cypher-sql-go"
	_ "modernc.org/sqlite"
)

type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id"`
	Result  any    `json:"result,omitempty"`
	Error   any    `json:"error,omitempty"`
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

var serverTools = []map[string]any{
	{
		"name":        "graph_query",
		"description": "Execute an OpenCypher query against the knowledge graph and return structured results.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "OpenCypher query (e.g., MATCH (p:Person)-[:LIVES_AT]->(a:Apartment) RETURN p, a)",
				},
			},
			"required": []string{"query"},
		},
	},
	{
		"name":        "graph_set_node",
		"description": "Create or update a graph node with a unique ID, kind/label, and properties.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "Unique identifier for the node (e.g., 'person:slava', 'apt:franziska_6')",
				},
				"kind": map[string]any{
					"type":        "string",
					"description": "Node kind / label (e.g., 'Person', 'Apartment', 'BankAccount', 'Service')",
				},
				"properties": map[string]any{
					"type":        "object",
					"description": "Key-value attributes for the node",
				},
			},
			"required": []string{"id", "kind"},
		},
	},
	{
		"name":        "graph_set_edge",
		"description": "Create or update a directed relationship/edge between two nodes.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"from": map[string]any{
					"type":        "string",
					"description": "Source node ID",
				},
				"to": map[string]any{
					"type":        "string",
					"description": "Target node ID",
				},
				"kind": map[string]any{
					"type":        "string",
					"description": "Relationship type (e.g., 'LIVES_AT', 'RENTED_FROM', 'HAS_ACCOUNT', 'CALLS')",
				},
				"properties": map[string]any{
					"type":        "object",
					"description": "Optional attributes for the edge",
				},
			},
			"required": []string{"from", "to", "kind"},
		},
	},
	{
		"name":        "graph_delete_node",
		"description": "Delete a node and all its connected relationships from the graph.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "Node ID to remove",
				},
			},
			"required": []string{"id"},
		},
	},
	{
		"name":        "graph_schema",
		"description": "Inspect graph metrics: counts of nodes by kind, edges by kind, and total graph volume.",
		"inputSchema": map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	},
}

func initDatabase(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}

	pragmas := []string{
		"PRAGMA journal_mode = WAL;",
		"PRAGMA synchronous = NORMAL;",
		"PRAGMA foreign_keys = ON;",
		"PRAGMA busy_timeout = 5000;",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			return nil, fmt.Errorf("execute pragma %s: %w", p, err)
		}
	}

	schema := `
		CREATE TABLE IF NOT EXISTS nodes (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			properties TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS edges (
			from_id TEXT NOT NULL,
			to_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			properties TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_edges_from_kind ON edges(from_id, kind);
		CREATE INDEX IF NOT EXISTS idx_edges_to_kind ON edges(to_id, kind);
		CREATE INDEX IF NOT EXISTS idx_nodes_kind ON nodes(kind);
	`
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("init schema: %w", err)
	}

	return db, nil
}

func handleGraphQuery(db *sql.DB, cypherQuery string) (string, error) {
	start := time.Now()
	compiled, err := cyphersql.Compile(cypherQuery)
	if err != nil {
		return "", fmt.Errorf("cypher compile error: %w", err)
	}
	compileDuration := time.Since(start)

	qStart := time.Now()
	rows, err := db.Query(compiled.SQL)
	if err != nil {
		return "", fmt.Errorf("sql execution error (%s): %w", compiled.SQL, err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return "", fmt.Errorf("reading columns: %w", err)
	}

	results := make([]map[string]any, 0)
	for rows.Next() {
		colVals := make([]any, len(cols))
		colPointers := make([]any, len(cols))
		for i := range colVals {
			colPointers[i] = &colVals[i]
		}
		if err := rows.Scan(colPointers...); err != nil {
			return "", fmt.Errorf("row scan error: %w", err)
		}

		rowMap := make(map[string]any)
		for i, colName := range cols {
			val := colVals[i]
			if b, ok := val.([]byte); ok {
				var jsonParsed any
				if err := json.Unmarshal(b, &jsonParsed); err == nil {
					val = jsonParsed
				} else {
					val = string(b)
				}
			}
			rowMap[colName] = val
		}
		results = append(results, rowMap)
	}
	execDuration := time.Since(qStart)

	payload := map[string]any{
		"results":         results,
		"count":           len(results),
		"compiled_sql":    compiled.SQL,
		"compile_time_us": compileDuration.Microseconds(),
		"execute_time_us": execDuration.Microseconds(),
	}

	b, _ := json.MarshalIndent(payload, "", "  ")
	return string(b), nil
}

func handleSetNode(db *sql.DB, id, kind string, props map[string]any) (string, error) {
	if props == nil {
		props = make(map[string]any)
	}
	propsJSON, err := json.Marshal(props)
	if err != nil {
		return "", fmt.Errorf("marshal properties: %w", err)
	}

	query := `
		INSERT INTO nodes (id, kind, properties) VALUES (?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET kind = excluded.kind, properties = excluded.properties;
	`
	if _, err := db.Exec(query, id, kind, string(propsJSON)); err != nil {
		return "", fmt.Errorf("upsert node %s: %w", id, err)
	}

	return fmt.Sprintf("Node '%s' of kind '%s' upserted successfully.", id, kind), nil
}

func handleSetEdge(db *sql.DB, from, to, kind string, props map[string]any) (string, error) {
	if props == nil {
		props = make(map[string]any)
	}
	propsJSON, err := json.Marshal(props)
	if err != nil {
		return "", fmt.Errorf("marshal properties: %w", err)
	}

	delQuery := `DELETE FROM edges WHERE from_id = ? AND to_id = ? AND kind = ?;`
	if _, err := db.Exec(delQuery, from, to, kind); err != nil {
		return "", fmt.Errorf("delete existing edge: %w", err)
	}

	insQuery := `INSERT INTO edges (from_id, to_id, kind, properties) VALUES (?, ?, ?, ?);`
	if _, err := db.Exec(insQuery, from, to, kind, string(propsJSON)); err != nil {
		return "", fmt.Errorf("insert edge (%s)-[%s]->(%s): %w", from, kind, to, err)
	}

	return fmt.Sprintf("Edge (%s)-[:%s]->(%s) saved successfully.", from, kind, to), nil
}

func handleDeleteNode(db *sql.DB, id string) (string, error) {
	tx, err := db.Begin()
	if err != nil {
		return "", fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM edges WHERE from_id = ? OR to_id = ?", id, id); err != nil {
		return "", fmt.Errorf("delete connected edges: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM nodes WHERE id = ?", id); err != nil {
		return "", fmt.Errorf("delete node %s: %w", id, err)
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit delete: %w", err)
	}

	return fmt.Sprintf("Node '%s' and connected edges deleted.", id), nil
}

func handleSchema(db *sql.DB) (string, error) {
	type KindCount struct {
		Kind  string `json:"kind"`
		Count int    `json:"count"`
	}

	nodeRows, err := db.Query("SELECT kind, count(*) FROM nodes GROUP BY kind ORDER BY count(*) DESC")
	if err != nil {
		return "", fmt.Errorf("query node kinds: %w", err)
	}

	var nodeCounts []KindCount
	totalNodes := 0
	for nodeRows.Next() {
		var kc KindCount
		if err := nodeRows.Scan(&kc.Kind, &kc.Count); err == nil {
			nodeCounts = append(nodeCounts, kc)
			totalNodes += kc.Count
		}
	}
	nodeRows.Close()

	edgeRows, err := db.Query("SELECT kind, count(*) FROM edges GROUP BY kind ORDER BY count(*) DESC")
	if err != nil {
		return "", fmt.Errorf("query edge kinds: %w", err)
	}

	var edgeCounts []KindCount
	totalEdges := 0
	for edgeRows.Next() {
		var kc KindCount
		if err := edgeRows.Scan(&kc.Kind, &kc.Count); err == nil {
			edgeCounts = append(edgeCounts, kc)
			totalEdges += kc.Count
		}
	}
	edgeRows.Close()

	summary := map[string]any{
		"total_nodes": totalNodes,
		"total_edges": totalEdges,
		"node_kinds":  nodeCounts,
		"edge_kinds":  edgeCounts,
	}

	b, _ := json.MarshalIndent(summary, "", "  ")
	return string(b), nil
}

func main() {
	dbPath := flag.String("db", "knowledge_graph.db", "Path to SQLite database file")
	logPath := flag.String("log", "", "Path to debug log file")
	flag.Parse()

	var logger *log.Logger
	if *logPath != "" {
		_ = os.MkdirAll(filepath.Dir(*logPath), 0755)
		if f, err := os.OpenFile(*logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644); err == nil {
			defer f.Close()
			logger = log.New(f, "[mcp] ", log.LstdFlags|log.Lmicroseconds)
		}
	}

	logMsg := func(format string, v ...any) {
		if logger != nil {
			logger.Printf(format, v...)
		}
	}

	logMsg("Starting cypher-mcp with db=%s", *dbPath)

	db, err := initDatabase(*dbPath)
	if err != nil {
		logMsg("Database init failed: %v", err)
		log.Fatalf("database init failed: %v", err)
	}
	defer db.Close()

	reader := bufio.NewReader(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)

	sendResponse := func(resp JSONRPCResponse) {
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
		if err != nil {
			if err == io.EOF {
				logMsg("Stdin EOF received, shutting down")
				break
			}
			logMsg("Error reading stdin: %v", err)
			continue
		}

		trimmed := strings.TrimSpace(string(line))
		if len(trimmed) == 0 {
			continue
		}

		logMsg("IN: %s", trimmed)

		var req JSONRPCRequest
		if err := json.Unmarshal([]byte(trimmed), &req); err != nil {
			logMsg("JSON unmarshal error: %v", err)
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
						"version": "1.0.0",
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
					"tools": serverTools,
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

			var outText string
			var callErr error

			switch params.Name {
			case "graph_query":
				query, _ := params.Arguments["query"].(string)
				outText, callErr = handleGraphQuery(db, query)

			case "graph_set_node":
				id, _ := params.Arguments["id"].(string)
				kind, _ := params.Arguments["kind"].(string)
				props, _ := params.Arguments["properties"].(map[string]any)
				outText, callErr = handleSetNode(db, id, kind, props)

			case "graph_set_edge":
				from, _ := params.Arguments["from"].(string)
				to, _ := params.Arguments["to"].(string)
				kind, _ := params.Arguments["kind"].(string)
				props, _ := params.Arguments["properties"].(map[string]any)
				outText, callErr = handleSetEdge(db, from, to, kind, props)

			case "graph_delete_node":
				id, _ := params.Arguments["id"].(string)
				outText, callErr = handleDeleteNode(db, id)

			case "graph_schema":
				outText, callErr = handleSchema(db)

			default:
				callErr = fmt.Errorf("unknown tool: %s", params.Name)
			}

			if callErr != nil {
				sendResponse(JSONRPCResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Result: ToolResult{
						IsError: true,
						Content: []ToolContent{
							{Type: "text", Text: callErr.Error()},
						},
					},
				})
			} else {
				sendResponse(JSONRPCResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Result: ToolResult{
						Content: []ToolContent{
							{Type: "text", Text: outText},
						},
					},
				})
			}

		default:
			logMsg("Unhandled method: %s", req.Method)
			if req.ID != nil {
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
	}
}
