package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMCPServerLifecycle(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cypher-mcp-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test_graph.db")
	db, err := initDatabase(dbPath)
	if err != nil {
		t.Fatalf("failed to init database: %v", err)
	}
	defer db.Close()

	dbRO, err := initRODatabase(dbPath)
	if err != nil {
		t.Fatalf("failed to init ro database: %v", err)
	}
	defer dbRO.Close()

	// 1. Create nodes
	msg1, err := handleSetNode(db, "person:alice", "Person", map[string]any{
		"name": "Alice", "role": "Engineer",
	})
	if err != nil {
		t.Fatalf("handleSetNode 1 failed: %v", err)
	}
	if !strings.Contains(msg1, "upserted successfully") {
		t.Fatalf("unexpected message: %s", msg1)
	}

	msg2, err := handleSetNode(db, "service:billing", "Service", map[string]any{
		"name": "Billing API", "tier": 1,
	})
	if err != nil {
		t.Fatalf("handleSetNode 2 failed: %v", err)
	}
	if !strings.Contains(msg2, "upserted successfully") {
		t.Fatalf("unexpected message: %s", msg2)
	}

	// 2. Create edge
	msgEdge, err := handleSetEdge(db, "person:alice", "service:billing", "MAINTAINS", map[string]any{
		"since": 2024,
	})
	if err != nil {
		t.Fatalf("handleSetEdge failed: %v", err)
	}
	if !strings.Contains(msgEdge, "saved successfully") {
		t.Fatalf("unexpected message: %s", msgEdge)
	}

	// 3. Query via OpenCypher using dbRO
	resStr, err := handleGraphQuery(dbRO,
		"MATCH (p:Person {name: 'Alice'})-[:MAINTAINS]->(s:Service) RETURN p.name AS dev, s.name AS svc")
	if err != nil {
		t.Fatalf("handleQuery failed: %v", err)
	}

	var qRes struct {
		Count   int              `json:"count"`
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal([]byte(resStr), &qRes); err != nil {
		t.Fatalf("unmarshal query result failed: %v", err)
	}
	if qRes.Count != 1 {
		t.Fatalf("expected 1 result, got %d", qRes.Count)
	}
	if qRes.Results[0]["dev"] != "Alice" || qRes.Results[0]["svc"] != "Billing API" {
		t.Fatalf("unexpected query result: %+v", qRes.Results)
	}

	// 4. Schema check using dbRO
	schemaStr, err := handleSchema(dbRO)
	if err != nil {
		t.Fatalf("handleSchema failed: %v", err)
	}
	var sRes struct {
		TotalNodes int `json:"total_nodes"`
		TotalEdges int `json:"total_edges"`
	}
	if err := json.Unmarshal([]byte(schemaStr), &sRes); err != nil {
		t.Fatalf("unmarshal schema failed: %v", err)
	}
	if sRes.TotalNodes != 2 || sRes.TotalEdges != 1 {
		t.Fatalf("unexpected counts: nodes=%d, edges=%d", sRes.TotalNodes, sRes.TotalEdges)
	}

	// 5. Delete node and test cascade
	msgDel, err := handleDeleteNode(db, "service:billing")
	if err != nil {
		t.Fatalf("handleDeleteNode failed: %v", err)
	}
	if !strings.Contains(msgDel, "deleted") {
		t.Fatalf("unexpected delete message: %s", msgDel)
	}

	schemaStrAfter, err := handleSchema(dbRO)
	if err != nil {
		t.Fatalf("handleSchema after delete failed: %v", err)
	}
	if err := json.Unmarshal([]byte(schemaStrAfter), &sRes); err != nil {
		t.Fatalf("unmarshal schema failed: %v", err)
	}
	if sRes.TotalNodes != 1 || sRes.TotalEdges != 0 {
		t.Fatalf("expected 1 node and 0 edges, got nodes=%d, edges=%d", sRes.TotalNodes, sRes.TotalEdges)
	}
}

func TestSecurity_IdentifierInjectionRejected(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cypher-mcp-sec-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "sec_graph.db")
	db, err := initDatabase(dbPath)
	if err != nil {
		t.Fatalf("failed to init database: %v", err)
	}
	defer db.Close()

	dbRO, err := initRODatabase(dbPath)
	if err != nil {
		t.Fatalf("failed to init ro database: %v", err)
	}
	defer dbRO.Close()

	// Seed 3 nodes
	for i := 1; i <= 3; i++ {
		_, err := handleSetNode(db, filepath.Base(tmpDir)+string(rune('0'+i)), "Person", map[string]any{"num": i})
		if err != nil {
			t.Fatalf("seed failed: %v", err)
		}
	}

	// Attempt identifier injection
	injectionQueries := []string{
		"MATCH (p:`Person'; DELETE FROM nodes; --`) RETURN count(p) AS c",
		"MATCH (p:`Person' UNION SELECT name FROM sqlite_master --`) RETURN p.name",
		"MATCH (a)-[:`CALLS'; DROP TABLE nodes; --`]->(b) RETURN a",
		"MATCH (p) WHERE p.`name' OR 1=1 --` = 'Alice' RETURN p",
	}

	for _, q := range injectionQueries {
		_, err := handleGraphQuery(dbRO, q)
		if err == nil {
			t.Fatalf("expected query %q to fail compilation, but succeeded", q)
		}
	}

	// Verify database is completely intact (all 3 nodes still exist)
	var count int
	if err := dbRO.QueryRow("SELECT count(*) FROM nodes").Scan(&count); err != nil {
		t.Fatalf("count failed: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected 3 nodes preserved, but found %d", count)
	}
}

func TestSecurity_ReadOnlyEnforced(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cypher-mcp-ro-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "ro_graph.db")
	db, err := initDatabase(dbPath)
	if err != nil {
		t.Fatalf("failed to init database: %v", err)
	}
	defer db.Close()

	dbRO, err := initRODatabase(dbPath)
	if err != nil {
		t.Fatalf("failed to init ro database: %v", err)
	}
	defer dbRO.Close()

	// Direct write on dbRO MUST fail with readonly error
	_, err = dbRO.Exec("INSERT INTO nodes (id, kind, properties) VALUES ('test', 'Test', '{}')")
	if err == nil {
		t.Fatalf("expected write to dbRO to fail, but succeeded")
	}
	if !strings.Contains(err.Error(), "readonly") {
		t.Fatalf("expected 'readonly' in error, got: %v", err)
	}
}

