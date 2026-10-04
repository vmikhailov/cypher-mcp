package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	cyphersql "github.com/vmikhailov/cypher-sql-go"
)

var (
	benchDB   *sql.DB
	benchRODB *sql.DB
	benchDir  string
)

func setupBenchmarkGraph(b *testing.B, numServices int) (*sql.DB, *sql.DB) {
	tmpDir, err := os.MkdirTemp("", "cypher-mcp-bench-*")
	if err != nil {
		b.Fatalf("failed to create temp dir: %v", err)
	}
	benchDir = tmpDir

	dbPath := filepath.Join(tmpDir, "bench_graph.db")
	db, err := initDatabase(dbPath)
	if err != nil {
		b.Fatalf("init db: %v", err)
	}

	dbRO, err := initRODatabase(dbPath)
	if err != nil {
		b.Fatalf("init ro db: %v", err)
	}

	// Build a realistic enterprise service topology graph:
	// Teams -> Engineers -> Services -> Databases & Infra -> Incidents
	var nodes []BatchNodeItem
	var edges []BatchEdgeItem

	numTeams := numServices / 5
	if numTeams < 2 {
		numTeams = 2
	}
	numEngineers := numServices * 2
	numDatabases := numServices / 2
	if numDatabases < 2 {
		numDatabases = 2
	}

	// 1. Teams
	for i := 0; i < numTeams; i++ {
		nodes = append(nodes, BatchNodeItem{
			ID:   fmt.Sprintf("team:%03d", i),
			Kind: "Team",
			Properties: map[string]any{
				"name":   fmt.Sprintf("Engineering Team %d", i),
				"domain": fmt.Sprintf("Domain-%d", i%5),
			},
		})
	}

	// 2. Engineers
	for i := 0; i < numEngineers; i++ {
		nodes = append(nodes, BatchNodeItem{
			ID:   fmt.Sprintf("engineer:%04d", i),
			Kind: "Engineer",
			Properties: map[string]any{
				"name":  fmt.Sprintf("Engineer %d", i),
				"level": fmt.Sprintf("L%d", 3+(i%4)),
				"email": fmt.Sprintf("eng%d@corp.local", i),
			},
		})
		teamID := fmt.Sprintf("team:%03d", i%numTeams)
		edges = append(edges, BatchEdgeItem{
			From: fmt.Sprintf("engineer:%04d", i),
			To:   teamID,
			Kind: "MEMBER_OF",
			Properties: map[string]any{
				"role": "Software Engineer",
			},
		})
	}

	// 3. Databases / Infrastructure
	for i := 0; i < numDatabases; i++ {
		nodes = append(nodes, BatchNodeItem{
			ID:   fmt.Sprintf("db:%03d", i),
			Kind: "Database",
			Properties: map[string]any{
				"name":   fmt.Sprintf("db-cluster-%03d", i),
				"engine": "PostgreSQL",
				"tier":   "production",
			},
		})
	}

	// 4. Services
	for i := 0; i < numServices; i++ {
		nodes = append(nodes, BatchNodeItem{
			ID:   fmt.Sprintf("svc:%04d", i),
			Kind: "Service",
			Properties: map[string]any{
				"name":     fmt.Sprintf("service-%04d", i),
				"tier":     fmt.Sprintf("tier-%d", 1+(i%3)),
				"language": "Go",
				"repo":     fmt.Sprintf("github.com/corp/service-%04d", i),
			},
		})

		// Team OWNS Service
		teamID := fmt.Sprintf("team:%03d", i%numTeams)
		edges = append(edges, BatchEdgeItem{
			From:       teamID,
			To:         fmt.Sprintf("svc:%04d", i),
			Kind:       "OWNS",
			Properties: map[string]any{"sla": "99.95%"},
		})

		// Service USES Database
		dbID := fmt.Sprintf("db:%03d", i%numDatabases)
		edges = append(edges, BatchEdgeItem{
			From:       fmt.Sprintf("svc:%04d", i),
			To:         dbID,
			Kind:       "USES_STORAGE",
			Properties: map[string]any{"mode": "read_write"},
		})

		// Multi-hop dependencies between services (DAG-like)
		if i > 0 {
			target1 := fmt.Sprintf("svc:%04d", (i*7)%(i))
			edges = append(edges, BatchEdgeItem{
				From:       fmt.Sprintf("svc:%04d", i),
				To:         target1,
				Kind:       "DEPENDS_ON",
				Properties: map[string]any{"protocol": "gRPC"},
			})
			if i > 2 {
				target2 := fmt.Sprintf("svc:%04d", (i*13)%(i))
				if target2 != target1 {
					edges = append(edges, BatchEdgeItem{
						From:       fmt.Sprintf("svc:%04d", i),
						To:         target2,
						Kind:       "DEPENDS_ON",
						Properties: map[string]any{"protocol": "REST"},
					})
				}
			}
		}
	}

	// Batch upsert in chunks of 500
	chunkSize := 500
	for i := 0; i < len(nodes); i += chunkSize {
		end := i + chunkSize
		if end > len(nodes) {
			end = len(nodes)
		}
		if _, err := handleBatchUpsert(db, nodes[i:end], nil); err != nil {
			b.Fatalf("upsert nodes chunk: %v", err)
		}
	}
	for i := 0; i < len(edges); i += chunkSize {
		end := i + chunkSize
		if end > len(edges) {
			end = len(edges)
		}
		if _, err := handleBatchUpsert(db, nil, edges[i:end]); err != nil {
			b.Fatalf("upsert edges chunk: %v", err)
		}
	}

	return db, dbRO
}

