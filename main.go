// Package main provides a Zero-CGO Model Context Protocol (MCP) server for cypher-sql-go on SQLite.
package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	cyphersql "github.com/vmikhailov/cypher-sql-go"
	_ "modernc.org/sqlite"
)

var (
	safeIdentifierRegex = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
	backtickRegex       = regexp.MustCompile("`([^`]+)`")
	jsonExtractRegex    = regexp.MustCompile(`^json_extract\(([a-zA-Z0-9_]+)\.[^,]+,\s*'\$\.([^']+)'\)`)
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
		"description": "Execute an OpenCypher query against the knowledge graph and return structured results. Supports variable-length paths like -[*1..3]-> or <-[*1..5]-. Unbounded [*] and [*1..] default to a 10-hop maximum. Always anchor at least one endpoint (with labels or properties) or use LIMIT to avoid full-graph scans (queries time out after 15s).",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "OpenCypher query (e.g., MATCH (p:Person)-[:LIVES_AT]->(a:Apartment) RETURN p, a)",
				},
				"params": map[string]any{
					"type":        "object",
					"description": "Optional parameters to pass to the query (e.g., {\"name\": \"Alice\"})",
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

	tables := `
		CREATE TABLE IF NOT EXISTS nodes (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			properties TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS edges (
			from_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
			to_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
			kind TEXT NOT NULL,
			properties TEXT NOT NULL
		);
	`
	if _, err := db.Exec(tables); err != nil {
		return nil, fmt.Errorf("init tables: %w", err)
	}

	dedupEdges := `
		DELETE FROM edges
		WHERE rowid NOT IN (
			SELECT min(rowid)
			FROM edges
			GROUP BY from_id, to_id, kind
		);
	`
	if _, err := db.Exec(dedupEdges); err != nil {
		return nil, fmt.Errorf("deduplicate edges: %w", err)
	}

	indices := `
		CREATE UNIQUE INDEX IF NOT EXISTS idx_edges_unique ON edges(from_id, to_id, kind);
		CREATE INDEX IF NOT EXISTS idx_edges_from_kind ON edges(from_id, kind);
		CREATE INDEX IF NOT EXISTS idx_edges_to_kind ON edges(to_id, kind);
		CREATE INDEX IF NOT EXISTS idx_nodes_kind ON nodes(kind);
	`
	if _, err := db.Exec(indices); err != nil {
		return nil, fmt.Errorf("init indices: %w", err)
	}

	return db, nil
}

func initRODatabase(dbPath string) (*sql.DB, error) {
	sep := "?"
	if strings.Contains(dbPath, "?") {
		sep = "&"
	}
	dsn := fmt.Sprintf("%s%s_query_only=1&_busy_timeout=5000&_defensive=1", dbPath, sep)
	dbRO, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite ro db: %w", err)
	}

	if _, err := dbRO.Exec("PRAGMA query_only = ON;"); err != nil {
		return nil, fmt.Errorf("set ro pragma query_only: %w", err)
	}
	return dbRO, nil
}

func hasMultipleStatements(sql string) bool {
	inSingleQuote := false
	inDoubleQuote := false
	n := len(sql)
	for i := 0; i < n; i++ {
		ch := sql[i]
		if inSingleQuote {
			if ch == '\'' {
				if i+1 < n && sql[i+1] == '\'' {
					i++ // escaped quote ''
				} else {
					inSingleQuote = false
				}
			}
			continue
		}
		if inDoubleQuote {
			if ch == '"' {
				if i+1 < n && sql[i+1] == '"' {
					i++ // escaped quote ""
				} else {
					inDoubleQuote = false
				}
			}
			continue
		}

		if ch == '\'' {
			inSingleQuote = true
			continue
		}
		if ch == '"' {
			inDoubleQuote = true
			continue
		}
		if ch == '-' && i+1 < n && sql[i+1] == '-' {
			i += 2
			for i < n && sql[i] != '\n' {
				i++
			}
			continue
		}
		if ch == '/' && i+1 < n && sql[i+1] == '*' {
			i += 2
			for i+1 < n && !(sql[i] == '*' && sql[i+1] == '/') {
				i++
			}
			i++
			continue
		}

		if ch == ';' {
			rem := sql[i+1:]
			rem = stripCommentsAndSpace(rem)
			if len(rem) > 0 {
				return true
			}
		}
	}
	return false
}

func stripCommentsAndSpace(s string) string {
	n := len(s)
	i := 0
	var sb strings.Builder
	for i < n {
		if s[i] == ' ' || s[i] == '	' || s[i] == '\r' || s[i] == '\n' || s[i] == ';' {
			i++
			continue
		}
		if s[i] == '-' && i+1 < n && s[i+1] == '-' {
			i += 2
			for i < n && s[i] != '\n' {
				i++
			}
			continue
		}
		if s[i] == '/' && i+1 < n && s[i+1] == '*' {
			i += 2
			for i+1 < n && !(s[i] == '*' && s[i+1] == '/') {
				i++
			}
			i += 2
			continue
		}
		sb.WriteByte(s[i])
		i++
	}
	return sb.String()
}

