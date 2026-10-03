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
	Exec(query string, args ...any) (sql.Result, error)
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
				"limit": map[string]any{
					"type":        "integer",
					"description": "Optional maximum number of rows to return (default: server limit of 1000, 0 = unlimited/default)",
				},
			},
			"required": []string{"query"},
		},
	},
	{
		"name":        "graph_search",
		"description": "Full-text keyword and substring search across nodes using SQLite FTS5 (trigram-indexed). Searches node IDs and JSON property values (names, addresses, notes, etc.) across languages without strict syntax restrictions. Returns an empty array if no match.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Keywords or substrings to search for (e.g. 'Франциска', 'Михайлов', 'BMW', 'M-EW 330', 'KIT Timur')",
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
		"description": "Create or update a graph node with a unique ID, kind/label, and properties. Properties are merged with existing properties by default; set merge=false to overwrite completely.",
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
				"merge": map[string]any{
					"type":        "boolean",
					"description": "If true (default), merges properties with existing properties. If false, completely overwrites properties.",
				},
			},
			"required": []string{"id", "kind"},
		},
	},
	{
		"name":        "graph_set_edge",
		"description": "Create or update a directed relationship/edge between two nodes. Properties are merged with existing properties by default; set merge=false to overwrite completely.",
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
				"merge": map[string]any{
					"type":        "boolean",
					"description": "If true (default), merges properties with existing properties. If false, completely overwrites properties.",
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
		"description": "Governance tool for defining or modifying allowed node kinds and relationship rules in the knowledge graph schema. Note: requires --allow-schema-edit server startup flag.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"add_kind", "remove_kind", "add_relation", "remove_relation", "rename_kind", "rename_relation"},
					"description": "Schema action to perform",
				},
				"kind": map[string]any{
					"type":        "string",
					"description": "Node kind name (for add_kind / remove_kind / rename_kind)",
				},
				"relation": map[string]any{
					"type":        "string",
					"description": "Relationship type (for add_relation / remove_relation / rename_relation)",
				},
				"from_kind": map[string]any{
					"type":        "string",
					"description": "Source node kind (for add_relation / remove_relation / rename_relation)",
				},
				"to_kind": map[string]any{
					"type":        "string",
					"description": "Target node kind (for add_relation / remove_relation / rename_relation)",
				},
				"new_kind": map[string]any{
					"type":        "string",
					"description": "New node kind name (for rename_kind)",
				},
				"new_relation": map[string]any{
					"type":        "string",
					"description": "New relationship type (for rename_relation)",
				},
				"cascade": map[string]any{
					"type":        "boolean",
					"description": "If true, cascades deletion of nodes or edges associated with the removed kind/relation",
				},
				"migrate_to": map[string]any{
					"type":        "string",
					"description": "Target kind or relationship type to migrate existing data to before removal",
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

	// 1. Full-Text Search (FTS5) table with trigram tokenizer, json_tree value-only indexing, and triggers
	ftsDDL := `
		CREATE VIRTUAL TABLE IF NOT EXISTS nodes_fts USING fts5(id, kind, content, tokenize='trigram');

		DROP TRIGGER IF EXISTS nodes_ai;
		DROP TRIGGER IF EXISTS nodes_ad;
		DROP TRIGGER IF EXISTS nodes_au;

		CREATE TRIGGER nodes_ai AFTER INSERT ON nodes BEGIN
			INSERT INTO nodes_fts(rowid, id, kind, content) VALUES (
				new.rowid, 
				new.id, 
				new.kind, 
				CASE 
					WHEN json_valid(new.properties) THEN (SELECT coalesce(group_concat(value, ' '), '') FROM json_tree(new.properties) WHERE atom IS NOT NULL)
					ELSE new.properties 
				END
			);
		END;
		CREATE TRIGGER nodes_ad AFTER DELETE ON nodes BEGIN
			DELETE FROM nodes_fts WHERE rowid = old.rowid;
		END;
		CREATE TRIGGER nodes_au AFTER UPDATE ON nodes BEGIN
			DELETE FROM nodes_fts WHERE rowid = old.rowid;
			INSERT INTO nodes_fts(rowid, id, kind, content) VALUES (
				new.rowid, 
				new.id, 
				new.kind, 
				CASE 
					WHEN json_valid(new.properties) THEN (SELECT coalesce(group_concat(value, ' '), '') FROM json_tree(new.properties) WHERE atom IS NOT NULL)
					ELSE new.properties 
				END
			);
		END;
	`
	if _, err := db.Exec(ftsDDL); err != nil {
		return nil, fmt.Errorf("init fts5: %w", err)
	}

	// Re-sync FTS index for all nodes extracting only values via json_tree
	rebuildFTS := `
		DELETE FROM nodes_fts;
		INSERT INTO nodes_fts(rowid, id, kind, content)
		SELECT rowid, id, kind, 
			CASE 
				WHEN json_valid(properties) THEN (SELECT coalesce(group_concat(value, ' '), '') FROM json_tree(properties) WHERE atom IS NOT NULL)
				ELSE properties 
			END
		FROM nodes;
	`
	if _, err := db.Exec(rebuildFTS); err != nil {
		return nil, fmt.Errorf("rebuild fts5: %w", err)
	}

	// 2. Schema governance tables and changelog
	schemaDDL := `
		CREATE TABLE IF NOT EXISTS schema_kinds (
			kind TEXT PRIMARY KEY COLLATE NOCASE,
			description TEXT
		);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_schema_kinds_nocase ON schema_kinds(kind COLLATE NOCASE);

		CREATE TABLE IF NOT EXISTS schema_relations (
			rel_type TEXT NOT NULL COLLATE NOCASE,
			from_kind TEXT NOT NULL COLLATE NOCASE,
			to_kind TEXT NOT NULL COLLATE NOCASE,
			description TEXT,
			PRIMARY KEY(rel_type, from_kind, to_kind)
		);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_schema_relations_nocase ON schema_relations(rel_type COLLATE NOCASE, from_kind COLLATE NOCASE, to_kind COLLATE NOCASE);
		CREATE INDEX IF NOT EXISTS idx_schema_rel_lookup ON schema_relations(rel_type, from_kind, to_kind);

		CREATE TABLE IF NOT EXISTS schema_changelog (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
			action TEXT NOT NULL,
			entity_type TEXT NOT NULL,
			entity_name TEXT NOT NULL,
			details TEXT
		);
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
	err = q.QueryRow("SELECT EXISTS(SELECT 1 FROM schema_kinds WHERE kind = ? COLLATE NOCASE)", kind).Scan(&exists)
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
		if err := rows.Err(); err != nil {
			return fmt.Errorf("schema validation error: %w", err)
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
	err = q.QueryRow(`
		SELECT EXISTS(
			SELECT 1 FROM schema_relations 
			WHERE rel_type = ? COLLATE NOCASE 
			  AND from_kind = ? COLLATE NOCASE 
			  AND to_kind = ? COLLATE NOCASE
		)
	`, relType, fromKind, toKind).Scan(&exists)
	if err != nil {
		return fmt.Errorf("validate relation: %w", err)
	}
	if !exists {
		rows, err := q.Query(`
			SELECT rel_type FROM schema_relations 
			WHERE from_kind = ? COLLATE NOCASE AND to_kind = ? COLLATE NOCASE 
			ORDER BY rel_type
		`, fromKind, toKind)
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
		if err := rows.Err(); err != nil {
			return fmt.Errorf("schema validation error: %w", err)
		}
		if len(allowed) > 0 {
			return fmt.Errorf("schema validation error: relation '%s' is not permitted between '%s' and '%s'. Allowed relations: [%s]", relType, fromKind, toKind, strings.Join(allowed, ", "))
		}
		return fmt.Errorf("schema validation error: no relationship '%s' is permitted between '%s' and '%s' (no relations defined between these kinds)", relType, fromKind, toKind)
	}
	return nil
}

func validateNodeKindChange(q dbOrTx, id, existingKind, newKind string, strictFlag bool) error {
	enforced, err := isSchemaEnforced(q, strictFlag)
	if err != nil {
		return fmt.Errorf("check schema enforcement: %w", err)
	}
	if !enforced || strings.EqualFold(existingKind, newKind) {
		return nil
	}

	// 1. Check outgoing edges from this node
	outSQL := `
		SELECT e.kind, nt.kind
		FROM edges e
		JOIN nodes nt ON e.to_id = nt.id
		WHERE e.from_id = ?
		  AND NOT EXISTS (
			  SELECT 1 FROM schema_relations sr
			  WHERE sr.rel_type = e.kind COLLATE NOCASE
			    AND sr.from_kind = ? COLLATE NOCASE
			    AND sr.to_kind = nt.kind COLLATE NOCASE
		  )
		LIMIT 1;
	`
	var outRel, outTargetKind string
	err = q.QueryRow(outSQL, id, newKind).Scan(&outRel, &outTargetKind)
	if err == nil {
		return fmt.Errorf("schema validation error: cannot change kind of node '%s' from '%s' to '%s': outgoing relationship (:%s)-[:%s]->(:%s) is not permitted by schema", id, existingKind, newKind, newKind, outRel, outTargetKind)
	} else if err != sql.ErrNoRows {
		return fmt.Errorf("validate outgoing edges on kind change: %w", err)
	}

	// 2. Check incoming edges to this node
	inSQL := `
		SELECT e.kind, nf.kind
		FROM edges e
		JOIN nodes nf ON e.from_id = nf.id
		WHERE e.to_id = ?
		  AND NOT EXISTS (
			  SELECT 1 FROM schema_relations sr
			  WHERE sr.rel_type = e.kind COLLATE NOCASE
			    AND sr.from_kind = nf.kind COLLATE NOCASE
			    AND sr.to_kind = ? COLLATE NOCASE
		  )
		LIMIT 1;
	`
	var inRel, inSourceKind string
	err = q.QueryRow(inSQL, id, newKind).Scan(&inRel, &inSourceKind)
	if err == nil {
		return fmt.Errorf("schema validation error: cannot change kind of node '%s' from '%s' to '%s': incoming relationship (:%s)-[:%s]->(:%s) is not permitted by schema", id, existingKind, newKind, inSourceKind, inRel, newKind)
	} else if err != sql.ErrNoRows {
		return fmt.Errorf("validate incoming edges on kind change: %w", err)
	}

	return nil
}

// ── Query & Search Handlers ────────────────────────────────────────────────

var defaultMaxRows = 1000

func handleGraphQuery(db *sql.DB, cypherQuery string, options ...any) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return handleGraphQueryContext(ctx, db, cypherQuery, options...)
}

func handleGraphQueryContext(ctx context.Context, db *sql.DB, cypherQuery string, options ...any) (string, error) {
	start := time.Now()

	// Defense-in-depth: check unsafe identifiers
	for _, m := range backtickRegex.FindAllStringSubmatch(cypherQuery, -1) {
		if !safeIdentifierRegex.MatchString(m[1]) {
			return "", fmt.Errorf("invalid identifier %q: only alphanumeric characters and underscores are permitted", m[1])
		}
	}
	var params map[string]any
	maxRows := defaultMaxRows

	for _, opt := range options {
		switch v := opt.(type) {
		case map[string]any:
			params = v
		case int:
			maxRows = v
		}
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
	truncated := false
	for rows.Next() {
		if maxRows > 0 && len(results) >= maxRows {
			truncated = true
			break
		}
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
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("row iteration error: %w", err)
	}
	execDuration := time.Since(qStart)

	payload := map[string]any{
		"results":         results,
		"count":           len(results),
		"compiled_sql":    compiled.SQL,
		"compile_time_us": compileDuration.Microseconds(),
		"execute_time_us": execDuration.Microseconds(),
	}
	if truncated {
		payload["truncated"] = true
		payload["warning"] = fmt.Sprintf("Query result was truncated to %d rows. Use Cypher LIMIT to narrow your query.", maxRows)
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

	var trigramTokens []string
	for _, t := range tokens {
		if len([]rune(t)) >= 3 {
			trigramTokens = append(trigramTokens, fmt.Sprintf("\"%s\"", t))
		}
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

		items := make([]SearchResultItem, 0)
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
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("fts row iteration error: %w", err)
		}
		return items, nil
	}

	var items []SearchResultItem

	// 1. Try trigram FTS search if tokens of length >= 3 exist
	if len(trigramTokens) > 0 {
		ftsAnd := strings.Join(trigramTokens, " AND ")
		var err error
		items, err = executeFTS(ftsAnd)
		if err == nil && len(items) == 0 && len(trigramTokens) > 1 {
			ftsOr := strings.Join(trigramTokens, " OR ")
			items, _ = executeFTS(ftsOr)
		}
	}

	// 2. If 0 results or short query (< 3 chars), fallback to LIKE on nodes table (values only)
	if len(items) == 0 {
		likePat := "%" + strings.TrimSpace(query) + "%"
		likeSQL := `
			SELECT id, kind, properties, 0.0 AS rank
			FROM nodes
			WHERE (id LIKE ? OR (
				CASE 
					WHEN json_valid(properties) THEN (SELECT coalesce(group_concat(value, ' '), '') FROM json_tree(properties) WHERE atom IS NOT NULL)
					ELSE properties 
				END
			) LIKE ?)
			  AND (? = '' OR kind = ?)
			LIMIT ?;
		`
		rows, err := db.Query(likeSQL, likePat, likePat, kindFilter, kindFilter, limit)
		if err != nil {
			return "", fmt.Errorf("fallback search query error: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var item SearchResultItem
			var propsRaw string
			if err := rows.Scan(&item.ID, &item.Kind, &propsRaw, &item.Rank); err != nil {
				return "", fmt.Errorf("fallback search scan error: %w", err)
			}
			var parsedProps any
			if err := json.Unmarshal([]byte(propsRaw), &parsedProps); err == nil {
				item.Properties = parsedProps
			} else {
				item.Properties = propsRaw
			}
			items = append(items, item)
		}
		if err := rows.Err(); err != nil {
			return "", fmt.Errorf("fallback search iteration error: %w", err)
		}
	}

	if items == nil {
		items = make([]SearchResultItem, 0)
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

func handleSetNode(db *sql.DB, id, kind string, props map[string]any, options ...any) (string, error) {
	isStrict := false
	merge := true
	boolCount := 0
	for _, opt := range options {
		switch v := opt.(type) {
		case bool:
			if boolCount == 0 {
				isStrict = v
			} else if boolCount == 1 {
				merge = v
			}
			boolCount++
		}
	}

	id = strings.TrimSpace(id)
	kind = strings.TrimSpace(kind)
	if id == "" {
		return "", fmt.Errorf("node id cannot be empty")
	}
	if kind == "" {
		return "", fmt.Errorf("node kind cannot be empty")
	}

	if err := cyphersql.ValidateIdentifier("kind", kind); err != nil {
		return "", err
	}

	var canonicalKind string
	if err := db.QueryRow("SELECT kind FROM schema_kinds WHERE kind = ? COLLATE NOCASE", kind).Scan(&canonicalKind); err == nil {
		kind = canonicalKind
	}

	if err := validateNodeKind(db, kind, isStrict); err != nil {
		return "", err
	}

	// Validate relationship consistency if updating an existing node's kind
	var existingKind string
	if err := db.QueryRow("SELECT kind FROM nodes WHERE id = ?", id).Scan(&existingKind); err == nil && !strings.EqualFold(existingKind, kind) {
		if err := validateNodeKindChange(db, id, existingKind, kind, isStrict); err != nil {
			return "", err
		}
	}

	if props == nil {
		props = make(map[string]any)
	}
	propsJSON, err := json.Marshal(props)
	if err != nil {
		return "", fmt.Errorf("marshal properties: %w", err)
	}

	var query string
	if merge {
		query = `
			INSERT INTO nodes (id, kind, properties) VALUES (?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET 
				kind = excluded.kind, 
				properties = json_patch(CASE WHEN json_valid(nodes.properties) THEN nodes.properties ELSE '{}' END, excluded.properties);
		`
	} else {
		query = `
			INSERT INTO nodes (id, kind, properties) VALUES (?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET kind = excluded.kind, properties = excluded.properties;
		`
	}
	if _, err := db.Exec(query, id, kind, string(propsJSON)); err != nil {
		return "", fmt.Errorf("upsert node %s: %w", id, err)
	}

	return fmt.Sprintf("Node '%s' of kind '%s' upserted successfully.", id, kind), nil
}

func handleSetEdge(db *sql.DB, from, to, kind string, props map[string]any, options ...any) (string, error) {
	isStrict := false
	merge := true
	boolCount := 0
	for _, opt := range options {
		switch v := opt.(type) {
		case bool:
			if boolCount == 0 {
				isStrict = v
			} else if boolCount == 1 {
				merge = v
			}
			boolCount++
		}
	}
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

	if err := cyphersql.ValidateIdentifier("relationship type", kind); err != nil {
		return "", err
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

	var canonicalRel string
	if err := tx.QueryRow(`
		SELECT rel_type FROM schema_relations 
		WHERE rel_type = ? COLLATE NOCASE 
		  AND from_kind = ? COLLATE NOCASE 
		  AND to_kind = ? COLLATE NOCASE
	`, kind, fromKind, toKind).Scan(&canonicalRel); err == nil {
		kind = canonicalRel
	}

	if err := validateEdgeRelation(tx, fromKind, toKind, kind, isStrict); err != nil {
		return "", err
	}

	var upsertQuery string
	if merge {
		upsertQuery = `
			INSERT INTO edges (from_id, to_id, kind, properties) VALUES (?, ?, ?, ?)
			ON CONFLICT(from_id, to_id, kind) DO UPDATE SET 
				properties = json_patch(CASE WHEN json_valid(edges.properties) THEN edges.properties ELSE '{}' END, excluded.properties);
		`
	} else {
		upsertQuery = `
			INSERT INTO edges (from_id, to_id, kind, properties) VALUES (?, ?, ?, ?)
			ON CONFLICT(from_id, to_id, kind) DO UPDATE SET properties = excluded.properties;
		`
	}
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

	nodeStmt, err := tx.Prepare(`
		INSERT INTO nodes (id, kind, properties) VALUES (?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET 
			kind = excluded.kind, 
			properties = json_patch(CASE WHEN json_valid(nodes.properties) THEN nodes.properties ELSE '{}' END, excluded.properties);
	`)
	if err != nil {
		return "", fmt.Errorf("prepare node upsert: %w", err)
	}
	defer nodeStmt.Close()

	// 1. Process and upsert nodes
	upsertedNodes := 0
	nodeKindCache := make(map[string]string, len(nodes))
	for i, n := range nodes {
		id := strings.TrimSpace(n.ID)
		kind := strings.TrimSpace(n.Kind)
		if id == "" {
			return "", fmt.Errorf("node[%d]: id cannot be empty", i)
		}
		if kind == "" {
			return "", fmt.Errorf("node[%d] (%s): kind cannot be empty", i, id)
		}
		if err := cyphersql.ValidateIdentifier("kind", kind); err != nil {
			return "", fmt.Errorf("node[%d] (%s): %w", i, id, err)
		}
		var canonicalKind string
		if err := tx.QueryRow("SELECT kind FROM schema_kinds WHERE kind = ? COLLATE NOCASE", kind).Scan(&canonicalKind); err == nil {
			kind = canonicalKind
		}
		if err := validateNodeKind(tx, kind, isStrict); err != nil {
			return "", fmt.Errorf("node[%d] (%s): %w", i, id, err)
		}

		// Validate relationship consistency if updating an existing node's kind
		var existingKind string
		if err := tx.QueryRow("SELECT kind FROM nodes WHERE id = ?", id).Scan(&existingKind); err == nil && !strings.EqualFold(existingKind, kind) {
			if err := validateNodeKindChange(tx, id, existingKind, kind, isStrict); err != nil {
				return "", fmt.Errorf("node[%d] (%s): %w", i, id, err)
			}
		}
		props := n.Properties
		if props == nil {
			props = make(map[string]any)
		}
		propsJSON, err := json.Marshal(props)
		if err != nil {
			return "", fmt.Errorf("node[%d] (%s): marshal properties: %w", i, id, err)
		}
		if _, err := nodeStmt.Exec(id, kind, string(propsJSON)); err != nil {
			return "", fmt.Errorf("upsert node %s: %w", id, err)
		}
		nodeKindCache[id] = kind
		upsertedNodes++
	}

	getNodeKind := func(nodeID string) (string, error) {
		if k, ok := nodeKindCache[nodeID]; ok {
			return k, nil
		}
		var k string
		err := tx.QueryRow("SELECT kind FROM nodes WHERE id = ?", nodeID).Scan(&k)
		if err != nil {
			if err == sql.ErrNoRows {
				return "", sql.ErrNoRows
			}
			return "", err
		}
		nodeKindCache[nodeID] = k
		return k, nil
	}

	edgeStmt, err := tx.Prepare(`
		INSERT INTO edges (from_id, to_id, kind, properties) VALUES (?, ?, ?, ?)
		ON CONFLICT(from_id, to_id, kind) DO UPDATE SET 
			properties = json_patch(CASE WHEN json_valid(edges.properties) THEN edges.properties ELSE '{}' END, excluded.properties);
	`)
	if err != nil {
		return "", fmt.Errorf("prepare edge upsert: %w", err)
	}
	defer edgeStmt.Close()

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
		if err := cyphersql.ValidateIdentifier("relationship type", kind); err != nil {
			return "", fmt.Errorf("edge[%d]: %w", i, err)
		}

		fromKind, err := getNodeKind(from)
		if err != nil {
			if err == sql.ErrNoRows {
				return "", fmt.Errorf("edge[%d]: source node '%s' does not exist", i, from)
			}
			return "", fmt.Errorf("edge[%d]: check source node '%s': %w", i, from, err)
		}

		toKind, err := getNodeKind(to)
		if err != nil {
			if err == sql.ErrNoRows {
				return "", fmt.Errorf("edge[%d]: target node '%s' does not exist", i, to)
			}
			return "", fmt.Errorf("edge[%d]: check target node '%s': %w", i, to, err)
		}

		var canonicalRel string
		if err := tx.QueryRow(`
			SELECT rel_type FROM schema_relations 
			WHERE rel_type = ? COLLATE NOCASE 
			  AND from_kind = ? COLLATE NOCASE 
			  AND to_kind = ? COLLATE NOCASE
		`, kind, fromKind, toKind).Scan(&canonicalRel); err == nil {
			kind = canonicalRel
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

		if _, err := edgeStmt.Exec(from, to, kind, string(propsJSON)); err != nil {
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

	type SchemaChangelogEntry struct {
		ID         int    `json:"id"`
		Timestamp  string `json:"timestamp"`
		Action     string `json:"action"`
		EntityType string `json:"entity_type"`
		EntityName string `json:"entity_name"`
		Details    string `json:"details,omitempty"`
	}
	var changelog []SchemaChangelogEntry
	clRows, err := db.Query("SELECT id, timestamp, action, entity_type, entity_name, coalesce(details, '') FROM schema_changelog ORDER BY id DESC LIMIT 20")
	if err == nil {
		for clRows.Next() {
			var entry SchemaChangelogEntry
			if err := clRows.Scan(&entry.ID, &entry.Timestamp, &entry.Action, &entry.EntityType, &entry.EntityName, &entry.Details); err == nil {
				changelog = append(changelog, entry)
			}
		}
		clRows.Close()
	}
	if changelog == nil {
		changelog = make([]SchemaChangelogEntry, 0)
	}

	summary := map[string]any{
		"total_nodes":       totalNodes,
		"total_edges":       totalEdges,
		"node_kinds":        nodeCounts,
		"edge_kinds":        edgeCounts,
		"schema_enforced":   enforced,
		"allowed_kinds":     allowedKinds,
		"allowed_relations": allowedRelations,
		"changelog":         changelog,
	}

	b, _ := json.MarshalIndent(summary, "", "  ")
	return string(b), nil
}

type SchemaDefineOptions struct {
	AllowSchemaEdit bool
	Cascade         bool
	MigrateTo       string
	NewKind         string
	NewRelation     string
}

func logSchemaChange(q dbOrTx, action, entityType, entityName, details string) error {
	_, err := q.Exec(`
		INSERT INTO schema_changelog (action, entity_type, entity_name, details)
		VALUES (?, ?, ?, ?);
	`, action, entityType, entityName, details)
	return err
}

func handleSchemaDefine(db *sql.DB, action, kind, relation, fromKind, toKind, description string, extraOpts ...any) (string, error) {
	var opts SchemaDefineOptions
	if len(extraOpts) > 0 {
		switch v := extraOpts[0].(type) {
		case bool:
			opts.AllowSchemaEdit = v
		case SchemaDefineOptions:
			opts = v
		}
	}
	if !opts.AllowSchemaEdit {
		return "", fmt.Errorf("schema modification is locked (read-only mode). Restart cypher-mcp with --allow-schema-edit to modify taxonomy schema rules")
	}

	action = strings.ToLower(strings.TrimSpace(action))
	switch action {
	case "add_kind":
		kind = strings.TrimSpace(kind)
		if kind == "" {
			return "", fmt.Errorf("'kind' cannot be empty for add_kind")
		}
		if err := cyphersql.ValidateIdentifier("kind", kind); err != nil {
			return "", err
		}
		var existingKind string
		err := db.QueryRow("SELECT kind FROM schema_kinds WHERE kind = ? COLLATE NOCASE", kind).Scan(&existingKind)
		if err == nil && existingKind != kind {
			return "", fmt.Errorf("node kind '%s' conflicts with existing kind '%s' (case-insensitive uniqueness required)", kind, existingKind)
		}
		tx, err := db.Begin()
		if err != nil {
			return "", fmt.Errorf("begin transaction: %w", err)
		}
		defer tx.Rollback()

		_, err = tx.Exec(`
			INSERT INTO schema_kinds (kind, description) VALUES (?, ?)
			ON CONFLICT(kind) DO UPDATE SET description = excluded.description;
		`, kind, description)
		if err != nil {
			return "", fmt.Errorf("add kind: %w", err)
		}
		if err := logSchemaChange(tx, "add_kind", "kind", kind, description); err != nil {
			return "", fmt.Errorf("log schema change: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return "", fmt.Errorf("commit add kind: %w", err)
		}
		return fmt.Sprintf("Node kind '%s' registered in schema.", kind), nil

	case "remove_kind":
		kind = strings.TrimSpace(kind)
		if kind == "" {
			return "", fmt.Errorf("'kind' cannot be empty for remove_kind")
		}
		var canonicalKind string
		err := db.QueryRow("SELECT kind FROM schema_kinds WHERE kind = ? COLLATE NOCASE", kind).Scan(&canonicalKind)
		if err != nil {
			if err == sql.ErrNoRows {
				return "", fmt.Errorf("node kind '%s' not found in schema", kind)
			}
			return "", fmt.Errorf("query schema_kinds: %w", err)
		}
		kind = canonicalKind

		// Check for existing data
		var nodeCount, edgeCount int
		if err := db.QueryRow("SELECT count(*) FROM nodes WHERE kind = ?", kind).Scan(&nodeCount); err != nil {
			return "", fmt.Errorf("count existing nodes: %w", err)
		}
		if err := db.QueryRow(`
			SELECT count(*) FROM edges 
			WHERE from_id IN (SELECT id FROM nodes WHERE kind = ?) 
			   OR to_id IN (SELECT id FROM nodes WHERE kind = ?)
		`, kind, kind).Scan(&edgeCount); err != nil {
			return "", fmt.Errorf("count existing edges: %w", err)
		}

		if (nodeCount > 0 || edgeCount > 0) && !opts.Cascade && opts.MigrateTo == "" {
			return "", fmt.Errorf("cannot remove kind '%s': %d nodes (and %d related edges) exist. Use cascade=true to delete data or migrate_to to reassign nodes to another kind", kind, nodeCount, edgeCount)
		}

		tx, err := db.Begin()
		if err != nil {
			return "", fmt.Errorf("begin transaction: %w", err)
		}
		defer tx.Rollback()

		if opts.MigrateTo != "" {
			migrateTo := strings.TrimSpace(opts.MigrateTo)
			if err := cyphersql.ValidateIdentifier("kind", migrateTo); err != nil {
				return "", fmt.Errorf("invalid migrate_to kind %q: %w", migrateTo, err)
			}
			var targetCanonical string
			err := tx.QueryRow("SELECT kind FROM schema_kinds WHERE kind = ? COLLATE NOCASE", migrateTo).Scan(&targetCanonical)
			if err != nil {
				return "", fmt.Errorf("migrate_to target kind '%s' does not exist in schema", migrateTo)
			}
			migrateTo = targetCanonical

			if _, err := tx.Exec("UPDATE nodes SET kind = ? WHERE kind = ?", migrateTo, kind); err != nil {
				return "", fmt.Errorf("migrate nodes: %w", err)
			}
			if _, err := tx.Exec("UPDATE schema_relations SET from_kind = ? WHERE from_kind = ?", migrateTo, kind); err != nil {
				return "", fmt.Errorf("migrate schema relations from_kind: %w", err)
			}
			if _, err := tx.Exec("UPDATE schema_relations SET to_kind = ? WHERE to_kind = ?", migrateTo, kind); err != nil {
				return "", fmt.Errorf("migrate schema relations to_kind: %w", err)
			}
			if _, err := tx.Exec("DELETE FROM schema_kinds WHERE kind = ?", kind); err != nil {
				return "", fmt.Errorf("delete schema kind: %w", err)
			}
			detail := fmt.Sprintf("migrated %d nodes to %s", nodeCount, migrateTo)
			if err := logSchemaChange(tx, "remove_kind", "kind", kind, detail); err != nil {
				return "", fmt.Errorf("log schema change: %w", err)
			}
			if err := tx.Commit(); err != nil {
				return "", fmt.Errorf("commit remove kind with migration: %w", err)
			}
			return fmt.Sprintf("Node kind '%s' removed from schema, %d nodes migrated to '%s'.", kind, nodeCount, migrateTo), nil
		}

		if opts.Cascade {
			if _, err := tx.Exec(`
				DELETE FROM edges 
				WHERE from_id IN (SELECT id FROM nodes WHERE kind = ?) 
				   OR to_id IN (SELECT id FROM nodes WHERE kind = ?)
			`, kind, kind); err != nil {
				return "", fmt.Errorf("cascade delete edges: %w", err)
			}
			if _, err := tx.Exec("DELETE FROM nodes WHERE kind = ?", kind); err != nil {
				return "", fmt.Errorf("cascade delete nodes: %w", err)
			}
		}

		if _, err := tx.Exec("DELETE FROM schema_relations WHERE from_kind = ? OR to_kind = ?", kind, kind); err != nil {
			return "", fmt.Errorf("delete schema relations: %w", err)
		}
		if _, err := tx.Exec("DELETE FROM schema_kinds WHERE kind = ?", kind); err != nil {
			return "", fmt.Errorf("delete schema kind: %w", err)
		}
		detail := fmt.Sprintf("cascade=%v deleted %d nodes, %d edges", opts.Cascade, nodeCount, edgeCount)
		if err := logSchemaChange(tx, "remove_kind", "kind", kind, detail); err != nil {
			return "", fmt.Errorf("log schema change: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return "", fmt.Errorf("commit remove kind: %w", err)
		}
		return fmt.Sprintf("Node kind '%s' and associated relations removed from schema.", kind), nil

	case "rename_kind":
		kind = strings.TrimSpace(kind)
		newKind := strings.TrimSpace(opts.NewKind)
		if kind == "" || newKind == "" {
			return "", fmt.Errorf("'kind' and 'new_kind' are required for rename_kind")
		}
		if err := cyphersql.ValidateIdentifier("kind", newKind); err != nil {
			return "", err
		}
		var canonicalOld string
		err := db.QueryRow("SELECT kind FROM schema_kinds WHERE kind = ? COLLATE NOCASE", kind).Scan(&canonicalOld)
		if err != nil {
			return "", fmt.Errorf("node kind '%s' not found in schema", kind)
		}
		var existingNew string
		err = db.QueryRow("SELECT kind FROM schema_kinds WHERE kind = ? COLLATE NOCASE", newKind).Scan(&existingNew)
		if err == nil && !strings.EqualFold(existingNew, canonicalOld) {
			return "", fmt.Errorf("cannot rename to '%s': kind already exists in schema", newKind)
		}

		tx, err := db.Begin()
		if err != nil {
			return "", fmt.Errorf("begin transaction: %w", err)
		}
		defer tx.Rollback()

		res, err := tx.Exec("UPDATE nodes SET kind = ? WHERE kind = ?", newKind, canonicalOld)
		if err != nil {
			return "", fmt.Errorf("update nodes kind: %w", err)
		}
		nodesUpdated, _ := res.RowsAffected()

		if _, err := tx.Exec("UPDATE schema_kinds SET kind = ? WHERE kind = ?", newKind, canonicalOld); err != nil {
			return "", fmt.Errorf("update schema_kinds: %w", err)
		}
		if _, err := tx.Exec("UPDATE schema_relations SET from_kind = ? WHERE from_kind = ?", newKind, canonicalOld); err != nil {
			return "", fmt.Errorf("update schema_relations from_kind: %w", err)
		}
		if _, err := tx.Exec("UPDATE schema_relations SET to_kind = ? WHERE to_kind = ?", newKind, canonicalOld); err != nil {
			return "", fmt.Errorf("update schema_relations to_kind: %w", err)
		}

		detail := fmt.Sprintf("renamed to %s (updated %d nodes)", newKind, nodesUpdated)
		if err := logSchemaChange(tx, "rename_kind", "kind", canonicalOld, detail); err != nil {
			return "", fmt.Errorf("log schema change: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return "", fmt.Errorf("commit rename kind: %w", err)
		}
		return fmt.Sprintf("Node kind '%s' renamed to '%s' (updated %d nodes and schema rules).", canonicalOld, newKind, nodesUpdated), nil

	case "add_relation":
		relation = strings.TrimSpace(relation)
		fromKind = strings.TrimSpace(fromKind)
		toKind = strings.TrimSpace(toKind)
		if relation == "" || fromKind == "" || toKind == "" {
			return "", fmt.Errorf("'relation', 'from_kind', and 'to_kind' are required for add_relation")
		}
		if err := cyphersql.ValidateIdentifier("relationship type", relation); err != nil {
			return "", err
		}
		if err := cyphersql.ValidateIdentifier("kind", fromKind); err != nil {
			return "", err
		}
		if err := cyphersql.ValidateIdentifier("kind", toKind); err != nil {
			return "", err
		}

		tx, err := db.Begin()
		if err != nil {
			return "", fmt.Errorf("begin transaction: %w", err)
		}
		defer tx.Rollback()

		// Canonicalize fromKind and toKind from schema_kinds if they exist
		var canFrom, canTo string
		if err := tx.QueryRow("SELECT kind FROM schema_kinds WHERE kind = ? COLLATE NOCASE", fromKind).Scan(&canFrom); err == nil {
			fromKind = canFrom
		} else {
			if _, err := tx.Exec("INSERT OR IGNORE INTO schema_kinds (kind, description) VALUES (?, '')", fromKind); err != nil {
				return "", fmt.Errorf("ensure from_kind: %w", err)
			}
		}
		if err := tx.QueryRow("SELECT kind FROM schema_kinds WHERE kind = ? COLLATE NOCASE", toKind).Scan(&canTo); err == nil {
			toKind = canTo
		} else {
			if _, err := tx.Exec("INSERT OR IGNORE INTO schema_kinds (kind, description) VALUES (?, '')", toKind); err != nil {
				return "", fmt.Errorf("ensure to_kind: %w", err)
			}
		}

		_, err = tx.Exec(`
			INSERT INTO schema_relations (rel_type, from_kind, to_kind, description) VALUES (?, ?, ?, ?)
			ON CONFLICT(rel_type, from_kind, to_kind) DO UPDATE SET description = excluded.description;
		`, relation, fromKind, toKind, description)
		if err != nil {
			return "", fmt.Errorf("add relation: %w", err)
		}

		detail := fmt.Sprintf("(%s)-[:%s]->(%s): %s", fromKind, relation, toKind, description)
		if err := logSchemaChange(tx, "add_relation", "relation", relation, detail); err != nil {
			return "", fmt.Errorf("log schema change: %w", err)
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

		// Count existing edges matching this relation
		var edgeCount int
		countSQL := `
			SELECT count(*) 
			FROM edges e
			JOIN nodes nf ON e.from_id = nf.id
			JOIN nodes nt ON e.to_id = nt.id
			WHERE e.kind = ? COLLATE NOCASE 
			  AND nf.kind = ? COLLATE NOCASE 
			  AND nt.kind = ? COLLATE NOCASE;
		`
		if err := db.QueryRow(countSQL, relation, fromKind, toKind).Scan(&edgeCount); err != nil {
			return "", fmt.Errorf("count existing edges: %w", err)
		}

		if edgeCount > 0 && !opts.Cascade && opts.MigrateTo == "" {
			return "", fmt.Errorf("cannot remove relation '%s': %d edges exist between %s and %s. Use cascade=true to delete data or migrate_to to reassign edges to another relation type", relation, edgeCount, fromKind, toKind)
		}

		tx, err := db.Begin()
		if err != nil {
			return "", fmt.Errorf("begin transaction: %w", err)
		}
		defer tx.Rollback()

		if opts.MigrateTo != "" {
			migrateTo := strings.TrimSpace(opts.MigrateTo)
			if err := cyphersql.ValidateIdentifier("relationship type", migrateTo); err != nil {
				return "", fmt.Errorf("invalid migrate_to relation %q: %w", migrateTo, err)
			}
			migrateSQL := `
				UPDATE edges SET kind = ?
				WHERE kind = ? COLLATE NOCASE
				  AND from_id IN (SELECT id FROM nodes WHERE kind = ? COLLATE NOCASE)
				  AND to_id IN (SELECT id FROM nodes WHERE kind = ? COLLATE NOCASE);
			`
			if _, err := tx.Exec(migrateSQL, migrateTo, relation, fromKind, toKind); err != nil {
				return "", fmt.Errorf("migrate edges: %w", err)
			}
			if _, err := tx.Exec("DELETE FROM schema_relations WHERE rel_type = ? COLLATE NOCASE AND from_kind = ? COLLATE NOCASE AND to_kind = ? COLLATE NOCASE", relation, fromKind, toKind); err != nil {
				return "", fmt.Errorf("delete schema relation: %w", err)
			}
			detail := fmt.Sprintf("migrated %d edges (%s)->(%s) to %s", edgeCount, fromKind, toKind, migrateTo)
			if err := logSchemaChange(tx, "remove_relation", "relation", relation, detail); err != nil {
				return "", fmt.Errorf("log schema change: %w", err)
			}
			if err := tx.Commit(); err != nil {
				return "", fmt.Errorf("commit remove relation with migration: %w", err)
			}
			return fmt.Sprintf("Relationship (%s)-[:%s]->(%s) removed from schema, %d edges migrated to '%s'.", fromKind, relation, toKind, edgeCount, migrateTo), nil
		}

		if opts.Cascade {
			deleteEdgesSQL := `
				DELETE FROM edges 
				WHERE kind = ? COLLATE NOCASE
				  AND from_id IN (SELECT id FROM nodes WHERE kind = ? COLLATE NOCASE)
				  AND to_id IN (SELECT id FROM nodes WHERE kind = ? COLLATE NOCASE);
			`
			if _, err := tx.Exec(deleteEdgesSQL, relation, fromKind, toKind); err != nil {
				return "", fmt.Errorf("cascade delete edges: %w", err)
			}
		}

		if _, err := tx.Exec("DELETE FROM schema_relations WHERE rel_type = ? COLLATE NOCASE AND from_kind = ? COLLATE NOCASE AND to_kind = ? COLLATE NOCASE", relation, fromKind, toKind); err != nil {
			return "", fmt.Errorf("delete schema relation: %w", err)
		}
		detail := fmt.Sprintf("cascade=%v deleted %d edges between %s and %s", opts.Cascade, edgeCount, fromKind, toKind)
		if err := logSchemaChange(tx, "remove_relation", "relation", relation, detail); err != nil {
			return "", fmt.Errorf("log schema change: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return "", fmt.Errorf("commit remove relation: %w", err)
		}
		return fmt.Sprintf("Relationship (%s)-[:%s]->(%s) removed from schema.", fromKind, relation, toKind), nil

	case "rename_relation":
		relation = strings.TrimSpace(relation)
		newRelation := strings.TrimSpace(opts.NewRelation)
		fromKind = strings.TrimSpace(fromKind)
		toKind = strings.TrimSpace(toKind)
		if relation == "" || newRelation == "" {
			return "", fmt.Errorf("'relation' and 'new_relation' are required for rename_relation")
		}
		if err := cyphersql.ValidateIdentifier("relationship type", newRelation); err != nil {
			return "", err
		}

		tx, err := db.Begin()
		if err != nil {
			return "", fmt.Errorf("begin transaction: %w", err)
		}
		defer tx.Rollback()

		var edgesUpdated int64
		if fromKind != "" && toKind != "" {
			if _, err := tx.Exec(`
				UPDATE schema_relations 
				SET rel_type = ? 
				WHERE rel_type = ? COLLATE NOCASE 
				  AND from_kind = ? COLLATE NOCASE 
				  AND to_kind = ? COLLATE NOCASE
			`, newRelation, relation, fromKind, toKind); err != nil {
				return "", fmt.Errorf("update schema_relations: %w", err)
			}

			res, err := tx.Exec(`
				UPDATE edges SET kind = ?
				WHERE kind = ? COLLATE NOCASE
				  AND from_id IN (SELECT id FROM nodes WHERE kind = ? COLLATE NOCASE)
				  AND to_id IN (SELECT id FROM nodes WHERE kind = ? COLLATE NOCASE)
			`, newRelation, relation, fromKind, toKind)
			if err != nil {
				return "", fmt.Errorf("update edges: %w", err)
			}
			edgesUpdated, _ = res.RowsAffected()
		} else {
			if _, err := tx.Exec("UPDATE schema_relations SET rel_type = ? WHERE rel_type = ? COLLATE NOCASE", newRelation, relation); err != nil {
				return "", fmt.Errorf("update schema_relations: %w", err)
			}
			res, err := tx.Exec("UPDATE edges SET kind = ? WHERE kind = ? COLLATE NOCASE", newRelation, relation)
			if err != nil {
				return "", fmt.Errorf("update edges: %w", err)
			}
			edgesUpdated, _ = res.RowsAffected()
		}

		detail := fmt.Sprintf("renamed to %s (updated %d edges)", newRelation, edgesUpdated)
		if err := logSchemaChange(tx, "rename_relation", "relation", relation, detail); err != nil {
			return "", fmt.Errorf("log schema change: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return "", fmt.Errorf("commit rename relation: %w", err)
		}
		return fmt.Sprintf("Relationship '%s' renamed to '%s' (updated %d edges and schema rules).", relation, newRelation, edgesUpdated), nil

	default:
		return "", fmt.Errorf("unknown schema action '%s'. Allowed: add_kind, remove_kind, rename_kind, add_relation, remove_relation, rename_relation", action)
	}
}

// ── Server Bootstrap & Event Loop ──────────────────────────────────────────

func main() {
	dbPath := flag.String("db", "knowledge_graph.db", "Path to SQLite database file")
	logPath := flag.String("log", "", "Path to debug log file")
	strictSchema := flag.Bool("strict-schema", false, "Strictly enforce schema even if schema tables are empty")
	allowSchemaEdit := flag.Bool("allow-schema-edit", false, "Allow schema modifications via graph_schema_define tool")
	maxRows := flag.Int("max-rows", 1000, "Maximum number of rows returned by graph_query (0 = unlimited)")
	flag.Parse()

	if *maxRows >= 0 {
		defaultMaxRows = *maxRows
	}

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

	logMsg("Starting cypher-mcp with db=%s, strict-schema=%v, allow-schema-edit=%v", *dbPath, *strictSchema, *allowSchemaEdit)

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

	runServerWithConfig(os.Stdin, os.Stdout, db, dbRO, logger, *strictSchema, *allowSchemaEdit)
}

func runServer(in io.Reader, out io.Writer, db, dbRO *sql.DB, logger *log.Logger, flags ...bool) {
	strict := len(flags) > 0 && flags[0]
	allowEdit := len(flags) > 1 && flags[1]
	runServerWithConfig(in, out, db, dbRO, logger, strict, allowEdit)
}

func runServerWithConfig(in io.Reader, out io.Writer, db, dbRO *sql.DB, logger *log.Logger, isStrict, allowSchemaEdit bool) {
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
								"version": "0.3.1",
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
						effectiveLimit := defaultMaxRows
						if lVal, ok := params.Arguments["limit"]; ok {
							switch v := lVal.(type) {
							case float64:
								effectiveLimit = int(v)
							case int:
								effectiveLimit = v
							}
						}
						outText, callErr = handleGraphQuery(dbRO, query, queryParams, effectiveLimit)

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
						merge := true
						if mVal, ok := params.Arguments["merge"].(bool); ok {
							merge = mVal
						}
						outText, callErr = handleSetNode(db, id, kind, props, isStrict, merge)

					case "graph_set_edge":
						from, _ := params.Arguments["from"].(string)
						to, _ := params.Arguments["to"].(string)
						kind, _ := params.Arguments["kind"].(string)
						props, _ := params.Arguments["properties"].(map[string]any)
						merge := true
						if mVal, ok := params.Arguments["merge"].(bool); ok {
							merge = mVal
						}
						outText, callErr = handleSetEdge(db, from, to, kind, props, isStrict, merge)

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
						newKind, _ := params.Arguments["new_kind"].(string)
						newRelation, _ := params.Arguments["new_relation"].(string)
						cascade, _ := params.Arguments["cascade"].(bool)
						migrateTo, _ := params.Arguments["migrate_to"].(string)
						opts := SchemaDefineOptions{
							AllowSchemaEdit: allowSchemaEdit,
							Cascade:         cascade,
							MigrateTo:       migrateTo,
							NewKind:         newKind,
							NewRelation:     newRelation,
						}
						outText, callErr = handleSchemaDefine(db, action, kind, relation, fromKind, toKind, description, opts)

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
