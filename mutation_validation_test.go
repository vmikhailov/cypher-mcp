package main

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestBatchMalformedEdges_RejectsWithoutCommit(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := initDatabase(dbPath)
	if err != nil {
		t.Fatalf("initDatabase: %v", err)
	}
	defer db.Close()

	// Simulate dispatching graph_batch_upsert with malformed edges (object instead of array)
	// {"nodes":[{"id":"p1","kind":"Person"}],"edges":{"from":"p1","to":"p1","kind":"REL"}}
	rawArgs := `{"nodes":[{"id":"p1","kind":"Person"}],"edges":{"from":"p1","to":"p1","kind":"REL"}}`
	var args map[string]any
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		t.Fatalf("unmarshal args: %v", err)
	}

	callErr := dispatchToolCall(db, db, "graph_batch_upsert", args)
	if callErr == nil {
		t.Errorf("FAIL invariant F03: expected error for malformed edges in batch, but got success")
	}

	// Verify p1 was NOT committed
	var count int
	if err := db.QueryRow("SELECT count(*) FROM nodes WHERE id = 'p1'").Scan(&count); err != nil {
		t.Fatalf("query nodes: %v", err)
	}
	if count != 0 {
		t.Errorf("FAIL invariant F03: node p1 was committed despite malformed batch! count=%d", count)
	}
}

func TestBatchMalformedProperties_ReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := initDatabase(dbPath)
	if err != nil {
		t.Fatalf("initDatabase: %v", err)
	}
	defer db.Close()

	rawArgs := `{"nodes":[{"id":"p1","kind":"Person","properties":"invalid_string_properties"}]}`
	var args map[string]any
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		t.Fatalf("unmarshal args: %v", err)
	}

	callErr := dispatchToolCall(db, db, "graph_batch_upsert", args)
	if callErr == nil {
		t.Errorf("FAIL invariant F03: expected error for string properties in batch node, got success")
	}

	var count int
	if err := db.QueryRow("SELECT count(*) FROM nodes WHERE id = 'p1'").Scan(&count); err != nil {
		t.Fatalf("query nodes: %v", err)
	}
	if count != 0 {
		t.Errorf("FAIL invariant F03: node p1 was committed with empty properties! count=%d", count)
	}
}

func TestSetNode_MalformedProperties_ReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := initDatabase(dbPath)
	if err != nil {
		t.Fatalf("initDatabase: %v", err)
	}
	defer db.Close()

	// Insert initial node with properties
	if _, err := handleSetNode(db, "p1", "Person", map[string]any{"name": "Alice"}, false, false); err != nil {
		t.Fatalf("initial set node: %v", err)
	}

	// Try updating with malformed properties (string instead of map)
	rawArgs := `{"id":"p1","kind":"Person","merge":false,"properties":"{\"name\":\"Bob\"}"}`
	var args map[string]any
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		t.Fatalf("unmarshal args: %v", err)
	}

	callErr := dispatchToolCall(db, db, "graph_set_node", args)
	if callErr == nil {
		t.Errorf("FAIL invariant F04: expected error when properties is string, got success")
	}

	// Verify existing properties were NOT wiped
	var propsJSON string
	if err := db.QueryRow("SELECT properties FROM nodes WHERE id = 'p1'").Scan(&propsJSON); err != nil {
		t.Fatalf("query node props: %v", err)
	}
	if !strings.Contains(propsJSON, "Alice") {
		t.Errorf("FAIL invariant F04: node properties were wiped or corrupted: %s", propsJSON)
	}
}

func TestSetEdge_MalformedProperties_ReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := initDatabase(dbPath)
	if err != nil {
		t.Fatalf("initDatabase: %v", err)
	}
	defer db.Close()

	if _, err := handleSetNode(db, "p1", "Person", map[string]any{}, false, false); err != nil {
		t.Fatalf("set node p1: %v", err)
	}
	if _, err := handleSetNode(db, "p2", "Person", map[string]any{}, false, false); err != nil {
		t.Fatalf("set node p2: %v", err)
	}

	rawArgs := `{"from":"p1","to":"p2","kind":"KNOWS","properties":[1, 2, 3]}`
	var args map[string]any
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		t.Fatalf("unmarshal args: %v", err)
	}

	callErr := dispatchToolCall(db, db, "graph_set_edge", args)
	if callErr == nil {
		t.Errorf("FAIL invariant F04: expected error when edge properties is array, got success")
	}
}

// dispatchToolCall delegates directly to executeToolCall to test real server tool dispatch
func dispatchToolCall(db, dbRO *sql.DB, toolName string, args map[string]any) error {
	_, err := executeToolCall(db, dbRO, toolName, args, false)
	return err
}
