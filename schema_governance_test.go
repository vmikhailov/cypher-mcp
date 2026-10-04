package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoveKind_RejectSelfMigration(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := initDatabase(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("initDatabase: %v", err)
	}
	defer db.Close()

	if _, err := handleSchemaDefine(db, "add_kind", "Person", "", "", "", "Human entity", SchemaDefineOptions{AllowSchemaEdit: true}); err != nil {
		t.Fatalf("add_kind Person: %v", err)
	}
	if _, err := handleSetNode(db, "p1", "Person", map[string]any{"name": "Alice"}, true, false); err != nil {
		t.Fatalf("set node p1: %v", err)
	}

	// Attempt self-migration: remove_kind Person with migrate_to Person
	opts := SchemaDefineOptions{
		AllowSchemaEdit: true,
		MigrateTo:       "Person",
	}
	_, err = handleSchemaDefine(db, "remove_kind", "Person", "", "", "", "", opts)
	if err == nil {
		t.Fatalf("FAIL invariant F05: expected error when migrating kind to itself, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "itself") && !strings.Contains(strings.ToLower(err.Error()), "cannot migrate") {
		t.Errorf("FAIL invariant F05: expected self-migration error message, got %v", err)
	}

	// Verify Person still exists in schema_kinds and nodes
	var kindCount int
	if err := db.QueryRow("SELECT count(*) FROM schema_kinds WHERE kind = 'Person'").Scan(&kindCount); err != nil {
		t.Fatalf("query schema_kinds: %v", err)
	}
	if kindCount != 1 {
		t.Errorf("FAIL invariant F05: Person was deleted from schema_kinds after failed self-migration")
	}
}

func TestRemoveRelation_ValidateTargetRelation(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := initDatabase(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("initDatabase: %v", err)
	}
	defer db.Close()

	if _, err := handleSchemaDefine(db, "add_kind", "Person", "", "", "", "", SchemaDefineOptions{AllowSchemaEdit: true}); err != nil {
		t.Fatalf("add Person: %v", err)
	}
	if _, err := handleSchemaDefine(db, "add_kind", "Company", "", "", "", "", SchemaDefineOptions{AllowSchemaEdit: true}); err != nil {
		t.Fatalf("add Company: %v", err)
	}
	if _, err := handleSchemaDefine(db, "add_relation", "", "WORKS_AT", "Person", "Company", "", SchemaDefineOptions{AllowSchemaEdit: true}); err != nil {
		t.Fatalf("add relation WORKS_AT: %v", err)
	}

	if _, err := handleSetNode(db, "p1", "Person", nil, true, false); err != nil {
		t.Fatalf("set p1: %v", err)
	}
	if _, err := handleSetNode(db, "c1", "Company", nil, true, false); err != nil {
		t.Fatalf("set c1: %v", err)
	}
	if _, err := handleSetEdge(db, "p1", "c1", "WORKS_AT", nil, true, false); err != nil {
		t.Fatalf("set edge: %v", err)
	}

	// Attempt remove_relation with non-existent target relation
	opts := SchemaDefineOptions{
		AllowSchemaEdit: true,
		MigrateTo:       "NON_EXISTENT_REL",
	}
	_, err = handleSchemaDefine(db, "remove_relation", "", "WORKS_AT", "Person", "Company", "", opts)
	if err == nil {
		t.Fatalf("FAIL invariant F06: expected error when migrate_to relation does not exist in schema, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "not exist") && !strings.Contains(strings.ToLower(err.Error()), "schema") {
		t.Errorf("FAIL invariant F06: expected schema validation error, got %v", err)
	}

	// Verify edge still has WORKS_AT
	var relKind string
	if err := db.QueryRow("SELECT kind FROM edges WHERE from_id = 'p1' AND to_id = 'c1'").Scan(&relKind); err != nil {
		t.Fatalf("query edge: %v", err)
	}
	if relKind != "WORKS_AT" {
		t.Errorf("FAIL invariant F06: edge kind was mutated to %s despite invalid migration target", relKind)
	}
}

func TestRenameRelation_RejectPartialScope(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := initDatabase(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("initDatabase: %v", err)
	}
	defer db.Close()

	if _, err := handleSchemaDefine(db, "add_kind", "Person", "", "", "", "", SchemaDefineOptions{AllowSchemaEdit: true}); err != nil {
		t.Fatalf("add Person: %v", err)
	}
	if _, err := handleSchemaDefine(db, "add_relation", "", "KNOWS", "Person", "Person", "", SchemaDefineOptions{AllowSchemaEdit: true}); err != nil {
		t.Fatalf("add relation KNOWS: %v", err)
	}

	// Only provide from_kind without to_kind
	opts := SchemaDefineOptions{
		AllowSchemaEdit: true,
		NewRelation:     "FRIEND",
	}
	_, err = handleSchemaDefine(db, "rename_relation", "", "KNOWS", "Person", "", "", opts)
	if err == nil {
		t.Fatalf("FAIL invariant F07: expected error on partial scope (from_kind only), got nil")
	}

	// Only provide to_kind without from_kind
	_, err = handleSchemaDefine(db, "rename_relation", "", "KNOWS", "", "Person", "", opts)
	if err == nil {
		t.Fatalf("FAIL invariant F07: expected error on partial scope (to_kind only), got nil")
	}
}

func TestRenameRelation_ScopedTargetNotFound_ReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := initDatabase(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("initDatabase: %v", err)
	}
	defer db.Close()

	if _, err := handleSchemaDefine(db, "add_kind", "Person", "", "", "", "", SchemaDefineOptions{AllowSchemaEdit: true}); err != nil {
		t.Fatalf("add Person: %v", err)
	}

	opts := SchemaDefineOptions{
		AllowSchemaEdit: true,
		NewRelation:     "FRIEND",
	}
	_, err = handleSchemaDefine(db, "rename_relation", "", "NONEXISTENT", "Person", "Person", "", opts)
	if err == nil {
		t.Fatalf("FAIL invariant F07: expected error when scoped relation does not exist in schema, got nil")
	}

	// Verify no changelog entry was made
	var logCount int
	if err := db.QueryRow("SELECT count(*) FROM schema_changelog WHERE entity_name = 'NONEXISTENT'").Scan(&logCount); err != nil {
		t.Fatalf("query changelog: %v", err)
	}
	if logCount != 0 {
		t.Errorf("FAIL invariant F07: changelog was written for non-existent relation rename")
	}
}

func TestSchemaMigration_CollisionPreflight(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := initDatabase(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("initDatabase: %v", err)
	}
	defer db.Close()

	if _, err := handleSchemaDefine(db, "add_kind", "Person", "", "", "", "", SchemaDefineOptions{AllowSchemaEdit: true}); err != nil {
		t.Fatalf("add Person: %v", err)
	}
	if _, err := handleSchemaDefine(db, "add_relation", "", "KNOWS", "Person", "Person", "", SchemaDefineOptions{AllowSchemaEdit: true}); err != nil {
		t.Fatalf("add relation KNOWS: %v", err)
	}
	if _, err := handleSchemaDefine(db, "add_relation", "", "FRIEND", "Person", "Person", "", SchemaDefineOptions{AllowSchemaEdit: true}); err != nil {
		t.Fatalf("add relation FRIEND: %v", err)
	}

	// Rename KNOWS to FRIEND between Person and Person -> already exists in schema_relations!
	opts := SchemaDefineOptions{
		AllowSchemaEdit: true,
		NewRelation:     "FRIEND",
	}
	_, err = handleSchemaDefine(db, "rename_relation", "", "KNOWS", "Person", "Person", "", opts)
	if err == nil {
		t.Fatalf("FAIL invariant F11: expected error when renaming to existing relation, got nil")
	}
	// Check that the error is a domain validation error, not a raw SQLite crash
	if strings.Contains(err.Error(), "UNIQUE constraint failed") {
		t.Errorf("FAIL invariant F11: unhandled SQLite UNIQUE constraint error was leaked: %v", err)
	}
}