func TestSecurity_MultipleStatementsDetection(t *testing.T) {
	multiStmt := []string{
		"SELECT 1; DELETE FROM nodes;",
		"SELECT 1; DROP TABLE edges;",
		"SELECT * FROM nodes WHERE id = 1; SELECT * FROM edges",
		"SELECT 1;\n-- comment\nSELECT 2",
	}
	for _, sql := range multiStmt {
		if !hasMultipleStatements(sql) {
			t.Errorf("expected hasMultipleStatements=true for: %s", sql)
		}
	}

	singleStmt := []string{
		"SELECT 1",
		"SELECT 1;",
		"SELECT 1;\n",
		"SELECT 1; -- trailing comment\n",
		"SELECT 'semi;colon' FROM nodes",
		"SELECT \"quoted;col\" FROM nodes",
		"SELECT 1; /* block comment */",
	}
	for _, sql := range singleStmt {
		if hasMultipleStatements(sql) {
			t.Errorf("expected hasMultipleStatements=false for: %s", sql)
		}
	}
}

func TestVariableLengthRelationships_ErrorReturned(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cypher-mcp-hops-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "hops_graph.db")
	db, err := initDatabase(dbPath)
	if err != nil {
		t.Fatalf("failed to init database: %v", err)
	}
	defer db.Close()

	dbRO, err := initRODatabase(dbPath)
	if err != nil {
		t.Fatalf("failed to init ro database: %v", err)
	}
	defer dbRO.Close()

	_, err = handleGraphQuery(dbRO, "MATCH (p:Person)-[*1..3]-(a:Apartment) RETURN p, a")
	if err == nil {
		t.Fatalf("expected error for variable-length query, got nil")
	}
	if !strings.Contains(err.Error(), "variable-length") {
		t.Fatalf("expected error mentioning 'variable-length', got: %v", err)
	}
}

func TestHandleGraphQuery_WithParams(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cypher-mcp-params-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "params_graph.db")
	db, err := initDatabase(dbPath)
	if err != nil {
		t.Fatalf("failed to init database: %v", err)
	}
	defer db.Close()

	dbRO, err := initRODatabase(dbPath)
	if err != nil {
		t.Fatalf("failed to init ro database: %v", err)
	}
	defer dbRO.Close()

	if _, err := handleSetNode(db, "person:bob", "Person", map[string]any{"name": "Bob"}); err != nil {
		t.Fatalf("set node failed: %v", err)
	}
	if _, err := handleSetNode(db, "person:carol", "Person", map[string]any{"name": "Carol"}); err != nil {
		t.Fatalf("set node failed: %v", err)
	}

	resStr, err := handleGraphQuery(dbRO,
		"MATCH (p:Person) WHERE p.name = $target RETURN p.id AS id, p.name AS name",
		map[string]any{"target": "Bob"})
	if err != nil {
		t.Fatalf("handleGraphQuery with params failed: %v", err)
	}

	var qRes struct {
		Count   int              `json:"count"`
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal([]byte(resStr), &qRes); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if qRes.Count != 1 {
		t.Fatalf("expected 1 result, got %d", qRes.Count)
	}
	if qRes.Results[0]["name"] != "Bob" || qRes.Results[0]["id"] != "person:bob" {
		t.Fatalf("unexpected result: %+v", qRes.Results)
	}
}

func TestHandleSetEdge_ForeignKeysAndAtomicity(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cypher-mcp-edges-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "edges_graph.db")
	db, err := initDatabase(dbPath)
	if err != nil {
		t.Fatalf("failed to init database: %v", err)
	}
	defer db.Close()

	if _, err := handleSetNode(db, "n:node1", "Node", map[string]any{}); err != nil {
		t.Fatalf("seed node1 failed: %v", err)
	}

	// 1. Missing target node
	_, err = handleSetEdge(db, "n:node1", "n:nonexistent", "CONNECTS", nil)
	if err == nil {
		t.Fatalf("expected error for non-existent target node, but got nil")
	}
	if !strings.Contains(err.Error(), "target node 'n:nonexistent' does not exist") {
		t.Fatalf("unexpected error message: %v", err)
	}

	// 2. Missing source node
	_, err = handleSetEdge(db, "n:nonexistent", "n:node1", "CONNECTS", nil)
	if err == nil {
		t.Fatalf("expected error for non-existent source node, but got nil")
	}
	if !strings.Contains(err.Error(), "source node 'n:nonexistent' does not exist") {
		t.Fatalf("unexpected error message: %v", err)
	}

	// 3. Create target node and insert valid edge
	if _, err := handleSetNode(db, "n:node2", "Node", map[string]any{}); err != nil {
		t.Fatalf("seed node2 failed: %v", err)
	}
	_, err = handleSetEdge(db, "n:node1", "n:node2", "CONNECTS", map[string]any{"v": 1})
	if err != nil {
		t.Fatalf("valid edge insert failed: %v", err)
	}

	// 4. Upsert same edge with updated properties (atomic ON CONFLICT)
	_, err = handleSetEdge(db, "n:node1", "n:node2", "CONNECTS", map[string]any{"v": 2})
	if err != nil {
		t.Fatalf("edge upsert failed: %v", err)
	}

	// Verify only 1 edge exists with v=2
	var count int
	var propsStr string
	err = db.QueryRow("SELECT count(*), properties FROM edges WHERE from_id = 'n:node1' AND to_id = 'n:node2'").Scan(&count, &propsStr)
	if err != nil {
		t.Fatalf("query edges failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 edge, found %d", count)
	}
	if !strings.Contains(propsStr, `"v":2`) {
		t.Fatalf("expected updated property v:2, got %s", propsStr)
	}
}

