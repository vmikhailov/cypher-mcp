package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mattn/go-isatty"
)

// OutputFormat specifies how command results should be printed.
type OutputFormat string

const (
	FormatTable   OutputFormat = "table"
	FormatJSON    OutputFormat = "json"
	FormatCompact OutputFormat = "compact"
	FormatRaw     OutputFormat = "raw"
)

// expandHome replaces a leading ~/ or ~\ with the user's home directory.
func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "~\\") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

// resolveDBPath returns the target SQLite database path.
func resolveDBPath(explicitPath string) string {
	if explicitPath != "" {
		return expandHome(explicitPath)
	}
	if env := os.Getenv("CYPHER_DB"); env != "" {
		return expandHome(env)
	}
	if env := os.Getenv("HERMES_GRAPH_DB"); env != "" {
		return expandHome(env)
	}
	return "knowledge_graph.db"
}

// openDatabaseForCLI opens a SQLite connection, automatically initializing the schema if the file does not yet exist.
func openDatabaseForCLI(targetDB string, readOnly bool) (*sql.DB, error) {
	cleanPath := resolveDBPath(targetDB)
	if _, err := os.Stat(cleanPath); os.IsNotExist(err) {
		// Initialize the database schema and tables first
		initDB, err := initDatabase(cleanPath)
		if err != nil {
			return nil, fmt.Errorf("initialize database: %w", err)
		}
		if !readOnly {
			return initDB, nil
		}
		initDB.Close()
	}
	if readOnly {
		return initRODatabase(cleanPath)
	}
	return initDatabase(cleanPath)
}

// parseFlagSet partitions arguments so flags always precede positional arguments,
// enabling users to place positional arguments anywhere (e.g. 'node set user:1 --kind User').
func parseFlagSet(fs *flag.FlagSet, args []string) ([]string, error) {
	var flagArgs []string
	var positionalArgs []string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionalArgs = append(positionalArgs, args[i+1:]...)
			break
		}
		if arg == "-" {
			positionalArgs = append(positionalArgs, arg)
			continue
		}
		if strings.HasPrefix(arg, "-") {
			flagName := strings.TrimLeft(arg, "-")
			if strings.Contains(flagName, "=") {
				flagArgs = append(flagArgs, arg)
				continue
			}
			f := fs.Lookup(flagName)
			if f != nil {
				if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
					flagArgs = append(flagArgs, arg)
					continue
				}
				if i+1 < len(args) {
					flagArgs = append(flagArgs, arg, args[i+1])
					i++
					continue
				}
			}
			flagArgs = append(flagArgs, arg)
		} else {
			positionalArgs = append(positionalArgs, arg)
		}
	}

	ordered := append(flagArgs, positionalArgs...)
	err := fs.Parse(ordered)
	return fs.Args(), err
}

// resolveFormat determines the output format based on flags and terminal status.
func resolveFormat(formatFlag string, jsonFlag bool) OutputFormat {
	if jsonFlag || strings.EqualFold(formatFlag, "json") {
		return FormatJSON
	}
	if strings.EqualFold(formatFlag, "compact") {
		return FormatCompact
	}
	if strings.EqualFold(formatFlag, "raw") {
		return FormatRaw
	}
	if strings.EqualFold(formatFlag, "table") {
		return FormatTable
	}
	if isTerminal(os.Stdout) {
		return FormatTable
	}
	return FormatJSON
}

var knownSubcommands = map[string]bool{
	"query":   true,
	"q":       true,
	"search":  true,
	"s":       true,
	"resolve": true,
	"r":       true,
	"node":    true,
	"edge":    true,
	"schema":  true,
	"alias":   true,
	"batch":   true,
	"serve":   true,
	"version": true,
	"help":    true,
}

// shouldRunCLI determines whether cypher-mcp should run as a CLI or as an MCP server.
func shouldRunCLI(args []string) bool {
	if len(args) == 0 {
		return isTerminal(os.Stdin)
	}

	for _, arg := range args {
		if arg == "-h" || arg == "--help" || arg == "-v" || arg == "--version" {
			return true
		}
		if !strings.HasPrefix(arg, "-") {
			if knownSubcommands[strings.ToLower(arg)] {
				return true
			}
		}
	}
	return false
}

// runCLI parses arguments and executes the appropriate CLI subcommand.
func runCLI(args []string) int {
	return runCLIWithIO(args, os.Stdin, os.Stdout, os.Stderr)
}

// runCLIWithIO runs the CLI with explicit input/output streams for testability.
func runCLIWithIO(args []string, in io.Reader, out, errOut io.Writer) int {
	if len(args) == 0 {
		printMainHelp(out)
		return 0
	}

	var globalDB string
	var globalFormat string
	var globalJSON bool
	var globalStrict bool
	var remaining []string

	i := 0
	for i < len(args) {
		arg := args[i]
		if arg == "-h" || arg == "--help" || arg == "help" {
			if i+1 < len(args) {
				printSubcommandHelp(args[i+1], out)
				return 0
			}
			printMainHelp(out)
			return 0
		}
		if arg == "-v" || arg == "--version" || arg == "version" {
			fmt.Fprintf(out, "cypher-mcp v%s\n", appVersion)
			return 0
		}
		if arg == "--db" || arg == "-db" {
			if i+1 < len(args) {
				globalDB = args[i+1]
				i += 2
				continue
			}
		} else if strings.HasPrefix(arg, "--db=") || strings.HasPrefix(arg, "-db=") {
			parts := strings.SplitN(arg, "=", 2)
			globalDB = parts[1]
			i++
			continue
		} else if arg == "--format" || arg == "-format" || arg == "-f" {
			if i+1 < len(args) {
				globalFormat = args[i+1]
				i += 2
				continue
			}
		} else if strings.HasPrefix(arg, "--format=") || strings.HasPrefix(arg, "-format=") {
			parts := strings.SplitN(arg, "=", 2)
			globalFormat = parts[1]
			i++
			continue
		} else if arg == "--json" || arg == "-json" {
			globalJSON = true
			i++
			continue
		} else if arg == "--strict-schema" || arg == "-strict-schema" {
			globalStrict = true
			i++
			continue
		} else if !strings.HasPrefix(arg, "-") {
			remaining = args[i:]
			break
		}
		i++
	}

	if len(remaining) == 0 {
		printMainHelp(out)
		return 0
	}

	subcmd := strings.ToLower(remaining[0])
	subArgs := remaining[1:]

	switch subcmd {
	case "query", "q":
		return runQueryCmd(subArgs, globalDB, globalFormat, globalJSON, globalStrict, in, out, errOut)
	case "search", "s":
		return runSearchCmd(subArgs, globalDB, globalFormat, globalJSON, in, out, errOut)
	case "resolve", "r":
		return runResolveCmd(subArgs, globalDB, globalFormat, globalJSON, in, out, errOut)
	case "node":
		return runNodeCmd(subArgs, globalDB, globalFormat, globalJSON, globalStrict, in, out, errOut)
	case "edge":
		return runEdgeCmd(subArgs, globalDB, globalFormat, globalJSON, globalStrict, in, out, errOut)
	case "schema":
		return runSchemaCmd(subArgs, globalDB, globalFormat, globalJSON, globalStrict, in, out, errOut)
	case "alias":
		return runAliasCmd(subArgs, globalDB, globalFormat, globalJSON, in, out, errOut)
	case "batch":
		return runBatchCmd(subArgs, globalDB, globalStrict, in, out, errOut)
	case "serve":
		return runServeCmd(subArgs, globalDB, globalStrict, in, out, errOut)
	default:
		fmt.Fprintf(errOut, "Unknown command: %q. Run 'cypher-mcp --help' for available commands.\n", subcmd)
		return 2
	}
}

