package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

const SchemaVersion = 1

// InitDatabase initializes the SQLite database pool for write operations with WAL mode,
// foreign keys, and busy timeout, and executes one-time versioned migrations.
func InitDatabase(dbPath string) (*sql.DB, error) {
	sep := "?"
	if strings.Contains(dbPath, "?") {
		sep = "&"
	}
	dsn := fmt.Sprintf("%s%s_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)", dbPath, sep)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}

	pragmas := []string{
		"PRAGMA journal_mode = WAL;",
		"PRAGMA synchronous = NORMAL;",
		"PRAGMA foreign_keys = ON;",
		"PRAGMA busy_timeout = 5000;",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("execute pragma %s: %w", p, err)
		}
	}

	if err := RunMigrations(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	return db, nil
}

// InitRODatabase initializes a read-only SQLite database pool.
func InitRODatabase(dbPath string) (*sql.DB, error) {
	var dsn string
	if strings.HasPrefix(dbPath, "file:") {
		sep := "&"
		if !strings.Contains(dbPath, "?") {
			sep = "?"
		}
		dsn = fmt.Sprintf("%s%smode=ro&_pragma=query_only(1)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", dbPath, sep)
	} else {
		cleanPath := filepath.ToSlash(dbPath)
		dsn = fmt.Sprintf("file:%s?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", cleanPath)
	}
	dbRO, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite ro db: %w", err)
	}

	if _, err := dbRO.Exec("PRAGMA query_only = ON;"); err != nil {
		dbRO.Close()
		return nil, fmt.Errorf("set ro pragma query_only: %w", err)
	}
	return dbRO, nil
}

// RunMigrations checks PRAGMA user_version and runs pending migrations.
func RunMigrations(db *sql.DB) error {
	var currentVersion int
	if err := db.QueryRow("PRAGMA user_version;").Scan(&currentVersion); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}

	if currentVersion < 1 {
		if err := MigrateToV1(db); err != nil {
			return fmt.Errorf("migrate to v1: %w", err)
		}
	} else {
		if err := EnsureFTSHealthy(db); err != nil {
			return fmt.Errorf("ensure fts healthy: %w", err)
		}
	}

	return nil
}

// MigrateToV1 sets up the initial schema, deduplicates legacy edges, and creates indices.
func MigrateToV1(db *sql.DB) error {
	tables := `
		CREATE TABLE IF NOT EXISTS nodes (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			properties TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS edges (
			from_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
			to_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
			kind TEXT NOT NULL,
			properties TEXT NOT NULL
		);
	`
	if _, err := db.Exec(tables); err != nil {
		return fmt.Errorf("init tables: %w", err)
	}

	dedupEdges := `
		DELETE FROM edges
		WHERE rowid NOT IN (
			SELECT min(rowid)
			FROM edges
			GROUP BY from_id, to_id, kind
		);
	`
	if _, err := db.Exec(dedupEdges); err != nil {
		return fmt.Errorf("deduplicate edges: %w", err)
	}

	indices := `
		CREATE UNIQUE INDEX IF NOT EXISTS idx_edges_unique ON edges(from_id, to_id, kind);
		CREATE INDEX IF NOT EXISTS idx_edges_from_kind ON edges(from_id, kind);
		CREATE INDEX IF NOT EXISTS idx_edges_to_kind ON edges(to_id, kind);
		CREATE INDEX IF NOT EXISTS idx_nodes_kind ON nodes(kind);
	`
	if _, err := db.Exec(indices); err != nil {
		return fmt.Errorf("init indices: %w", err)
	}

	vectorTables := `
		CREATE TABLE IF NOT EXISTS entity_embeddings (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
			alias TEXT NOT NULL,
			embedding BLOB NOT NULL,
			UNIQUE(node_id, alias)
		);
		CREATE INDEX IF NOT EXISTS idx_entity_embeddings_node ON entity_embeddings(node_id);
		CREATE INDEX IF NOT EXISTS idx_entity_embeddings_alias ON entity_embeddings(alias);
	`
	if _, err := db.Exec(vectorTables); err != nil {
		return fmt.Errorf("init vector tables: %w", err)
	}

	if err := RebuildFTSIndex(db); err != nil {
		return fmt.Errorf("init fts: %w", err)
	}

	schemaDDL := `
		CREATE TABLE IF NOT EXISTS schema_kinds (
			kind TEXT PRIMARY KEY COLLATE NOCASE,
			description TEXT
		);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_schema_kinds_nocase ON schema_kinds(kind COLLATE NOCASE);

		CREATE TABLE IF NOT EXISTS schema_relations (
			rel_type TEXT NOT NULL COLLATE NOCASE,
			from_kind TEXT NOT NULL COLLATE NOCASE,
			to_kind TEXT NOT NULL COLLATE NOCASE,
			description TEXT,
			PRIMARY KEY(rel_type, from_kind, to_kind)
		);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_schema_relations_nocase ON schema_relations(rel_type COLLATE NOCASE, from_kind COLLATE NOCASE, to_kind COLLATE NOCASE);
		CREATE INDEX IF NOT EXISTS idx_schema_rel_lookup ON schema_relations(rel_type, from_kind, to_kind);

		CREATE TABLE IF NOT EXISTS schema_changelog (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
			action TEXT NOT NULL,
			entity_type TEXT NOT NULL,
			entity_name TEXT NOT NULL,
			details TEXT
		);
	`
	if _, err := db.Exec(schemaDDL); err != nil {
		return fmt.Errorf("init schema tables: %w", err)
	}

	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d;", SchemaVersion)); err != nil {
		return fmt.Errorf("set user_version: %w", err)
	}

	return nil
}

