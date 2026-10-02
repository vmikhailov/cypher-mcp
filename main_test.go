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

	// 3. Query via OpenCypher
	resStr, err := handleGraphQuery(db,
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

	// 4. Schema check
	schemaStr, err := handleSchema(db)
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

	schemaStrAfter, err := handleSchema(db)
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
