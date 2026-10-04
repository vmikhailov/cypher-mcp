package graph

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

// TokenizeFTS5 splits a query string into alphanumeric tokens
func TokenizeFTS5(query string) []string {
	var tokens []string
	var cur strings.Builder
	for _, r := range query {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			cur.WriteRune(r)
		} else {
			if cur.Len() > 0 {
				tokens = append(tokens, cur.String())
				cur.Reset()
			}
		}
	}
	if cur.Len() > 0 {
		tokens = append(tokens, cur.String())
	}
	return tokens
}

// Search executes trigram FTS5 search with inflected wordform and LIKE fallbacks
func Search(db *sql.DB, query string, kindFilter string, limit int) (*SearchResult, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}

	tokens := TokenizeFTS5(query)
	if len(tokens) == 0 {
		return &SearchResult{
			Query: query,
			Count: 0,
			Nodes: []SearchMatch{},
		}, nil
	}

	var trigramTokens []string
	for _, t := range tokens {
		if len([]rune(t)) >= 3 {
			trigramTokens = append(trigramTokens, fmt.Sprintf("\"%s\"", t))
		}
	}

	searchSQL := `
		SELECT n.id, n.kind, n.properties, nodes_fts.rank
		FROM nodes_fts
		JOIN nodes n ON n.rowid = nodes_fts.rowid
		WHERE nodes_fts MATCH ?
		  AND (? = '' OR n.kind = ?)
		ORDER BY nodes_fts.rank
		LIMIT ?;
	`

	executeFTS := func(ftsExpr string) ([]SearchMatch, error) {
		rows, err := db.Query(searchSQL, ftsExpr, kindFilter, kindFilter, limit)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		var items []SearchMatch
		for rows.Next() {
			var item SearchMatch
			var propsRaw string
			if err := rows.Scan(&item.ID, &item.Kind, &propsRaw, &item.Score); err != nil {
				return nil, err
			}
			var parsedProps map[string]any
			if err := json.Unmarshal([]byte(propsRaw), &parsedProps); err == nil {
				item.Properties = parsedProps
			}
			items = append(items, item)
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("fts row iteration error: %w", err)
		}
		return items, nil
	}

	var items []SearchMatch

	// 1. Trigram search if tokens >= 3 chars
	if len(trigramTokens) > 0 {
		ftsAnd := strings.Join(trigramTokens, " AND ")
		var err error
		items, err = executeFTS(ftsAnd)
		if err == nil && len(items) == 0 && len(trigramTokens) > 1 {
			ftsOr := strings.Join(trigramTokens, " OR ")
			items, _ = executeFTS(ftsOr)
		}
	}

	// 2. Inflected wordforms fallback
	if len(items) == 0 {
		for trimLen := 1; trimLen <= 2; trimLen++ {
			var stemmedTokens []string
			hasStemmed := false
			for _, t := range tokens {
				r := []rune(t)
				if len(r) > 5 {
					stemmed := string(r[:len(r)-trimLen])
					if len([]rune(stemmed)) >= 3 {
						stemmedTokens = append(stemmedTokens, fmt.Sprintf("\"%s\"", stemmed))
						hasStemmed = true
					}
				} else if len(r) >= 3 {
					stemmedTokens = append(stemmedTokens, fmt.Sprintf("\"%s\"", t))
				}
			}
			if !hasStemmed {
				break
			}
			ftsAnd := strings.Join(stemmedTokens, " AND ")
			var err error
			items, err = executeFTS(ftsAnd)
			if err == nil && len(items) > 0 {
				break
			}
			if len(stemmedTokens) > 1 {
				ftsOr := strings.Join(stemmedTokens, " OR ")
				items, _ = executeFTS(ftsOr)
				if len(items) > 0 {
					break
				}
			}
		}
	}

	// 3. Fallback to LIKE if FTS produced 0 results
	if len(items) == 0 {
		likePat := "%" + strings.TrimSpace(query) + "%"
		likeSQL := `
			SELECT id, kind, properties, 0.0 AS rank
			FROM nodes
			WHERE (id LIKE ? OR (
				CASE 
					WHEN json_valid(properties) THEN (SELECT coalesce(group_concat(value, ' '), '') FROM json_tree(properties) WHERE atom IS NOT NULL)
					ELSE properties 
				END
			) LIKE ?)
			  AND (? = '' OR kind = ?)
			LIMIT ?;
		`
		runLike := func(pat string) ([]SearchMatch, error) {
			rows, err := db.Query(likeSQL, pat, pat, kindFilter, kindFilter, limit)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			var res []SearchMatch
			for rows.Next() {
				var item SearchMatch
				var propsRaw string
				if err := rows.Scan(&item.ID, &item.Kind, &propsRaw, &item.Score); err != nil {
					return nil, err
				}
				var parsedProps map[string]any
				if err := json.Unmarshal([]byte(propsRaw), &parsedProps); err == nil {
					item.Properties = parsedProps
				}
				res = append(res, item)
			}
			if err := rows.Err(); err != nil {
				return nil, err
			}
			return res, nil
		}

		var err error
		items, err = runLike(likePat)
		if err != nil {
			return nil, fmt.Errorf("fallback search query error: %w", err)
		}

		if len(items) == 0 {
			for trimLen := 1; trimLen <= 2; trimLen++ {
				var stemmedWords []string
				hasStemmed := false
				for _, t := range tokens {
					r := []rune(t)
					if len(r) > 5 {
						stemmedWords = append(stemmedWords, string(r[:len(r)-trimLen]))
						hasStemmed = true
					} else {
						stemmedWords = append(stemmedWords, t)
					}
				}
				if !hasStemmed {
					break
				}
				stemmedLike := "%" + strings.Join(stemmedWords, "%") + "%"
				items, err = runLike(stemmedLike)
				if err == nil && len(items) > 0 {
					break
				}
			}
		}
	}

	if items == nil {
		items = make([]SearchMatch, 0)
	}

	return &SearchResult{
		Query: query,
		Kind:  kindFilter,
		Count: len(items),
		Nodes: items,
	}, nil
}
