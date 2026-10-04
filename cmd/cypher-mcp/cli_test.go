package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLI_VersionAndHelp(t *testing.T) {
	var out, errOut bytes.Buffer

	// Test version
	code := runCLIWithIO([]string{"version"}, nil, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected code 0, got %d. stderr: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "cypher-mcp v"+appVersion) {
		t.Fatalf("unexpected version output: %s", out.String())
	}

	// Test help
	out.Reset()
	errOut.Reset()
	code = runCLIWithIO([]string{"help"}, nil, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected code 0, got %d", code)
	}
	if !strings.Contains(out.String(), "Core Commands:") {
		t.Fatalf("unexpected main help: %s", out.String())
	}

	// Test subcommand help
	out.Reset()
	errOut.Reset()
	code = runCLIWithIO([]string{"help", "query"}, nil, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected code 0, got %d", code)
	}
	if !strings.Contains(out.String(), "cypher-mcp query [flags]") {
		t.Fatalf("unexpected query help: %s", out.String())
	}
}

func TestCLI_ShouldRunCLIDetection(t *testing.T) {
	// CLI subcommands
	if !shouldRunCLI([]string{"query", "MATCH (n) RETURN n"}) {
		t.Errorf("expected shouldRunCLI=true for 'query'")
	}
	if !shouldRunCLI([]string{"search", "Alice"}) {
		t.Errorf("expected shouldRunCLI=true for 'search'")
	}
	if !shouldRunCLI([]string{"node", "get", "user:1"}) {
		t.Errorf("expected shouldRunCLI=true for 'node'")
	}
	if !shouldRunCLI([]string{"--help"}) {
		t.Errorf("expected shouldRunCLI=true for '--help'")
	}
	if !shouldRunCLI([]string{"--version"}) {
		t.Errorf("expected shouldRunCLI=true for '--version'")
	}
	if !shouldRunCLI([]string{"serve", "--db", "test.db"}) {
		t.Errorf("expected shouldRunCLI=true for 'serve'")
	}

	// MCP server invocations (flags only, no subcommands)
	if shouldRunCLI([]string{"--db", "test.db"}) {
		t.Errorf("expected shouldRunCLI=false for '--db test.db'")
	}
	if shouldRunCLI([]string{"-db", "test.db", "--strict-schema"}) {
		t.Errorf("expected shouldRunCLI=false for '-db test.db --strict-schema'")
	}
}

