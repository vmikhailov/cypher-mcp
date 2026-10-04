package graph

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	cyphersql "github.com/vmikhailov/cypher-sql-go"
)

// SetNode upserts a node and returns the domain Node model
func SetNode(db *sql.DB, id, kind string, props map[string]any, isStrict, overwrite bool) (*Node, error) {
	id = strings.TrimSpace(id)
	kind = strings.TrimSpace(kind)
	if id == "" {
		return nil, fmt.Errorf("node id cannot be empty")
	}
	if kind == "" {
		return nil, fmt.Errorf("node kind cannot be empty")
	}

	if err := cyphersql.ValidateIdentifier("kind", kind); err != nil {
		return nil, err
	}

	var canonicalKind string
	if err := db.QueryRow("SELECT kind FROM schema_kinds WHERE kind = ? COLLATE NOCASE", kind).Scan(&canonicalKind); err == nil {
		kind = canonicalKind
	}

	if err := ValidateNodeKind(db, kind, isStrict); err != nil {
		return nil, err
	}

	// Validate relationship consistency if updating an existing node's kind
	var existingKind string
	if err := db.QueryRow("SELECT kind FROM nodes WHERE id = ?", id).Scan(&existingKind); err == nil && !strings.EqualFold(existingKind, kind) {
		if err := ValidateNodeKindChange(db, id, existingKind, kind, isStrict); err != nil {
			return nil, err
		}
	}

	if props == nil {
		props = make(map[string]any)
	}
	propsJSON, err := json.Marshal(props)
	if err != nil {
		return nil, fmt.Errorf("marshal properties: %w", err)
	}

	var query string
	if !overwrite {
		query = `
			INSERT INTO nodes (id, kind, properties) VALUES (?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET 
				kind = excluded.kind, 
				properties = json_patch(CASE WHEN json_valid(nodes.properties) THEN nodes.properties ELSE '{}' END, excluded.properties);
		`
	} else {
		query = `
			INSERT INTO nodes (id, kind, properties) VALUES (?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET kind = excluded.kind, properties = excluded.properties;
		`
	}
	if _, err := db.Exec(query, id, kind, string(propsJSON)); err != nil {
		return nil, fmt.Errorf("upsert node %s: %w", id, err)
	}

	// Return updated node properties from DB
	var finalPropsRaw string
	var finalKind string
	if err := db.QueryRow("SELECT kind, properties FROM nodes WHERE id = ?", id).Scan(&finalKind, &finalPropsRaw); err == nil {
		var finalProps map[string]any
		_ = json.Unmarshal([]byte(finalPropsRaw), &finalProps)
		return &Node{
			ID:         id,
			Kind:       finalKind,
			Properties: finalProps,
		}, nil
	}

	return &Node{
		ID:         id,
		Kind:       kind,
		Properties: props,
	}, nil
}

// SetEdge upserts an edge and returns the domain Edge model
func SetEdge(db *sql.DB, from, to, kind string, props map[string]any, isStrict, overwrite bool) (*Edge, error) {
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	kind = strings.TrimSpace(kind)
	if from == "" {
		return nil, fmt.Errorf("edge 'from' cannot be empty")
	}
	if to == "" {
		return nil, fmt.Errorf("edge 'to' cannot be empty")
	}
	if kind == "" {
		return nil, fmt.Errorf("edge 'kind' cannot be empty")
	}

	if err := cyphersql.ValidateIdentifier("relationship type", kind); err != nil {
		return nil, err
	}

	if props == nil {
		props = make(map[string]any)
	}
	propsJSON, err := json.Marshal(props)
	if err != nil {
		return nil, fmt.Errorf("marshal properties: %w", err)
	}

	tx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	var fromKind, toKind string
	if err := tx.QueryRow("SELECT kind FROM nodes WHERE id = ?", from).Scan(&fromKind); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("source node '%s' does not exist", from)
		}
		return nil, fmt.Errorf("check from node: %w", err)
	}

	if err := tx.QueryRow("SELECT kind FROM nodes WHERE id = ?", to).Scan(&toKind); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("target node '%s' does not exist", to)
		}
		return nil, fmt.Errorf("check to node: %w", err)
	}

	var canonicalRel string
	if err := tx.QueryRow(`
		SELECT rel_type FROM schema_relations 
		WHERE rel_type = ? COLLATE NOCASE 
		  AND from_kind = ? COLLATE NOCASE 
		  AND to_kind = ? COLLATE NOCASE
	`, kind, fromKind, toKind).Scan(&canonicalRel); err == nil {
		kind = canonicalRel
	}

	if err := ValidateEdgeRelation(tx, fromKind, toKind, kind, isStrict); err != nil {
		return nil, err
	}

	var upsertQuery string
	if !overwrite {
		upsertQuery = `
			INSERT INTO edges (from_id, to_id, kind, properties) VALUES (?, ?, ?, ?)
			ON CONFLICT(from_id, to_id, kind) DO UPDATE SET 
				properties = json_patch(CASE WHEN json_valid(edges.properties) THEN edges.properties ELSE '{}' END, excluded.properties);
		`
	} else {
		upsertQuery = `
			INSERT INTO edges (from_id, to_id, kind, properties) VALUES (?, ?, ?, ?)
			ON CONFLICT(from_id, to_id, kind) DO UPDATE SET properties = excluded.properties;
		`
	}
	if _, err := tx.Exec(upsertQuery, from, to, kind, string(propsJSON)); err != nil {
		return nil, fmt.Errorf("upsert edge (%s)-[%s]->(%s): %w", from, kind, to, err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit edge upsert: %w", err)
	}

	return &Edge{
		FromID:     from,
		ToID:       to,
		Kind:       kind,
		Properties: props,
	}, nil
}