// ── Command Handlers ────────────────────────────────────────────────────────

func runQueryCmd(args []string, defaultDB, defaultFormat string, defaultJSON, defaultStrict bool, in io.Reader, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("query", flag.ContinueOnError)
	fs.SetOutput(errOut)

	dbPath := fs.String("db", defaultDB, "Path to SQLite database")
	formatFlag := fs.String("format", defaultFormat, "Output format: table, json, compact")
	fs.StringVar(formatFlag, "f", defaultFormat, "Output format (shorthand)")
	jsonFlag := fs.Bool("json", defaultJSON, "Output formatted JSON")
	paramsFlag := fs.String("params", "", "JSON parameters for Cypher query")
	limitFlag := fs.Int("limit", 1000, "Maximum number of rows to return")
	strictFlag := fs.Bool("strict-schema", defaultStrict, "Strictly enforce schema")
	rawSQLFlag := fs.Bool("raw-sql", false, "Print compiled SQLite SQL query")

	positional, err := parseFlagSet(fs, args)
	if err != nil {
		return 2
	}

	var query string
	if len(positional) > 0 && positional[0] != "-" {
		query = strings.Join(positional, " ")
	} else {
		b, err := io.ReadAll(in)
		if err != nil {
			fmt.Fprintf(errOut, "Error reading query from stdin: %v\n", err)
			return 1
		}
		query = strings.TrimSpace(string(b))
	}

	if strings.TrimSpace(query) == "" {
		fmt.Fprintln(errOut, "Error: no query specified. Provide a Cypher query as an argument or via stdin.")
		return 2
	}

	var params map[string]any
	if *paramsFlag != "" {
		pStr := *paramsFlag
		if strings.HasPrefix(pStr, "@") {
			b, err := os.ReadFile(pStr[1:])
			if err != nil {
				fmt.Fprintf(errOut, "Error reading params file %s: %v\n", pStr[1:], err)
				return 1
			}
			pStr = string(b)
		}
		if err := json.Unmarshal([]byte(pStr), &params); err != nil {
			fmt.Fprintf(errOut, "Error parsing JSON params: %v\n", err)
			return 2
		}
	}

	targetDB := resolveDBPath(*dbPath)
	dbRO, err := openDatabaseForCLI(targetDB, true)
	if err != nil {
		fmt.Fprintf(errOut, "Database open error: %v\n", err)
		return 1
	}
	defer dbRO.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var options []any
	if params != nil {
		options = append(options, params)
	}
	if *limitFlag > 0 {
		options = append(options, *limitFlag)
	}

	resJSON, err := handleGraphQueryContext(ctx, dbRO, query, options...)
	if err != nil {
		fmt.Fprintf(errOut, "Query error: %v\n", err)
		return 1
	}

	format := resolveFormat(*formatFlag, *jsonFlag)

	var payload struct {
		Results       []map[string]any `json:"results"`
		Count         int              `json:"count"`
		CompiledSQL   string           `json:"compiled_sql"`
		CompileTimeUs int64            `json:"compile_time_us"`
		ExecuteTimeUs int64            `json:"execute_time_us"`
		Truncated     bool             `json:"truncated"`
		Warning       string           `json:"warning"`
	}
	_ = json.Unmarshal([]byte(resJSON), &payload)

	if *rawSQLFlag && payload.CompiledSQL != "" {
		fmt.Fprintf(errOut, "-- SQL: %s\n\n", payload.CompiledSQL)
	}

	switch format {
	case FormatJSON:
		fmt.Fprintln(out, resJSON)
	case FormatCompact:
		b, _ := json.Marshal(payload.Results)
		fmt.Fprintln(out, string(b))
	case FormatRaw:
		fmt.Fprintln(out, resJSON)
	case FormatTable:
		if payload.Count == 0 {
			fmt.Fprintf(out, "No results found. (0 rows in %.2fms)\n", float64(payload.CompileTimeUs+payload.ExecuteTimeUs)/1000.0)
			return 0
		}

		var headers []string
		seen := make(map[string]bool)
		for _, row := range payload.Results {
			for k := range row {
				if !seen[k] {
					seen[k] = true
					headers = append(headers, k)
				}
			}
		}
		sort.Strings(headers)

		var rows [][]string
		for _, row := range payload.Results {
			var r []string
			for _, h := range headers {
				val := row[h]
				if val == nil {
					r = append(r, "")
				} else {
					switch v := val.(type) {
					case string:
						r = append(r, v)
					case map[string]any, []any:
						b, _ := json.Marshal(v)
						r = append(r, string(b))
					default:
						r = append(r, fmt.Sprintf("%v", v))
					}
				}
			}
			rows = append(rows, r)
		}

		fmt.Fprintln(out, renderTable(headers, rows))
		if payload.Truncated && payload.Warning != "" {
			fmt.Fprintf(out, "Warning: %s\n", payload.Warning)
		}
		fmt.Fprintf(out, "(%d rows, compile: %.2fms, exec: %.2fms)\n", payload.Count, float64(payload.CompileTimeUs)/1000.0, float64(payload.ExecuteTimeUs)/1000.0)
	}

	_ = strictFlag
	return 0
}