// EnsureFTSHealthy checks if FTS5 table is queryable; if missing/corrupt, rebuilds it.
func EnsureFTSHealthy(db *sql.DB) error {
	var dummy int
	err := db.QueryRow("SELECT 1 FROM nodes_fts LIMIT 1;").Scan(&dummy)
	if err == nil || errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return RebuildFTSIndex(db)
}

// RebuildFTSIndex recreates the FTS5 virtual table, triggers, and index content.
func RebuildFTSIndex(db *sql.DB) error {
	ftsDDL := `
		CREATE VIRTUAL TABLE IF NOT EXISTS nodes_fts USING fts5(id, kind, content, tokenize='trigram');

		DROP TRIGGER IF EXISTS nodes_ai;
		DROP TRIGGER IF EXISTS nodes_ad;
		DROP TRIGGER IF EXISTS nodes_au;

		CREATE TRIGGER nodes_ai AFTER INSERT ON nodes BEGIN
			INSERT INTO nodes_fts(rowid, id, kind, content) VALUES (
				new.rowid, 
				new.id, 
				new.kind, 
				CASE 
					WHEN json_valid(new.properties) THEN (SELECT coalesce(group_concat(value, ' '), '') FROM json_tree(new.properties) WHERE atom IS NOT NULL)
					ELSE new.properties 
				END
			);
		END;
		CREATE TRIGGER nodes_ad AFTER DELETE ON nodes BEGIN
			DELETE FROM nodes_fts WHERE rowid = old.rowid;
		END;
		CREATE TRIGGER nodes_au AFTER UPDATE ON nodes BEGIN
			DELETE FROM nodes_fts WHERE rowid = old.rowid;
			INSERT INTO nodes_fts(rowid, id, kind, content) VALUES (
				new.rowid, 
				new.id, 
				new.kind, 
				CASE 
					WHEN json_valid(new.properties) THEN (SELECT coalesce(group_concat(value, ' '), '') FROM json_tree(new.properties) WHERE atom IS NOT NULL)
					ELSE new.properties 
				END
			);
		END;
	`
	if _, err := db.Exec(ftsDDL); err != nil {
		return fmt.Errorf("create fts5 ddl: %w", err)
	}

	rebuildFTS := `
		DELETE FROM nodes_fts;
		INSERT INTO nodes_fts(rowid, id, kind, content)
		SELECT rowid, id, kind, 
			CASE 
				WHEN json_valid(properties) THEN (SELECT coalesce(group_concat(value, ' '), '') FROM json_tree(properties) WHERE atom IS NOT NULL)
				ELSE properties 
			END
		FROM nodes;
	`
	if _, err := db.Exec(rebuildFTS); err != nil {
		return fmt.Errorf("populate fts5: %w", err)
	}

	return nil
}
