package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/vmikhailov/cypher-mcp/internal/graph"
	"github.com/vmikhailov/cypher-mcp/internal/vector"
)

// ExecuteToolCall strictly parses and validates arguments before executing the designated tool
func ExecuteToolCall(db, dbRO *sql.DB, toolName string, arguments map[string]any, isStrict bool) (string, error) {
	if arguments == nil {
		arguments = make(map[string]any)
	}

	switch toolName {
	case "graph_query", "graph_search", "graph_resolve_entity", "graph_schema":
		if dbRO == nil {
			return "", fmt.Errorf("read database connection is not available")
		}
	case "graph_upsert_alias", "graph_batch_upsert", "graph_set_node", "graph_set_edge", "graph_delete_node", "graph_schema_define":
		if db == nil {
			return "", fmt.Errorf("write database connection is not available")
		}
	}

	switch toolName {
	case "graph_query":
		query, _ := arguments["query"].(string)
		queryParams, _ := arguments["params"].(map[string]any)
		effectiveLimit := graph.DefaultMaxRows
		if lVal, ok := arguments["limit"]; ok {
			switch v := lVal.(type) {
			case float64:
				effectiveLimit = int(v)
			case int:
				effectiveLimit = v
			}
		}
		if effectiveLimit < 0 {
			return "", fmt.Errorf("invalid 'limit': %d (must be >= 0)", effectiveLimit)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		res, err := graph.ExecuteQuery(ctx, dbRO, query, queryParams, effectiveLimit)
		if err != nil {
			return "", err
		}
		out, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return "", err
		}
		return string(out), nil

	case "graph_search":
		query, _ := arguments["query"].(string)
		kindFilter, _ := arguments["kind"].(string)
		limit := 10
		if lVal, ok := arguments["limit"]; ok {
			switch v := lVal.(type) {
			case float64:
				limit = int(v)
			case int:
				limit = v
			}
		}
		if limit <= 0 {
			return "", fmt.Errorf("invalid 'limit': %d (must be > 0)", limit)
		}

		res, err := graph.Search(dbRO, query, kindFilter, limit)
		if err != nil {
			return "", err
		}
		out, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return "", err
		}
		return string(out), nil

	case "graph_resolve_entity":
		query, _ := arguments["query"].(string)
		kindFilter, _ := arguments["kind"].(string)
		limit := 5
		if lVal, ok := arguments["limit"]; ok {
			switch v := lVal.(type) {
			case float64:
				limit = int(v)
			case int:
				limit = v
			}
		}
		if limit <= 0 {
			return "", fmt.Errorf("invalid 'limit': %d (must be > 0)", limit)
		}
		minScore := 0.50
		if sVal, ok := arguments["min_score"]; ok {
			switch v := sVal.(type) {
			case float64:
				minScore = v
			case int:
				minScore = float64(v)
			default:
				return "", fmt.Errorf("invalid 'min_score': expected number, got %T", sVal)
			}
		}
		if minScore < 0.0 || minScore > 1.0 {
			return "", fmt.Errorf("invalid 'min_score': %f (must be between 0.0 and 1.0)", minScore)
		}

		res, err := vector.ResolveEntity(dbRO, query, kindFilter, limit, minScore)
		if err != nil {
			return "", err
		}
		out, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return "", err
		}
		return string(out), nil

	case "graph_upsert_alias":
		nodeID, _ := arguments["node_id"].(string)
		alias, _ := arguments["alias"].(string)
		var explicitVec []float32
		if rawVec, ok := arguments["embedding"]; ok && rawVec != nil {
			vecSlice, isSlice := rawVec.([]any)
			if !isSlice {
				return "", fmt.Errorf("invalid 'embedding': expected array of numbers, got %T", rawVec)
			}
			for idx, v := range vecSlice {
				f, ok := v.(float64)
				if !ok {
					return "", fmt.Errorf("invalid 'embedding[%d]': expected number, got %T", idx, v)
				}
				explicitVec = append(explicitVec, float32(f))
			}
		}
		res, err := vector.UpsertAlias(db, nodeID, alias, explicitVec)
		if err != nil {
			return "", err
		}
		out, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return "", err
		}
		return string(out), nil

	case "graph_batch_upsert":
		var batchNodes []graph.BatchNodeItem
		if rawNodes, exists := arguments["nodes"]; exists && rawNodes != nil {
			if _, isSlice := rawNodes.([]any); !isSlice {
				return "", fmt.Errorf("invalid 'nodes': expected an array of node objects, got %T", rawNodes)
			}
			b, err := json.Marshal(rawNodes)
			if err != nil {
				return "", fmt.Errorf("marshal 'nodes': %w", err)
			}
			if err := json.Unmarshal(b, &batchNodes); err != nil {
				return "", fmt.Errorf("invalid 'nodes' array: %w", err)
			}
		}
		var batchEdges []graph.BatchEdgeItem
		if rawEdges, exists := arguments["edges"]; exists && rawEdges != nil {
			if _, isSlice := rawEdges.([]any); !isSlice {
				return "", fmt.Errorf("invalid 'edges': expected an array of edge objects, got %T", rawEdges)
			}
			b, err := json.Marshal(rawEdges)
			if err != nil {
				return "", fmt.Errorf("marshal 'edges': %w", err)
			}
			if err := json.Unmarshal(b, &batchEdges); err != nil {
				return "", fmt.Errorf("invalid 'edges' array: %w", err)
			}
		}
		res, err := graph.BatchUpsert(db, batchNodes, batchEdges, isStrict)
		if err != nil {
			return "", err
		}
		if res.Status == "empty" {
			return "No nodes or edges provided in batch.", nil
		}
		return fmt.Sprintf("Successfully upserted %d nodes and %d edges in 1 transaction.", res.NodesCount, res.EdgesCount), nil

	case "graph_set_node":
		id, _ := arguments["id"].(string)
		kind, _ := arguments["kind"].(string)
		var props map[string]any
		if rawProps, exists := arguments["properties"]; exists && rawProps != nil {
			var ok bool
			props, ok = rawProps.(map[string]any)
			if !ok {
				return "", fmt.Errorf("invalid 'properties': expected JSON object, got %T", rawProps)
			}
		}
		merge := true
		if mVal, ok := arguments["merge"].(bool); ok {
			merge = mVal
		}
		node, err := graph.SetNode(db, id, kind, props, isStrict, !merge)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Node '%s' of kind '%s' upserted successfully.", node.ID, node.Kind), nil

	case "graph_set_edge":
		from, _ := arguments["from"].(string)
		to, _ := arguments["to"].(string)
		kind, _ := arguments["kind"].(string)
		var props map[string]any
		if rawProps, exists := arguments["properties"]; exists && rawProps != nil {
			var ok bool
			props, ok = rawProps.(map[string]any)
			if !ok {
				return "", fmt.Errorf("invalid 'properties': expected JSON object, got %T", rawProps)
			}
		}
		merge := true
		if mVal, ok := arguments["merge"].(bool); ok {
			merge = mVal
		}
		edge, err := graph.SetEdge(db, from, to, kind, props, isStrict, !merge)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Edge (%s)-[:%s]->(%s) saved successfully.", edge.FromID, edge.Kind, edge.ToID), nil

	case "graph_delete_node":
		id, _ := arguments["id"].(string)
		res, err := graph.DeleteNode(db, id)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Node '%s' and connected edges deleted.", res.ID), nil

	case "graph_schema":
		summary, err := graph.InspectSchema(dbRO, isStrict)
		if err != nil {
			return "", err
		}
		out, err := json.MarshalIndent(summary, "", "  ")
		if err != nil {
			return "", err
		}
		return string(out), nil

	case "graph_schema_define":
		action, _ := arguments["action"].(string)
		kind, _ := arguments["kind"].(string)
		relation, _ := arguments["relation"].(string)
		fromKind, _ := arguments["from_kind"].(string)
		toKind, _ := arguments["to_kind"].(string)
		description, _ := arguments["description"].(string)
		newKind, _ := arguments["new_kind"].(string)
		newRelation, _ := arguments["new_relation"].(string)
		cascade, _ := arguments["cascade"].(bool)
		migrateTo, _ := arguments["migrate_to"].(string)
		opts := graph.SchemaDefineOptions{
			AllowSchemaEdit: true,
			Cascade:         cascade,
			MigrateTo:       migrateTo,
			NewKind:         newKind,
			NewRelation:     newRelation,
		}
		res, err := graph.DefineSchema(db, action, kind, relation, fromKind, toKind, description, opts)
		if err != nil {
			return "", err
		}
		return res.Message, nil

	default:
		return "", fmt.Errorf("unknown tool: %s", toolName)
	}
}