func runSearchCmd(args []string, defaultDB, defaultFormat string, defaultJSON bool, in io.Reader, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(errOut)

	dbPath := fs.String("db", defaultDB, "Path to SQLite database")
	formatFlag := fs.String("format", defaultFormat, "Output format: table, json, compact")
	fs.StringVar(formatFlag, "f", defaultFormat, "Output format (shorthand)")
	jsonFlag := fs.Bool("json", defaultJSON, "Output formatted JSON")
	kindFlag := fs.String("kind", "", "Filter search results by node kind")
	limitFlag := fs.Int("limit", 10, "Maximum number of search results (1-50)")

	positional, err := parseFlagSet(fs, args)
	if err != nil {
		return 2
	}

	query := strings.Join(positional, " ")
	if strings.TrimSpace(query) == "" {
		fmt.Fprintln(errOut, "Error: search query term is required.")
		return 2
	}

	targetDB := resolveDBPath(*dbPath)
	dbRO, err := openDatabaseForCLI(targetDB, true)
	if err != nil {
		fmt.Fprintf(errOut, "Database open error: %v\n", err)
		return 1
	}
	defer dbRO.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	resJSON, err := handleGraphSearch(dbRO, query, *kindFlag, *limitFlag, ctx)
	if err != nil {
		fmt.Fprintf(errOut, "Search error: %v\n", err)
		return 1
	}

	format := resolveFormat(*formatFlag, *jsonFlag)
	if format == FormatJSON {
		fmt.Fprintln(out, resJSON)
		return 0
	}

	var payload struct {
		Query   string             `json:"query"`
		Count   int                `json:"count"`
		Results []SearchResultItem `json:"results"`
	}
	_ = json.Unmarshal([]byte(resJSON), &payload)

	if format == FormatCompact {
		b, _ := json.Marshal(payload.Results)
		fmt.Fprintln(out, string(b))
		return 0
	}

	if payload.Count == 0 {
		fmt.Fprintf(out, "No matches found for %q.\n", payload.Query)
		return 0
	}

	headers := []string{"ID", "Kind", "Properties", "Rank"}
	var rows [][]string
	for _, item := range payload.Results {
		propStr := ""
		if item.Properties != nil {
			switch p := item.Properties.(type) {
			case string:
				propStr = p
			default:
				b, _ := json.Marshal(p)
				propStr = string(b)
			}
		}
		rankStr := fmt.Sprintf("%.2f", item.Rank)
		rows = append(rows, []string{item.ID, item.Kind, propStr, rankStr})
	}

	fmt.Fprintln(out, renderTable(headers, rows))
	fmt.Fprintf(out, "(%d matches found)\n", payload.Count)
	return 0
}

func runResolveCmd(args []string, defaultDB, defaultFormat string, defaultJSON bool, in io.Reader, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("resolve", flag.ContinueOnError)
	fs.SetOutput(errOut)

	dbPath := fs.String("db", defaultDB, "Path to SQLite database")
	formatFlag := fs.String("format", defaultFormat, "Output format: table, json, compact")
	fs.StringVar(formatFlag, "f", defaultFormat, "Output format (shorthand)")
	jsonFlag := fs.Bool("json", defaultJSON, "Output formatted JSON")
	kindFlag := fs.String("kind", "", "Filter candidates by node kind")
	limitFlag := fs.Int("limit", 5, "Maximum candidate count (1-50)")
	minScoreFlag := fs.Float64("min-score", 0.50, "Minimum similarity score threshold (0.0 - 1.0)")

	positional, err := parseFlagSet(fs, args)
	if err != nil {
		return 2
	}

	query := strings.Join(positional, " ")
	if strings.TrimSpace(query) == "" {
		fmt.Fprintln(errOut, "Error: resolution query text is required.")
		return 2
	}

	targetDB := resolveDBPath(*dbPath)
	dbRO, err := openDatabaseForCLI(targetDB, true)
	if err != nil {
		fmt.Fprintf(errOut, "Database open error: %v\n", err)
		return 1
	}
	defer dbRO.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	resJSON, err := handleResolveEntity(dbRO, query, *kindFlag, *limitFlag, *minScoreFlag, ctx)
	if err != nil {
		fmt.Fprintf(errOut, "Resolve error: %v\n", err)
		return 1
	}

	format := resolveFormat(*formatFlag, *jsonFlag)
	if format == FormatJSON {
		fmt.Fprintln(out, resJSON)
		return 0
	}

	var payload struct {
		Query      string `json:"query"`
		Candidates []struct {
			NodeID string  `json:"node_id"`
			Kind   string  `json:"kind"`
			Score  float64 `json:"score"`
			Alias  string  `json:"alias"`
		} `json:"candidates"`
	}
	_ = json.Unmarshal([]byte(resJSON), &payload)

	if format == FormatCompact {
		b, _ := json.Marshal(payload.Candidates)
		fmt.Fprintln(out, string(b))
		return 0
	}

	if len(payload.Candidates) == 0 {
		fmt.Fprintf(out, "No matching candidates found for %q (min score: %.2f).\n", payload.Query, *minScoreFlag)
		return 0
	}

	headers := []string{"Node ID", "Kind", "Score", "Matched Alias"}
	var rows [][]string
	for _, c := range payload.Candidates {
		rows = append(rows, []string{c.NodeID, c.Kind, fmt.Sprintf("%.4f", c.Score), c.Alias})
	}

	fmt.Fprintln(out, renderTable(headers, rows))
	fmt.Fprintf(out, "(%d candidate(s) resolved)\n", len(payload.Candidates))
	return 0
}