func TestHandleGraphQuery_ReturnNodeAsObject(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cypher-mcp-nodeobj-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "nodeobj.db")
	db, err := initDatabase(dbPath)
	if err != nil {
		t.Fatalf("failed to init database: %v", err)
	}
	defer db.Close()

	dbRO, err := initRODatabase(dbPath)
	if err != nil {
		t.Fatalf("failed to init ro database: %v", err)
	}
	defer dbRO.Close()

	if _, err := handleSetNode(db, "person:alice", "Person", map[string]any{"city": "Prague", "age": 30}); err != nil {
		t.Fatalf("failed to set node: %v", err)
	}

	resStr, err := handleGraphQuery(dbRO, "MATCH (p:Person) RETURN p")
	if err != nil {
		t.Fatalf("handleGraphQuery failed: %v", err)
	}

	var qRes struct {
		Count   int              `json:"count"`
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal([]byte(resStr), &qRes); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if qRes.Count != 1 {
		t.Fatalf("expected 1 result, got %d", qRes.Count)
	}

	nodeVal, ok := qRes.Results[0]["p"]
	if !ok {
		t.Fatalf("expected key 'p' in result, got: %+v", qRes.Results[0])
	}

	// Must be parsed map, NOT a string!
	nodeMap, ok := nodeVal.(map[string]any)
	if !ok {
		t.Fatalf("expected 'p' to be map[string]any, got %T: %+v", nodeVal, nodeVal)
	}
	if nodeMap["id"] != "person:alice" || nodeMap["kind"] != "Person" {
		t.Fatalf("unexpected node content: %+v", nodeMap)
	}
}

func TestEmptyValidation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cypher-mcp-empty-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "empty.db")
	db, err := initDatabase(dbPath)
	if err != nil {
		t.Fatalf("failed to init database: %v", err)
	}
	defer db.Close()

	// Empty node id
	if _, err := handleSetNode(db, "", "Person", nil); err == nil {
		t.Fatalf("expected error for empty node id")
	}
	if _, err := handleSetNode(db, "   ", "Person", nil); err == nil {
		t.Fatalf("expected error for whitespace node id")
	}

	// Empty node kind
	if _, err := handleSetNode(db, "n1", "", nil); err == nil {
		t.Fatalf("expected error for empty node kind")
	}

	// Empty edge fields
	if _, err := handleSetEdge(db, "", "n2", "KNOWS", nil); err == nil {
		t.Fatalf("expected error for empty edge 'from'")
	}
	if _, err := handleSetEdge(db, "n1", "", "KNOWS", nil); err == nil {
		t.Fatalf("expected error for empty edge 'to'")
	}
	if _, err := handleSetEdge(db, "n1", "n2", "", nil); err == nil {
		t.Fatalf("expected error for empty edge 'kind'")
	}

	// Empty delete node id
	if _, err := handleDeleteNode(db, ""); err == nil {
		t.Fatalf("expected error for empty delete node id")
	}
}

func TestServer_MalformedJSON_ReturnsParseError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cypher-mcp-pipe-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "pipe.db")
	db, err := initDatabase(dbPath)
	if err != nil {
		t.Fatalf("failed to init database: %v", err)
	}
	defer db.Close()

	dbRO, err := initRODatabase(dbPath)
	if err != nil {
		t.Fatalf("failed to init ro database: %v", err)
	}
	defer dbRO.Close()

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	defer inR.Close()
	defer outR.Close()

	done := make(chan struct{})
	go func() {
		runServer(inR, outW, db, dbRO, nil)
		close(done)
	}()

	// Send broken JSON
	go func() {
		inW.Write([]byte("{ broken json ...\n"))
		inW.Close()
	}()

	scanner := bufio.NewScanner(outR)
	if !scanner.Scan() {
		t.Fatalf("expected response from server for malformed JSON, got none (EOF/timeout)")
	}
	line := scanner.Text()

	var resp JSONRPCResponse
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("failed to parse response JSON: %v, line: %s", err, line)
	}
	if resp.Error == nil {
		t.Fatalf("expected error in response, got nil. Line: %s", line)
	}
	errMap, ok := resp.Error.(map[string]any)
	if !ok {
		t.Fatalf("expected error to be map[string]any, got %T", resp.Error)
	}
	errCode, ok := errMap["code"].(float64)
	if !ok || int(errCode) != -32700 {
		t.Fatalf("expected error code -32700, got: %v", errMap["code"])
	}

	<-done
}

