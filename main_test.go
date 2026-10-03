package main

import (
	"encoding/json"
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

	_, err = handleGraphQuery(dbRO, "MATCH (p:Person)-[*1..3]->(a:Apartment) RETURN p, a")
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