func runNodeCmd(args []string, defaultDB, defaultFormat string, defaultJSON, defaultStrict bool, in io.Reader, out, errOut io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(errOut, "Usage: cypher-mcp node <get|set|delete> [flags] [arguments]")
		return 2
	}

	subaction := strings.ToLower(args[0])
	subArgs := args[1:]

	switch subaction {
	case "get":
		fs := flag.NewFlagSet("node get", flag.ContinueOnError)
		fs.SetOutput(errOut)
		dbPath := fs.String("db", defaultDB, "Path to SQLite database")
		formatFlag := fs.String("format", defaultFormat, "Output format: table, json")
		jsonFlag := fs.Bool("json", defaultJSON, "Output formatted JSON")

		positional, err := parseFlagSet(fs, subArgs)
		if err != nil {
			return 2
		}
		if len(positional) == 0 {
			fmt.Fprintln(errOut, "Error: node ID is required. Example: cypher-mcp node get user:1")
			return 2
		}
		nodeID := positional[0]

		targetDB := resolveDBPath(*dbPath)
		dbRO, err := openDatabaseForCLI(targetDB, true)
		if err != nil {
			fmt.Fprintf(errOut, "Database open error: %v\n", err)
			return 1
		}
		defer dbRO.Close()

		var kind, propsStr string
		err = dbRO.QueryRow("SELECT kind, properties FROM nodes WHERE id = ?", nodeID).Scan(&kind, &propsStr)
		if err == sql.ErrNoRows {
			fmt.Fprintf(errOut, "Node %q not found.\n", nodeID)
			return 1
		} else if err != nil {
			fmt.Fprintf(errOut, "Error fetching node: %v\n", err)
			return 1
		}

		var props map[string]any
		_ = json.Unmarshal([]byte(propsStr), &props)

		edgeRows, err := dbRO.Query(`
			SELECT from_id, to_id, kind, properties 
			FROM edges 
			WHERE from_id = ? OR to_id = ?
			LIMIT 50
		`, nodeID, nodeID)

		type EdgeInfo struct {
			Direction  string         `json:"direction"`
			Relation   string         `json:"relation"`
			TargetID   string         `json:"target_id"`
			Properties map[string]any `json:"properties,omitempty"`
		}

		var edges []EdgeInfo
		if err == nil {
			defer edgeRows.Close()
			for edgeRows.Next() {
				var fromID, toID, relKind, edgePropsStr string
				if err := edgeRows.Scan(&fromID, &toID, &relKind, &edgePropsStr); err == nil {
					var ep map[string]any
					_ = json.Unmarshal([]byte(edgePropsStr), &ep)
					if fromID == nodeID {
						edges = append(edges, EdgeInfo{
							Direction:  "outgoing",
							Relation:   relKind,
							TargetID:   toID,
							Properties: ep,
						})
					} else {
						edges = append(edges, EdgeInfo{
							Direction:  "incoming",
							Relation:   relKind,
							TargetID:   fromID,
							Properties: ep,
						})
					}
				}
			}
		}

		format := resolveFormat(*formatFlag, *jsonFlag)
		if format == FormatJSON || format == FormatCompact {
			res := map[string]any{
				"id":         nodeID,
				"kind":       kind,
				"properties": props,
				"edges":      edges,
			}
			b, _ := json.MarshalIndent(res, "", "  ")
			fmt.Fprintln(out, string(b))
			return 0
		}

		fmt.Fprintf(out, "Node: %s (kind: %s)\n", nodeID, kind)
		if len(props) > 0 {
			fmt.Fprintln(out, "Properties:")
			for k, v := range props {
				fmt.Fprintf(out, "  %s: %v\n", k, v)
			}
		} else {
			fmt.Fprintln(out, "Properties: (empty)")
		}

		if len(edges) > 0 {
			fmt.Fprintln(out, "\nConnected Relationships:")
			for _, e := range edges {
				if e.Direction == "outgoing" {
					fmt.Fprintf(out, "  -> [:%s] -> %s\n", e.Relation, e.TargetID)
				} else {
					fmt.Fprintf(out, "  <- [:%s] <- %s\n", e.Relation, e.TargetID)
				}
			}
		}
		return 0

	case "set":
		fs := flag.NewFlagSet("node set", flag.ContinueOnError)
		fs.SetOutput(errOut)
		dbPath := fs.String("db", defaultDB, "Path to SQLite database")
		kindFlag := fs.String("kind", "", "Node kind/label (required)")
		propsFlag := fs.String("props", "{}", "JSON properties map")
		noMergeFlag := fs.Bool("no-merge", false, "Overwrite properties instead of merging")
		strictFlag := fs.Bool("strict-schema", defaultStrict, "Strictly enforce schema")

		positional, err := parseFlagSet(fs, subArgs)
		if err != nil {
			return 2
		}
		if len(positional) == 0 && *kindFlag == "" {
			fmt.Fprintln(errOut, "Error: node ID and kind are required. Example: cypher-mcp node set user:1 Person '{\"name\":\"Alice\"}'")
			return 2
		}
		var nodeID string
		if len(positional) > 0 {
			nodeID = positional[0]
		}
		if *kindFlag == "" && len(positional) >= 2 {
			*kindFlag = positional[1]
		}
		if *propsFlag == "{}" && len(positional) >= 3 {
			*propsFlag = positional[2]
		}
		if nodeID == "" || *kindFlag == "" {
			fmt.Fprintln(errOut, "Error: node ID and kind are required. Example: cypher-mcp node set user:1 Person '{\"name\":\"Alice\"}' or with --kind")
			return 2
		}

		var props map[string]any
		if err := json.Unmarshal([]byte(*propsFlag), &props); err != nil {
			fmt.Fprintf(errOut, "Error parsing --props JSON: %v\n", err)
			return 2
		}

		targetDB := resolveDBPath(*dbPath)
		db, err := openDatabaseForCLI(targetDB, false)
		if err != nil {
			fmt.Fprintf(errOut, "Database open error: %v\n", err)
			return 1
		}
		defer db.Close()

		msg, err := handleSetNode(db, nodeID, *kindFlag, props, *strictFlag, !*noMergeFlag)
		if err != nil {
			fmt.Fprintf(errOut, "Error setting node: %v\n", err)
			return 1
		}
		fmt.Fprintln(out, msg)
		return 0

	case "delete":
		fs := flag.NewFlagSet("node delete", flag.ContinueOnError)
		fs.SetOutput(errOut)
		dbPath := fs.String("db", defaultDB, "Path to SQLite database")

		positional, err := parseFlagSet(fs, subArgs)
		if err != nil {
			return 2
		}
		if len(positional) == 0 {
			fmt.Fprintln(errOut, "Error: node ID is required. Example: cypher-mcp node delete user:1")
			return 2
		}
		nodeID := positional[0]

		targetDB := resolveDBPath(*dbPath)
		db, err := openDatabaseForCLI(targetDB, false)
		if err != nil {
			fmt.Fprintf(errOut, "Database open error: %v\n", err)
			return 1
		}
		defer db.Close()

		msg, err := handleDeleteNode(db, nodeID)
		if err != nil {
			fmt.Fprintf(errOut, "Error deleting node: %v\n", err)
			return 1
		}
		fmt.Fprintln(out, msg)
		return 0

	default:
		fmt.Fprintf(errOut, "Unknown node subaction: %q. Allowed: get, set, delete.\n", subaction)
		return 2
	}
}

