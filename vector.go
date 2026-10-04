package main

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var (
	embeddingHTTPClient = &http.Client{Timeout: 10 * time.Second}
	credentialPatterns  = regexp.MustCompile(`(?i)(key|token|secret|password|api[_-]?key)=[^\s&"']+`)
)

// sanitizeError removes sensitive credentials and query parameters from error strings
func sanitizeError(err error, secrets ...string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	for _, s := range secrets {
		if s != "" {
			msg = strings.ReplaceAll(msg, s, "[REDACTED]")
		}
	}
	msg = credentialPatterns.ReplaceAllString(msg, "$1=[REDACTED]")
	return errors.New(msg)
}

// initVectorTables creates the entity_embeddings table and indexes
func initVectorTables(db *sql.DB) error {
	ddl := `
		CREATE TABLE IF NOT EXISTS entity_embeddings (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
			alias TEXT NOT NULL,
			embedding BLOB NOT NULL,
			UNIQUE(node_id, alias)
		);
		DELETE FROM entity_embeddings
		WHERE rowid NOT IN (
			SELECT min(rowid)
			FROM entity_embeddings
			GROUP BY node_id, alias
		);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_entity_embeddings_unique ON entity_embeddings(node_id, alias);
		CREATE INDEX IF NOT EXISTS idx_entity_embeddings_node_id ON entity_embeddings(node_id);
		CREATE INDEX IF NOT EXISTS idx_entity_embeddings_alias ON entity_embeddings(alias);
	`
	_, err := db.Exec(ddl)
	return err
}

// encodeEmbedding normalizes the float32 vector and serializes it to little-endian bytes
func encodeEmbedding(vec []float32) ([]byte, error) {
	if len(vec) == 0 {
		return nil, fmt.Errorf("vector cannot be empty")
	}
	// L2 normalize in float64 to prevent float32 overflow
	var sumSq float64
	for i, v := range vec {
		f := float64(v)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, fmt.Errorf("vector element at index %d is not a finite number", i)
		}
		sumSq += f * f
	}
	norm := math.Sqrt(sumSq)
	if math.IsNaN(norm) || math.IsInf(norm, 0) {
		return nil, fmt.Errorf("vector norm is not finite")
	}
	if norm == 0 {
		norm = 1.0
	}

	buf := make([]byte, len(vec)*4)
	for i, v := range vec {
		normalized := float32(float64(v) / norm)
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(normalized))
	}
	return buf, nil
}

// decodeEmbedding deserializes little-endian bytes back to []float32
func decodeEmbedding(blob []byte) ([]float32, error) {
	if len(blob)%4 != 0 {
		return nil, fmt.Errorf("invalid embedding blob length: %d bytes (must be a multiple of 4)", len(blob))
	}
	count := len(blob) / 4
	vec := make([]float32, count)
	for i := 0; i < count; i++ {
		bits := binary.LittleEndian.Uint32(blob[i*4:])
		val := math.Float32frombits(bits)
		if math.IsNaN(float64(val)) || math.IsInf(float64(val), 0) {
			return nil, fmt.Errorf("decoded embedding element at index %d is not finite", i)
		}
		vec[i] = val
	}
	return vec, nil
}

// dotProduct computes cosine similarity for L2-normalized vectors
func dotProduct(v1, v2 []float32) (float64, error) {
	if len(v1) != len(v2) {
		return 0, fmt.Errorf("dimension mismatch: vector length %d != %d", len(v1), len(v2))
	}
	var dot float64
	for i := 0; i < len(v1); i++ {
		f1 := float64(v1[i])
		f2 := float64(v2[i])
		if math.IsNaN(f1) || math.IsInf(f1, 0) || math.IsNaN(f2) || math.IsInf(f2, 0) {
			return 0, fmt.Errorf("vector element at index %d is not finite", i)
		}
		dot += f1 * f2
	}
	if math.IsNaN(dot) || math.IsInf(dot, 0) {
		return 0, fmt.Errorf("dot product result is not finite")
	}
	return dot, nil
}

// getEmbeddingAPIKey tries environment variables and standard .env files
func getEmbeddingAPIKey() string {
	if k := os.Getenv("GOOGLE_API_KEY"); k != "" {
		return k
	}
	if k := os.Getenv("GEMINI_API_KEY"); k != "" {
		return k
	}

	// Try reading from .env in current directory or user home directory
	candidates := []string{".env"}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".env"))
	}

	for _, path := range candidates {
		if f, err := os.Open(path); err == nil {
			defer f.Close()
			scanner := bufio.NewScanner(f)
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if strings.HasPrefix(line, "GOOGLE_API_KEY=") {
					val := strings.TrimPrefix(line, "GOOGLE_API_KEY=")
					return strings.Trim(val, `"' `)
				}
				if strings.HasPrefix(line, "GEMINI_API_KEY=") {
					val := strings.TrimPrefix(line, "GEMINI_API_KEY=")
					return strings.Trim(val, `"' `)
				}
			}
		}
	}
	return ""
}

