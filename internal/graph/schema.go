package graph

import (
	"database/sql"
	"fmt"
	"strings"

	cyphersql "github.com/vmikhailov/cypher-sql-go"
)

type SchemaDefineOptions struct {
	AllowSchemaEdit bool
	Cascade         bool
	MigrateTo       string
	NewKind         string
	NewRelation     string
}

type KindCount struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

type SchemaKindInfo struct {
	Kind        string `json:"kind"`
	Description string `json:"description,omitempty"`
}

type SchemaRelationInfo struct {
	Relation    string `json:"relation"`
	FromKind    string `json:"from_kind"`
	ToKind      string `json:"to_kind"`
	Description string `json:"description,omitempty"`
}

type SchemaChangelogEntry struct {
	ID         int    `json:"id"`
	Timestamp  string `json:"timestamp"`
	Action     string `json:"action"`
	EntityType string `json:"entity_type"`
	EntityName string `json:"entity_name"`
	Details    string `json:"details,omitempty"`
}

// LogSchemaChange records an administrative schema modification in schema_changelog
func LogSchemaChange(q dbOrTx, action, entityType, entityName, details string) error {
	_, err := q.Exec(`
		INSERT INTO schema_changelog (action, entity_type, entity_name, details)
		VALUES (?, ?, ?, ?);
	`, action, entityType, entityName, details)
	return err
}

type SchemaSummary struct {
	TotalNodes       int                    `json:"total_nodes"`
	TotalEdges       int                    `json:"total_edges"`
	NodeKinds        []KindCount            `json:"node_kinds"`
	EdgeKinds        []KindCount            `json:"edge_kinds"`
	SchemaEnforced   bool                   `json:"schema_enforced"`
	AllowedKinds     []SchemaKindInfo       `json:"allowed_kinds"`
	AllowedRelations []SchemaRelationInfo   `json:"allowed_relations"`
	Changelog        []SchemaChangelogEntry `json:"changelog"`
}

// InspectSchema gathers current graph metrics and taxonomy configuration
func InspectSchema(db *sql.DB, strictFlag ...bool) (*SchemaSummary, error) {
	isStrict := len(strictFlag) > 0 && strictFlag[0]

	nodeRows, err := db.Query("SELECT kind, count(*) FROM nodes GROUP BY kind ORDER BY count(*) DESC")
	if err != nil {
		return nil, fmt.Errorf("query node kinds: %w", err)
	}

	var nodeCounts []KindCount
	totalNodes := 0
	for nodeRows.Next() {
		var kc KindCount
		if err := nodeRows.Scan(&kc.Kind, &kc.Count); err == nil {
			nodeCounts = append(nodeCounts, kc)
			totalNodes += kc.Count
		}
	}
	nodeRows.Close()

	edgeRows, err := db.Query("SELECT kind, count(*) FROM edges GROUP BY kind ORDER BY count(*) DESC")
	if err != nil {
		return nil, fmt.Errorf("query edge kinds: %w", err)
	}

	var edgeCounts []KindCount
	totalEdges := 0
	for edgeRows.Next() {
		var kc KindCount
		if err := edgeRows.Scan(&kc.Kind, &kc.Count); err == nil {
			edgeCounts = append(edgeCounts, kc)
			totalEdges += kc.Count
		}
	}
	edgeRows.Close()

	enforced, err := IsSchemaEnforced(db, isStrict)
	if err != nil {
		return nil, fmt.Errorf("check schema enforcement: %w", err)
	}

	var allowedKinds []SchemaKindInfo
	kRows, err := db.Query("SELECT kind, coalesce(description, '') FROM schema_kinds ORDER BY kind")
	if err == nil {
		for kRows.Next() {
			var ki SchemaKindInfo
			if err := kRows.Scan(&ki.Kind, &ki.Description); err == nil {
				allowedKinds = append(allowedKinds, ki)
			}
		}
		kRows.Close()
	}

	var allowedRelations []SchemaRelationInfo
	rRows, err := db.Query("SELECT rel_type, from_kind, to_kind, coalesce(description, '') FROM schema_relations ORDER BY rel_type, from_kind, to_kind")
	if err == nil {
		for rRows.Next() {
			var ri SchemaRelationInfo
			if err := rRows.Scan(&ri.Relation, &ri.FromKind, &ri.ToKind, &ri.Description); err == nil {
				allowedRelations = append(allowedRelations, ri)
			}
		}
		rRows.Close()
	}

	var changelog []SchemaChangelogEntry
	clRows, err := db.Query("SELECT id, timestamp, action, entity_type, entity_name, coalesce(details, '') FROM schema_changelog ORDER BY id DESC LIMIT 20")
	if err == nil {
		for clRows.Next() {
			var entry SchemaChangelogEntry
			if err := clRows.Scan(&entry.ID, &entry.Timestamp, &entry.Action, &entry.EntityType, &entry.EntityName, &entry.Details); err == nil {
				changelog = append(changelog, entry)
			}
		}
		clRows.Close()
	}
	if changelog == nil {
		changelog = make([]SchemaChangelogEntry, 0)
	}

	return &SchemaSummary{
		TotalNodes:       totalNodes,
		TotalEdges:       totalEdges,
		NodeKinds:        nodeCounts,
		EdgeKinds:        edgeCounts,
		SchemaEnforced:   enforced,
		AllowedKinds:     allowedKinds,
		AllowedRelations: allowedRelations,
		Changelog:        changelog,
	}, nil
}