func runEdgeCmd(args []string, defaultDB, defaultFormat string, defaultJSON, defaultStrict bool, in io.Reader, out, errOut io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(errOut, "Usage: cypher-mcp edge set --from <id> --to <id> --kind <rel> [flags]")
		return 2
	}

	subaction := strings.ToLower(args[0])
	subArgs := args[1:]

	switch subaction {
	case "set":
		fs := flag.NewFlagSet("edge set", flag.ContinueOnError)
		fs.SetOutput(errOut)
		dbPath := fs.String("db", defaultDB, "Path to SQLite database")
		fromFlag := fs.String("from", "", "Source node ID (required)")
		toFlag := fs.String("to", "", "Target node ID (required)")
		kindFlag := fs.String("kind", "", "Relationship kind (required)")
		propsFlag := fs.String("props", "{}", "JSON properties map")
		noMergeFlag := fs.Bool("no-merge", false, "Overwrite properties instead of merging")
		strictFlag := fs.Bool("strict-schema", defaultStrict, "Strictly enforce schema")

		positional, err := parseFlagSet(fs, subArgs)
		if err != nil {
			return 2
		}
		if *fromFlag == "" && len(positional) >= 1 {
			*fromFlag = positional[0]
		}
		if *toFlag == "" && len(positional) >= 2 {
			*toFlag = positional[1]
		}
		if *kindFlag == "" && len(positional) >= 3 {
			*kindFlag = positional[2]
		}
		if *propsFlag == "{}" && len(positional) >= 4 {
			*propsFlag = positional[3]
		}
		if *fromFlag == "" || *toFlag == "" || *kindFlag == "" {
			fmt.Fprintln(errOut, "Error: from, to, and kind are required. Example: cypher-mcp edge set user:1 team:dev MEMBER_OF '{\"role\":\"lead\"}'")
			return 2
		}

		var props map[string]any
		if err := json.Unmarshal([]byte(*propsFlag), &props); err != nil {
			fmt.Fprintf(errOut, "Error parsing --props JSON: %v\n", err)
			return 2
		}

		targetDB := resolveDBPath(*dbPath)
		db, err := openDatabaseForCLI(targetDB, false)
		if err != nil {
			fmt.Fprintf(errOut, "Database open error: %v\n", err)
			return 1
		}
		defer db.Close()

		msg, err := handleSetEdge(db, *fromFlag, *toFlag, *kindFlag, props, *strictFlag, !*noMergeFlag)
		if err != nil {
			fmt.Fprintf(errOut, "Error setting edge: %v\n", err)
			return 1
		}
		fmt.Fprintln(out, msg)
		return 0

	default:
		fmt.Fprintf(errOut, "Unknown edge subaction: %q. Allowed: set.\n", subaction)
		return 2
	}
}

