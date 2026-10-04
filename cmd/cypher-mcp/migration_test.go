package main

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestMigration_VersionedStartup_NoRebuild(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// 1. Initial startup runs migration to v1
	func() {
		db, err := initDatabase(dbPath)
		if err != nil {
			t.Fatalf("first initDatabase: %v", err)
		}
		defer db.Close()

		var version int
		if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
			t.Fatalf("query user_version: %v", err)
		}
		if version != 1 {
			t.Fatalf("expected user_version 1, got %d", version)
		}

		// Add node
		if _, err := handleSetNode(db, "p1", "Person", map[string]any{"name": "Alice"}, false, false); err != nil {
			t.Fatalf("set node: %v", err)
		}
	}()

	// 2. Second startup should check version and NOT rebuild FTS
	db2, err := initDatabase(dbPath)
	if err != nil {
		t.Fatalf("second initDatabase: %v", err)
	}
	defer db2.Close()

	var version2 int
	if err := db2.QueryRow("PRAGMA user_version").Scan(&version2); err != nil {
		t.Fatalf("query user_version: %v", err)
	}
	if version2 != 1 {
		t.Fatalf("expected user_version 1 on reopen, got %d", version2)
	}

	// Verify node and FTS index are intact
	var count int
	if err := db2.QueryRow("SELECT count(*) FROM nodes WHERE id = 'p1'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("expected 1 node, got %d", count)
	}
	var ftsCount int
	if err := db2.QueryRow("SELECT count(*) FROM nodes_fts WHERE id = 'p1'").Scan(&ftsCount); err != nil || ftsCount != 1 {
		t.Fatalf("expected 1 fts entry, got %d", ftsCount)
	}
}

func TestMigration_FromLegacyV0(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "legacy_v0.db")

	// Manually create legacy v0 tables without user_version
	rawDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	rawSchema := `
		CREATE TABLE nodes (id TEXT PRIMARY KEY, kind TEXT NOT NULL, properties TEXT NOT NULL);
		CREATE TABLE edges (from_id TEXT NOT NULL, to_id TEXT NOT NULL, kind TEXT NOT NULL, properties TEXT NOT NULL);
		INSERT INTO nodes VALUES ('p1', 'Person', '{"name":"Alice"}'), ('p2', 'Person', '{"name":"Bob"}');
		INSERT INTO edges VALUES ('p1', 'p2', 'KNOWS', '{}'), ('p1', 'p2', 'KNOWS', '{"dup":true}');
	`
	if _, err := rawDB.Exec(rawSchema); err != nil {
		t.Fatalf("seed legacy db: %v", err)
	}
	rawDB.Close()

	// initDatabase should migrate and deduplicate
	migratedDB, err := initDatabase(dbPath)
	if err != nil {
		t.Fatalf("initDatabase on legacy db: %v", err)
	}
	defer migratedDB.Close()

	var version int
	if err := migratedDB.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 1 {
		t.Fatalf("expected user_version 1 after migration, got %d (err: %v)", version, err)
	}

	var edgeCount int
	if err := migratedDB.QueryRow("SELECT count(*) FROM edges WHERE from_id = 'p1' AND to_id = 'p2'").Scan(&edgeCount); err != nil || edgeCount != 1 {
		t.Fatalf("expected duplicate edges deduplicated to 1, got %d (err: %v)", edgeCount, err)
	}

	// Verify FTS5 table was created and populated
	var ftsCount int
	if err := migratedDB.QueryRow("SELECT count(*) FROM nodes_fts").Scan(&ftsCount); err != nil || ftsCount != 2 {
		t.Fatalf("expected FTS entries populated for 2 nodes, got %d (err: %v)", ftsCount, err)
	}
}

func TestMigration_PreservesExistingEmbeddingsAndAliases(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "legacy_embeddings.db")

	rawDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	rawSchema := `
		CREATE TABLE nodes (id TEXT PRIMARY KEY, kind TEXT NOT NULL, properties TEXT NOT NULL);
		CREATE TABLE edges (from_id TEXT NOT NULL, to_id TEXT NOT NULL, kind TEXT NOT NULL, properties TEXT NOT NULL);
		CREATE TABLE node_embeddings (node_id TEXT PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE, embedding BLOB NOT NULL);
		CREATE TABLE entity_aliases (alias TEXT PRIMARY KEY, canonical_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE);
		INSERT INTO nodes VALUES ('p1', 'Person', '{"name":"Alice"}');
		INSERT INTO entity_aliases VALUES ('alicia', 'p1');
	`
	if _, err := rawDB.Exec(rawSchema); err != nil {
		t.Fatalf("seed legacy db: %v", err)
	}
	rawDB.Close()

	migratedDB, err := initDatabase(dbPath)
	if err != nil {
		t.Fatalf("initDatabase on legacy db with aliases: %v", err)
	}
	defer migratedDB.Close()

	var canonical string
	if err := migratedDB.QueryRow("SELECT canonical_id FROM entity_aliases WHERE alias = 'alicia'").Scan(&canonical); err != nil || canonical != "p1" {
		t.Fatalf("expected alias 'alicia' mapped to 'p1', got %s (err: %v)", canonical, err)
	}
}

func TestMigration_CrashSafeFTSRebuild(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "fts_repair.db")

	func() {
		db, err := initDatabase(dbPath)
		if err != nil {
			t.Fatalf("first initDatabase: %v", err)
		}
		defer db.Close()

		if _, err := handleSetNode(db, "repair1", "Device", map[string]any{"model": "Pixel"}, false, false); err != nil {
			t.Fatalf("set node: %v", err)
		}

		// Simulate crash or corruption where nodes_fts was dropped while user_version remains 1
		if _, err := db.Exec("DROP TABLE nodes_fts;"); err != nil {
			t.Fatalf("drop fts table: %v", err)
		}
	}()

	// Re-opening should detect damaged FTS and rebuild it safely
	db2, err := initDatabase(dbPath)
	if err != nil {
		t.Fatalf("second initDatabase should repair FTS without error: %v", err)
	}
	defer db2.Close()

	var ftsCount int
	if err := db2.QueryRow("SELECT count(*) FROM nodes_fts WHERE id = 'repair1'").Scan(&ftsCount); err != nil || ftsCount != 1 {
		t.Fatalf("expected nodes_fts repaired with 1 entry, got %d (err: %v)", ftsCount, err)
	}
}