func TestVariableLengthRelationships_Success(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cypher-mcp-varlen-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "varlen_graph.db")
	db, err := initDatabase(dbPath)
	if err != nil {
		t.Fatalf("failed to init database: %v", err)
	}
	defer db.Close()

	dbRO, err := initRODatabase(dbPath)
	if err != nil {
		t.Fatalf("failed to init ro database: %v", err)
	}
	defer dbRO.Close()

	// Seed nodes: Anton -> Slava -> Istanbul Apt, and cycle Slava -> Anton
	if _, err := handleSetNode(db, "p:anton", "Person", map[string]any{"name": "Anton"}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	if _, err := handleSetNode(db, "p:slava", "Person", map[string]any{"name": "Slava"}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	if _, err := handleSetNode(db, "apt:1", "Apartment", map[string]any{"name": "Istanbul Apt"}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	if _, err := handleSetEdge(db, "p:anton", "p:slava", "KNOWS", nil); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	if _, err := handleSetEdge(db, "p:slava", "apt:1", "OWNS", nil); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	if _, err := handleSetEdge(db, "p:slava", "p:anton", "KNOWS", nil); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	resStr, err := handleGraphQuery(dbRO, "MATCH (a:Person {name: 'Anton'})-[*1..3]->(b:Apartment) RETURN a.name, b.name")
	if err != nil {
		t.Fatalf("handleGraphQuery failed: %v", err)
	}

	var qRes struct {
		Count   int              `json:"count"`
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal([]byte(resStr), &qRes); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if qRes.Count != 1 {
		t.Fatalf("expected 1 result, got %d", qRes.Count)
	}
	if qRes.Results[0]["a.name"] != "Anton" || qRes.Results[0]["b.name"] != "Istanbul Apt" {
		t.Fatalf("unexpected results: %+v", qRes.Results)
	}
}

func TestInitDatabase_LegacyDuplicateEdgesCleaned(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cypher-mcp-legacy-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "legacy.db")

	// 1. Manually create tables WITHOUT unique index (simulating legacy v0.1.0 database)
	rawDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed to open raw db: %v", err)
	}
	rawSchema := `
		CREATE TABLE nodes (id TEXT PRIMARY KEY, kind TEXT NOT NULL, properties TEXT NOT NULL);
		CREATE TABLE edges (from_id TEXT NOT NULL, to_id TEXT NOT NULL, kind TEXT NOT NULL, properties TEXT NOT NULL);
		INSERT INTO nodes VALUES ('n1', 'Person', '{}'), ('n2', 'Person', '{}');
		-- Insert duplicate edges
		INSERT INTO edges VALUES ('n1', 'n2', 'KNOWS', '{"version": 1}');
		INSERT INTO edges VALUES ('n1', 'n2', 'KNOWS', '{"version": 2}');
	`
	if _, err := rawDB.Exec(rawSchema); err != nil {
		t.Fatalf("failed to seed legacy db: %v", err)
	}
	rawDB.Close()

	// 2. Open via initDatabase: must NOT fail with "UNIQUE constraint failed" and must clean duplicates
	migratedDB, err := initDatabase(dbPath)
	if err != nil {
		t.Fatalf("initDatabase on legacy db with duplicates failed: %v", err)
	}
	defer migratedDB.Close()

	var edgeCount int
	if err := migratedDB.QueryRow("SELECT count(*) FROM edges WHERE from_id = 'n1' AND to_id = 'n2'").Scan(&edgeCount); err != nil {
		t.Fatalf("query edge count failed: %v", err)
	}
	if edgeCount != 1 {
		t.Fatalf("expected exactly 1 edge after deduplication, got: %d", edgeCount)
	}

	// Verify unique index was created and upserts succeed
	_, err = handleSetEdge(migratedDB, "n1", "n2", "KNOWS", map[string]any{"version": 3})
	if err != nil {
		t.Fatalf("handleSetEdge on migrated db failed: %v", err)
	}
}

func TestVariableLengthRelationships_UnanchoredRequiresLimit(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cypher-mcp-unanchored-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "unanchored.db")
	db, err := initDatabase(dbPath)
	if err != nil {
		t.Fatalf("failed to init database: %v", err)
	}
	defer db.Close()

	dbRO, err := initRODatabase(dbPath)
	if err != nil {
		t.Fatalf("failed to init ro database: %v", err)
	}
	defer dbRO.Close()

	// 1. Unanchored varlen query must fail requiring an anchor
	_, err = handleGraphQuery(dbRO, "MATCH (a)-[*1..2]->(b) RETURN a, b")
	if err == nil {
		t.Fatalf("expected unanchored varlen query without LIMIT to fail, but succeeded")
	}
	if !strings.Contains(err.Error(), "anchored") {
		t.Fatalf("expected error mentioning anchored, got: %v", err)
	}

	// 2. Unanchored even with LIMIT must fail requiring an anchor to prevent recursion explosion
	_, err = handleGraphQuery(dbRO, "MATCH (a)-[*1..2]->(b) RETURN a, b LIMIT 10")
	if err == nil {
		t.Fatalf("expected unanchored varlen query with LIMIT 10 to fail, but succeeded")
	}
	if !strings.Contains(err.Error(), "anchored") {
		t.Fatalf("expected error mentioning anchored, got: %v", err)
	}

	// 3. Label-only without property anchor must fail
	_, err = handleGraphQuery(dbRO, "MATCH (a:Person)-[*1..2]->(b) RETURN a, b LIMIT 10")
	if err == nil {
		t.Fatalf("expected label-only varlen query to fail without property anchor, but succeeded")
	}
	if !strings.Contains(err.Error(), "anchored") {
		t.Fatalf("expected error mentioning anchored, got: %v", err)
	}

	// 4. Anchored with property filter must succeed
	_, err = handleGraphQuery(dbRO, "MATCH (a:Person {name: 'Alice'})-[*1..2]->(b) RETURN a, b LIMIT 10")
	if err != nil {
		t.Fatalf("expected property-anchored varlen query to succeed, got error: %v", err)
	}
}

func TestGraphSearch_FTS5(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cypher-mcp-fts-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "fts.db")
	db, err := initDatabase(dbPath)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer db.Close()

	dbRO, err := initRODatabase(dbPath)
	if err != nil {
		t.Fatalf("failed to init ro db: %v", err)
	}
	defer dbRO.Close()

	// Seed nodes
	_, err = handleSetNode(db, "person:slava", "Person", map[string]any{
		"name":    "Vyacheslav Mikhailov",
		"name_ru": "Вячеслав Михайлов",
		"city":    "München",
		"note":    "Lives on Franziska-Schmitz-Str. 6",
	})
	if err != nil {
		t.Fatalf("set node 1 failed: %v", err)
	}

	_, err = handleSetNode(db, "person:katya", "Person", map[string]any{
		"name":    "Ekaterina Mikhailova",
		"name_ru": "Екатерина Михайлова",
		"city":    "München",
	})
	if err != nil {
		t.Fatalf("set node 2 failed: %v", err)
	}

	_, err = handleSetNode(db, "car:m_ew_330", "Vehicle", map[string]any{
		"brand":         "BMW",
		"model":         "X3",
		"license_plate": "M-EW 330",
	})
	if err != nil {
		t.Fatalf("set node 3 failed: %v", err)
	}

	_, err = handleSetNode(db, "apt:franziska_6", "Apartment", map[string]any{
		"address": "Franziska-Schmitz-Str. 6",
		"city":    "München",
		"plz":     80634,
	})
	if err != nil {
		t.Fatalf("set node 4 failed: %v", err)
	}

	// 1. Trigram substring matching in Russian: "Михайлов" should match BOTH Mikhailov and Mikhailova
	resStr, err := handleGraphSearch(dbRO, "Михайлов", "", 10)
	if err != nil {
		t.Fatalf("search Михайлов failed: %v", err)
	}
	var resM struct {
		Count   int                `json:"count"`
		Results []SearchResultItem `json:"results"`
	}
	if err := json.Unmarshal([]byte(resStr), &resM); err != nil {
		t.Fatalf("unmarshal resM failed: %v", err)
	}
	if resM.Count != 2 {
		t.Fatalf("expected 2 results for Михайлов (both Mikhailov and Mikhailova via trigram), got %d: %+v", resM.Count, resM.Results)
	}

	// 2. Search with umlaut / German characters: "München"
	resStr, err = handleGraphSearch(dbRO, "München", "", 10)
	if err != nil {
		t.Fatalf("search München failed: %v", err)
	}
	var res1 struct {
		Count   int                `json:"count"`
		Results []SearchResultItem `json:"results"`
	}
	if err := json.Unmarshal([]byte(resStr), &res1); err != nil {
		t.Fatalf("unmarshal res1 failed: %v", err)
	}
	if res1.Count != 3 {
		t.Fatalf("expected 3 results for München, got %d", res1.Count)
	}

	// 3. Short search (< 3 chars): "X3"
	resStr, err = handleGraphSearch(dbRO, "X3", "", 10)
	if err != nil {
		t.Fatalf("search X3 failed: %v", err)
	}
	var resShort struct {
		Count   int                `json:"count"`
		Results []SearchResultItem `json:"results"`
	}
	if err := json.Unmarshal([]byte(resStr), &resShort); err != nil {
		t.Fatalf("unmarshal resShort failed: %v", err)
	}
	if resShort.Count != 1 || resShort.Results[0].ID != "car:m_ew_330" {
		t.Fatalf("expected car:m_ew_330 for X3, got %+v", resShort)
	}

	// 4. Empty search results: must return [] (never null in JSON)
	resStr, err = handleGraphSearch(dbRO, "NonExistentWord12345", "", 10)
	if err != nil {
		t.Fatalf("search non-existent failed: %v", err)
	}
	if !strings.Contains(resStr, `"results": []`) {
		t.Fatalf("expected empty array '\"results\": []', got: %s", resStr)
	}
}