func runSchemaCmd(args []string, defaultDB, defaultFormat string, defaultJSON, defaultStrict bool, in io.Reader, out, errOut io.Writer) int {
	if len(args) > 0 && strings.ToLower(args[0]) == "define" {
		fs := flag.NewFlagSet("schema define", flag.ContinueOnError)
		fs.SetOutput(errOut)
		dbPath := fs.String("db", defaultDB, "Path to SQLite database")
		actionFlag := fs.String("action", "", "Schema action: add_kind, remove_kind, rename_kind, add_relation, remove_relation, rename_relation (required)")
		kindFlag := fs.String("kind", "", "Node kind")
		relFlag := fs.String("relation", "", "Relationship kind")
		fromKindFlag := fs.String("from-kind", "", "Source node kind")
		toKindFlag := fs.String("to-kind", "", "Target node kind")
		descFlag := fs.String("desc", "", "Description")
		newKindFlag := fs.String("new-kind", "", "New kind for rename")
		newRelFlag := fs.String("new-relation", "", "New relation for rename")
		cascadeFlag := fs.Bool("cascade", false, "Cascade deletion of associated relations and data")
		migrateToFlag := fs.String("migrate-to", "", "Target kind to migrate existing nodes to")

		if _, err := parseFlagSet(fs, args[1:]); err != nil {
			return 2
		}
		if *actionFlag == "" {
			fmt.Fprintln(errOut, "Error: --action is required. Run 'cypher-mcp schema define --help' for details.")
			return 2
		}

		targetDB := resolveDBPath(*dbPath)
		db, err := openDatabaseForCLI(targetDB, false)
		if err != nil {
			fmt.Fprintf(errOut, "Database open error: %v\n", err)
			return 1
		}
		defer db.Close()

		opts := SchemaDefineOptions{
			AllowSchemaEdit: true,
			Cascade:         *cascadeFlag,
			MigrateTo:       *migrateToFlag,
			NewKind:         *newKindFlag,
			NewRelation:     *newRelFlag,
		}

		msg, err := handleSchemaDefine(db, *actionFlag, *kindFlag, *relFlag, *fromKindFlag, *toKindFlag, *descFlag, opts)
		if err != nil {
			fmt.Fprintf(errOut, "Schema define error: %v\n", err)
			return 1
		}
		fmt.Fprintln(out, msg)
		return 0
	}

	fs := flag.NewFlagSet("schema", flag.ContinueOnError)
	fs.SetOutput(errOut)
	dbPath := fs.String("db", defaultDB, "Path to SQLite database")
	formatFlag := fs.String("format", defaultFormat, "Output format: table, json")
	jsonFlag := fs.Bool("json", defaultJSON, "Output formatted JSON")
	strictFlag := fs.Bool("strict-schema", defaultStrict, "Strictly enforce schema")

	if _, err := parseFlagSet(fs, args); err != nil {
		return 2
	}

	targetDB := resolveDBPath(*dbPath)
	dbRO, err := openDatabaseForCLI(targetDB, true)
	if err != nil {
		fmt.Fprintf(errOut, "Database open error: %v\n", err)
		return 1
	}
	defer dbRO.Close()

	resJSON, err := handleSchema(dbRO, *strictFlag)
	if err != nil {
		fmt.Fprintf(errOut, "Schema error: %v\n", err)
		return 1
	}

	format := resolveFormat(*formatFlag, *jsonFlag)
	if format == FormatJSON || format == FormatCompact {
		fmt.Fprintln(out, resJSON)
		return 0
	}

	var payload struct {
		TotalNodes     int  `json:"total_nodes"`
		TotalEdges     int  `json:"total_edges"`
		SchemaEnforced bool `json:"schema_enforced"`
		NodeKinds      []struct {
			Kind  string `json:"kind"`
			Count int    `json:"count"`
		} `json:"node_kinds"`
		EdgeKinds []struct {
			Kind  string `json:"kind"`
			Count int    `json:"count"`
		} `json:"edge_kinds"`
		AllowedKinds []struct {
			Kind        string `json:"kind"`
			Description string `json:"description"`
		} `json:"allowed_kinds"`
		AllowedRelations []struct {
			Relation    string `json:"relation"`
			FromKind    string `json:"from_kind"`
			ToKind      string `json:"to_kind"`
			Description string `json:"description"`
		} `json:"allowed_relations"`
	}
	_ = json.Unmarshal([]byte(resJSON), &payload)

	enforceStatus := "Permissive (dynamic)"
	if payload.SchemaEnforced {
		enforceStatus = "Enforced (strict)"
	}
	fmt.Fprintf(out, "Knowledge Graph Schema (Total Nodes: %d, Total Edges: %d, Mode: %s)\n\n", payload.TotalNodes, payload.TotalEdges, enforceStatus)

	nodeCounts := make(map[string]int)
	for _, nk := range payload.NodeKinds {
		nodeCounts[nk.Kind] = nk.Count
	}

	edgeCounts := make(map[string]int)
	for _, ek := range payload.EdgeKinds {
		edgeCounts[ek.Kind] = ek.Count
	}

	if len(payload.AllowedKinds) > 0 {
		fmt.Fprintln(out, "Defined Node Kinds:")
		headers := []string{"Kind", "Count", "Description"}
		var rows [][]string
		for _, k := range payload.AllowedKinds {
			cnt := nodeCounts[k.Kind]
			rows = append(rows, []string{k.Kind, strconv.Itoa(cnt), k.Description})
		}
		fmt.Fprintln(out, renderTable(headers, rows))
		fmt.Fprintln(out)
	} else if len(payload.NodeKinds) > 0 {
		fmt.Fprintln(out, "Observed Node Kinds:")
		headers := []string{"Kind", "Count"}
		var rows [][]string
		for _, k := range payload.NodeKinds {
			rows = append(rows, []string{k.Kind, strconv.Itoa(k.Count)})
		}
		fmt.Fprintln(out, renderTable(headers, rows))
		fmt.Fprintln(out)
	} else {
		fmt.Fprintln(out, "Node Kinds: (none defined or present)")
		fmt.Fprintln(out)
	}

	if len(payload.AllowedRelations) > 0 {
		fmt.Fprintln(out, "Defined Relationships:")
		headers := []string{"From Kind", "Relationship", "To Kind", "Count", "Description"}
		var rows [][]string
		for _, r := range payload.AllowedRelations {
			cnt := edgeCounts[r.Relation]
			rows = append(rows, []string{r.FromKind, r.Relation, r.ToKind, strconv.Itoa(cnt), r.Description})
		}
		fmt.Fprintln(out, renderTable(headers, rows))
	} else if len(payload.EdgeKinds) > 0 {
		fmt.Fprintln(out, "Observed Relationships:")
		headers := []string{"Relationship", "Count"}
		var rows [][]string
		for _, e := range payload.EdgeKinds {
			rows = append(rows, []string{e.Kind, strconv.Itoa(e.Count)})
		}
		fmt.Fprintln(out, renderTable(headers, rows))
	} else {
		fmt.Fprintln(out, "Relationships: (none defined or present)")
	}

	return 0
}

