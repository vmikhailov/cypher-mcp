package main

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"testing"
)

type mockFailingTransport struct {
	err error
}

func (m *mockFailingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return nil, m.err
}

func TestNetworkError_NoKeyLeak(t *testing.T) {
	secretKey := "SECRET_SUPER_SENSITIVE_KEY_12345"
	t.Setenv("GOOGLE_API_KEY", secretKey)
	t.Setenv("GEMINI_API_KEY", "")

	// Save original client transport and restore after test
	oldClient := embeddingHTTPClient
	embeddingHTTPClient = &http.Client{
		Transport: &mockFailingTransport{
			err: errors.New("simulated dial tcp connection refused"),
		},
	}
	defer func() {
		embeddingHTTPClient = oldClient
	}()

	_, err := fetchEmbedding("sample text")
	if err == nil {
		t.Fatalf("expected error from failing transport, got nil")
	}

	errStr := err.Error()
	if strings.Contains(errStr, secretKey) {
		t.Errorf("FAIL invariant F02: error message leaked API key %q: %s", secretKey, errStr)
	}
}

func TestAliasUpsert_FailurePreservesOldEmbedding(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`
		CREATE TABLE nodes (id TEXT PRIMARY KEY, kind TEXT NOT NULL, properties TEXT NOT NULL);
	`); err != nil {
		t.Fatalf("create nodes: %v", err)
	}
	if err := initVectorTables(db); err != nil {
		t.Fatalf("initVectorTables: %v", err)
	}

	if _, err := db.Exec("INSERT INTO nodes VALUES ('p1', 'Person', '{}')"); err != nil {
		t.Fatalf("insert node: %v", err)
	}

	initialVec := []float32{1.0, 0.0, 0.0}
	if _, err := handleUpsertAlias(db, "p1", "Alice", initialVec); err != nil {
		t.Fatalf("initial upsert alias: %v", err)
	}

	// Verify alias is present
	var count int
	if err := db.QueryRow("SELECT count(*) FROM entity_embeddings WHERE node_id = 'p1' AND alias = 'Alice'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("expected 1 initial alias, got %d", count)
	}

	// Create a trigger that forces INSERT to fail on entity_embeddings
	if _, err := db.Exec(`
		CREATE TRIGGER force_insert_fail BEFORE INSERT ON entity_embeddings
		BEGIN
			SELECT RAISE(ABORT, 'forced insert failure');
		END;
	`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	// Attempt second upsert which will fail at INSERT
	newVec := []float32{0.0, 1.0, 0.0}
	_, err = handleUpsertAlias(db, "p1", "Alice", newVec)
	if err == nil {
		t.Fatalf("expected handleUpsertAlias to fail due to trigger, but succeeded")
	}

	// Verify old embedding is STILL preserved and NOT lost
	var remainingCount int
	var blob []byte
	err = db.QueryRow("SELECT count(*), coalesce(embedding, X'') FROM entity_embeddings WHERE node_id = 'p1' AND alias = 'Alice'").Scan(&remainingCount, &blob)
	if err != nil {
		t.Fatalf("query remaining alias: %v", err)
	}
	if remainingCount != 1 {
		t.Fatalf("FAIL invariant F09: old embedding was lost on INSERT failure! remainingCount=%d (expected 1)", remainingCount)
	}
	if len(blob) == 0 {
		t.Fatalf("FAIL invariant F09: embedding blob is empty")
	}
	decoded, err := decodeEmbedding(blob)
	if err != nil {
		t.Fatalf("decodeEmbedding: %v", err)
	}
	if len(decoded) == 0 || decoded[0] < 0.9 {
		t.Fatalf("FAIL invariant F09: old embedding data was altered or corrupted: %v", decoded)
	}
}

func TestAliasUpsert_UniqueConstraintAndNoDuplicates(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE nodes (id TEXT PRIMARY KEY, kind TEXT NOT NULL, properties TEXT NOT NULL);`); err != nil {
		t.Fatalf("create nodes: %v", err)
	}
	if err := initVectorTables(db); err != nil {
		t.Fatalf("initVectorTables: %v", err)
	}
	if _, err := db.Exec("INSERT INTO nodes VALUES ('p1', 'Person', '{}')"); err != nil {
		t.Fatalf("insert node: %v", err)
	}

	v1 := []float32{1.0, 0.0, 0.0}
	if _, err := handleUpsertAlias(db, "p1", "Alice", v1); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	v2 := []float32{0.0, 1.0, 0.0}
	if _, err := handleUpsertAlias(db, "p1", "Alice", v2); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	var count int
	if err := db.QueryRow("SELECT count(*) FROM entity_embeddings WHERE node_id = 'p1' AND alias = 'Alice'").Scan(&count); err != nil {
		t.Fatalf("count error: %v", err)
	}
	if count != 1 {
		t.Fatalf("FAIL invariant: expected exactly 1 record for (node_id, alias), got %d", count)
	}

	var blob []byte
	if err := db.QueryRow("SELECT embedding FROM entity_embeddings WHERE node_id = 'p1' AND alias = 'Alice'").Scan(&blob); err != nil {
		t.Fatalf("scan blob: %v", err)
	}
	decoded, err := decodeEmbedding(blob)
	if err != nil {
		t.Fatalf("decodeEmbedding: %v", err)
	}
	if decoded[1] < 0.9 {
		t.Fatalf("expected updated vector with v[1] ~ 1.0, got %v", decoded)
	}
}