// DefineSchema performs schema mutations with taxonomy validation
func DefineSchema(db *sql.DB, action, kind, relation, fromKind, toKind, description string, extraOpts ...any) (*SchemaMutationResult, error) {
	var opts SchemaDefineOptions
	opts.AllowSchemaEdit = true
	if len(extraOpts) > 0 {
		switch v := extraOpts[0].(type) {
		case bool:
			opts.AllowSchemaEdit = v
		case SchemaDefineOptions:
			opts = v
			opts.AllowSchemaEdit = true
		}
	}

	action = strings.ToLower(strings.TrimSpace(action))
	switch action {
	case "add_kind":
		kind = strings.TrimSpace(kind)
		if kind == "" {
			return nil, fmt.Errorf("'kind' cannot be empty for add_kind")
		}
		if err := cyphersql.ValidateIdentifier("kind", kind); err != nil {
			return nil, err
		}
		var existingKind string
		err := db.QueryRow("SELECT kind FROM schema_kinds WHERE kind = ? COLLATE NOCASE", kind).Scan(&existingKind)
		if err == nil && existingKind != kind {
			return nil, fmt.Errorf("node kind '%s' conflicts with existing kind '%s' (case-insensitive uniqueness required)", kind, existingKind)
		}
		tx, err := db.Begin()
		if err != nil {
			return nil, fmt.Errorf("begin transaction: %w", err)
		}
		defer tx.Rollback()

		_, err = tx.Exec(`
			INSERT INTO schema_kinds (kind, description) VALUES (?, ?)
			ON CONFLICT(kind) DO UPDATE SET description = excluded.description;
		`, kind, description)
		if err != nil {
			return nil, fmt.Errorf("add kind: %w", err)
		}
		if err := LogSchemaChange(tx, "add_kind", "kind", kind, description); err != nil {
			return nil, fmt.Errorf("log schema change: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit add kind: %w", err)
		}
		return &SchemaMutationResult{
			Status:  "success",
			Message: fmt.Sprintf("Node kind '%s' registered in schema.", kind),
		}, nil

	case "remove_kind":
		kind = strings.TrimSpace(kind)
		if kind == "" {
			return nil, fmt.Errorf("'kind' cannot be empty for remove_kind")
		}
		var canonicalKind string
		err := db.QueryRow("SELECT kind FROM schema_kinds WHERE kind = ? COLLATE NOCASE", kind).Scan(&canonicalKind)
		if err != nil {
			if err == sql.ErrNoRows {
				return nil, fmt.Errorf("node kind '%s' not found in schema", kind)
			}
			return nil, fmt.Errorf("query schema_kinds: %w", err)
		}
		kind = canonicalKind

		var nodeCount, edgeCount int
		if err := db.QueryRow("SELECT count(*) FROM nodes WHERE kind = ?", kind).Scan(&nodeCount); err != nil {
			return nil, fmt.Errorf("count existing nodes: %w", err)
		}
		if err := db.QueryRow(`
			SELECT count(*) FROM edges 
			WHERE from_id IN (SELECT id FROM nodes WHERE kind = ?) 
			   OR to_id IN (SELECT id FROM nodes WHERE kind = ?)
		`, kind, kind).Scan(&edgeCount); err != nil {
			return nil, fmt.Errorf("count existing edges: %w", err)
		}

		if (nodeCount > 0 || edgeCount > 0) && !opts.Cascade && opts.MigrateTo == "" {
			return nil, fmt.Errorf("cannot remove kind '%s': %d nodes (and %d related edges) exist. Use cascade=true to delete data or migrate_to to reassign nodes to another kind", kind, nodeCount, edgeCount)
		}

		tx, err := db.Begin()
		if err != nil {
			return nil, fmt.Errorf("begin transaction: %w", err)
		}
		defer tx.Rollback()

		if opts.MigrateTo != "" {
			migrateTo := strings.TrimSpace(opts.MigrateTo)
			if strings.EqualFold(kind, migrateTo) {
				return nil, fmt.Errorf("cannot migrate kind '%s' to itself", kind)
			}
			if err := cyphersql.ValidateIdentifier("kind", migrateTo); err != nil {
				return nil, fmt.Errorf("invalid migrate_to kind %q: %w", migrateTo, err)
			}
			var targetCanonical string
			err := tx.QueryRow("SELECT kind FROM schema_kinds WHERE kind = ? COLLATE NOCASE", migrateTo).Scan(&targetCanonical)
			if err != nil {
				return nil, fmt.Errorf("migrate_to target kind '%s' does not exist in schema", migrateTo)
			}
			migrateTo = targetCanonical

			if _, err := tx.Exec(`
				DELETE FROM schema_relations 
				WHERE from_kind = ? COLLATE NOCASE
				  AND (rel_type, ?, to_kind) IN (SELECT rel_type, from_kind, to_kind FROM schema_relations)
			`, kind, migrateTo); err != nil {
				return nil, fmt.Errorf("pre-clean schema relations from_kind: %w", err)
			}
			if _, err := tx.Exec(`
				DELETE FROM schema_relations 
				WHERE to_kind = ? COLLATE NOCASE
				  AND (rel_type, from_kind, ?) IN (SELECT rel_type, from_kind, to_kind FROM schema_relations)
			`, kind, migrateTo); err != nil {
				return nil, fmt.Errorf("pre-clean schema relations to_kind: %w", err)
			}

			if _, err := tx.Exec("UPDATE nodes SET kind = ? WHERE kind = ?", migrateTo, kind); err != nil {
				return nil, fmt.Errorf("migrate nodes: %w", err)
			}
			if _, err := tx.Exec("UPDATE schema_relations SET from_kind = ? WHERE from_kind = ?", migrateTo, kind); err != nil {
				return nil, fmt.Errorf("migrate schema relations from_kind: %w", err)
			}
			if _, err := tx.Exec("UPDATE schema_relations SET to_kind = ? WHERE to_kind = ?", migrateTo, kind); err != nil {
				return nil, fmt.Errorf("migrate schema relations to_kind: %w", err)
			}
			if _, err := tx.Exec("DELETE FROM schema_kinds WHERE kind = ?", kind); err != nil {
				return nil, fmt.Errorf("delete schema kind: %w", err)
			}
			detail := fmt.Sprintf("migrated %d nodes to %s", nodeCount, migrateTo)
			if err := LogSchemaChange(tx, "remove_kind", "kind", kind, detail); err != nil {
				return nil, fmt.Errorf("log schema change: %w", err)
			}
			if err := tx.Commit(); err != nil {
				return nil, fmt.Errorf("commit remove kind with migration: %w", err)
			}
			return &SchemaMutationResult{
				Status:  "success",
				Message: fmt.Sprintf("Node kind '%s' removed from schema, %d nodes migrated to '%s'.", kind, nodeCount, migrateTo),
			}, nil
		}

		if opts.Cascade {
			if _, err := tx.Exec(`
				DELETE FROM edges 
				WHERE from_id IN (SELECT id FROM nodes WHERE kind = ?) 
				   OR to_id IN (SELECT id FROM nodes WHERE kind = ?)
			`, kind, kind); err != nil {
				return nil, fmt.Errorf("cascade delete edges: %w", err)
			}
			if _, err := tx.Exec("DELETE FROM nodes WHERE kind = ?", kind); err != nil {
				return nil, fmt.Errorf("cascade delete nodes: %w", err)
			}
		}

		if _, err := tx.Exec("DELETE FROM schema_relations WHERE from_kind = ? COLLATE NOCASE OR to_kind = ? COLLATE NOCASE", kind, kind); err != nil {
			return nil, fmt.Errorf("delete associated schema relations: %w", err)
		}
		if _, err := tx.Exec("DELETE FROM schema_kinds WHERE kind = ?", kind); err != nil {
			return nil, fmt.Errorf("delete schema kind: %w", err)
		}
		detail := fmt.Sprintf("cascade=%v, deleted %d nodes and %d edges", opts.Cascade, nodeCount, edgeCount)
		if err := LogSchemaChange(tx, "remove_kind", "kind", kind, detail); err != nil {
			return nil, fmt.Errorf("log schema change: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit remove kind: %w", err)
		}
		return &SchemaMutationResult{
			Status:  "success",
			Message: fmt.Sprintf("Node kind '%s' removed from schema (cascade=%v, deleted %d nodes).", kind, opts.Cascade, nodeCount),
		}, nil

	case "rename_kind":
		kind = strings.TrimSpace(kind)
		newKind := strings.TrimSpace(opts.NewKind)
		if kind == "" {
			return nil, fmt.Errorf("'kind' cannot be empty for rename_kind")
		}
		if newKind == "" {
			return nil, fmt.Errorf("'new_kind' cannot be empty for rename_kind")
		}
		if strings.EqualFold(kind, newKind) {
			return nil, fmt.Errorf("new_kind '%s' must be different from current kind '%s'", newKind, kind)
		}
		if err := cyphersql.ValidateIdentifier("kind", newKind); err != nil {
			return nil, fmt.Errorf("invalid new_kind %q: %w", newKind, err)
		}
		var canonicalKind, desc string
		err := db.QueryRow("SELECT kind, coalesce(description, '') FROM schema_kinds WHERE kind = ? COLLATE NOCASE", kind).Scan(&canonicalKind, &desc)
		if err != nil {
			return nil, fmt.Errorf("node kind '%s' not found in schema", kind)
		}
		kind = canonicalKind

		var existingDest string
		if err := db.QueryRow("SELECT kind FROM schema_kinds WHERE kind = ? COLLATE NOCASE", newKind).Scan(&existingDest); err == nil {
			return nil, fmt.Errorf("target kind '%s' already exists in schema (use remove_kind with migrate_to instead)", existingDest)
		}

		tx, err := db.Begin()
		if err != nil {
			return nil, fmt.Errorf("begin transaction: %w", err)
		}
		defer tx.Rollback()

		if _, err := tx.Exec("UPDATE schema_kinds SET kind = ? WHERE kind = ?", newKind, kind); err != nil {
			return nil, fmt.Errorf("update schema_kinds: %w", err)
		}
		if _, err := tx.Exec("UPDATE schema_relations SET from_kind = ? WHERE from_kind = ?", newKind, kind); err != nil {
			return nil, fmt.Errorf("update schema_relations from_kind: %w", err)
		}
		if _, err := tx.Exec("UPDATE schema_relations SET to_kind = ? WHERE to_kind = ?", newKind, kind); err != nil {
			return nil, fmt.Errorf("update schema_relations to_kind: %w", err)
		}
		res, err := tx.Exec("UPDATE nodes SET kind = ? WHERE kind = ?", newKind, kind)
		if err != nil {
			return nil, fmt.Errorf("update nodes: %w", err)
		}
		nodesUpdated, _ := res.RowsAffected()

		if err := LogSchemaChange(tx, "rename_kind", "kind", kind, fmt.Sprintf("renamed to %s, updated %d nodes", newKind, nodesUpdated)); err != nil {
			return nil, fmt.Errorf("log schema change: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit rename kind: %w", err)
		}
		return &SchemaMutationResult{
			Status:  "success",
			Message: fmt.Sprintf("Node kind '%s' renamed to '%s' (updated %d nodes and schema rules).", kind, newKind, nodesUpdated),
		}, nil

	case "add_relation":
		relation = strings.TrimSpace(relation)
		fromKind = strings.TrimSpace(fromKind)
		toKind = strings.TrimSpace(toKind)
		if relation == "" {
			return nil, fmt.Errorf("'relation' cannot be empty for add_relation")
		}
		if fromKind == "" {
			return nil, fmt.Errorf("'from_kind' cannot be empty for add_relation")
		}
		if toKind == "" {
			return nil, fmt.Errorf("'to_kind' cannot be empty for add_relation")
		}
		if err := cyphersql.ValidateIdentifier("relationship type", relation); err != nil {
			return nil, err
		}

		var canonFrom, canonTo string
		if err := db.QueryRow("SELECT kind FROM schema_kinds WHERE kind = ? COLLATE NOCASE", fromKind).Scan(&canonFrom); err != nil {
			return nil, fmt.Errorf("source kind '%s' not registered in schema (register with add_kind first)", fromKind)
		}
		if err := db.QueryRow("SELECT kind FROM schema_kinds WHERE kind = ? COLLATE NOCASE", toKind).Scan(&canonTo); err != nil {
			return nil, fmt.Errorf("target kind '%s' not registered in schema (register with add_kind first)", toKind)
		}
		fromKind = canonFrom
		toKind = canonTo

		tx, err := db.Begin()
		if err != nil {
			return nil, fmt.Errorf("begin transaction: %w", err)
		}
		defer tx.Rollback()

		_, err = tx.Exec(`
			INSERT INTO schema_relations (rel_type, from_kind, to_kind, description) VALUES (?, ?, ?, ?)
			ON CONFLICT(rel_type, from_kind, to_kind) DO UPDATE SET description = excluded.description;
		`, relation, fromKind, toKind, description)
		if err != nil {
			return nil, fmt.Errorf("add relation: %w", err)
		}
		detail := fmt.Sprintf("(%s)-[:%s]->(%s): %s", fromKind, relation, toKind, description)
		if err := LogSchemaChange(tx, "add_relation", "relation", relation, detail); err != nil {
			return nil, fmt.Errorf("log schema change: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit add relation: %w", err)
		}
		return &SchemaMutationResult{
			Status:  "success",
			Message: fmt.Sprintf("Relation (%s)-[:%s]->(%s) registered in schema.", fromKind, relation, toKind),
		}, nil

	case "remove_relation":
		relation = strings.TrimSpace(relation)
		fromKind = strings.TrimSpace(fromKind)
		toKind = strings.TrimSpace(toKind)
		if relation == "" {
			return nil, fmt.Errorf("'relation' cannot be empty for remove_relation")
		}

		scoped := fromKind != "" || toKind != ""
		if scoped {
			if fromKind == "" || toKind == "" {
				return nil, fmt.Errorf("both 'from_kind' and 'to_kind' must be provided for scoped remove_relation, or both omitted for global remove")
			}
			var existsRel bool
			err := db.QueryRow(`
				SELECT EXISTS(
					SELECT 1 FROM schema_relations 
					WHERE rel_type = ? COLLATE NOCASE AND from_kind = ? COLLATE NOCASE AND to_kind = ? COLLATE NOCASE
				)`, relation, fromKind, toKind).Scan(&existsRel)
			if err != nil {
				return nil, fmt.Errorf("query schema_relations: %w", err)
			}
			if !existsRel {
				return nil, fmt.Errorf("relation '%s' from '%s' to '%s' not found in schema", relation, fromKind, toKind)
			}
		} else {
			var count int
			if err := db.QueryRow("SELECT count(*) FROM schema_relations WHERE rel_type = ? COLLATE NOCASE", relation).Scan(&count); err != nil {
				return nil, fmt.Errorf("query schema_relations: %w", err)
			}
			if count == 0 {
				return nil, fmt.Errorf("relation '%s' not found in schema", relation)
			}
		}

		var countQuery string
		var countArgs []any
		if scoped {
			countQuery = `
				SELECT count(*) FROM edges e
				JOIN nodes fn ON e.from_id = fn.id
				JOIN nodes tn ON e.to_id = tn.id
				WHERE e.kind = ? COLLATE NOCASE AND fn.kind = ? COLLATE NOCASE AND tn.kind = ? COLLATE NOCASE
			`
			countArgs = []any{relation, fromKind, toKind}
		} else {
			countQuery = "SELECT count(*) FROM edges WHERE kind = ? COLLATE NOCASE"
			countArgs = []any{relation}
		}

		var existingEdgeCount int
		if err := db.QueryRow(countQuery, countArgs...).Scan(&existingEdgeCount); err != nil {
			return nil, fmt.Errorf("count existing edges: %w", err)
		}

		if existingEdgeCount > 0 && !opts.Cascade && opts.MigrateTo == "" {
			return nil, fmt.Errorf("cannot remove relation '%s': %d edges exist. Use cascade=true to delete edges or migrate_to to rename them", relation, existingEdgeCount)
		}

		tx, err := db.Begin()
		if err != nil {
			return nil, fmt.Errorf("begin transaction: %w", err)
		}
		defer tx.Rollback()

		if opts.MigrateTo != "" {
			migrateTo := strings.TrimSpace(opts.MigrateTo)
			if strings.EqualFold(relation, migrateTo) {
				return nil, fmt.Errorf("cannot migrate relation '%s' to itself", relation)
			}
			if err := cyphersql.ValidateIdentifier("relationship type", migrateTo); err != nil {
				return nil, fmt.Errorf("invalid migrate_to relation %q: %w", migrateTo, err)
			}

			if scoped {
				var targetExists bool
				_ = tx.QueryRow(`
					SELECT EXISTS(
						SELECT 1 FROM schema_relations 
						WHERE rel_type = ? COLLATE NOCASE AND from_kind = ? COLLATE NOCASE AND to_kind = ? COLLATE NOCASE
					)`, migrateTo, fromKind, toKind).Scan(&targetExists)
				if !targetExists {
					return nil, fmt.Errorf("migrate_to target relation '%s' between '%s' and '%s' does not exist in schema", migrateTo, fromKind, toKind)
				}
				var collisionCount int
				if err := tx.QueryRow(`
					SELECT count(*) FROM edges e1
					JOIN nodes fn ON e1.from_id = fn.id
					JOIN nodes tn ON e1.to_id = tn.id
					JOIN edges e2 ON e1.from_id = e2.from_id AND e1.to_id = e2.to_id
					WHERE e1.kind = ? COLLATE NOCASE 
					  AND fn.kind = ? COLLATE NOCASE 
					  AND tn.kind = ? COLLATE NOCASE 
					  AND e2.kind = ? COLLATE NOCASE
				`, relation, fromKind, toKind, migrateTo).Scan(&collisionCount); err != nil {
					return nil, fmt.Errorf("pre-check duplicate edges: %w", err)
				}
				if collisionCount > 0 {
					return nil, fmt.Errorf("cannot migrate relation '%s' to '%s': %d edge collision(s) detected", relation, migrateTo, collisionCount)
				}

				if _, err := tx.Exec(`
					UPDATE edges SET kind = ?
					WHERE rowid IN (
						SELECT e.rowid FROM edges e
						JOIN nodes fn ON e.from_id = fn.id
						JOIN nodes tn ON e.to_id = tn.id
						WHERE e.kind = ? COLLATE NOCASE AND fn.kind = ? COLLATE NOCASE AND tn.kind = ? COLLATE NOCASE
					)
				`, migrateTo, relation, fromKind, toKind); err != nil {
					return nil, fmt.Errorf("migrate edges: %w", err)
				}
				if _, err := tx.Exec(`
					DELETE FROM schema_relations 
					WHERE rel_type = ? COLLATE NOCASE AND from_kind = ? COLLATE NOCASE AND to_kind = ? COLLATE NOCASE
				`, relation, fromKind, toKind); err != nil {
					return nil, fmt.Errorf("delete scoped schema relation: %w", err)
				}
			} else {
				if _, err := tx.Exec(`
					DELETE FROM schema_relations 
					WHERE rel_type = ? COLLATE NOCASE 
					  AND (from_kind, to_kind) IN (SELECT from_kind, to_kind FROM schema_relations WHERE rel_type = ? COLLATE NOCASE)
				`, relation, migrateTo); err != nil {
					return nil, fmt.Errorf("pre-clean duplicate schema relations: %w", err)
				}
				if _, err := tx.Exec("UPDATE edges SET kind = ? WHERE kind = ? COLLATE NOCASE", migrateTo, relation); err != nil {
					return nil, fmt.Errorf("migrate edges: %w", err)
				}
				if _, err := tx.Exec("UPDATE schema_relations SET rel_type = ? WHERE rel_type = ? COLLATE NOCASE", migrateTo, relation); err != nil {
					return nil, fmt.Errorf("migrate schema relations: %w", err)
				}
			}

			detail := fmt.Sprintf("migrated %d edges to %s", existingEdgeCount, migrateTo)
			if err := LogSchemaChange(tx, "remove_relation", "relation", relation, detail); err != nil {
				return nil, fmt.Errorf("log schema change: %w", err)
			}
			if err := tx.Commit(); err != nil {
				return nil, fmt.Errorf("commit remove relation with migration: %w", err)
			}
			return &SchemaMutationResult{
				Status:  "success",
				Message: fmt.Sprintf("Relation '%s' removed from schema, %d edges migrated to '%s'.", relation, existingEdgeCount, migrateTo),
			}, nil
		}

		if opts.Cascade {
			if scoped {
				if _, err := tx.Exec(`
					DELETE FROM edges WHERE rowid IN (
						SELECT e.rowid FROM edges e
						JOIN nodes fn ON e.from_id = fn.id
						JOIN nodes tn ON e.to_id = tn.id
						WHERE e.kind = ? COLLATE NOCASE AND fn.kind = ? COLLATE NOCASE AND tn.kind = ? COLLATE NOCASE
					)
				`, relation, fromKind, toKind); err != nil {
					return nil, fmt.Errorf("cascade delete scoped edges: %w", err)
				}
			} else {
				if _, err := tx.Exec("DELETE FROM edges WHERE kind = ? COLLATE NOCASE", relation); err != nil {
					return nil, fmt.Errorf("cascade delete edges: %w", err)
				}
			}
		}

		if scoped {
			if _, err := tx.Exec(`
				DELETE FROM schema_relations 
				WHERE rel_type = ? COLLATE NOCASE AND from_kind = ? COLLATE NOCASE AND to_kind = ? COLLATE NOCASE
			`, relation, fromKind, toKind); err != nil {
				return nil, fmt.Errorf("delete scoped schema relation: %w", err)
			}
		} else {
			if _, err := tx.Exec("DELETE FROM schema_relations WHERE rel_type = ? COLLATE NOCASE", relation); err != nil {
				return nil, fmt.Errorf("delete schema relation: %w", err)
			}
		}

		detail := fmt.Sprintf("cascade=%v, deleted %d edges", opts.Cascade, existingEdgeCount)
		if err := LogSchemaChange(tx, "remove_relation", "relation", relation, detail); err != nil {
			return nil, fmt.Errorf("log schema change: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit remove relation: %w", err)
		}
		return &SchemaMutationResult{
			Status:  "success",
			Message: fmt.Sprintf("Relation '%s' removed from schema (cascade=%v, deleted %d edges).", relation, opts.Cascade, existingEdgeCount),
		}, nil

	case "rename_relation":
		relation = strings.TrimSpace(relation)
		newRelation := strings.TrimSpace(opts.NewRelation)
		fromKind = strings.TrimSpace(fromKind)
		toKind = strings.TrimSpace(toKind)
		if relation == "" {
			return nil, fmt.Errorf("'relation' cannot be empty for rename_relation")
		}
		if newRelation == "" {
			return nil, fmt.Errorf("'new_relation' cannot be empty for rename_relation")
		}
		if strings.EqualFold(relation, newRelation) {
			return nil, fmt.Errorf("new_relation '%s' must be different from current relation '%s'", newRelation, relation)
		}
		if err := cyphersql.ValidateIdentifier("relationship type", newRelation); err != nil {
			return nil, fmt.Errorf("invalid new_relation %q: %w", newRelation, err)
		}

		scoped := fromKind != "" || toKind != ""
		if scoped {
			if fromKind == "" || toKind == "" {
				return nil, fmt.Errorf("both 'from_kind' and 'to_kind' must be provided for scoped rename_relation, or both omitted for global rename")
			}
			var existsRel bool
			err := db.QueryRow(`
				SELECT EXISTS(
					SELECT 1 FROM schema_relations 
					WHERE rel_type = ? COLLATE NOCASE AND from_kind = ? COLLATE NOCASE AND to_kind = ? COLLATE NOCASE
				)`, relation, fromKind, toKind).Scan(&existsRel)
			if err != nil {
				return nil, fmt.Errorf("query schema_relations: %w", err)
			}
			if !existsRel {
				return nil, fmt.Errorf("relation '%s' from '%s' to '%s' not found in schema", relation, fromKind, toKind)
			}
		} else {
			var count int
			if err := db.QueryRow("SELECT count(*) FROM schema_relations WHERE rel_type = ? COLLATE NOCASE", relation).Scan(&count); err != nil {
				return nil, fmt.Errorf("query schema_relations: %w", err)
			}
			if count == 0 {
				return nil, fmt.Errorf("relation '%s' not found in schema", relation)
			}
		}

		tx, err := db.Begin()
		if err != nil {
			return nil, fmt.Errorf("begin transaction: %w", err)
		}
		defer tx.Rollback()

		var edgesUpdated int64
		if scoped {
			var collisionCount int
			if err := tx.QueryRow(`
				SELECT count(*) FROM edges e1
				JOIN nodes fn ON e1.from_id = fn.id
				JOIN nodes tn ON e1.to_id = tn.id
				JOIN edges e2 ON e1.from_id = e2.from_id AND e1.to_id = e2.to_id
				WHERE e1.kind = ? COLLATE NOCASE 
				  AND fn.kind = ? COLLATE NOCASE 
				  AND tn.kind = ? COLLATE NOCASE 
				  AND e2.kind = ? COLLATE NOCASE
			`, relation, fromKind, toKind, newRelation).Scan(&collisionCount); err != nil {
				return nil, fmt.Errorf("pre-check duplicate edges: %w", err)
			}
			if collisionCount > 0 {
				return nil, fmt.Errorf("cannot rename relation '%s' to '%s': %d edge collision(s) detected where an edge with new_relation already connects the same nodes", relation, newRelation, collisionCount)
			}

			if _, err := tx.Exec(`
				UPDATE schema_relations SET rel_type = ? 
				WHERE rel_type = ? COLLATE NOCASE AND from_kind = ? COLLATE NOCASE AND to_kind = ? COLLATE NOCASE
			`, newRelation, relation, fromKind, toKind); err != nil {
				return nil, fmt.Errorf("update scoped schema_relation: %w", err)
			}
			res, err := tx.Exec(`
				UPDATE edges SET kind = ?
				WHERE rowid IN (
					SELECT e.rowid FROM edges e
					JOIN nodes fn ON e.from_id = fn.id
					JOIN nodes tn ON e.to_id = tn.id
					WHERE e.kind = ? COLLATE NOCASE AND fn.kind = ? COLLATE NOCASE AND tn.kind = ? COLLATE NOCASE
				)
			`, newRelation, relation, fromKind, toKind)
			if err != nil {
				return nil, fmt.Errorf("update scoped edges: %w", err)
			}
			edgesUpdated, _ = res.RowsAffected()
		} else {
			var collisionCount int
			if err := tx.QueryRow(`
				SELECT count(*) FROM edges e1
				JOIN edges e2 ON e1.from_id = e2.from_id AND e1.to_id = e2.to_id
				WHERE e1.kind = ? COLLATE NOCASE AND e2.kind = ? COLLATE NOCASE
			`, relation, newRelation).Scan(&collisionCount); err != nil {
				return nil, fmt.Errorf("pre-check duplicate edges: %w", err)
			}
			if collisionCount > 0 {
				return nil, fmt.Errorf("cannot rename relation '%s' to '%s': %d edge collision(s) detected where an edge with new_relation already connects the same nodes", relation, newRelation, collisionCount)
			}

			if _, err := tx.Exec("UPDATE schema_relations SET rel_type = ? WHERE rel_type = ? COLLATE NOCASE", newRelation, relation); err != nil {
				return nil, fmt.Errorf("update schema_relations: %w", err)
			}
			res, err := tx.Exec("UPDATE edges SET kind = ? WHERE kind = ? COLLATE NOCASE", newRelation, relation)
			if err != nil {
				return nil, fmt.Errorf("update edges: %w", err)
			}
			edgesUpdated, _ = res.RowsAffected()
		}

		if err := LogSchemaChange(tx, "rename_relation", "relation", relation, fmt.Sprintf("renamed to %s, updated %d edges", newRelation, edgesUpdated)); err != nil {
			return nil, fmt.Errorf("log schema change: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit rename relation: %w", err)
		}
		return &SchemaMutationResult{
			Status:  "success",
			Message: fmt.Sprintf("Relationship '%s' renamed to '%s' (updated %d edges and schema rules).", relation, newRelation, edgesUpdated),
		}, nil

	default:
		return nil, fmt.Errorf("unknown schema action '%s'. Allowed: add_kind, remove_kind, rename_kind, add_relation, remove_relation, rename_relation", action)
	}
}
