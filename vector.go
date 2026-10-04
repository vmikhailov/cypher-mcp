package main

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// initVectorTables creates the entity_embeddings table and indexes
func initVectorTables(db *sql.DB) error {
	ddl := `
		CREATE TABLE IF NOT EXISTS entity_embeddings (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
			alias TEXT NOT NULL,
			embedding BLOB NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_entity_embeddings_node_id ON entity_embeddings(node_id);
		CREATE INDEX IF NOT EXISTS idx_entity_embeddings_alias ON entity_embeddings(alias);
	`
	_, err := db.Exec(ddl)
	return err
}

// encodeEmbedding normalizes the float32 vector and serializes it to little-endian bytes
func encodeEmbedding(vec []float32) []byte {
	// L2 normalize
	var sumSq float64
	for _, v := range vec {
		sumSq += float64(v) * float64(v)
	}
	norm := float32(math.Sqrt(sumSq))
	if norm == 0 {
		norm = 1.0
	}

	buf := make([]byte, len(vec)*4)
	for i, v := range vec {
		normalized := v / norm
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(normalized))
	}
	return buf
}

// decodeEmbedding deserializes little-endian bytes back to []float32
func decodeEmbedding(blob []byte) []float32 {
	count := len(blob) / 4
	vec := make([]float32, count)
	for i := 0; i < count; i++ {
		bits := binary.LittleEndian.Uint32(blob[i*4:])
		vec[i] = math.Float32frombits(bits)
	}
	return vec
}

// dotProduct computes cosine similarity for L2-normalized vectors
func dotProduct(v1, v2 []float32) float64 {
	n := len(v1)
	if len(v2) < n {
		n = len(v2)
	}
	var dot float64
	for i := 0; i < n; i++ {
		dot += float64(v1[i]) * float64(v2[i])
	}
	return dot
}

// getEmbeddingAPIKey tries environment variables and standard .env files
func getEmbeddingAPIKey() string {
	if k := os.Getenv("GOOGLE_API_KEY"); k != "" {
		return k
	}
	if k := os.Getenv("GEMINI_API_KEY"); k != "" {
		return k
	}

	// Try reading from ~/.hermes/.env or %LOCALAPPDATA%/hermes/.env
	candidates := []string{".env"}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".hermes", ".env"),
			filepath.Join(home, "AppData", "Local", "hermes", ".env"),
		)
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

	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/gemini-embedding-001:embedContent?key=%s", apiKey)
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

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("embedding request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("embedding api returned %s: %s", resp.Status, string(body))
	}

	var res struct {
		Embedding struct {
			Values []float32 `json:"values"`
		} `json:"embedding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("decode embedding response: %w", err)
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
		limit = 5
	}
	if minScore <= 0 {
		minScore = 0.50
	}

	qVec, err := fetchEmbedding(query)
	if err != nil {
		return "", fmt.Errorf("failed to embed query '%s': %w", query, err)
	}
	normQVec := decodeEmbedding(encodeEmbedding(qVec))

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

		candVec := decodeEmbedding(blob)
		score := dotProduct(normQVec, candVec)
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

	blob := encodeEmbedding(vec)

	// Upsert alias in separate statements to ensure proper binary parameter binding
	_, _ = db.Exec("DELETE FROM entity_embeddings WHERE node_id = ? AND alias = ?", nodeID, alias)
	_, err = db.Exec("INSERT INTO entity_embeddings (node_id, alias, embedding) VALUES (?, ?, ?)", nodeID, alias, blob)
	if err != nil {
		return "", fmt.Errorf("upsert alias embedding: %w", err)
	}

	res := map[string]any{
		"node_id":   nodeID,
		"alias":     alias,
		"vector_dim": len(vec),
		"status":    "upserted",
	}
	out, _ := json.MarshalIndent(res, "", "  ")
	return string(out), nil
}
