package main

import (
	"path/filepath"
	"testing"
)

func TestReadOnlyPoolReplacement(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Initialize database with tables
	writerDB, err := initDatabase(dbPath)
	if err != nil {
		t.Fatalf("initDatabase failed: %v", err)
	}
	defer writerDB.Close()

	roDB, err := initRODatabase(dbPath)
	if err != nil {
		t.Fatalf("initRODatabase failed: %v", err)
	}
	defer roDB.Close()

	// Evict idle connections so next operation gets a brand new connection
	roDB.SetMaxIdleConns(0)

	// Use first connection
	var qOnly int
	if err := roDB.QueryRow("PRAGMA query_only;").Scan(&qOnly); err != nil {
		t.Fatalf("check query_only on conn 1: %v", err)
	}
	if qOnly != 1 {
		t.Fatalf("expected query_only=1 on conn 1, got %d", qOnly)
	}

	// Next query should use connection 2 (new connection)
	var qOnly2 int
	if err := roDB.QueryRow("PRAGMA query_only;").Scan(&qOnly2); err != nil {
		t.Fatalf("check query_only on conn 2: %v", err)
	}
	if qOnly2 != 1 {
		t.Errorf("FAIL invariant: expected query_only=1 on connection 2, got %d", qOnly2)
	}

	// Try write on connection 2
	_, err = roDB.Exec("INSERT INTO nodes (id, kind, properties) VALUES ('ro1', 'Test', '{}');")
	if err == nil {
		t.Errorf("FAIL invariant: expected write on read-only pool to fail, but it succeeded")
	}
}

func TestReadOnlyConcurrentSecondConnection(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	writerDB, err := initDatabase(dbPath)
	if err != nil {
		t.Fatalf("initDatabase failed: %v", err)
	}
	defer writerDB.Close()

	roDB, err := initRODatabase(dbPath)
	if err != nil {
		t.Fatalf("initRODatabase failed: %v", err)
	}
	defer roDB.Close()

	// Hold connection 1 with a transaction
	tx, err := roDB.Begin()
	if err != nil {
		t.Fatalf("begin tx on conn 1: %v", err)
	}
	defer tx.Rollback()

	// Query via roDB directly - this forces checkout of connection 2 because conn 1 is in tx
	var qOnly2 int
	if err := roDB.QueryRow("PRAGMA query_only;").Scan(&qOnly2); err != nil {
		t.Fatalf("check query_only on concurrent conn 2: %v", err)
	}
	if qOnly2 != 1 {
		t.Errorf("FAIL invariant: expected query_only=1 on concurrent conn 2, got %d", qOnly2)
	}

	_, err = roDB.Exec("INSERT INTO nodes (id, kind, properties) VALUES ('ro2', 'Test', '{}');")
	if err == nil {
		t.Errorf("FAIL invariant: write on concurrent connection 2 in read-only pool succeeded")
	}
}

func TestForeignKeysPoolReplacement(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	writerDB, err := initDatabase(dbPath)
	if err != nil {
		t.Fatalf("initDatabase failed: %v", err)
	}
	defer writerDB.Close()

	// Evict idle connections
	writerDB.SetMaxIdleConns(0)

	// First query
	var fk1 int
	if err := writerDB.QueryRow("PRAGMA foreign_keys;").Scan(&fk1); err != nil {
		t.Fatalf("check foreign_keys on conn 1: %v", err)
	}
	if fk1 != 1 {
		t.Fatalf("expected foreign_keys=1 on conn 1, got %d", fk1)
	}

	// Second query on new connection
	var fk2 int
	if err := writerDB.QueryRow("PRAGMA foreign_keys;").Scan(&fk2); err != nil {
		t.Fatalf("check foreign_keys on conn 2: %v", err)
	}
	if fk2 != 1 {
		t.Errorf("FAIL invariant: expected foreign_keys=1 on conn 2, got %d", fk2)
	}

	// Try inserting dangling edge on conn 3
	_, err = writerDB.Exec("INSERT INTO edges (from_id, to_id, kind, properties) VALUES ('nonexistent1', 'nonexistent2', 'REL', '{}');")
	if err == nil {
		t.Errorf("FAIL invariant: expected foreign key constraint violation on dangling edge, but INSERT succeeded")
	}
}
