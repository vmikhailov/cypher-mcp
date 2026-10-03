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
	"unicode"

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

type BatchNodeItem struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind"`
	Properties map[string]any `json:"properties"`
}

type BatchEdgeItem struct {
	From       string         `json:"from"`
	To         string         `json:"to"`
	Kind       string         `json:"kind"`
	Properties map[string]any `json:"properties"`
}

type dbOrTx interface {
	QueryRow(query string, args ...any) *sql.Row
	Query(query string, args ...any) (*sql.Rows, error)
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
		"name":        "graph_search",
		"description": "Full-text keyword search across nodes using SQLite FTS5. Searches node IDs and JSON property values (names, addresses, license plates, notes, etc.). Use this to find entry-point nodes before path traversals.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Keywords to search for (e.g. 'Franziska', 'München', 'BMW M-EW 330', 'KIT Timur')",
				},
				"kind": map[string]any{
					"type":        "string",
					"description": "Optional node kind/label filter (e.g. 'Person', 'Apartment', 'Vehicle')",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of results to return (default: 10, max: 50)",
				},
			},
			"required": []string{"query"},
		},
	},
	{
		"name":        "graph_batch_upsert",
		"description": "Atomically upsert multiple nodes and/or edges in a single ACID transaction. If any node or edge fails validation, the entire batch is rolled back.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"nodes": map[string]any{
					"type":        "array",
					"description": "List of nodes to upsert",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"id": map[string]any{
								"type":        "string",
								"description": "Unique identifier (e.g. 'person:slava')",
							},
							"kind": map[string]any{
								"type":        "string",
								"description": "Node kind / label (e.g. 'Person')",
							},
							"properties": map[string]any{
								"type":        "object",
								"description": "Key-value attributes for the node",
							},
						},
						"required": []string{"id", "kind"},
					},
				},
				"edges": map[string]any{
					"type":        "array",
					"description": "List of edges to upsert",
					"items": map[string]any{
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
								"description": "Relationship type (e.g. 'LIVES_AT')",
							},
							"properties": map[string]any{
								"type":        "object",
								"description": "Optional attributes for the edge",
							},
						},
						"required": []string{"from", "to", "kind"},
					},
				},
			},
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
		"description": "Inspect graph metrics and active taxonomy schema: node and edge counts by kind, total volume, and enforced schema rules.",
		"inputSchema": map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	},
	{
		"name":        "graph_schema_define",
		"description": "Governance tool for defining or modifying allowed node kinds and relationship rules in the knowledge graph schema.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"add_kind", "remove_kind", "add_relation", "remove_relation"},
					"description": "Schema action to perform",
				},
				"kind": map[string]any{
					"type":        "string",
					"description": "Node kind name (for add_kind / remove_kind)",
				},
				"relation": map[string]any{
					"type":        "string",
					"description": "Relationship type (for add_relation / remove_relation)",
				},
				"from_kind": map[string]any{
					"type":        "string",
					"description": "Source node kind (for add_relation / remove_relation)",
				},
				"to_kind": map[string]any{
					"type":        "string",
					"description": "Target node kind (for add_relation / remove_relation)",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "Human-readable description / rationale for this schema rule",
				},
			},
			"required": []string{"action"},
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

	// 1. Full-Text Search (FTS5) table and triggers
	ftsDDL := `
		CREATE VIRTUAL TABLE IF NOT EXISTS nodes_fts USING fts5(id, kind, content);

		CREATE TRIGGER IF NOT EXISTS nodes_ai AFTER INSERT ON nodes BEGIN
			INSERT INTO nodes_fts(rowid, id, kind, content) VALUES (new.rowid, new.id, new.kind, new.properties);
		END;
		CREATE TRIGGER IF NOT EXISTS nodes_ad AFTER DELETE ON nodes BEGIN
			DELETE FROM nodes_fts WHERE rowid = old.rowid;
		END;
		CREATE TRIGGER IF NOT EXISTS nodes_au AFTER UPDATE ON nodes BEGIN
			DELETE FROM nodes_fts WHERE rowid = old.rowid;
			INSERT INTO nodes_fts(rowid, id, kind, content) VALUES (new.rowid, new.id, new.kind, new.properties);
		END;
	`
	if _, err := db.Exec(ftsDDL); err != nil {
		return nil, fmt.Errorf("init fts5: %w", err)
	}

	// Backfill FTS index for any existing nodes
	backfillFTS := `
		INSERT INTO nodes_fts(rowid, id, kind, content)
		SELECT rowid, id, kind, properties FROM nodes
		WHERE rowid NOT IN (SELECT rowid FROM nodes_fts);
	`
	if _, err := db.Exec(backfillFTS); err != nil {
		return nil, fmt.Errorf("backfill fts5: %w", err)
	}

	// 2. Schema governance tables
	schemaDDL := `
		CREATE TABLE IF NOT EXISTS schema_kinds (
			kind TEXT PRIMARY KEY,
			description TEXT
		);
		CREATE TABLE IF NOT EXISTS schema_relations (
			rel_type TEXT NOT NULL,
			from_kind TEXT NOT NULL,
			to_kind TEXT NOT NULL,
			description TEXT,
			PRIMARY KEY(rel_type, from_kind, to_kind)
		);
		CREATE INDEX IF NOT EXISTS idx_schema_rel_lookup ON schema_relations(rel_type, from_kind, to_kind);
	`
	if _, err := db.Exec(schemaDDL); err != nil {
		return nil, fmt.Errorf("init schema tables: %w", err)
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
		if s[i] == ' ' || s[i] == '\t' || s[i] == '\r' || s[i] == '\n' || s[i] == ';' {
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

// ── Schema Validation Helpers ───────────────────────────────────────────────

func isSchemaEnforced(q dbOrTx, strictFlag bool) (bool, error) {
	if strictFlag {
		return true, nil
	}
	var count int
	err := q.QueryRow("SELECT count(*) FROM schema_kinds").Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func validateNodeKind(q dbOrTx, kind string, strictFlag bool) error {
	enforced, err := isSchemaEnforced(q, strictFlag)
	if err != nil {
		return fmt.Errorf("check schema enforcement: %w", err)
	}
	if !enforced {
		return nil
	}

	var exists bool
	err = q.QueryRow("SELECT EXISTS(SELECT 1 FROM schema_kinds WHERE kind = ?)", kind).Scan(&exists)
	if err != nil {
		return fmt.Errorf("validate kind: %w", err)
	}
	if !exists {
		rows, err := q.Query("SELECT kind FROM schema_kinds ORDER BY kind")
		if err != nil {
			return fmt.Errorf("schema validation error: node kind '%s' is not registered", kind)
		}
		defer rows.Close()
		var allowed []string
		for rows.Next() {
			var k string
			if rows.Scan(&k) == nil {
				allowed = append(allowed, k)
			}
		}
		return fmt.Errorf("schema validation error: node kind '%s' is not registered in schema. Allowed kinds: [%s]", kind, strings.Join(allowed, ", "))
	}
	return nil
}

func validateEdgeRelation(q dbOrTx, fromKind, toKind, relType string, strictFlag bool) error {
	enforced, err := isSchemaEnforced(q, strictFlag)
	if err != nil {
		return fmt.Errorf("check schema enforcement: %w", err)
	}
	if !enforced {
		return nil
	}

	var exists bool
	err = q.QueryRow("SELECT EXISTS(SELECT 1 FROM schema_relations WHERE rel_type = ? AND from_kind = ? AND to_kind = ?)", relType, fromKind, toKind).Scan(&exists)
	if err != nil {
		return fmt.Errorf("validate relation: %w", err)
	}
	if !exists {
		rows, err := q.Query("SELECT rel_type FROM schema_relations WHERE from_kind = ? AND to_kind = ? ORDER BY rel_type", fromKind, toKind)
		if err != nil {
			return fmt.Errorf("schema validation error: relation '%s' is not permitted between '%s' and '%s'", relType, fromKind, toKind)
		}
		defer rows.Close()
		var allowed []string
		for rows.Next() {
			var r string
			if rows.Scan(&r) == nil {
				allowed = append(allowed, r)
			}
		}
		if len(allowed) > 0 {
			return fmt.Errorf("schema validation error: relation '%s' is not permitted between '%s' and '%s'. Allowed relations: [%s]", relType, fromKind, toKind, strings.Join(allowed, ", "))
		}
		return fmt.Errorf("schema validation error: no relationship '%s' is permitted between '%s' and '%s' (no relations defined between these kinds)", relType, fromKind, toKind)
	}
	return nil
}

// ── Query & Search Handlers ────────────────────────────────────────────────

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

func tokenizeFTS5(query string) []string {
	var tokens []string
	var cur strings.Builder
	for _, r := range query {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			cur.WriteRune(r)
		} else {
			if cur.Len() > 0 {
				tokens = append(tokens, cur.String())
				cur.Reset()
			}
		}
	}
	if cur.Len() > 0 {
		tokens = append(tokens, cur.String())
	}
	return tokens
}

type SearchResultItem struct {
	ID         string  `json:"id"`
	Kind       string  `json:"kind"`
	Properties any     `json:"properties"`
	Rank       float64 `json:"rank"`
}

func handleGraphSearch(db *sql.DB, query string, kindFilter string, limit int) (string, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}

	tokens := tokenizeFTS5(query)
	if len(tokens) == 0 {
		emptyRes := map[string]any{
			"query":   query,
			"count":   0,
			"results": []SearchResultItem{},
		}
		b, _ := json.MarshalIndent(emptyRes, "", "  ")
		return string(b), nil
	}

	var quotedTokens []string
	for _, t := range tokens {
		quotedTokens = append(quotedTokens, fmt.Sprintf("\"%s\"*", t))
	}

	searchSQL := `
		SELECT n.id, n.kind, n.properties, nodes_fts.rank
		FROM nodes_fts
		JOIN nodes n ON n.rowid = nodes_fts.rowid
		WHERE nodes_fts MATCH ?
		  AND (? = '' OR n.kind = ?)
		ORDER BY nodes_fts.rank
		LIMIT ?;
	`

	executeFTS := func(ftsExpr string) ([]SearchResultItem, error) {
		rows, err := db.Query(searchSQL, ftsExpr, kindFilter, kindFilter, limit)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		var items []SearchResultItem
		for rows.Next() {
			var item SearchResultItem
			var propsRaw string
			if err := rows.Scan(&item.ID, &item.Kind, &propsRaw, &item.Rank); err != nil {
				return nil, err
			}
			var parsedProps any
			if err := json.Unmarshal([]byte(propsRaw), &parsedProps); err == nil {
				item.Properties = parsedProps
			} else {
				item.Properties = propsRaw
			}
			items = append(items, item)
		}
		return items, nil
	}

	// 1. Try AND query first
	ftsAnd := strings.Join(quotedTokens, " ")
	items, err := executeFTS(ftsAnd)
	if err != nil {
		return "", fmt.Errorf("fts search execution error: %w", err)
	}

	// 2. If no matches and multiple tokens, fallback to OR
	if len(items) == 0 && len(quotedTokens) > 1 {
		ftsOr := strings.Join(quotedTokens, " OR ")
		items, err = executeFTS(ftsOr)
		if err != nil {
			return "", fmt.Errorf("fts fallback search execution error: %w", err)
		}
	}

	resp := map[string]any{
		"query":   query,
		"count":   len(items),
		"results": items,
	}
	b, _ := json.MarshalIndent(resp, "", "  ")
	return string(b), nil
}

// ── Mutation Handlers ──────────────────────────────────────────────────────

func handleSetNode(db *sql.DB, id, kind string, props map[string]any, strictFlag ...bool) (string, error) {
	isStrict := len(strictFlag) > 0 && strictFlag[0]
	id = strings.TrimSpace(id)
	kind = strings.TrimSpace(kind)
	if id == "" {
		return "", fmt.Errorf("node id cannot be empty")
	}
	if kind == "" {
		return "", fmt.Errorf("node kind cannot be empty")
	}

	if err := validateNodeKind(db, kind, isStrict); err != nil {
		return "", err
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

func handleSetEdge(db *sql.DB, from, to, kind string, props map[string]any, strictFlag ...bool) (string, error) {
	isStrict := len(strictFlag) > 0 && strictFlag[0]
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	kind = strings.TrimSpace(kind)
	if from == "" {
		return "", fmt.Errorf("edge 'from' cannot be empty")
	}
	if to == "" {
		return "", fmt.Errorf("edge 'to' cannot be empty")
	}
	if kind == "" {
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

	// Verify both source and target nodes exist and fetch their kinds
	var fromKind, toKind string
	if err := tx.QueryRow("SELECT kind FROM nodes WHERE id = ?", from).Scan(&fromKind); err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("source node '%s' does not exist", from)
		}
		return "", fmt.Errorf("check from node: %w", err)
	}

	if err := tx.QueryRow("SELECT kind FROM nodes WHERE id = ?", to).Scan(&toKind); err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("target node '%s' does not exist", to)
		}
		return "", fmt.Errorf("check to node: %w", err)
	}

	if err := validateEdgeRelation(tx, fromKind, toKind, kind, isStrict); err != nil {
		return "", err
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

func handleBatchUpsert(db *sql.DB, nodes []BatchNodeItem, edges []BatchEdgeItem, strictFlag ...bool) (string, error) {
	isStrict := len(strictFlag) > 0 && strictFlag[0]
	if len(nodes) == 0 && len(edges) == 0 {
		return "No nodes or edges provided in batch.", nil
	}

	tx, err := db.Begin()
	if err != nil {
		return "", fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// 1. Process and upsert nodes
	upsertedNodes := 0
	for i, n := range nodes {
		id := strings.TrimSpace(n.ID)
		kind := strings.TrimSpace(n.Kind)
		if id == "" {
			return "", fmt.Errorf("node[%d]: id cannot be empty", i)
		}
		if kind == "" {
			return "", fmt.Errorf("node[%d] (%s): kind cannot be empty", i, id)
		}
		if err := validateNodeKind(tx, kind, isStrict); err != nil {
			return "", fmt.Errorf("node[%d] (%s): %w", i, id, err)
		}
		props := n.Properties
		if props == nil {
			props = make(map[string]any)
		}
		propsJSON, err := json.Marshal(props)
		if err != nil {
			return "", fmt.Errorf("node[%d] (%s): marshal properties: %w", i, id, err)
		}
		query := `
			INSERT INTO nodes (id, kind, properties) VALUES (?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET kind = excluded.kind, properties = excluded.properties;
		`
		if _, err := tx.Exec(query, id, kind, string(propsJSON)); err != nil {
			return "", fmt.Errorf("upsert node %s: %w", id, err)
		}
		upsertedNodes++
	}

	// 2. Process and upsert edges
	upsertedEdges := 0
	for i, e := range edges {
		from := strings.TrimSpace(e.From)
		to := strings.TrimSpace(e.To)
		kind := strings.TrimSpace(e.Kind)
		if from == "" {
			return "", fmt.Errorf("edge[%d]: 'from' cannot be empty", i)
		}
		if to == "" {
			return "", fmt.Errorf("edge[%d]: 'to' cannot be empty", i)
		}
		if kind == "" {
			return "", fmt.Errorf("edge[%d]: 'kind' cannot be empty", i)
		}

		var fromKind, toKind string
		err := tx.QueryRow("SELECT kind FROM nodes WHERE id = ?", from).Scan(&fromKind)
		if err != nil {
			if err == sql.ErrNoRows {
				return "", fmt.Errorf("edge[%d]: source node '%s' does not exist", i, from)
			}
			return "", fmt.Errorf("edge[%d]: check source node '%s': %w", i, from, err)
		}

		err = tx.QueryRow("SELECT kind FROM nodes WHERE id = ?", to).Scan(&toKind)
		if err != nil {
			if err == sql.ErrNoRows {
				return "", fmt.Errorf("edge[%d]: target node '%s' does not exist", i, to)
			}
			return "", fmt.Errorf("edge[%d]: check target node '%s': %w", i, to, err)
		}

		if err := validateEdgeRelation(tx, fromKind, toKind, kind, isStrict); err != nil {
			return "", fmt.Errorf("edge[%d] (%s)-[:%s]->(%s): %w", i, from, kind, to, err)
		}

		props := e.Properties
		if props == nil {
			props = make(map[string]any)
		}
		propsJSON, err := json.Marshal(props)
		if err != nil {
			return "", fmt.Errorf("edge[%d]: marshal properties: %w", i, err)
		}

		upsertQuery := `
			INSERT INTO edges (from_id, to_id, kind, properties) VALUES (?, ?, ?, ?)
			ON CONFLICT(from_id, to_id, kind) DO UPDATE SET properties = excluded.properties;
		`
		if _, err := tx.Exec(upsertQuery, from, to, kind, string(propsJSON)); err != nil {
			return "", fmt.Errorf("upsert edge (%s)-[%s]->(%s): %w", from, kind, to, err)
		}
		upsertedEdges++
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit batch transaction: %w", err)
	}

	return fmt.Sprintf("Successfully upserted %d nodes and %d edges in 1 transaction.", upsertedNodes, upsertedEdges), nil
}

// ── Schema Governance Handlers ──────────────────────────────────────────────

func handleSchema(db *sql.DB, strictFlag ...bool) (string, error) {
	isStrict := len(strictFlag) > 0 && strictFlag[0]

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

	// Query schema rules
	enforced, err := isSchemaEnforced(db, isStrict)
	if err != nil {
		return "", fmt.Errorf("check schema enforcement: %w", err)
	}

	type SchemaKindInfo struct {
		Kind        string `json:"kind"`
		Description string `json:"description,omitempty"`
	}
	type SchemaRelationInfo struct {
		Relation    string `json:"relation"`
		FromKind    string `json:"from_kind"`
		ToKind      string `json:"to_kind"`
		Description string `json:"description,omitempty"`
	}

	var allowedKinds []SchemaKindInfo
	kRows, err := db.Query("SELECT kind, coalesce(description, '') FROM schema_kinds ORDER BY kind")
	if err == nil {
		for kRows.Next() {
			var ki SchemaKindInfo
			if err := kRows.Scan(&ki.Kind, &ki.Description); err == nil {
				allowedKinds = append(allowedKinds, ki)
			}
		}
		kRows.Close()
	}

	var allowedRelations []SchemaRelationInfo
	rRows, err := db.Query("SELECT rel_type, from_kind, to_kind, coalesce(description, '') FROM schema_relations ORDER BY rel_type, from_kind, to_kind")
	if err == nil {
		for rRows.Next() {
			var ri SchemaRelationInfo
			if err := rRows.Scan(&ri.Relation, &ri.FromKind, &ri.ToKind, &ri.Description); err == nil {
				allowedRelations = append(allowedRelations, ri)
			}
		}
		rRows.Close()
	}

	summary := map[string]any{
		"total_nodes":       totalNodes,
		"total_edges":       totalEdges,
		"node_kinds":        nodeCounts,
		"edge_kinds":        edgeCounts,
		"schema_enforced":   enforced,
		"allowed_kinds":     allowedKinds,
		"allowed_relations": allowedRelations,
	}

	b, _ := json.MarshalIndent(summary, "", "  ")
	return string(b), nil
}

func handleSchemaDefine(db *sql.DB, action, kind, relation, fromKind, toKind, description string) (string, error) {
	action = strings.ToLower(strings.TrimSpace(action))
	switch action {
	case "add_kind":
		kind = strings.TrimSpace(kind)
		if kind == "" {
			return "", fmt.Errorf("'kind' cannot be empty for add_kind")
		}
		_, err := db.Exec(`
			INSERT INTO schema_kinds (kind, description) VALUES (?, ?)
			ON CONFLICT(kind) DO UPDATE SET description = excluded.description;
		`, kind, description)
		if err != nil {
			return "", fmt.Errorf("add kind: %w", err)
		}
		return fmt.Sprintf("Node kind '%s' registered in schema.", kind), nil

	case "remove_kind":
		kind = strings.TrimSpace(kind)
		if kind == "" {
			return "", fmt.Errorf("'kind' cannot be empty for remove_kind")
		}
		tx, err := db.Begin()
		if err != nil {
			return "", fmt.Errorf("begin transaction: %w", err)
		}
		defer tx.Rollback()

		if _, err := tx.Exec("DELETE FROM schema_relations WHERE from_kind = ? OR to_kind = ?", kind, kind); err != nil {
			return "", fmt.Errorf("delete schema relations: %w", err)
		}
		if _, err := tx.Exec("DELETE FROM schema_kinds WHERE kind = ?", kind); err != nil {
			return "", fmt.Errorf("delete schema kind: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return "", fmt.Errorf("commit remove kind: %w", err)
		}
		return fmt.Sprintf("Node kind '%s' and associated relations removed from schema.", kind), nil

	case "add_relation":
		relation = strings.TrimSpace(relation)
		fromKind = strings.TrimSpace(fromKind)
		toKind = strings.TrimSpace(toKind)
		if relation == "" || fromKind == "" || toKind == "" {
			return "", fmt.Errorf("'relation', 'from_kind', and 'to_kind' are required for add_relation")
		}
		tx, err := db.Begin()
		if err != nil {
			return "", fmt.Errorf("begin transaction: %w", err)
		}
		defer tx.Rollback()

		// Ensure fromKind and toKind are recorded in schema_kinds
		if _, err := tx.Exec("INSERT OR IGNORE INTO schema_kinds (kind, description) VALUES (?, '')", fromKind); err != nil {
			return "", fmt.Errorf("ensure from_kind: %w", err)
		}
		if _, err := tx.Exec("INSERT OR IGNORE INTO schema_kinds (kind, description) VALUES (?, '')", toKind); err != nil {
			return "", fmt.Errorf("ensure to_kind: %w", err)
		}
		_, err = tx.Exec(`
			INSERT INTO schema_relations (rel_type, from_kind, to_kind, description) VALUES (?, ?, ?, ?)
			ON CONFLICT(rel_type, from_kind, to_kind) DO UPDATE SET description = excluded.description;
		`, relation, fromKind, toKind, description)
		if err != nil {
			return "", fmt.Errorf("add relation: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return "", fmt.Errorf("commit add relation: %w", err)
		}
		return fmt.Sprintf("Relationship (%s)-[:%s]->(%s) registered in schema.", fromKind, relation, toKind), nil

	case "remove_relation":
		relation = strings.TrimSpace(relation)
		fromKind = strings.TrimSpace(fromKind)
		toKind = strings.TrimSpace(toKind)
		if relation == "" || fromKind == "" || toKind == "" {
			return "", fmt.Errorf("'relation', 'from_kind', and 'to_kind' are required for remove_relation")
		}
		_, err := db.Exec("DELETE FROM schema_relations WHERE rel_type = ? AND from_kind = ? AND to_kind = ?", relation, fromKind, toKind)
		if err != nil {
			return "", fmt.Errorf("delete relation: %w", err)
		}
		return fmt.Sprintf("Relationship (%s)-[:%s]->(%s) removed from schema.", fromKind, relation, toKind), nil

	default:
		return "", fmt.Errorf("unknown schema action '%s'. Allowed: add_kind, remove_kind, add_relation, remove_relation", action)
	}
}

// ── Server Bootstrap & Event Loop ──────────────────────────────────────────

func main() {
	dbPath := flag.String("db", "knowledge_graph.db", "Path to SQLite database file")
	logPath := flag.String("log", "", "Path to debug log file")
	strictSchema := flag.Bool("strict-schema", false, "Strictly enforce schema even if schema tables are empty")
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

	logMsg("Starting cypher-mcp with db=%s, strict-schema=%v", *dbPath, *strictSchema)

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

	runServer(os.Stdin, os.Stdout, db, dbRO, logger, *strictSchema)
}

func runServer(in io.Reader, out io.Writer, db, dbRO *sql.DB, logger *log.Logger, strictSchema ...bool) {
	isStrict := len(strictSchema) > 0 && strictSchema[0]

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
								"version": "0.3.0",
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

					case "graph_search":
						query, _ := params.Arguments["query"].(string)
						kindFilter, _ := params.Arguments["kind"].(string)
						limit := 10
						if lVal, ok := params.Arguments["limit"]; ok {
							switch v := lVal.(type) {
							case float64:
								limit = int(v)
							case int:
								limit = v
							}
						}
						outText, callErr = handleGraphSearch(dbRO, query, kindFilter, limit)

					case "graph_batch_upsert":
						var batchNodes []BatchNodeItem
						if rawNodes, ok := params.Arguments["nodes"]; ok && rawNodes != nil {
							b, _ := json.Marshal(rawNodes)
							_ = json.Unmarshal(b, &batchNodes)
						}
						var batchEdges []BatchEdgeItem
						if rawEdges, ok := params.Arguments["edges"]; ok && rawEdges != nil {
							b, _ := json.Marshal(rawEdges)
							_ = json.Unmarshal(b, &batchEdges)
						}
						outText, callErr = handleBatchUpsert(db, batchNodes, batchEdges, isStrict)

					case "graph_set_node":
						id, _ := params.Arguments["id"].(string)
						kind, _ := params.Arguments["kind"].(string)
						props, _ := params.Arguments["properties"].(map[string]any)
						outText, callErr = handleSetNode(db, id, kind, props, isStrict)

					case "graph_set_edge":
						from, _ := params.Arguments["from"].(string)
						to, _ := params.Arguments["to"].(string)
						kind, _ := params.Arguments["kind"].(string)
						props, _ := params.Arguments["properties"].(map[string]any)
						outText, callErr = handleSetEdge(db, from, to, kind, props, isStrict)

					case "graph_delete_node":
						id, _ := params.Arguments["id"].(string)
						outText, callErr = handleDeleteNode(db, id)

					case "graph_schema":
						outText, callErr = handleSchema(dbRO, isStrict)

					case "graph_schema_define":
						action, _ := params.Arguments["action"].(string)
						kind, _ := params.Arguments["kind"].(string)
						relation, _ := params.Arguments["relation"].(string)
						fromKind, _ := params.Arguments["from_kind"].(string)
						toKind, _ := params.Arguments["to_kind"].(string)
						description, _ := params.Arguments["description"].(string)
						outText, callErr = handleSchemaDefine(db, action, kind, relation, fromKind, toKind, description)

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