func TestGraphBatchUpsert_Atomicity(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cypher-mcp-batch-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "batch.db")
	db, err := initDatabase(dbPath)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer db.Close()

	// 1. Batch upsert nodes and edges together
	nodes := []BatchNodeItem{
		{ID: "person:timur", Kind: "Person", Properties: map[string]any{"name": "Timur"}},
		{ID: "uni:kit", Kind: "University", Properties: map[string]any{"name": "KIT Karlsruhe"}},
	}
	edges := []BatchEdgeItem{
		{From: "person:timur", To: "uni:kit", Kind: "STUDIES_AT", Properties: map[string]any{"since": 2026}},
	}

	msg, err := handleBatchUpsert(db, nodes, edges)
	if err != nil {
		t.Fatalf("batch upsert failed: %v", err)
	}
	if !strings.Contains(msg, "Successfully upserted 2 nodes and 1 edges") {
		t.Fatalf("unexpected batch message: %s", msg)
	}

	// Verify both exist
	var countNodes, countEdges int
	_ = db.QueryRow("SELECT count(*) FROM nodes").Scan(&countNodes)
	_ = db.QueryRow("SELECT count(*) FROM edges").Scan(&countEdges)
	if countNodes != 2 || countEdges != 1 {
		t.Fatalf("expected 2 nodes and 1 edge, got nodes=%d edges=%d", countNodes, countEdges)
	}

	// 2. Batch upsert failure should ROLL BACK all changes in the batch
	badNodes := []BatchNodeItem{
		{ID: "person:marat", Kind: "Person", Properties: map[string]any{"name": "Marat"}},
	}
	badEdges := []BatchEdgeItem{
		{From: "person:marat", To: "non_existent_node", Kind: "CONNECTED_TO"},
	}

	_, err = handleBatchUpsert(db, badNodes, badEdges)
	if err == nil {
		t.Fatalf("expected batch upsert to fail due to dangling edge, but succeeded")
	}
	if !strings.Contains(err.Error(), "target node 'non_existent_node' does not exist") {
		t.Fatalf("unexpected error message: %v", err)
	}

	// Verify person:marat was NOT saved (atomic rollback)
	var maratExists bool
	_ = db.QueryRow("SELECT EXISTS(SELECT 1 FROM nodes WHERE id = 'person:marat')").Scan(&maratExists)
	if maratExists {
		t.Fatalf("expected rollback: person:marat should NOT exist in database")
	}
}

func TestSchemaGovernanceAndValidation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cypher-mcp-schema-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "schema.db")
	db, err := initDatabase(dbPath)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer db.Close()

	// 1. Initially schema is empty -> permissive
	_, err = handleSetNode(db, "a:1", "AnyKind", nil)
	if err != nil {
		t.Fatalf("permissive node set failed: %v", err)
	}

	// 2. Define schema rules (schema editing is always allowed by default)
	_, err = handleSchemaDefine(db, "add_kind", "Person", "", "", "", "Human individual")
	if err != nil {
		t.Fatalf("add_kind Person failed: %v", err)
	}
	_, err = handleSchemaDefine(db, "add_kind", "Apartment", "", "", "", "Living residence")
	if err != nil {
		t.Fatalf("add_kind Apartment failed: %v", err)
	}
	_, err = handleSchemaDefine(db, "add_relation", "", "RENTS", "Person", "Apartment", "Lease agreement")
	if err != nil {
		t.Fatalf("add_relation RENTS failed: %v", err)
	}

	// 4. Now schema is enforced:
	// a) Unregistered kind must fail
	_, err = handleSetNode(db, "alien:1", "Alien", nil)
	if err == nil {
		t.Fatalf("expected set unknown kind Alien to fail, but succeeded")
	}
	if !strings.Contains(err.Error(), "node kind 'Alien' is not registered") {
		t.Fatalf("unexpected error message: %v", err)
	}

	// b) Allowed kinds succeed
	_, err = handleSetNode(db, "person:alice", "Person", map[string]any{"name": "Alice"})
	if err != nil {
		t.Fatalf("set Person node failed: %v", err)
	}
	_, err = handleSetNode(db, "apt:sunny", "Apartment", map[string]any{"city": "Munich"})
	if err != nil {
		t.Fatalf("set Apartment node failed: %v", err)
	}

	// c) Unregistered edge relation between Person and Apartment must fail
	_, err = handleSetEdge(db, "person:alice", "apt:sunny", "OWNS_VEHICLE", nil)
	if err == nil {
		t.Fatalf("expected illegal relation OWNS_VEHICLE to fail, but succeeded")
	}
	if !strings.Contains(err.Error(), "Allowed relations: [RENTS]") {
		t.Fatalf("unexpected error message: %v", err)
	}

	// d) Allowed edge relation succeeds
	_, err = handleSetEdge(db, "person:alice", "apt:sunny", "RENTS", nil)
	if err != nil {
		t.Fatalf("set legal relation RENTS failed: %v", err)
	}

	// e) Batch upsert adheres to schema
	badBatchNodes := []BatchNodeItem{
		{ID: "person:bob", Kind: "Person"},
		{ID: "apt:cosy", Kind: "Apartment"},
	}
	badBatchEdges := []BatchEdgeItem{
		{From: "person:bob", To: "apt:cosy", Kind: "WORKS_AT"},
	}
	_, err = handleBatchUpsert(db, badBatchNodes, badBatchEdges)
	if err == nil {
		t.Fatalf("expected batch upsert with invalid schema relation to fail, but succeeded")
	}
	if !strings.Contains(err.Error(), "Allowed relations: [RENTS]") {
		t.Fatalf("unexpected batch schema error: %v", err)
	}

	// 5. Verify handleSchema returns active rules
	schemaStr, err := handleSchema(db)
	if err != nil {
		t.Fatalf("handleSchema failed: %v", err)
	}
	if !strings.Contains(schemaStr, `"schema_enforced": true`) {
		t.Fatalf("expected schema_enforced to be true, got: %s", schemaStr)
	}
	if !strings.Contains(schemaStr, "RENTS") || !strings.Contains(schemaStr, "Person") {
		t.Fatalf("expected schema to contain RENTS and Person, got: %s", schemaStr)
	}
}