// DeleteNode removes a node and all connected edges
func DeleteNode(db *sql.DB, id string) (*DeleteResult, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("node id cannot be empty")
	}
	tx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM edges WHERE from_id = ? OR to_id = ?", id, id); err != nil {
		return nil, fmt.Errorf("delete connected edges: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM nodes WHERE id = ?", id); err != nil {
		return nil, fmt.Errorf("delete node %s: %w", id, err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit delete: %w", err)
	}

	return &DeleteResult{
		ID:     id,
		Status: "deleted",
	}, nil
}

// BatchUpsert processes multiple nodes and edges in a single atomic transaction
func BatchUpsert(db *sql.DB, nodes []BatchNodeItem, edges []BatchEdgeItem, isStrict bool) (*BatchUpsertResult, error) {
	if len(nodes) == 0 && len(edges) == 0 {
		return &BatchUpsertResult{NodesCount: 0, EdgesCount: 0, Status: "empty"}, nil
	}

	// Pre-validate all nodes and edges before starting transaction
	for i, n := range nodes {
		id := strings.TrimSpace(n.ID)
		kind := strings.TrimSpace(n.Kind)
		if id == "" {
			return nil, fmt.Errorf("node[%d]: id cannot be empty", i)
		}
		if kind == "" {
			return nil, fmt.Errorf("node[%d] (%s): kind cannot be empty", i, id)
		}
		if err := cyphersql.ValidateIdentifier("kind", kind); err != nil {
			return nil, fmt.Errorf("node[%d] (%s): %w", i, id, err)
		}
	}
	for i, e := range edges {
		from := strings.TrimSpace(e.From)
		to := strings.TrimSpace(e.To)
		kind := strings.TrimSpace(e.Kind)
		if from == "" {
			return nil, fmt.Errorf("edge[%d]: from cannot be empty", i)
		}
		if to == "" {
			return nil, fmt.Errorf("edge[%d]: to cannot be empty", i)
		}
		if kind == "" {
			return nil, fmt.Errorf("edge[%d]: kind cannot be empty", i)
		}
		if err := cyphersql.ValidateIdentifier("relationship type", kind); err != nil {
			return nil, fmt.Errorf("edge[%d]: %w", i, err)
		}
	}

	tx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	nodeStmt, err := tx.Prepare(`
		INSERT INTO nodes (id, kind, properties) VALUES (?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET 
			kind = excluded.kind, 
			properties = json_patch(CASE WHEN json_valid(nodes.properties) THEN nodes.properties ELSE '{}' END, excluded.properties);
	`)
	if err != nil {
		return nil, fmt.Errorf("prepare node upsert: %w", err)
	}
	defer nodeStmt.Close()

	// Precompute canonical target kinds for all nodes in the batch to coordinate migrations
	batchTargetKinds := make(map[string]string, len(nodes))
	for _, n := range nodes {
		id := strings.TrimSpace(n.ID)
		kind := strings.TrimSpace(n.Kind)
		if id != "" && kind != "" {
			var canonicalKind string
			if err := tx.QueryRow("SELECT kind FROM schema_kinds WHERE kind = ? COLLATE NOCASE", kind).Scan(&canonicalKind); err == nil {
				kind = canonicalKind
			}
			batchTargetKinds[id] = kind
		}
	}

	upsertedNodes := 0
	nodeKindCache := make(map[string]string, len(nodes))
	for i, n := range nodes {
		id := strings.TrimSpace(n.ID)
		kind := strings.TrimSpace(n.Kind)
		if id == "" {
			return nil, fmt.Errorf("node[%d]: id cannot be empty", i)
		}
		if kind == "" {
			return nil, fmt.Errorf("node[%d] (%s): kind cannot be empty", i, id)
		}
		if err := cyphersql.ValidateIdentifier("kind", kind); err != nil {
			return nil, fmt.Errorf("node[%d] (%s): %w", i, id, err)
		}
		var canonicalKind string
		if err := tx.QueryRow("SELECT kind FROM schema_kinds WHERE kind = ? COLLATE NOCASE", kind).Scan(&canonicalKind); err == nil {
			kind = canonicalKind
		}
		if err := ValidateNodeKind(tx, kind, isStrict); err != nil {
			return nil, fmt.Errorf("node[%d] (%s): %w", i, id, err)
		}

		var existingKind string
		if err := tx.QueryRow("SELECT kind FROM nodes WHERE id = ?", id).Scan(&existingKind); err == nil && !strings.EqualFold(existingKind, kind) {
			if err := ValidateNodeKindChange(tx, id, existingKind, kind, isStrict, batchTargetKinds); err != nil {
				return nil, fmt.Errorf("node[%d] (%s): %w", i, id, err)
			}
		}
		props := n.Properties
		if props == nil {
			props = make(map[string]any)
		}
		propsJSON, err := json.Marshal(props)
		if err != nil {
			return nil, fmt.Errorf("node[%d] (%s): marshal properties: %w", i, id, err)
		}
		if _, err := nodeStmt.Exec(id, kind, string(propsJSON)); err != nil {
			return nil, fmt.Errorf("upsert node %s: %w", id, err)
		}
		nodeKindCache[id] = kind
		upsertedNodes++
	}

	getNodeKind := func(nodeID string) (string, error) {
		if k, ok := nodeKindCache[nodeID]; ok {
			return k, nil
		}
		var k string
		err := tx.QueryRow("SELECT kind FROM nodes WHERE id = ?", nodeID).Scan(&k)
		if err != nil {
			if err == sql.ErrNoRows {
				return "", sql.ErrNoRows
			}
			return "", err
		}
		nodeKindCache[nodeID] = k
		return k, nil
	}

	edgeStmt, err := tx.Prepare(`
		INSERT INTO edges (from_id, to_id, kind, properties) VALUES (?, ?, ?, ?)
		ON CONFLICT(from_id, to_id, kind) DO UPDATE SET 
			properties = json_patch(CASE WHEN json_valid(edges.properties) THEN edges.properties ELSE '{}' END, excluded.properties);
	`)
	if err != nil {
		return nil, fmt.Errorf("prepare edge upsert: %w", err)
	}
	defer edgeStmt.Close()

	upsertedEdges := 0
	for i, e := range edges {
		from := strings.TrimSpace(e.From)
		to := strings.TrimSpace(e.To)
		kind := strings.TrimSpace(e.Kind)
		if from == "" {
			return nil, fmt.Errorf("edge[%d]: 'from' cannot be empty", i)
		}
		if to == "" {
			return nil, fmt.Errorf("edge[%d]: 'to' cannot be empty", i)
		}
		if kind == "" {
			return nil, fmt.Errorf("edge[%d]: 'kind' cannot be empty", i)
		}
		if err := cyphersql.ValidateIdentifier("relationship type", kind); err != nil {
			return nil, fmt.Errorf("edge[%d]: %w", i, err)
		}

		fromKind, err := getNodeKind(from)
		if err != nil {
			if err == sql.ErrNoRows {
				return nil, fmt.Errorf("edge[%d]: source node '%s' does not exist", i, from)
			}
			return nil, fmt.Errorf("edge[%d]: check source node '%s': %w", i, from, err)
		}

		toKind, err := getNodeKind(to)
		if err != nil {
			if err == sql.ErrNoRows {
				return nil, fmt.Errorf("edge[%d]: target node '%s' does not exist", i, to)
			}
			return nil, fmt.Errorf("edge[%d]: check target node '%s': %w", i, to, err)
		}

		var canonicalRel string
		if err := tx.QueryRow(`
			SELECT rel_type FROM schema_relations 
			WHERE rel_type = ? COLLATE NOCASE 
			  AND from_kind = ? COLLATE NOCASE 
			  AND to_kind = ? COLLATE NOCASE
		`, kind, fromKind, toKind).Scan(&canonicalRel); err == nil {
			kind = canonicalRel
		}

		if err := ValidateEdgeRelation(tx, fromKind, toKind, kind, isStrict); err != nil {
			return nil, fmt.Errorf("edge[%d] (%s)-[:%s]->(%s): %w", i, from, kind, to, err)
		}

		props := e.Properties
		if props == nil {
			props = make(map[string]any)
		}
		propsJSON, err := json.Marshal(props)
		if err != nil {
			return nil, fmt.Errorf("edge[%d]: marshal properties: %w", i, err)
		}

		if _, err := edgeStmt.Exec(from, to, kind, string(propsJSON)); err != nil {
			return nil, fmt.Errorf("upsert edge (%s)-[%s]->(%s): %w", from, kind, to, err)
		}
		upsertedEdges++
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit batch transaction: %w", err)
	}

	return &BatchUpsertResult{
		NodesCount: upsertedNodes,
		EdgesCount: upsertedEdges,
		Status:     "success",
	}, nil
}