func runAliasCmd(args []string, defaultDB, defaultFormat string, defaultJSON bool, in io.Reader, out, errOut io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(errOut, "Usage: cypher-mcp alias upsert --id <node_id> --alias <alias> [flags]")
		return 2
	}

	subaction := strings.ToLower(args[0])
	subArgs := args[1:]

	switch subaction {
	case "upsert":
		fs := flag.NewFlagSet("alias upsert", flag.ContinueOnError)
		fs.SetOutput(errOut)
		dbPath := fs.String("db", defaultDB, "Path to SQLite database")
		nodeIDFlag := fs.String("id", "", "Node ID (required)")
		aliasFlag := fs.String("alias", "", "Entity alias string (required)")
		embedFlag := fs.String("embedding", "", "Optional JSON array of floats for embedding vector")

		positional, err := parseFlagSet(fs, subArgs)
		if err != nil {
			return 2
		}
		if *nodeIDFlag == "" && len(positional) >= 1 {
			*nodeIDFlag = positional[0]
		}
		if *aliasFlag == "" && len(positional) >= 2 {
			*aliasFlag = positional[1]
		}
		if *nodeIDFlag == "" || *aliasFlag == "" {
			fmt.Fprintln(errOut, "Error: node ID and alias are required. Example: cypher-mcp alias upsert person:alex 'Саша'")
			return 2
		}

		var vec []float32
		if *embedFlag != "" {
			var raw []float32
			if err := json.Unmarshal([]byte(*embedFlag), &raw); err != nil {
				fmt.Fprintf(errOut, "Error parsing --embedding JSON array: %v\n", err)
				return 2
			}
			vec = raw
		}

		targetDB := resolveDBPath(*dbPath)
		db, err := openDatabaseForCLI(targetDB, false)
		if err != nil {
			fmt.Fprintf(errOut, "Database open error: %v\n", err)
			return 1
		}
		defer db.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		msg, err := handleUpsertAlias(db, *nodeIDFlag, *aliasFlag, vec, ctx)
		if err != nil {
			fmt.Fprintf(errOut, "Error upserting alias: %v\n", err)
			return 1
		}
		fmt.Fprintln(out, msg)
		return 0

	default:
		fmt.Fprintf(errOut, "Unknown alias subaction: %q. Allowed: upsert.\n", subaction)
		return 2
	}
}

