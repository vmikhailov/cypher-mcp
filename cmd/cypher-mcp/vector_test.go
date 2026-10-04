package main

import (
	"database/sql"
	"encoding/json"
	"math"
	"testing"

	_ "modernc.org/sqlite"
)

func TestVectorEncodingAndCosine(t *testing.T) {
	v1 := []float32{1.0, 0.0, 0.0}
	v2 := []float32{0.0, 1.0, 0.0}
	v3 := []float32{0.7071, 0.7071, 0.0}

	b1, err := encodeEmbedding(v1)
	if err != nil {
		t.Fatalf("encode v1: %v", err)
	}
	b2, err := encodeEmbedding(v2)
	if err != nil {
		t.Fatalf("encode v2: %v", err)
	}
	b3, err := encodeEmbedding(v3)
	if err != nil {
		t.Fatalf("encode v3: %v", err)
	}

	d1, err := decodeEmbedding(b1)
	if err != nil {
		t.Fatalf("decode b1: %v", err)
	}
	d2, err := decodeEmbedding(b2)
	if err != nil {
		t.Fatalf("decode b2: %v", err)
	}
	d3, err := decodeEmbedding(b3)
	if err != nil {
		t.Fatalf("decode b3: %v", err)
	}

	// v1 and v1 should have similarity 1.0
	sim11, err := dotProduct(d1, d1)
	if err != nil {
		t.Fatalf("dotProduct(d1, d1): %v", err)
	}
	if math.Abs(sim11-1.0) > 1e-4 {
		t.Fatalf("expected sim(v1, v1) == 1.0, got %f", sim11)
	}

	// orthogonal vectors should have similarity 0.0
	sim12, err := dotProduct(d1, d2)
	if err != nil {
		t.Fatalf("dotProduct(d1, d2): %v", err)
	}
	if math.Abs(sim12) > 1e-4 {
		t.Fatalf("expected sim(v1, v2) == 0.0, got %f", sim12)
	}

	// 45 degree angle should be ~0.7071
	sim13, err := dotProduct(d1, d3)
	if err != nil {
		t.Fatalf("dotProduct(d1, d3): %v", err)
	}
	if math.Abs(sim13-0.7071) > 1e-3 {
		t.Fatalf("expected sim(v1, v3) == 0.7071, got %f", sim13)
	}
}

func TestVectorEntityResolutionDB(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`
		CREATE TABLE nodes (id TEXT PRIMARY KEY, kind TEXT NOT NULL, properties TEXT NOT NULL);
		CREATE TABLE edges (from_id TEXT NOT NULL, to_id TEXT NOT NULL, kind TEXT NOT NULL, properties TEXT NOT NULL);
	`); err != nil {
		t.Fatalf("create tables: %v", err)
	}

	if err := initVectorTables(db); err != nil {
		t.Fatalf("init vector tables: %v", err)
	}

	// Insert nodes
	_, err = db.Exec("INSERT INTO nodes VALUES (?, ?, ?)", "person:alexander", "Person", `{"name": "Alexander"}`)
	if err != nil {
		t.Fatalf("insert node: %v", err)
	}

	// Upsert alias with precomputed vector
	vecSlava := []float32{0.9, 0.1, 0.0}
	_, err = handleUpsertAlias(db, "person:alexander", "Саша", vecSlava)
	if err != nil {
		t.Fatalf("upsert alias: %v", err)
	}

	// Verify alias stored
	var count int
	if err := db.QueryRow("SELECT count(*) FROM entity_embeddings WHERE node_id = ?", "person:alexander").Scan(&count); err != nil || count != 1 {
		t.Fatalf("expected 1 embedding, got %d (err: %v)", count, err)
	}

	// Verify query with fake embedding via decode/encode
	// Instead of calling remote API in unit test, test candidate scoring logic directly
	qBlob, _ := encodeEmbedding([]float32{0.88, 0.12, 0.0})
	qVec, _ := decodeEmbedding(qBlob)
	candBlob, _ := encodeEmbedding(vecSlava)
	candVec, _ := decodeEmbedding(candBlob)
	score, err := dotProduct(qVec, candVec)
	if err != nil {
		t.Fatalf("dotProduct: %v", err)
	}
	if score < 0.95 {
		t.Fatalf("expected high cosine similarity > 0.95, got %f", score)
	}
}

func TestResolveEntitySchema(t *testing.T) {
	// Verify tool definition presence in serverTools
	foundResolve := false
	foundUpsert := false
	for _, tool := range serverTools {
		if tool["name"] == "graph_resolve_entity" {
			foundResolve = true
		}
		if tool["name"] == "graph_upsert_alias" {
			foundUpsert = true
		}
	}
	if !foundResolve {
		t.Fatalf("graph_resolve_entity not in serverTools")
	}
	if !foundUpsert {
		t.Fatalf("graph_upsert_alias not in serverTools")
	}

	schemaBytes, _ := json.Marshal(serverTools)
	if len(schemaBytes) == 0 {
		t.Fatalf("empty schema bytes")
	}
}
