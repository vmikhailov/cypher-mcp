package main

import (
	"path/filepath"
	"testing"
)

func TestSelfLoop_KindChangeValidation(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := initDatabase(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("initDatabase: %v", err)
	}
	defer db.Close()

	// Register schema
	if _, err := handleSchemaDefine(db, "add_kind", "Person", "", "", "", "", SchemaDefineOptions{AllowSchemaEdit: true}); err != nil {
		t.Fatalf("add Person: %v", err)
	}
	if _, err := handleSchemaDefine(db, "add_kind", "Leader", "", "", "", "", SchemaDefineOptions{AllowSchemaEdit: true}); err != nil {
		t.Fatalf("add Leader: %v", err)
	}
	// Self-loops allowed for both
	if _, err := handleSchemaDefine(db, "add_relation", "", "MANAGES", "Person", "Person", "", SchemaDefineOptions{AllowSchemaEdit: true}); err != nil {
		t.Fatalf("add Person-MANAGES-Person: %v", err)
	}
	if _, err := handleSchemaDefine(db, "add_relation", "", "MANAGES", "Leader", "Leader", "", SchemaDefineOptions{AllowSchemaEdit: true}); err != nil {
		t.Fatalf("add Leader-MANAGES-Leader: %v", err)
	}

	// Create node with kind Person and self-loop edge
	if _, err := handleSetNode(db, "p1", "Person", map[string]any{"name": "Alice"}, true, false); err != nil {
		t.Fatalf("set node p1: %v", err)
	}
	if _, err := handleSetEdge(db, "p1", "p1", "MANAGES", nil, true, false); err != nil {
		t.Fatalf("set self-loop edge: %v", err)
	}

	// Changing p1 to Leader must succeed because Leader-MANAGES-Leader is valid in schema
	_, err = handleSetNode(db, "p1", "Leader", nil, true, true)
	if err != nil {
		t.Fatalf("FAIL invariant F08: kind change for node with valid self-loop rejected: %v", err)
	}

	// Verify node kind was successfully updated
	var actualKind string
	if err := db.QueryRow("SELECT kind FROM nodes WHERE id = 'p1'").Scan(&actualKind); err != nil {
		t.Fatalf("query kind: %v", err)
	}
	if actualKind != "Leader" {
		t.Errorf("FAIL invariant F08: expected kind Leader, got %s", actualKind)
	}
}

func TestBatchUpsert_CoordinatedKindChange(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := initDatabase(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("initDatabase: %v", err)
	}
	defer db.Close()

	// Register schema
	if _, err := handleSchemaDefine(db, "add_kind", "Person", "", "", "", "", SchemaDefineOptions{AllowSchemaEdit: true}); err != nil {
		t.Fatalf("add Person: %v", err)
	}
	if _, err := handleSchemaDefine(db, "add_kind", "Company", "", "", "", "", SchemaDefineOptions{AllowSchemaEdit: true}); err != nil {
		t.Fatalf("add Company: %v", err)
	}
	if _, err := handleSchemaDefine(db, "add_kind", "Employee", "", "", "", "", SchemaDefineOptions{AllowSchemaEdit: true}); err != nil {
		t.Fatalf("add Employee: %v", err)
	}
	if _, err := handleSchemaDefine(db, "add_kind", "Org", "", "", "", "", SchemaDefineOptions{AllowSchemaEdit: true}); err != nil {
		t.Fatalf("add Org: %v", err)
	}

	// Permitted: Person->Company and Employee->Org
	if _, err := handleSchemaDefine(db, "add_relation", "", "AFFILIATED", "Person", "Company", "", SchemaDefineOptions{AllowSchemaEdit: true}); err != nil {
		t.Fatalf("add Person-AFFILIATED-Company: %v", err)
	}
	if _, err := handleSchemaDefine(db, "add_relation", "", "AFFILIATED", "Employee", "Org", "", SchemaDefineOptions{AllowSchemaEdit: true}); err != nil {
		t.Fatalf("add Employee-AFFILIATED-Org: %v", err)
	}

	// Initial graph: p1 (Person) -> c1 (Company)
	if _, err := handleSetNode(db, "p1", "Person", nil, true, false); err != nil {
		t.Fatalf("set p1: %v", err)
	}
	if _, err := handleSetNode(db, "c1", "Company", nil, true, false); err != nil {
		t.Fatalf("set c1: %v", err)
	}
	if _, err := handleSetEdge(db, "p1", "c1", "AFFILIATED", nil, true, false); err != nil {
		t.Fatalf("set edge: %v", err)
	}

	// Mutate BOTH p1 and c1 in a single batch to Employee and Org
	batchNodes := []BatchNodeItem{
		{ID: "p1", Kind: "Employee"},
		{ID: "c1", Kind: "Org"},
	}
	_, err = handleBatchUpsert(db, batchNodes, nil, true)
	if err != nil {
		t.Fatalf("FAIL invariant F10: coordinated batch kind change failed: %v", err)
	}

	var k1, k2 string
	if err := db.QueryRow("SELECT kind FROM nodes WHERE id = 'p1'").Scan(&k1); err != nil {
		t.Fatalf("query p1: %v", err)
	}
	if err := db.QueryRow("SELECT kind FROM nodes WHERE id = 'c1'").Scan(&k2); err != nil {
		t.Fatalf("query c1: %v", err)
	}
	if k1 != "Employee" || k2 != "Org" {
		t.Errorf("FAIL invariant F10: expected Employee and Org, got %s and %s", k1, k2)
	}
}
