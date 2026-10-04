package graph

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/vmikhailov/cypher-mcp/internal/storage"
)

func TestGraph_DomainModelsAndMutations(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := storage.InitDatabase(dbPath)
	if err != nil {
		t.Fatalf("InitDatabase: %v", err)
	}
	defer db.Close()

	// 1. SetNode returns typed *Node
	node, err := SetNode(db, "usr:alice", "Person", map[string]any{"name": "Alice"}, false, false)
	if err != nil {
		t.Fatalf("SetNode: %v", err)
	}
	if node.ID != "usr:alice" || node.Kind != "Person" {
		t.Fatalf("unexpected node: %+v", node)
	}

	_, err = SetNode(db, "usr:bob", "Person", map[string]any{"name": "Bob"}, false, false)
	if err != nil {
		t.Fatalf("SetNode bob: %v", err)
	}

	// 2. SetEdge returns typed *Edge
	edge, err := SetEdge(db, "usr:alice", "usr:bob", "KNOWS", map[string]any{"since": 2024}, false, false)
	if err != nil {
		t.Fatalf("SetEdge: %v", err)
	}
	if edge.FromID != "usr:alice" || edge.ToID != "usr:bob" || edge.Kind != "KNOWS" {
		t.Fatalf("unexpected edge: %+v", edge)
	}

	// 3. ExecuteQuery returns typed *QueryResult
	ctx := context.Background()
	queryRes, err := ExecuteQuery(ctx, db, "MATCH (a:Person)-[:KNOWS]->(b:Person) RETURN a.name, b.name")
	if err != nil {
		t.Fatalf("ExecuteQuery: %v", err)
	}
	if queryRes.Count != 1 {
		t.Fatalf("expected 1 result row, got %d", queryRes.Count)
	}

	// 4. Search returns typed *SearchResult
	searchRes, err := Search(db, "Alice", "", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if searchRes.Count < 1 {
		t.Fatalf("expected at least 1 search result, got %d", searchRes.Count)
	}

	// 5. InspectSchema returns typed *SchemaSummary
	schemaRes, err := InspectSchema(db)
	if err != nil {
		t.Fatalf("InspectSchema: %v", err)
	}
	if schemaRes.TotalNodes != 2 || schemaRes.TotalEdges != 1 {
		t.Fatalf("expected 2 nodes and 1 edge in schema summary, got %d and %d", schemaRes.TotalNodes, schemaRes.TotalEdges)
	}

	// 6. DeleteNode returns typed *DeleteResult
	delRes, err := DeleteNode(db, "usr:bob")
	if err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}
	if delRes.ID != "usr:bob" || delRes.Status != "deleted" {
		t.Fatalf("unexpected delete result: %+v", delRes)
	}
}
