package graph

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
)

var (
	safeIdentifierRegex = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
	backtickRegex       = regexp.MustCompile("`([^`]+)`")
)

type dbOrTx interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// HasMultipleStatements checks if a SQL string contains more than one non-comment statement
func HasMultipleStatements(sqlStr string) bool {
	inSingleQuote := false
	inDoubleQuote := false
	n := len(sqlStr)
	for i := 0; i < n; i++ {
		ch := sqlStr[i]
		if inSingleQuote {
			if ch == '\'' {
				if i+1 < n && sqlStr[i+1] == '\'' {
					i++ // escaped quote ''
				} else {
					inSingleQuote = false
				}
			}
			continue
		}
		if inDoubleQuote {
			if ch == '"' {
				if i+1 < n && sqlStr[i+1] == '"' {
					i++ // escaped quote ""
				} else {
					inDoubleQuote = false
				}
			}
			continue
		}

		if ch == '\'' {
			inSingleQuote = true
			continue
		}
		if ch == '"' {
			inDoubleQuote = true
			continue
		}
		if ch == '-' && i+1 < n && sqlStr[i+1] == '-' {
			i += 2
			for i < n && sqlStr[i] != '\n' {
				i++
			}
			continue
		}
		if ch == '/' && i+1 < n && sqlStr[i+1] == '*' {
			i += 2
			for i+1 < n && !(sqlStr[i] == '*' && sqlStr[i+1] == '/') {
				i++
			}
			i++
			continue
		}

		if ch == ';' {
			rem := sqlStr[i+1:]
			rem = StripCommentsAndSpace(rem)
			if len(rem) > 0 {
				return true
			}
		}
	}
	return false
}

// StripCommentsAndSpace removes SQL comments and whitespace
func StripCommentsAndSpace(s string) string {
	n := len(s)
	i := 0
	var sb strings.Builder
	for i < n {
		if s[i] == ' ' || s[i] == '\t' || s[i] == '\r' || s[i] == '\n' || s[i] == ';' {
			i++
			continue
		}
		if s[i] == '-' && i+1 < n && s[i+1] == '-' {
			i += 2
			for i < n && s[i] != '\n' {
				i++
			}
			continue
		}
		if s[i] == '/' && i+1 < n && s[i+1] == '*' {
			i += 2
			for i+1 < n && !(s[i] == '*' && s[i+1] == '/') {
				i++
			}
			i += 2
			continue
		}
		sb.WriteByte(s[i])
		i++
	}
	return sb.String()
}

// StripCypherStringLiterals blanks out the contents of string literals in Cypher queries
func StripCypherStringLiterals(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	inSingle := false
	inDouble := false
	n := len(s)
	for i := 0; i < n; i++ {
		ch := s[i]
		if inSingle {
			if ch == '\\' && i+1 < n {
				sb.WriteByte(' ')
				sb.WriteByte(' ')
				i++
				continue
			}
			if ch == '\'' {
				if i+1 < n && s[i+1] == '\'' {
					sb.WriteByte(' ')
					sb.WriteByte(' ')
					i++
					continue
				}
				inSingle = false
			}
			sb.WriteByte(' ')
			continue
		}
		if inDouble {
			if ch == '\\' && i+1 < n {
				sb.WriteByte(' ')
				sb.WriteByte(' ')
				i++
				continue
			}
			if ch == '"' {
				if i+1 < n && s[i+1] == '"' {
					sb.WriteByte(' ')
					sb.WriteByte(' ')
					i++
					continue
				}
				inDouble = false
			}
			sb.WriteByte(' ')
			continue
		}

		if ch == '\'' {
			inSingle = true
			sb.WriteByte(' ')
			continue
		}
		if ch == '"' {
			inDouble = true
			sb.WriteByte(' ')
			continue
		}
		sb.WriteByte(ch)
	}
	return sb.String()
}