func TestSchema_V030_AllFixes(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cypher-mcp-v030-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test_v030.db")
	db, err := initDatabase(dbPath)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer db.Close()

	dbRO, err := initRODatabase(dbPath)
	if err != nil {
		t.Fatalf("failed to init ro db: %v", err)
	}
	defer dbRO.Close()

	// 1. P1: Identifier validation rules (only ASCII alphanumeric + underscore)
	_, err = handleSchemaDefine(db, "add_kind", "Проект", "", "", "", "", true)
	if err == nil || !strings.Contains(err.Error(), "alphanumeric") {
		t.Fatalf("expected non-ASCII identifier 'Проект' to be rejected, got: %v", err)
	}
	_, err = handleSchemaDefine(db, "add_kind", "my-kind", "", "", "", "", true)
	if err == nil || !strings.Contains(err.Error(), "alphanumeric") {
		t.Fatalf("expected hyphenated identifier 'my-kind' to be rejected, got: %v", err)
	}

	// 2. P1: Case-insensitive uniqueness for kind
	_, err = handleSchemaDefine(db, "add_kind", "Person", "", "", "", "Person kind", true)
	if err != nil {
		t.Fatalf("failed to add Person: %v", err)
	}
	_, err = handleSchemaDefine(db, "add_kind", "person", "", "", "", "Duplicate person kind", true)
	if err == nil || !strings.Contains(err.Error(), "case-insensitive uniqueness required") {
		t.Fatalf("expected duplicate case-insensitive kind 'person' to fail, got: %v", err)
	}

	// Add second kind and relation
	_, err = handleSchemaDefine(db, "add_kind", "Company", "", "", "", "Company kind", true)
	if err != nil {
		t.Fatalf("failed to add Company: %v", err)
	}
	_, err = handleSchemaDefine(db, "add_relation", "", "WORKS_FOR", "Person", "Company", "Employment", true)
	if err != nil {
		t.Fatalf("failed to add relation: %v", err)
	}

	// Insert nodes and edges
	_, err = handleSetNode(db, "p1", "Person", map[string]any{"name": "Вячеслав Михайлов", "role": "engineer"})
	if err != nil {
		t.Fatalf("set node p1: %v", err)
	}
	_, err = handleSetNode(db, "c1", "Company", map[string]any{"title": "Tech Corp", "city": "Munich"})
	if err != nil {
		t.Fatalf("set node c1: %v", err)
	}
	_, err = handleSetEdge(db, "p1", "c1", "WORKS_FOR", map[string]any{"since": 2020})
	if err != nil {
		t.Fatalf("set edge: %v", err)
	}

	// 3. P0: Cyrillic text in Cypher queries works properly
	cyrRes, err := handleGraphQuery(dbRO, "MATCH (p:Person {name: 'Вячеслав Михайлов'}) RETURN p.role AS role, p.name AS name")
	if err != nil {
		t.Fatalf("cyrillic query failed: %v", err)
	}
	if !strings.Contains(cyrRes, "Вячеслав Михайлов") || !strings.Contains(cyrRes, "engineer") {
		t.Fatalf("unexpected cyrillic query response: %s", cyrRes)
	}

	// 4. P2: graph_search does NOT find by JSON property keys (e.g. searching 'name' should not match p1 whose property name is 'name')
	searchKeyRes, err := handleGraphSearch(dbRO, "role", "", 10)
	if err != nil {
		t.Fatalf("search 'role' failed: %v", err)
	}
	// 'role' is a key in p1, but its value is 'engineer'. If searching 'role', it should not find p1 unless 'role' appears in value or ID.
	if strings.Contains(searchKeyRes, `"p1"`) {
		t.Fatalf("search for JSON key 'role' incorrectly matched node p1: %s", searchKeyRes)
	}

	// Searching with inflected Russian forms ('Михайлова', 'Михайловым') should find p1 ('Михайлов')
	for _, inflected := range []string{"Михайлова", "Михайловым"} {
		searchValRes, err := handleGraphSearch(dbRO, inflected, "", 10)
		if err != nil {
			t.Fatalf("search '%s' failed: %v", inflected, err)
		}
		if !strings.Contains(searchValRes, `"id": "p1"`) {
			t.Fatalf("expected inflected search '%s' to match 'Михайлов' in p1, got: %s", inflected, searchValRes)
		}
	}

	// 5. P2: Empty search returns results: []
	emptySearchRes, err := handleGraphSearch(dbRO, "NonExistentTermXYZ999", "", 10)
	if err != nil {
		t.Fatalf("empty search failed: %v", err)
	}
	if !strings.Contains(emptySearchRes, `"results": []`) {
		t.Fatalf("expected empty search to return 'results: []', got: %s", emptySearchRes)
	}

	// 6. P1: remove_kind without cascade or migrate_to must reject and state counts
	_, err = handleSchemaDefine(db, "remove_kind", "Person", "", "", "", "", SchemaDefineOptions{AllowSchemaEdit: true})
	if err == nil || !strings.Contains(err.Error(), "1 nodes (and 1 related edges) exist") {
		t.Fatalf("expected remove_kind to reject with count info, got: %v", err)
	}

	// 7. P1: rename_kind changes schema and node kinds in one transaction
	_, err = handleSchemaDefine(db, "rename_kind", "Person", "", "", "", "", SchemaDefineOptions{AllowSchemaEdit: true, NewKind: "Human"})
	if err != nil {
		t.Fatalf("rename_kind failed: %v", err)
	}
	var nodeKind string
	err = db.QueryRow("SELECT kind FROM nodes WHERE id = 'p1'").Scan(&nodeKind)
	if err != nil || nodeKind != "Human" {
		t.Fatalf("expected node p1 kind to be 'Human', got: %s (err: %v)", nodeKind, err)
	}

	// 8. P1: remove_relation without cascade/migrate rejects when edges exist
	_, err = handleSchemaDefine(db, "remove_relation", "", "WORKS_FOR", "Human", "Company", "", SchemaDefineOptions{AllowSchemaEdit: true})
	if err == nil || !strings.Contains(err.Error(), "1 edges exist") {
		t.Fatalf("expected remove_relation to reject with count, got: %v", err)
	}

	// 9. P1: rename_relation renames relation in schema and edges
	_, err = handleSchemaDefine(db, "rename_relation", "", "WORKS_FOR", "Human", "Company", "", SchemaDefineOptions{AllowSchemaEdit: true, NewRelation: "EMPLOYED_BY"})
	if err != nil {
		t.Fatalf("rename_relation failed: %v", err)
	}
	var edgeKind string
	err = db.QueryRow("SELECT kind FROM edges WHERE from_id = 'p1' AND to_id = 'c1'").Scan(&edgeKind)
	if err != nil || edgeKind != "EMPLOYED_BY" {
		t.Fatalf("expected edge kind to be 'EMPLOYED_BY', got: %s (err: %v)", edgeKind, err)
	}

	// 10. P1: remove_kind with migrate_to
	_, err = handleSchemaDefine(db, "add_kind", "Individual", "", "", "", "Migrate target", true)
	if err != nil {
		t.Fatalf("add_kind Individual: %v", err)
	}
	_, err = handleSchemaDefine(db, "remove_kind", "Human", "", "", "", "", SchemaDefineOptions{AllowSchemaEdit: true, MigrateTo: "Individual"})
	if err != nil {
		t.Fatalf("remove_kind with migrate_to failed: %v", err)
	}
	err = db.QueryRow("SELECT kind FROM nodes WHERE id = 'p1'").Scan(&nodeKind)
	if err != nil || nodeKind != "Individual" {
		t.Fatalf("expected node p1 kind to be 'Individual', got: %s (err: %v)", nodeKind, err)
	}

	// 11. P1: remove_kind with cascade=true removes remaining node and edges
	_, err = handleSchemaDefine(db, "remove_kind", "Individual", "", "", "", "", SchemaDefineOptions{AllowSchemaEdit: true, Cascade: true})
	if err != nil {
		t.Fatalf("remove_kind with cascade failed: %v", err)
	}
	var p1Exists bool
	_ = db.QueryRow("SELECT EXISTS(SELECT 1 FROM nodes WHERE id = 'p1')").Scan(&p1Exists)
	if p1Exists {
		t.Fatalf("expected node p1 to be deleted by cascade")
	}

	// 12. P2: schema changelog has entries and appears in handleSchema
	schemaInfo, err := handleSchema(db)
	if err != nil {
		t.Fatalf("handleSchema failed: %v", err)
	}
	if !strings.Contains(schemaInfo, `"changelog": [`) || !strings.Contains(schemaInfo, "rename_kind") {
		t.Fatalf("expected schema to include changelog entries, got: %s", schemaInfo)
	}
}

