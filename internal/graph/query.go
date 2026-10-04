package graph

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	cyphersql "github.com/vmikhailov/cypher-sql-go"
)

const DefaultMaxRows = 100

var jsonExtractRegex = regexp.MustCompile(`^json_extract\(([a-zA-Z0-9_]+)\.[^,]+,\s*'\$\.([^']+)'\)`)

// ExecuteQuery compiles and executes an OpenCypher query against SQLite
func ExecuteQuery(ctx context.Context, db *sql.DB, cypherQuery string, options ...any) (*QueryResult, error) {
	start := time.Now()

	// Defense-in-depth: check unsafe identifiers (only outside string literals)
	strippedQuery := StripCypherStringLiterals(cypherQuery)
	for _, m := range backtickRegex.FindAllStringSubmatch(strippedQuery, -1) {
		if !safeIdentifierRegex.MatchString(m[1]) {
			return nil, fmt.Errorf("invalid identifier %q: only alphanumeric characters and underscores are permitted", m[1])
		}
	}
	var params map[string]any
	maxRows := DefaultMaxRows

	for _, opt := range options {
		switch v := opt.(type) {
		case map[string]any:
			params = v
		case int:
			maxRows = v
		}
	}
	if maxRows < 0 {
		return nil, fmt.Errorf("invalid limit %d: must be >= 0", maxRows)
	}

	compiled, err := cyphersql.CompileWithParams(cypherQuery, params)
	if err != nil {
		return nil, fmt.Errorf("cypher compile error: %w", err)
	}
	compileDuration := time.Since(start)

	if HasMultipleStatements(compiled.SQL) {
		return nil, fmt.Errorf("multiple SQL statements are not permitted")
	}

	var sqlArgs []any
	for k, v := range compiled.Params {
		sqlArgs = append(sqlArgs, sql.Named(k, v))
	}

	qStart := time.Now()
	rows, err := db.QueryContext(ctx, compiled.SQL, sqlArgs...)
	if err != nil {
		return nil, fmt.Errorf("sql execution error (%s): %w", compiled.SQL, err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("reading columns: %w", err)
	}

	results := make([]map[string]any, 0)
	truncated := false
	for rows.Next() {
		if maxRows > 0 && len(results) >= maxRows {
			truncated = true
			break
		}
		colVals := make([]any, len(cols))
		colPointers := make([]any, len(cols))
		for i := range colVals {
			colPointers[i] = &colVals[i]
		}
		if err := rows.Scan(colPointers...); err != nil {
			return nil, fmt.Errorf("row scan error: %w", err)
		}

		rowMap := make(map[string]any)
		for i, colName := range cols {
			cleanCol := colName
			if strings.HasPrefix(colName, "json_extract(") {
				if m := jsonExtractRegex.FindStringSubmatch(colName); len(m) == 3 {
					cleanCol = m[1] + "." + m[2]
				}
			}
			val := colVals[i]
			switch v := val.(type) {
			case []byte:
				var jsonParsed any
				if err := json.Unmarshal(v, &jsonParsed); err == nil {
					val = jsonParsed
				} else {
					val = string(v)
				}
			case string:
				trimmed := strings.TrimSpace(v)
				if (strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")) ||
					(strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")) {
					var jsonParsed any
					if err := json.Unmarshal([]byte(trimmed), &jsonParsed); err == nil {
						val = jsonParsed
					}
				}
			}
			rowMap[cleanCol] = val
		}
		results = append(results, rowMap)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}
	execDuration := time.Since(qStart)

	res := &QueryResult{
		Results:       results,
		Count:         len(results),
		CompiledSQL:   compiled.SQL,
		CompileTimeUs: compileDuration.Microseconds(),
		ExecuteTimeUs: execDuration.Microseconds(),
		Truncated:     truncated,
	}
	if truncated {
		res.Warning = fmt.Sprintf("Query result was truncated to %d rows. Use Cypher LIMIT to narrow your query.", maxRows)
	}

	return res, nil
}