// IsSchemaEnforced checks if schema validation is active
func IsSchemaEnforced(q dbOrTx, strictFlag bool) (bool, error) {
	if strictFlag {
		return true, nil
	}
	var count int
	err := q.QueryRow("SELECT count(*) FROM schema_kinds").Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// ValidateNodeKind verifies that the node kind is registered in the schema if enforced
func ValidateNodeKind(q dbOrTx, kind string, strictFlag bool) error {
	enforced, err := IsSchemaEnforced(q, strictFlag)
	if err != nil {
		return fmt.Errorf("check schema enforcement: %w", err)
	}
	if !enforced {
		return nil
	}

	var exists bool
	err = q.QueryRow("SELECT EXISTS(SELECT 1 FROM schema_kinds WHERE kind = ? COLLATE NOCASE)", kind).Scan(&exists)
	if err != nil {
		return fmt.Errorf("validate kind: %w", err)
	}
	if !exists {
		rows, err := q.Query("SELECT kind FROM schema_kinds ORDER BY kind")
		if err != nil {
			return fmt.Errorf("schema validation error: node kind '%s' is not registered", kind)
		}
		defer rows.Close()
		var allowed []string
		for rows.Next() {
			var k string
			if rows.Scan(&k) == nil {
				allowed = append(allowed, k)
			}
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("schema validation error: %w", err)
		}
		return fmt.Errorf("schema validation error: node kind '%s' is not registered in schema. Allowed kinds: [%s]", kind, strings.Join(allowed, ", "))
	}
	return nil
}

// ValidateEdgeRelation verifies edge relationship conformance
func ValidateEdgeRelation(q dbOrTx, fromKind, toKind, relType string, strictFlag bool) error {
	enforced, err := IsSchemaEnforced(q, strictFlag)
	if err != nil {
		return fmt.Errorf("check schema enforcement: %w", err)
	}
	if !enforced {
		return nil
	}

	var exists bool
	err = q.QueryRow(`
		SELECT EXISTS(
			SELECT 1 FROM schema_relations 
			WHERE rel_type = ? COLLATE NOCASE AND from_kind = ? COLLATE NOCASE AND to_kind = ? COLLATE NOCASE
		)`, relType, fromKind, toKind).Scan(&exists)
	if err != nil {
		return fmt.Errorf("validate relation: %w", err)
	}
	if !exists {
		rows, err := q.Query("SELECT rel_type, from_kind, to_kind FROM schema_relations ORDER BY rel_type, from_kind, to_kind")
		if err != nil {
			return fmt.Errorf("schema validation error: relation '%s' from '%s' to '%s' is not allowed", relType, fromKind, toKind)
		}
		defer rows.Close()
		var allowed []string
		for rows.Next() {
			var r, f, t string
			if rows.Scan(&r, &f, &t) == nil {
				allowed = append(allowed, fmt.Sprintf("(%s)-[:%s]->(%s)", f, r, t))
			}
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("schema validation error: %w", err)
		}
		return fmt.Errorf("schema validation error: relation '%s' from '%s' to '%s' is not allowed by schema. Allowed relations: [%s]",
			relType, fromKind, toKind, strings.Join(allowed, ", "))
	}
	return nil
}

// ValidateNodeKindChange checks if changing a node's kind would invalidate connected edges
func ValidateNodeKindChange(q dbOrTx, id, existingKind, newKind string, strictFlag bool, batchTargetKinds ...map[string]string) error {
	if strings.EqualFold(existingKind, newKind) {
		return nil
	}
	enforced, err := IsSchemaEnforced(q, strictFlag)
	if err != nil {
		return fmt.Errorf("check schema enforcement: %w", err)
	}
	if !enforced {
		return nil
	}

	var batchMap map[string]string
	if len(batchTargetKinds) > 0 && batchTargetKinds[0] != nil {
		batchMap = batchTargetKinds[0]
	}

	// 1. Outgoing edges: id -> to_node
	outRows, err := q.Query(`
		SELECT e.to_id, e.kind, n.kind 
		FROM edges e 
		JOIN nodes n ON e.to_id = n.id 
		WHERE e.from_id = ?
	`, id)
	if err != nil {
		return fmt.Errorf("query outgoing edges: %w", err)
	}
	defer outRows.Close()

	type edgeCheck struct {
		targetID string
		relType  string
		peerKind string
	}
	var outEdges []edgeCheck
	for outRows.Next() {
		var ec edgeCheck
		if err := outRows.Scan(&ec.targetID, &ec.relType, &ec.peerKind); err != nil {
			return err
		}
		outEdges = append(outEdges, ec)
	}
	if err := outRows.Err(); err != nil {
		return err
	}

	for _, e := range outEdges {
		effectiveToKind := e.peerKind
		if e.targetID == id {
			effectiveToKind = newKind
		} else if batchMap != nil {
			if targetKind, ok := batchMap[e.targetID]; ok {
				effectiveToKind = targetKind
			}
		}
		if err := ValidateEdgeRelation(q, newKind, effectiveToKind, e.relType, strictFlag); err != nil {
			return fmt.Errorf("cannot change kind of node '%s' from '%s' to '%s': outgoing relation '(:%s)-[:%s]->(:%s)' violates schema (%w)",
				id, existingKind, newKind, newKind, e.relType, effectiveToKind, err)
		}
	}

	// 2. Incoming edges: from_node -> id
	inRows, err := q.Query(`
		SELECT e.from_id, e.kind, n.kind 
		FROM edges e 
		JOIN nodes n ON e.from_id = n.id 
		WHERE e.to_id = ?
	`, id)
	if err != nil {
		return fmt.Errorf("query incoming edges: %w", err)
	}
	defer inRows.Close()

	var inEdges []edgeCheck
	for inRows.Next() {
		var ec edgeCheck
		if err := inRows.Scan(&ec.targetID, &ec.relType, &ec.peerKind); err != nil {
			return err
		}
		inEdges = append(inEdges, ec)
	}
	if err := inRows.Err(); err != nil {
		return err
	}

	for _, e := range inEdges {
		effectiveFromKind := e.peerKind
		if e.targetID == id {
			effectiveFromKind = newKind
		} else if batchMap != nil {
			if targetKind, ok := batchMap[e.targetID]; ok {
				effectiveFromKind = targetKind
			}
		}
		if err := ValidateEdgeRelation(q, effectiveFromKind, newKind, e.relType, strictFlag); err != nil {
			return fmt.Errorf("cannot change kind of node '%s' from '%s' to '%s': incoming relation '(:%s)-[:%s]->(:%s)' violates schema (%w)",
				id, existingKind, newKind, effectiveFromKind, e.relType, newKind, err)
		}
	}

	return nil
}