func handleGraphQuery(db *sql.DB, cypherQuery string, queryParams ...map[string]any) (string, error) {
	start := time.Now()

	// Defense-in-depth: check unsafe identifiers
	for _, m := range backtickRegex.FindAllStringSubmatch(cypherQuery, -1) {
		if !safeIdentifierRegex.MatchString(m[1]) {
			return "", fmt.Errorf("invalid identifier %q: only alphanumeric characters and underscores are permitted", m[1])
		}
	}
	var params map[string]any
	if len(queryParams) > 0 && queryParams[0] != nil {
		params = queryParams[0]
	}

	compiled, err := cyphersql.CompileWithParams(cypherQuery, params)
	if err != nil {
		return "", fmt.Errorf("cypher compile error: %w", err)
	}
	compileDuration := time.Since(start)

	if hasMultipleStatements(compiled.SQL) {
		return "", fmt.Errorf("multiple SQL statements are not permitted")
	}

	var sqlArgs []any
	for k, v := range compiled.Params {
		sqlArgs = append(sqlArgs, sql.Named(k, v))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	qStart := time.Now()
	rows, err := db.QueryContext(ctx, compiled.SQL, sqlArgs...)
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
			cleanCol := colName
			if strings.HasPrefix(colName, "json_extract(") {
				if m := jsonExtractRegex.FindStringSubmatch(colName); len(m) == 3 {
					cleanCol = m[1] + "." + m[2]
				}
			}
			val := colVals[i]
			switch v := val.(type) {
			case []byte:
				var jsonParsed any
				if err := json.Unmarshal(v, &jsonParsed); err == nil {
					val = jsonParsed
				} else {
					val = string(v)
				}
			case string:
				trimmed := strings.TrimSpace(v)
				if (strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")) ||
					(strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")) {
					var jsonParsed any
					if err := json.Unmarshal([]byte(trimmed), &jsonParsed); err == nil {
						val = jsonParsed
					}
				}
			}
			rowMap[cleanCol] = val
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
	if strings.TrimSpace(id) == "" {
		return "", fmt.Errorf("node id cannot be empty")
	}
	if strings.TrimSpace(kind) == "" {
		return "", fmt.Errorf("node kind cannot be empty")
	}
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
	if strings.TrimSpace(from) == "" {
		return "", fmt.Errorf("edge 'from' cannot be empty")
	}
	if strings.TrimSpace(to) == "" {
		return "", fmt.Errorf("edge 'to' cannot be empty")
	}
	if strings.TrimSpace(kind) == "" {
		return "", fmt.Errorf("edge 'kind' cannot be empty")
	}
	if props == nil {
		props = make(map[string]any)
	}
	propsJSON, err := json.Marshal(props)
	if err != nil {
		return "", fmt.Errorf("marshal properties: %w", err)
	}

	tx, err := db.Begin()
	if err != nil {
		return "", fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Verify both source and target nodes exist (prevent dangling edges)
	var fromExists, toExists bool
	if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM nodes WHERE id = ?)", from).Scan(&fromExists); err != nil {
		return "", fmt.Errorf("check from node: %w", err)
	}
	if !fromExists {
		return "", fmt.Errorf("source node '%s' does not exist", from)
	}

	if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM nodes WHERE id = ?)", to).Scan(&toExists); err != nil {
		return "", fmt.Errorf("check to node: %w", err)
	}
	if !toExists {
		return "", fmt.Errorf("target node '%s' does not exist", to)
	}

	upsertQuery := `
		INSERT INTO edges (from_id, to_id, kind, properties) VALUES (?, ?, ?, ?)
		ON CONFLICT(from_id, to_id, kind) DO UPDATE SET properties = excluded.properties;
	`
	if _, err := tx.Exec(upsertQuery, from, to, kind, string(propsJSON)); err != nil {
		return "", fmt.Errorf("upsert edge (%s)-[%s]->(%s): %w", from, kind, to, err)
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit edge upsert: %w", err)
	}

	return fmt.Sprintf("Edge (%s)-[:%s]->(%s) saved successfully.", from, kind, to), nil
}

func handleDeleteNode(db *sql.DB, id string) (string, error) {
	if strings.TrimSpace(id) == "" {
		return "", fmt.Errorf("node id cannot be empty")
	}
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

	dbRO, err := initRODatabase(*dbPath)
	if err != nil {
		logMsg("RO Database init failed: %v", err)
		log.Fatalf("ro database init failed: %v", err)
	}
	defer dbRO.Close()

	runServer(os.Stdin, os.Stdout, db, dbRO, logger)
}

func runServer(in io.Reader, out io.Writer, db, dbRO *sql.DB, logger *log.Logger) {
	logMsg := func(format string, v ...any) {
		if logger != nil {
			logger.Printf(format, v...)
		}
	}

	reader := bufio.NewReader(in)
	writer := bufio.NewWriter(out)

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
		trimmed := strings.TrimSpace(string(line))
		if len(trimmed) > 0 {
			logMsg("IN: %s", trimmed)

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
			} else {
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
						queryParams, _ := params.Arguments["params"].(map[string]any)
						outText, callErr = handleGraphQuery(dbRO, query, queryParams)

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
						outText, callErr = handleSchema(dbRO)

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
		if err != nil {
			if err == io.EOF {
				logMsg("Stdin EOF received, shutting down")
				break
			}
			logMsg("Error reading stdin: %v", err)
			continue
		}
	}
}