func TestHandleGraphQuery_RowsErrHandling(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cypher-mcp-rowserr-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test_rowserr.db")
	db, err := initDatabase(dbPath)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer db.Close()

	// Seed several nodes
	for i := 0; i < 50; i++ {
		_, err := handleSetNode(db, fmt.Sprintf("n%d", i), "Item", map[string]any{"val": i})
		if err != nil {
			t.Fatalf("seed node %d failed: %v", i, err)
		}
	}

	// Create an already-cancelled or instantly expiring context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err = handleGraphQueryContext(ctx, db, "MATCH (n:Item) RETURN n.val AS val")
	if err == nil {
		t.Fatalf("expected query with cancelled context to fail, but succeeded")
	}
	if !strings.Contains(err.Error(), "context canceled") && !strings.Contains(err.Error(), "row iteration error") {
		t.Fatalf("expected error mentioning context canceled or row iteration error, got: %v", err)
	}
}

func TestHandleGraphQuery_RowLimit(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cypher-mcp-limit-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test_limit.db")
	db, err := initDatabase(dbPath)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer db.Close()

	for i := 0; i < 25; i++ {
		_, err := handleSetNode(db, fmt.Sprintf("node_%02d", i), "Item", map[string]any{"val": i})
		if err != nil {
			t.Fatalf("seed node %d failed: %v", i, err)
		}
	}

	// 1. Query with rowLimit = 10
	resJSON, err := handleGraphQuery(db, "MATCH (n:Item) RETURN n.id AS id ORDER BY id", nil, 10)
	if err != nil {
		t.Fatalf("handleGraphQuery with limit failed: %v", err)
	}
	var res map[string]any
	if err := json.Unmarshal([]byte(resJSON), &res); err != nil {
		t.Fatalf("unmarshal result failed: %v", err)
	}
	if int(res["count"].(float64)) != 10 {
		t.Fatalf("expected count 10, got %v", res["count"])
	}
	if res["truncated"] != true {
		t.Fatalf("expected truncated to be true, got %v", res["truncated"])
	}
	if _, ok := res["warning"].(string); !ok {
		t.Fatalf("expected warning message when truncated")
	}

	// 2. Query with default limit (should return all 25)
	resJSON2, err := handleGraphQuery(db, "MATCH (n:Item) RETURN n.id AS id ORDER BY id")
	if err != nil {
		t.Fatalf("handleGraphQuery with default limit failed: %v", err)
	}
	var res2 map[string]any
	if err := json.Unmarshal([]byte(resJSON2), &res2); err != nil {
		t.Fatalf("unmarshal result 2 failed: %v", err)
	}
	if int(res2["count"].(float64)) != 25 {
		t.Fatalf("expected count 25, got %v", res2["count"])
	}
	if res2["truncated"] == true {
		t.Fatalf("expected truncated to be false/nil for 25 rows under default limit")
	}
}