func BenchmarkTranspileCypher(b *testing.B) {
	query := "MATCH (t:Team)-[:OWES]->(s:Service)-[:USES_STORAGE]->(db:Database) WHERE t.domain = 'Domain-1' RETURN s.name, db.name"
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := cyphersql.Compile(query)
		if err != nil {
			b.Fatalf("compile error: %v", err)
		}
	}
}

func BenchmarkQuery_1Hop_PointLookup(b *testing.B) {
	db, dbRO := setupBenchmarkGraph(b, 200)
	defer db.Close()
	defer dbRO.Close()
	defer os.RemoveAll(benchDir)

	query := "MATCH (s:Service {id: 'svc:0050'}) RETURN s.name, s.tier"
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		res, err := handleGraphQuery(dbRO, query)
		if err != nil {
			b.Fatalf("query failed: %v", err)
		}
		if len(res) == 0 {
			b.Fatal("empty result")
		}
	}
}

func BenchmarkQuery_2Hop_Join(b *testing.B) {
	db, dbRO := setupBenchmarkGraph(b, 200)
	defer db.Close()
	defer dbRO.Close()
	defer os.RemoveAll(benchDir)

	query := "MATCH (s:Service)-[:USES_STORAGE]->(db:Database) WHERE s.id = 'svc:0050' RETURN s.name, db.name, db.engine"
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		res, err := handleGraphQuery(dbRO, query)
		if err != nil {
			b.Fatalf("query failed: %v", err)
		}
		if len(res) == 0 {
			b.Fatal("empty result")
		}
	}
}

func BenchmarkQuery_3Hop_TeamToStorage(b *testing.B) {
	db, dbRO := setupBenchmarkGraph(b, 200)
	defer db.Close()
	defer dbRO.Close()
	defer os.RemoveAll(benchDir)

	query := "MATCH (e:Engineer)-[:MEMBER_OF]->(t:Team)-[:OWNS]->(s:Service)-[:USES_STORAGE]->(db:Database) WHERE e.id = 'engineer:0010' RETURN t.name, s.name, db.name"
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		res, err := handleGraphQuery(dbRO, query)
		if err != nil {
			b.Fatalf("query failed: %v", err)
		}
		if len(res) == 0 {
			b.Fatal("empty result")
		}
	}
}

func BenchmarkQuery_Recursive_MultiHop(b *testing.B) {
	db, dbRO := setupBenchmarkGraph(b, 200)
	defer db.Close()
	defer dbRO.Close()
	defer os.RemoveAll(benchDir)

	query := "MATCH (s:Service {id: 'svc:0100'})-[*1..3]->(target) RETURN DISTINCT target.id LIMIT 20"
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		res, err := handleGraphQuery(dbRO, query)
		if err != nil {
			b.Fatalf("query failed: %v", err)
		}
		if len(res) == 0 {
			b.Fatal("empty result")
		}
	}
}

func BenchmarkSearch_FTS5_Trigram(b *testing.B) {
	db, dbRO := setupBenchmarkGraph(b, 200)
	defer db.Close()
	defer dbRO.Close()
	defer os.RemoveAll(benchDir)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		res, err := handleGraphSearch(dbRO, "PostgreSQL", "", 10)
		if err != nil {
			b.Fatalf("search failed: %v", err)
		}
		if len(res) == 0 {
			b.Fatal("empty result")
		}
	}
}

func BenchmarkBatchUpsert_50Items(b *testing.B) {
	tmpDir, err := os.MkdirTemp("", "cypher-mcp-bench-upsert-*")
	if err != nil {
		b.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "bench_upsert.db")
	db, err := initDatabase(dbPath)
	if err != nil {
		b.Fatalf("init db: %v", err)
	}
	defer db.Close()

	nodes := make([]BatchNodeItem, 50)
	for i := 0; i < 50; i++ {
		nodes[i] = BatchNodeItem{
			ID:   fmt.Sprintf("node:batch:%d", i),
			Kind: "BenchmarkItem",
			Properties: map[string]any{
				"idx":   i,
				"label": "benchmark value",
			},
		}
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := handleBatchUpsert(db, nodes, nil)
		if err != nil {
			b.Fatalf("batch upsert failed: %v", err)
		}
	}
}