func runBatchCmd(args []string, defaultDB string, defaultStrict bool, in io.Reader, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("batch", flag.ContinueOnError)
	fs.SetOutput(errOut)
	dbPath := fs.String("db", defaultDB, "Path to SQLite database")
	strictFlag := fs.Bool("strict-schema", defaultStrict, "Strictly enforce schema")

	positional, err := parseFlagSet(fs, args)
	if err != nil {
		return 2
	}

	var data []byte
	if len(positional) > 0 && positional[0] != "-" {
		filePath := positional[0]
		data, err = os.ReadFile(filePath)
		if err != nil {
			fmt.Fprintf(errOut, "Error reading batch file %s: %v\n", filePath, err)
			return 1
		}
	} else {
		data, err = io.ReadAll(in)
		if err != nil {
			fmt.Fprintf(errOut, "Error reading batch JSON from stdin: %v\n", err)
			return 1
		}
	}

	if len(bytes.TrimSpace(data)) == 0 {
		fmt.Fprintln(errOut, "Error: batch JSON payload is empty. Provide a file path or pipe via stdin.")
		return 2
	}

	var batchPayload struct {
		Nodes []BatchNodeItem `json:"nodes"`
		Edges []BatchEdgeItem `json:"edges"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&batchPayload); err != nil {
		fmt.Fprintf(errOut, "Error decoding batch JSON: %v\n", err)
		return 2
	}

	targetDB := resolveDBPath(*dbPath)
	db, err := openDatabaseForCLI(targetDB, false)
	if err != nil {
		fmt.Fprintf(errOut, "Database open error: %v\n", err)
		return 1
	}
	defer db.Close()

	msg, err := handleBatchUpsert(db, batchPayload.Nodes, batchPayload.Edges, *strictFlag)
	if err != nil {
		fmt.Fprintf(errOut, "Batch upsert error: %v\n", err)
		return 1
	}
	fmt.Fprintln(out, msg)
	return 0
}

func runServeCmd(args []string, defaultDB string, defaultStrict bool, in io.Reader, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(errOut)
	dbPath := fs.String("db", defaultDB, "Path to SQLite database")
	logPath := fs.String("log", "", "Path to debug log file")
	strictFlag := fs.Bool("strict-schema", defaultStrict, "Strictly enforce schema")
	maxRowsFlag := fs.Int("max-rows", 1000, "Maximum number of rows returned by graph_query")

	if _, err := parseFlagSet(fs, args); err != nil {
		return 2
	}

	if *maxRowsFlag >= 0 {
		defaultMaxRows = *maxRowsFlag
	}

	targetDB := resolveDBPath(*dbPath)
	var logger *log.Logger
	if *logPath != "" {
		_ = os.MkdirAll(filepath.Dir(*logPath), 0755)
		if f, err := os.OpenFile(*logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644); err == nil {
			defer f.Close()
			logger = log.New(f, "[mcp] ", log.LstdFlags|log.Lmicroseconds)
		}
	}

	db, err := openDatabaseForCLI(targetDB, false)
	if err != nil {
		fmt.Fprintf(errOut, "database init failed: %v\n", err)
		return 1
	}
	defer db.Close()

	dbRO, err := openDatabaseForCLI(targetDB, true)
	if err != nil {
		fmt.Fprintf(errOut, "ro database init failed: %v\n", err)
		return 1
	}
	defer dbRO.Close()

	runServerWithConfig(in, out, db, dbRO, logger, *strictFlag)
	return 0
}

// ── Help Manual ────────────────────────────────────────────────────────────

func printMainHelp(out io.Writer) {
	fmt.Fprintf(out, `cypher-mcp - Fast OpenCypher Knowledge Graph CLI & MCP Server on SQLite (v%s)

Usage:
  cypher-mcp <command> [flags] [arguments]
  cypher-mcp [flags] (starts MCP server over stdio)

Core Commands:
  query, q       Execute an OpenCypher query against the knowledge graph
  search, s      Full-text search nodes and properties using SQLite FTS5
  resolve, r     Vector entity resolution / semantic linking using cosine similarity
  node           Inspect, upsert, or delete graph nodes
  edge           Upsert directed relationships between nodes
  schema         Inspect or define graph kinds and relationship schemas
  alias          Register entity aliases and embedding vectors for resolution
  batch          Bulk import nodes and edges from a JSON file or stdin
  serve          Start Model Context Protocol (MCP) JSON-RPC 2.0 stdio server
  version        Display version information
  help           Show help for any command

Global Flags:
  --db <path>           SQLite database path (default: $CYPHER_DB, $HERMES_GRAPH_DB, or knowledge_graph.db)
  --format, -f <format> Output format: table, json, compact (default: table for terminal, json for pipes)
  --json                Shorthand for --format json
  --strict-schema       Strictly enforce graph schema validations
  -h, --help            Show help documentation

Examples:
  # Run an OpenCypher query
  cypher-mcp query "MATCH (n:Person) RETURN n.name, n.role LIMIT 10"

  # Query with JSON parameters
  cypher-mcp query --params '{"name": "Alice"}' "MATCH (n:Person {name: $name}) RETURN n"

  # Search graph properties
  cypher-mcp search "Alice"

  # Resolve colloquial names to canonical graph entities
  cypher-mcp resolve "Саша"

  # Inspect a node and its connections
  cypher-mcp node get person:alice

  # Start MCP server explicitly
  cypher-mcp serve --db ~/.hermes/knowledge_graph.db

Run 'cypher-mcp help <command>' for details on a specific command.
`, appVersion)
}

func printSubcommandHelp(subcmd string, out io.Writer) {
	switch strings.ToLower(subcmd) {
	case "query", "q":
		fmt.Fprintln(out, `Usage: cypher-mcp query [flags] "<cypher_query>"
       cypher-mcp query [flags] - (reads from stdin)

Flags:
  --db <path>          Path to SQLite database
  --format, -f <fmt>   Output format: table, json, compact (default: table for TTY, json for pipes)
  --json               Output formatted JSON
  --params <json>      JSON parameters map or @path/to/params.json
  --limit <n>          Row limit (default: 1000)
  --strict-schema      Enforce strict schema
  --raw-sql            Print compiled SQLite SQL statement`)
	case "search", "s":
		fmt.Fprintln(out, `Usage: cypher-mcp search [flags] "<query_term>"

Flags:
  --db <path>          Path to SQLite database
  --format, -f <fmt>   Output format: table, json, compact
  --json               Output formatted JSON
  --kind <kind>        Filter by node kind/label
  --limit <n>          Maximum matches to return (1-50, default: 10)`)
	case "resolve", "r":
		fmt.Fprintln(out, `Usage: cypher-mcp resolve [flags] "<text>"

Flags:
  --db <path>          Path to SQLite database
  --format, -f <fmt>   Output format: table, json, compact
  --json               Output formatted JSON
  --kind <kind>        Filter candidates by node kind
  --limit <n>          Maximum candidates (default: 5)
  --min-score <f>      Minimum cosine similarity threshold (default: 0.50)`)
	case "node":
		fmt.Fprintln(out, `Usage: cypher-mcp node get <id> [flags]
       cypher-mcp node set <id> --kind <kind> [--props '{"k":"v"}'] [--no-merge] [flags]
       cypher-mcp node delete <id> [flags]`)
	case "edge":
		fmt.Fprintln(out, `Usage: cypher-mcp edge set --from <id> --to <id> --kind <rel> [--props '{"k":"v"}'] [--no-merge] [flags]`)
	case "schema":
		fmt.Fprintln(out, `Usage: cypher-mcp schema [flags]
       cypher-mcp schema define --action <action> [--kind <k>] [--relation <r>] [--from-kind <f>] [--to-kind <t>] [flags]`)
	case "alias":
		fmt.Fprintln(out, `Usage: cypher-mcp alias upsert --id <node_id> --alias <alias> [--embedding '[0.1, 0.2]'] [flags]`)
	case "batch":
		fmt.Fprintln(out, `Usage: cypher-mcp batch [flags] <file.json>
       cat file.json | cypher-mcp batch [flags] -`)
	default:
		printMainHelp(out)
	}
}

// isTerminal checks if the given file descriptor is an interactive terminal.
func isTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	fd := f.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

// renderTable renders headers and data rows as an aligned ASCII table.
func renderTable(headers []string, rows [][]string) string {
	if len(headers) == 0 {
		return ""
	}

	numCols := len(headers)
	colWidths := make([]int, numCols)

	// Measure headers
	for i, h := range headers {
		w := utf8.RuneCountInString(h)
		if w > colWidths[i] {
			colWidths[i] = w
		}
	}

	// Measure rows
	for _, row := range rows {
		for i := 0; i < numCols && i < len(row); i++ {
			lines := strings.Split(row[i], "\n")
			for _, line := range lines {
				w := utf8.RuneCountInString(line)
				if w > colWidths[i] {
					colWidths[i] = w
				}
			}
		}
	}

	// Minimum column width of 3
	for i := range colWidths {
		if colWidths[i] < 3 {
			colWidths[i] = 3
		}
		if colWidths[i] > 60 {
			colWidths[i] = 60
		}
	}

	var sb strings.Builder

	// Build separator line: +-------+-------+
	buildSep := func() string {
		var parts []string
		for _, w := range colWidths {
			parts = append(parts, strings.Repeat("-", w+2))
		}
		return "+" + strings.Join(parts, "+") + "+"
	}

	sep := buildSep()

	// Print top border
	sb.WriteString(sep)
	sb.WriteString("\n")

	// Print headers
	sb.WriteString("|")
	for i, h := range headers {
		sb.WriteString(" ")
		sb.WriteString(padRight(truncateRunes(h, colWidths[i]), colWidths[i]))
		sb.WriteString(" |")
	}
	sb.WriteString("\n")

	// Print header-row separator
	sb.WriteString(sep)
	sb.WriteString("\n")

	// Print data rows
	for _, row := range rows {
		sb.WriteString("|")
		for i := 0; i < numCols; i++ {
			val := ""
			if i < len(row) {
				val = strings.ReplaceAll(row[i], "\r\n", " ")
				val = strings.ReplaceAll(val, "\n", " ")
			}
			sb.WriteString(" ")
			sb.WriteString(padRight(truncateRunes(val, colWidths[i]), colWidths[i]))
			sb.WriteString(" |")
		}
		sb.WriteString("\n")
	}

	// Print bottom border
	sb.WriteString(sep)
	return sb.String()
}

func padRight(s string, width int) string {
	w := utf8.RuneCountInString(s)
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}

func truncateRunes(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	if maxRunes <= 3 {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-3]) + "..."
}
