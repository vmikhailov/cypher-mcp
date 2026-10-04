package main

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"path/filepath"
	"testing"
)

func TestDotProduct_DimensionMismatch_Error(t *testing.T) {
	v1 := []float32{1.0, 2.0, 3.0}
	v2 := []float32{1.0, 2.0, 3.0, 4.0}

	_, err := dotProduct(v1, v2)
	if err == nil {
		t.Fatalf("FAIL invariant F13: expected error on dimension mismatch, got nil")
	}
}

func TestDotProduct_NaNOrInf_Error(t *testing.T) {
	v1 := []float32{float32(math.NaN()), 1.0}
	v2 := []float32{1.0, 1.0}

	_, err := dotProduct(v1, v2)
	if err == nil {
		t.Fatalf("FAIL invariant F13: expected error when vector contains NaN, got nil")
	}

	v3 := []float32{float32(math.Inf(1)), 1.0}
	_, err = dotProduct(v3, v2)
	if err == nil {
		t.Fatalf("FAIL invariant F13: expected error when vector contains Inf, got nil")
	}
}

func TestDecodeEmbedding_InvalidLength(t *testing.T) {
	invalidBlob := []byte{1, 2, 3} // Not a multiple of 4
	_, err := decodeEmbedding(invalidBlob)
	if err == nil {
		t.Fatalf("FAIL invariant F13: expected error when blob length is not a multiple of 4, got nil")
	}
}

func TestResolveEntity_MinScoreRange(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := initDatabase(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("initDatabase: %v", err)
	}
	defer db.Close()

	// Calling with minScore > 1.0 should return error
	_, err = handleResolveEntity(db, "test", "", 5, 1.5)
	if err == nil {
		t.Fatalf("FAIL invariant F14: expected error when min_score > 1.0, got nil")
	}

	// Calling with minScore < 0.0 should return error
	_, err = handleResolveEntity(db, "test", "", 5, -0.5)
	if err == nil {
		t.Fatalf("FAIL invariant F14: expected error when min_score < 0.0, got nil")
	}
}

type mockEmbeddingTransport struct {
	values []float32
}

func (m *mockEmbeddingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	respBody := map[string]any{
		"embedding": map[string]any{
			"values": m.values,
		},
	}
	b, _ := json.Marshal(respBody)
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(bytes.NewReader(b)),
		Header:     make(http.Header),
	}, nil
}

func TestResolveEntity_ExplicitMinScoreZero(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := initDatabase(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("initDatabase: %v", err)
	}
	defer db.Close()

	t.Setenv("GOOGLE_API_KEY", "dummy_test_key")
	oldClient := embeddingHTTPClient
	embeddingHTTPClient = &http.Client{
		Transport: &mockEmbeddingTransport{
			values: []float32{1.0, 0.0, 0.0},
		},
	}
	defer func() {
		embeddingHTTPClient = oldClient
	}()

	// Setup node and alias
	if _, err := handleSetNode(db, "p1", "Person", nil, false, false); err != nil {
		t.Fatalf("set node: %v", err)
	}
	vec1 := []float32{1.0, 0.0, 0.0}
	if _, err := handleUpsertAlias(db, "p1", "Alice", vec1); err != nil {
		t.Fatalf("upsert alias: %v", err)
	}

	// Vector query with score 0.0
	// Query with min_score = 0.0 via tool execution
	args := map[string]any{
		"query":     "Alice",
		"min_score": 0.0,
	}
	resStr, err := executeToolCall(db, db, "graph_resolve_entity", args, false)
	if err != nil {
		t.Fatalf("executeToolCall: %v", err)
	}

	var res map[string]any
	if err := json.Unmarshal([]byte(resStr), &res); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	scoreVal, ok := res["min_score"].(float64)
	if !ok || scoreVal != 0.0 {
		t.Errorf("FAIL invariant F14: explicit min_score 0.0 was coerced to %v", res["min_score"])
	}
}

func TestResolveEntity_UnitVectorBoundary(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := initDatabase(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("initDatabase: %v", err)
	}
	defer db.Close()

	t.Setenv("GOOGLE_API_KEY", "dummy_test_key")
	oldClient := embeddingHTTPClient
	embeddingHTTPClient = &http.Client{
		Transport: &mockEmbeddingTransport{
			values: []float32{1.0, 1.0},
		},
	}
	defer func() {
		embeddingHTTPClient = oldClient
	}()

	if _, err := handleSetNode(db, "p1", "Person", nil, false, false); err != nil {
		t.Fatalf("set node: %v", err)
	}
	if _, err := handleUpsertAlias(db, "p1", "Alice", []float32{1.0, 1.0}); err != nil {
		t.Fatalf("upsert alias: %v", err)
	}

	// Query with min_score = 1.0: identical normalized vectors must not fail boundary
	args := map[string]any{
		"query":     "Alice",
		"min_score": 1.0,
	}
	resStr, err := executeToolCall(db, db, "graph_resolve_entity", args, false)
	if err != nil {
		t.Fatalf("executeToolCall: %v", err)
	}
	var res struct {
		Count   int               `json:"count"`
		Results []EntityCandidate `json:"results"`
	}
	if err := json.Unmarshal([]byte(resStr), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if res.Count != 1 || len(res.Results) != 1 {
		t.Fatalf("expected 1 result with min_score: 1.0 on identical vectors, got %d", res.Count)
	}
}