// fetchEmbedding calls Google Gemini embedding endpoint
func fetchEmbedding(text string) ([]float32, error) {
	apiKey := getEmbeddingAPIKey()
	if apiKey == "" {
		return nil, fmt.Errorf("no GOOGLE_API_KEY or GEMINI_API_KEY found in environment or .env")
	}

	url := "https://generativelanguage.googleapis.com/v1beta/models/gemini-embedding-001:embedContent"
	reqBody := map[string]any{
		"model": "models/gemini-embedding-001",
		"content": map[string]any{
			"parts": []map[string]any{
				{"text": text},
			},
		},
	}

	b, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(b))
	if err != nil {
		return nil, sanitizeError(fmt.Errorf("create embedding request: %w", err), apiKey)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", apiKey)

	resp, err := embeddingHTTPClient.Do(req)
	if err != nil {
		return nil, sanitizeError(fmt.Errorf("embedding request failed: %w", err), apiKey)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, sanitizeError(fmt.Errorf("embedding api returned %s: %s", resp.Status, string(body)), apiKey)
	}

	var res struct {
		Embedding struct {
			Values []float32 `json:"values"`
		} `json:"embedding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, sanitizeError(fmt.Errorf("decode embedding response: %w", err), apiKey)
	}

	if len(res.Embedding.Values) == 0 {
		return nil, fmt.Errorf("empty embedding values received")
	}

	return res.Embedding.Values, nil
}

type EntityCandidate struct {
	NodeID     string         `json:"node_id"`
	Alias      string         `json:"alias"`
	Kind       string         `json:"kind"`
	Score      float64        `json:"score"`
	Properties map[string]any `json:"properties,omitempty"`
}

// handleResolveEntity performs vector similarity search across entity_embeddings
func handleResolveEntity(db *sql.DB, query string, kindFilter string, limit int, minScore float64) (string, error) {
	if limit <= 0 {
		return "", fmt.Errorf("invalid 'limit': %d (must be > 0)", limit)
	}
	if minScore < 0.0 || minScore > 1.0 {
		return "", fmt.Errorf("invalid 'min_score': %f (must be between 0.0 and 1.0)", minScore)
	}

	qVec, err := fetchEmbedding(query)
	if err != nil {
		return "", fmt.Errorf("failed to embed query '%s': %w", query, err)
	}
	encodedQ, err := encodeEmbedding(qVec)
	if err != nil {
		return "", fmt.Errorf("encode query vector: %w", err)
	}
	normQVec, err := decodeEmbedding(encodedQ)
	if err != nil {
		return "", fmt.Errorf("decode query vector: %w", err)
	}

	sqlQuery := `
		SELECT e.node_id, e.alias, e.embedding, n.kind, n.properties
		FROM entity_embeddings e
		JOIN nodes n ON e.node_id = n.id
		WHERE (? = '' OR n.kind = ?)
	`
	rows, err := db.Query(sqlQuery, kindFilter, kindFilter)
	if err != nil {
		return "", fmt.Errorf("query embeddings: %w", err)
	}
	defer rows.Close()

	var candidates []EntityCandidate
	for rows.Next() {
		var (
			nodeID   string
			alias    string
			blob     []byte
			kind     string
			propsRaw string
		)
		if err := rows.Scan(&nodeID, &alias, &blob, &kind, &propsRaw); err != nil {
			return "", err
		}

		candVec, err := decodeEmbedding(blob)
		if err != nil {
			continue
		}
		score, err := dotProduct(normQVec, candVec)
		if err != nil {
			continue
		}
		if score >= minScore {
			var props map[string]any
			_ = json.Unmarshal([]byte(propsRaw), &props)

			candidates = append(candidates, EntityCandidate{
				NodeID:     nodeID,
				Alias:      alias,
				Kind:       kind,
				Score:      math.Round(score*10000) / 10000,
				Properties: props,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterate candidate embeddings: %w", err)
	}

	// Sort descending by score
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Score > candidates[j].Score
	})

	// Deduplicate by NodeID (keeping highest score per node)
	seen := make(map[string]bool)
	var deduped []EntityCandidate
	for _, c := range candidates {
		if !seen[c.NodeID] {
			seen[c.NodeID] = true
			deduped = append(deduped, c)
			if len(deduped) >= limit {
				break
			}
		}
	}

	res := map[string]any{
		"query":     query,
		"kind":      kindFilter,
		"count":     len(deduped),
		"min_score": minScore,
		"results":   deduped,
	}

	out, _ := json.MarshalIndent(res, "", "  ")
	return string(out), nil
}

// handleUpsertAlias adds an alias for a node with its vector embedding
func handleUpsertAlias(db *sql.DB, nodeID string, alias string, explicitVec []float32) (string, error) {
	if strings.TrimSpace(nodeID) == "" {
		return "", fmt.Errorf("node_id is required")
	}
	if strings.TrimSpace(alias) == "" {
		return "", fmt.Errorf("alias is required")
	}

	// Verify node exists
	var exists bool
	err := db.QueryRow("SELECT 1 FROM nodes WHERE id = ?", nodeID).Scan(&exists)
	if err != nil {
		return "", fmt.Errorf("node '%s' not found in graph: %w", nodeID, err)
	}

	var vec []float32
	if len(explicitVec) > 0 {
		vec = explicitVec
	} else {
		v, err := fetchEmbedding(alias)
		if err != nil {
			return "", fmt.Errorf("auto-embed failed for alias '%s': %w", alias, err)
		}
		vec = v
	}

	blob, err := encodeEmbedding(vec)
	if err != nil {
		return "", fmt.Errorf("encode alias embedding: %w", err)
	}

	// Upsert alias atomically in a transaction to prevent data loss on failure
	tx, err := db.Begin()
	if err != nil {
		return "", fmt.Errorf("begin tx for alias upsert: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		INSERT INTO entity_embeddings (node_id, alias, embedding) VALUES (?, ?, ?)
		ON CONFLICT(node_id, alias) DO UPDATE SET embedding = excluded.embedding
	`, nodeID, alias, blob)
	if err != nil {
		return "", fmt.Errorf("upsert alias embedding: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit alias upsert: %w", err)
	}

	res := map[string]any{
		"node_id":    nodeID,
		"alias":      alias,
		"vector_dim": len(vec),
		"status":     "upserted",
	}
	out, _ := json.MarshalIndent(res, "", "  ")
	return string(out), nil
}