func TestHandleSetNode_MergeDefaultAndOverwrite(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cypher-mcp-merge-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test_merge.db")
	db, err := initDatabase(dbPath)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer db.Close()

	// 1. Initial node creation
	_, err = handleSetNode(db, "srv1", "Server", map[string]any{"cpu": 4, "ram": 16})
	if err != nil {
		t.Fatalf("create node failed: %v", err)
	}

	// 2. Upsert with only owner (default merge = true)
	_, err = handleSetNode(db, "srv1", "Server", map[string]any{"owner": "Dmitry"})
	if err != nil {
		t.Fatalf("merge node failed: %v", err)
	}

	var propsStr string
	err = db.QueryRow("SELECT properties FROM nodes WHERE id = 'srv1'").Scan(&propsStr)
	if err != nil {
		t.Fatalf("select node properties failed: %v", err)
	}
	var props map[string]any
	_ = json.Unmarshal([]byte(propsStr), &props)

	if props["cpu"] != float64(4) || props["ram"] != float64(16) || props["owner"] != "Dmitry" {
		t.Fatalf("expected merged properties (cpu:4, ram:16, owner:Dmitry), got: %v", props)
	}

	// 3. Upsert with merge = false (complete overwrite)
	_, err = handleSetNode(db, "srv1", "Server", map[string]any{"owner": "Alice"}, false, false)
	if err != nil {
		t.Fatalf("overwrite node failed: %v", err)
	}

	err = db.QueryRow("SELECT properties FROM nodes WHERE id = 'srv1'").Scan(&propsStr)
	if err != nil {
		t.Fatalf("select node properties failed: %v", err)
	}
	var propsOverwrite map[string]any
	_ = json.Unmarshal([]byte(propsStr), &propsOverwrite)

	if _, exists := propsOverwrite["cpu"]; exists {
		t.Fatalf("expected 'cpu' to be overwritten/removed when merge=false, got: %v", propsOverwrite)
	}
	if propsOverwrite["owner"] != "Alice" {
		t.Fatalf("expected owner 'Alice', got: %v", propsOverwrite)
	}

	// 4. Edge merge test
	_, err = handleSetNode(db, "srv2", "Server", map[string]any{})
	if err != nil {
		t.Fatalf("create srv2 failed: %v", err)
	}
	_, err = handleSetEdge(db, "srv1", "srv2", "CONNECTS_TO", map[string]any{"port": 8080})
	if err != nil {
		t.Fatalf("create edge failed: %v", err)
	}

	// Merge edge properties
	_, err = handleSetEdge(db, "srv1", "srv2", "CONNECTS_TO", map[string]any{"protocol": "https"})
	if err != nil {
		t.Fatalf("merge edge failed: %v", err)
	}

	var edgePropsStr string
	err = db.QueryRow("SELECT properties FROM edges WHERE from_id = 'srv1' AND to_id = 'srv2' AND kind = 'CONNECTS_TO'").Scan(&edgePropsStr)
	if err != nil {
		t.Fatalf("select edge properties failed: %v", err)
	}
	var edgeProps map[string]any
	_ = json.Unmarshal([]byte(edgePropsStr), &edgeProps)

	if edgeProps["port"] != float64(8080) || edgeProps["protocol"] != "https" {
		t.Fatalf("expected merged edge properties (port:8080, protocol:https), got: %v", edgeProps)
	}
}

func TestHandleSetNode_KindChangeRelationshipValidation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cypher-mcp-kindval-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test_kindval.db")
	db, err := initDatabase(dbPath)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer db.Close()

	// Setup schema
	opts := SchemaDefineOptions{AllowSchemaEdit: true}
	handleSchemaDefine(db, "add_kind", "Person", "", "", "", "Person entity", opts)
	handleSchemaDefine(db, "add_kind", "Company", "", "", "", "Company entity", opts)
	handleSchemaDefine(db, "add_kind", "Pet", "", "", "", "Pet entity", opts)
	handleSchemaDefine(db, "add_relation", "", "WORKS_FOR", "Person", "Company", "Employment", opts)

	// Create nodes & edge
	_, err = handleSetNode(db, "alice", "Person", map[string]any{"name": "Alice"}, true)
	if err != nil {
		t.Fatalf("create alice failed: %v", err)
	}
	_, err = handleSetNode(db, "acme", "Company", map[string]any{"name": "Acme Corp"}, true)
	if err != nil {
		t.Fatalf("create acme failed: %v", err)
	}
	_, err = handleSetEdge(db, "alice", "acme", "WORKS_FOR", nil, true)
	if err != nil {
		t.Fatalf("create edge failed: %v", err)
	}

	// 1. Changing alice's kind to Pet should fail because Pet cannot have outgoing WORKS_FOR to Company
	_, err = handleSetNode(db, "alice", "Pet", map[string]any{"name": "Alice"}, true)
	if err == nil {
		t.Fatalf("expected changing alice to Pet to fail relationship validation, but succeeded")
	}
	if !strings.Contains(err.Error(), "outgoing relationship (:Pet)-[:WORKS_FOR]->(:Company) is not permitted") {
		t.Fatalf("unexpected error message: %v", err)
	}

	// 2. Changing acme's kind to Pet should fail because Person cannot have incoming WORKS_FOR to Pet
	_, err = handleSetNode(db, "acme", "Pet", map[string]any{"name": "Acme Corp"}, true)
	if err == nil {
		t.Fatalf("expected changing acme to Pet to fail relationship validation, but succeeded")
	}
	if !strings.Contains(err.Error(), "incoming relationship (:Person)-[:WORKS_FOR]->(:Pet) is not permitted") {
		t.Fatalf("unexpected error message: %v", err)
	}

	// 3. Batch upsert kind change should also be validated
	_, err = handleBatchUpsert(db, []BatchNodeItem{{ID: "alice", Kind: "Pet"}}, nil, true)
	if err == nil {
		t.Fatalf("expected batch upsert kind change to fail, but succeeded")
	}
	if !strings.Contains(err.Error(), "outgoing relationship (:Pet)-[:WORKS_FOR]->(:Company) is not permitted") {
		t.Fatalf("unexpected error message in batch upsert: %v", err)
	}

	// 4. Updating properties without kind change should succeed
	_, err = handleSetNode(db, "alice", "Person", map[string]any{"role": "Lead"}, true)
	if err != nil {
		t.Fatalf("updating alice properties with same kind failed: %v", err)
	}
}
