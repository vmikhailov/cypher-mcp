package storage

import (
	"path/filepath"
	"testing"
)

func TestStorage_InitDatabaseAndMigrations(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := InitDatabase(dbPath)
	if err != nil {
		t.Fatalf("InitDatabase: %v", err)
	}
	defer db.Close()

	var ver int
	if err := db.QueryRow("PRAGMA user_version").Scan(&ver); err != nil {
		t.Fatalf("query user_version: %v", err)
	}
	if ver != 1 {
		t.Fatalf("expected user_version 1, got %d", ver)
	}

	// Verify RO connection
	dbRO, err := InitRODatabase(dbPath)
	if err != nil {
		t.Fatalf("InitRODatabase: %v", err)
	}
	defer dbRO.Close()

	// Verify query_only is enforced on RO pool
	_, err = dbRO.Exec("CREATE TABLE fail (id INT)")
	if err == nil {
		t.Fatalf("expected write error on RO pool, got nil")
	}
}