func TestCLI_NodeAndEdgeLifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cli_test.db")

	var out, errOut bytes.Buffer

	// 1. Node Set
	code := runCLIWithIO([]string{
		"node", "set", "person:bob",
		"--db", dbPath,
		"--kind", "Person",
		"--props", `{"name":"Bob","role":"Architect"}`,
	}, nil, &out, &errOut)
	if code != 0 {
		t.Fatalf("node set failed with code %d. err: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "upserted successfully") {
		t.Fatalf("unexpected node set output: %s", out.String())
	}

	// 2. Node Get (JSON)
	out.Reset()
	errOut.Reset()
	code = runCLIWithIO([]string{
		"node", "get", "person:bob",
		"--db", dbPath,
		"--json",
	}, nil, &out, &errOut)
	if code != 0 {
		t.Fatalf("node get failed with code %d. err: %s", code, errOut.String())
	}
	var nodeData struct {
		ID         string         `json:"id"`
		Kind       string         `json:"kind"`
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(out.Bytes(), &nodeData); err != nil {
		t.Fatalf("invalid json from node get: %v. out: %s", err, out.String())
	}
	if nodeData.ID != "person:bob" || nodeData.Kind != "Person" || nodeData.Properties["name"] != "Bob" {
		t.Fatalf("unexpected node data: %+v", nodeData)
	}

	// 3. Node Get (Table/Readable)
	out.Reset()
	errOut.Reset()
	code = runCLIWithIO([]string{
		"node", "get", "person:bob",
		"--db", dbPath,
		"--format", "table",
	}, nil, &out, &errOut)
	if code != 0 {
		t.Fatalf("node get table failed with code %d. err: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "Node: person:bob") || !strings.Contains(out.String(), "role: Architect") {
		t.Fatalf("unexpected table output: %s", out.String())
	}

	// 4. Edge Set
	out.Reset()
	errOut.Reset()
	// Create second node
	_ = runCLIWithIO([]string{
		"node", "set", "team:core",
		"--db", dbPath,
		"--kind", "Team",
		"--props", `{"name":"Core Infrastructure"}`,
	}, nil, &out, &errOut)

	out.Reset()
	errOut.Reset()
	code = runCLIWithIO([]string{
		"edge", "set",
		"--db", dbPath,
		"--from", "person:bob",
		"--to", "team:core",
		"--kind", "LEADS",
		"--props", `{"since":2023}`,
	}, nil, &out, &errOut)
	if code != 0 {
		t.Fatalf("edge set failed with code %d. err: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "saved successfully") {
		t.Fatalf("unexpected edge set output: %s", out.String())
	}

	// Verify connected edge in node get
	out.Reset()
	errOut.Reset()
	code = runCLIWithIO([]string{
		"node", "get", "person:bob",
		"--db", dbPath,
		"--json",
	}, nil, &out, &errOut)
	if code != 0 {
		t.Fatalf("node get failed: %s", errOut.String())
	}
	var nodeWithEdges struct {
		Edges []struct {
			Direction string `json:"direction"`
			Relation  string `json:"relation"`
			TargetID  string `json:"target_id"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(out.Bytes(), &nodeWithEdges); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(nodeWithEdges.Edges) != 1 || nodeWithEdges.Edges[0].Relation != "LEADS" || nodeWithEdges.Edges[0].TargetID != "team:core" {
		t.Fatalf("unexpected edge info: %+v", nodeWithEdges.Edges)
	}

	// 5. Node Delete
	out.Reset()
	errOut.Reset()
	code = runCLIWithIO([]string{
		"node", "delete", "person:bob",
		"--db", dbPath,
	}, nil, &out, &errOut)
	if code != 0 {
		t.Fatalf("node delete failed with code %d. err: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "deleted") {
		t.Fatalf("unexpected node delete output: %s", out.String())
	}
}

func TestCLI_QueryTableAndJSON(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "query_test.db")

	var out, errOut bytes.Buffer

	// Seed nodes
	_ = runCLIWithIO([]string{
		"node", "set", "user:alice",
		"--db", dbPath,
		"--kind", "User",
		"--props", `{"name":"Alice","tier":"Premium"}`,
	}, nil, &out, &errOut)
	_ = runCLIWithIO([]string{
		"node", "set", "user:charlie",
		"--db", dbPath,
		"--kind", "User",
		"--props", `{"name":"Charlie","tier":"Basic"}`,
	}, nil, &out, &errOut)

	// Test Query JSON format
	out.Reset()
	errOut.Reset()
	code := runCLIWithIO([]string{
		"query",
		"--db", dbPath,
		"--format", "json",
		"MATCH (u:User) RETURN u.name AS name, u.tier AS tier ORDER BY name",
	}, nil, &out, &errOut)
	if code != 0 {
		t.Fatalf("query failed with code %d. err: %s", code, errOut.String())
	}

	var qRes struct {
		Count   int              `json:"count"`
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(out.Bytes(), &qRes); err != nil {
		t.Fatalf("unmarshal json query output: %v. out: %s", err, out.String())
	}
	if qRes.Count != 2 {
		t.Fatalf("expected count 2, got %d", qRes.Count)
	}
	if qRes.Results[0]["name"] != "Alice" || qRes.Results[1]["name"] != "Charlie" {
		t.Fatalf("unexpected results: %+v", qRes.Results)
	}

	// Test Query Table format
	out.Reset()
	errOut.Reset()
	code = runCLIWithIO([]string{
		"query",
		"--db", dbPath,
		"--format", "table",
		"MATCH (u:User) RETURN u.name AS name, u.tier AS tier ORDER BY name",
	}, nil, &out, &errOut)
	if code != 0 {
		t.Fatalf("table query failed with code %d. err: %s", code, errOut.String())
	}
	tableOut := out.String()
	if !strings.Contains(tableOut, "Alice") || !strings.Contains(tableOut, "Charlie") || !strings.Contains(tableOut, "+") {
		t.Fatalf("unexpected table output:\n%s", tableOut)
	}

	// Test Query with Parameters
	out.Reset()
	errOut.Reset()
	code = runCLIWithIO([]string{
		"query",
		"--db", dbPath,
		"--params", `{"target":"Alice"}`,
		"--format", "json",
		"MATCH (u:User {name: $target}) RETURN u.tier AS tier",
	}, nil, &out, &errOut)
	if code != 0 {
		t.Fatalf("parameterized query failed with code %d. err: %s", code, errOut.String())
	}
	var paramRes struct {
		Count   int              `json:"count"`
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(out.Bytes(), &paramRes); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if paramRes.Count != 1 || paramRes.Results[0]["tier"] != "Premium" {
		t.Fatalf("unexpected parameterized query result: %+v", paramRes)
	}

	// Test Piped Query via Stdin
	out.Reset()
	errOut.Reset()
	pipeInput := strings.NewReader("MATCH (u:User) RETURN count(u) AS total")
	code = runCLIWithIO([]string{
		"query",
		"--db", dbPath,
		"--format", "json",
		"-",
	}, pipeInput, &out, &errOut)
	if code != 0 {
		t.Fatalf("piped query failed with code %d. err: %s", code, errOut.String())
	}
	var pipeRes struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(out.Bytes(), &pipeRes); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(pipeRes.Results) != 1 {
		t.Fatalf("expected 1 row from count: %+v", pipeRes.Results)
	}
}

func TestCLI_Search(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "search_test.db")

	var out, errOut bytes.Buffer

	_ = runCLIWithIO([]string{
		"node", "set", "doc:101",
		"--db", dbPath,
		"--kind", "Document",
		"--props", `{"title":"Distributed Database Architecture","author":"Alice"}`,
	}, nil, &out, &errOut)

	// Search table
	out.Reset()
	errOut.Reset()
	code := runCLIWithIO([]string{
		"search",
		"--db", dbPath,
		"--format", "table",
		"Distributed",
	}, nil, &out, &errOut)
	if code != 0 {
		t.Fatalf("search failed with code %d. err: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "doc:101") || !strings.Contains(out.String(), "Document") {
		t.Fatalf("unexpected search table output:\n%s", out.String())
	}

	// Search JSON
	out.Reset()
	errOut.Reset()
	code = runCLIWithIO([]string{
		"search",
		"--db", dbPath,
		"--json",
		"Architecture",
	}, nil, &out, &errOut)
	if code != 0 {
		t.Fatalf("search json failed with code %d. err: %s", code, errOut.String())
	}
	var sRes struct {
		Count   int                `json:"count"`
		Results []SearchResultItem `json:"results"`
	}
	if err := json.Unmarshal(out.Bytes(), &sRes); err != nil {
		t.Fatalf("unmarshal search: %v", err)
	}
	if sRes.Count != 1 || sRes.Results[0].ID != "doc:101" {
		t.Fatalf("unexpected search results: %+v", sRes)
	}
}

func TestCLI_Schema(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "schema_test.db")

	var out, errOut bytes.Buffer

	// Define kind and relation
	code := runCLIWithIO([]string{
		"schema", "define",
		"--db", dbPath,
		"--action", "add_kind",
		"--kind", "Customer",
		"--desc", "Paying corporate client",
	}, nil, &out, &errOut)
	if code != 0 {
		t.Fatalf("schema define add_kind failed: %s", errOut.String())
	}

	out.Reset()
	errOut.Reset()
	code = runCLIWithIO([]string{
		"schema", "define",
		"--db", dbPath,
		"--action", "add_kind",
		"--kind", "Invoice",
	}, nil, &out, &errOut)
	if code != 0 {
		t.Fatalf("schema define add_kind invoice failed: %s", errOut.String())
	}

	out.Reset()
	errOut.Reset()
	code = runCLIWithIO([]string{
		"schema", "define",
		"--db", dbPath,
		"--action", "add_relation",
		"--relation", "BILLED_TO",
		"--from-kind", "Invoice",
		"--to-kind", "Customer",
	}, nil, &out, &errOut)
	if code != 0 {
		t.Fatalf("schema define add_relation failed: %s", errOut.String())
	}

	// Show schema (table)
	out.Reset()
	errOut.Reset()
	code = runCLIWithIO([]string{
		"schema",
		"--db", dbPath,
		"--format", "table",
	}, nil, &out, &errOut)
	if code != 0 {
		t.Fatalf("schema show failed: %s", errOut.String())
	}
	schemaOut := out.String()
	if !strings.Contains(schemaOut, "Customer") || !strings.Contains(schemaOut, "BILLED_TO") {
		t.Fatalf("unexpected schema table output:\n%s", schemaOut)
	}

	// Show schema (JSON)
	out.Reset()
	errOut.Reset()
	code = runCLIWithIO([]string{
		"schema",
		"--db", dbPath,
		"--json",
	}, nil, &out, &errOut)
	if code != 0 {
		t.Fatalf("schema show json failed: %s", errOut.String())
	}
	var sPayload struct {
		SchemaEnforced bool `json:"schema_enforced"`
		AllowedKinds   []struct {
			Kind string `json:"kind"`
		} `json:"allowed_kinds"`
	}
	if err := json.Unmarshal(out.Bytes(), &sPayload); err != nil {
		t.Fatalf("unmarshal schema json: %v", err)
	}
	if len(sPayload.AllowedKinds) != 2 {
		t.Fatalf("expected 2 allowed kinds: %+v", sPayload)
	}
}

func TestCLI_BatchImport(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "batch_test.db")

	batchJSON := `{
		"nodes": [
			{"id": "service:auth", "kind": "Microservice", "properties": {"name": "Auth Service"}},
			{"id": "cache:redis", "kind": "Cache", "properties": {"engine": "Redis"}}
		],
		"edges": [
			{"from": "service:auth", "to": "cache:redis", "kind": "USES_CACHE", "properties": {"ttl": 3600}}
		]
	}`

	// Test batch from stdin
	var out, errOut bytes.Buffer
	code := runCLIWithIO([]string{
		"batch",
		"--db", dbPath,
		"-",
	}, strings.NewReader(batchJSON), &out, &errOut)
	if code != 0 {
		t.Fatalf("batch from stdin failed with code %d. err: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "upserted 2 nodes and 1 edges") {
		t.Fatalf("unexpected batch output: %s", out.String())
	}

	// Verify query returns the imported entities
	out.Reset()
	errOut.Reset()
	code = runCLIWithIO([]string{
		"query",
		"--db", dbPath,
		"--format", "json",
		"MATCH (s:Microservice)-[:USES_CACHE]->(c:Cache) RETURN s.name AS svc, c.engine AS cache",
	}, nil, &out, &errOut)
	if code != 0 {
		t.Fatalf("query after batch failed: %s", errOut.String())
	}
	var qRes struct {
		Count   int              `json:"count"`
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(out.Bytes(), &qRes); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if qRes.Count != 1 || qRes.Results[0]["svc"] != "Auth Service" || qRes.Results[0]["cache"] != "Redis" {
		t.Fatalf("unexpected query result after batch: %+v", qRes)
	}
}

func TestCLI_AliasAndResolve(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "alias_test.db")

	var out, errOut bytes.Buffer

	// Insert node
	_ = runCLIWithIO([]string{
		"node", "set", "person:alex",
		"--db", dbPath,
		"--kind", "Person",
		"--props", `{"name":"Alexander"}`,
	}, nil, &out, &errOut)

	// Upsert alias with explicit embedding vector
	out.Reset()
	errOut.Reset()
	code := runCLIWithIO([]string{
		"alias", "upsert",
		"--db", dbPath,
		"--id", "person:alex",
		"--alias", "Саша",
		"--embedding", "[0.5, 0.5, 0.5, 0.5]",
	}, nil, &out, &errOut)
	if code != 0 {
		t.Fatalf("alias upsert failed: %s", errOut.String())
	}
	if !strings.Contains(out.String(), "upserted") {
		t.Fatalf("unexpected alias output: %s", out.String())
	}
}
