package transport

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/vmikhailov/cypher-mcp/internal/storage"
)

func TestTransport_ExecuteToolCall(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := storage.InitDatabase(dbPath)
	if err != nil {
		t.Fatalf("InitDatabase: %v", err)
	}
	defer db.Close()

	dbRO, err := storage.InitRODatabase(dbPath)
	if err != nil {
		t.Fatalf("InitRODatabase: %v", err)
	}
	defer dbRO.Close()

	// Execute graph_set_node tool call
	res, err := ExecuteToolCall(db, dbRO, "graph_set_node", map[string]any{
		"id":   "n1",
		"kind": "Item",
		"properties": map[string]any{
			"color": "blue",
		},
	}, false)
	if err != nil {
		t.Fatalf("ExecuteToolCall graph_set_node: %v", err)
	}
	if res == "" {
		t.Fatalf("expected non-empty response")
	}

	// Execute graph_query tool call
	queryRes, err := ExecuteToolCall(db, dbRO, "graph_query", map[string]any{
		"query": "MATCH (n:Item) RETURN n.color",
	}, false)
	if err != nil {
		t.Fatalf("ExecuteToolCall graph_query: %v", err)
	}
	if queryRes == "" {
		t.Fatalf("expected non-empty query response")
	}
}

func TestTransport_ValidateJSONRPCID(t *testing.T) {
	boolID := json.RawMessage("true")
	if err := ValidateJSONRPCID(&boolID); err == nil {
		t.Fatalf("expected error for boolean ID, got nil")
	}

	intID := json.RawMessage("9007199254740993")
	if err := ValidateJSONRPCID(&intID); err != nil {
		t.Fatalf("expected 64-bit int ID to be valid, got %v", err)
	}
}
