package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestJSONRPC_64BitIntegerID(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := initDatabase(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("initDatabase: %v", err)
	}
	defer db.Close()
	dbRO, err := initRODatabase(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("initRODatabase: %v", err)
	}
	defer dbRO.Close()

	// 9007199254740993 is 2^53 + 1, which loses precision in float64 (becomes 9007199254740992)
	inJSON := `{"jsonrpc":"2.0","id":9007199254740993,"method":"ping","params":{}}` + "\n"
	in := bytes.NewBufferString(inJSON)
	out := &bytes.Buffer{}

	runServerWithConfig(in, out, db, dbRO, nil, false)

	outStr := out.String()
	if !strings.Contains(outStr, "9007199254740993") {
		t.Errorf("FAIL invariant F12: 64-bit integer ID precision was lost: %s", outStr)
	}
}

func TestJSONRPC_BooleanID_Rejected(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := initDatabase(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("initDatabase: %v", err)
	}
	defer db.Close()
	dbRO, err := initRODatabase(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("initRODatabase: %v", err)
	}
	defer dbRO.Close()

	inJSON := `{"jsonrpc":"2.0","id":true,"method":"ping","params":{}}` + "\n"
	in := bytes.NewBufferString(inJSON)
	out := &bytes.Buffer{}

	runServerWithConfig(in, out, db, dbRO, nil, false)

	var resp JSONRPCResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Error == nil {
		t.Errorf("FAIL invariant F12: boolean ID should be rejected with an error, got: %s", out.String())
	}
}

func TestJSONRPC_Notification_NoResponse(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := initDatabase(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("initDatabase: %v", err)
	}
	defer db.Close()
	dbRO, err := initRODatabase(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("initRODatabase: %v", err)
	}
	defer dbRO.Close()

	// Notification has no "id" field
	inJSON := `{"jsonrpc":"2.0","method":"ping","params":{}}` + "\n"
	in := bytes.NewBufferString(inJSON)
	out := &bytes.Buffer{}

	runServerWithConfig(in, out, db, dbRO, nil, false)

	if out.Len() > 0 {
		t.Errorf("FAIL invariant F12: notifications MUST NOT receive responses, but got: %s", out.String())
	}
}

func TestJSONRPC_InvalidVersion_Rejected(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := initDatabase(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("initDatabase: %v", err)
	}
	defer db.Close()
	dbRO, err := initRODatabase(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("initRODatabase: %v", err)
	}
	defer dbRO.Close()

	inJSON := `{"jsonrpc":"1.0","id":1,"method":"ping","params":{}}` + "\n"
	in := bytes.NewBufferString(inJSON)
	out := &bytes.Buffer{}

	runServerWithConfig(in, out, db, dbRO, nil, false)

	outStr := out.String()
	if !strings.Contains(outStr, "-32600") {
		t.Errorf("FAIL invariant F12: invalid jsonrpc version should return -32600 Invalid Request, got: %s", outStr)
	}
}

func TestJSONRPC_ArrayBatch_InvalidRequest(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := initDatabase(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("initDatabase: %v", err)
	}
	defer db.Close()
	dbRO, err := initRODatabase(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("initRODatabase: %v", err)
	}
	defer dbRO.Close()

	inJSON := `[{"jsonrpc":"2.0","id":1,"method":"ping","params":{}}]` + "\n"
	in := bytes.NewBufferString(inJSON)
	out := &bytes.Buffer{}

	runServerWithConfig(in, out, db, dbRO, nil, false)

	outStr := out.String()
	if !strings.Contains(outStr, "-32600") {
		t.Errorf("FAIL invariant F12: array batch requests should return -32600 Invalid Request, got: %s", outStr)
	}
}

func TestGraphQuery_BacktickInStringLiteral(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := initDatabase(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("initDatabase: %v", err)
	}
	defer db.Close()

	// Seed node with message containing backticks
	msg := "Use `git push` to upload"
	if _, err := handleSetNode(db, "c1", "Commit", map[string]any{"msg": msg}, false, false); err != nil {
		t.Fatalf("set node: %v", err)
	}

	query := "MATCH (c:Commit) WHERE c.msg = 'Use `git push` to upload' RETURN c.msg"
	out, err := handleGraphQuery(db, query)
	if err != nil {
		t.Fatalf("FAIL invariant F15: backtick in string literal falsely rejected as identifier injection: %v", err)
	}
	if !strings.Contains(out, "git push") {
		t.Errorf("expected result to contain 'git push', got: %s", out)
	}
}

func TestGraphQuery_NegativeLimit_Rejected(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := initDatabase(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("initDatabase: %v", err)
	}
	defer db.Close()

	args := map[string]any{
		"query": "MATCH (n) RETURN n",
		"limit": -5,
	}
	_, err = executeToolCall(db, db, "graph_query", args, false)
	if err == nil {
		t.Fatalf("FAIL invariant F15: expected error when limit is negative, got nil")
	}
}

func TestJSONRPC_IntegerPrecisionPreserved(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := initDatabase(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("initDatabase: %v", err)
	}
	defer db.Close()

	// 9007199254740993 is 2^53 + 1, which loses precision in float64
	largeNum := json.Number("9007199254740993")
	args := map[string]any{
		"id":   "n1",
		"kind": "Node",
		"properties": map[string]any{
			"big_int": largeNum,
		},
	}
	_, err = executeToolCall(db, db, "graph_set_node", args, false)
	if err != nil {
		t.Fatalf("set node: %v", err)
	}

	var propsJSON string
	if err := db.QueryRow("SELECT properties FROM nodes WHERE id = 'n1'").Scan(&propsJSON); err != nil {
		t.Fatalf("query properties: %v", err)
	}
	if !strings.Contains(propsJSON, "9007199254740993") {
		t.Fatalf("expected 9007199254740993 in properties, got: %s", propsJSON)
	}
}

func TestGraphQuery_LimitCeilingAndFractional(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := initDatabase(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("initDatabase: %v", err)
	}
	defer db.Close()

	// Insert 3 nodes
	for i := 1; i <= 3; i++ {
		_, _ = handleSetNode(db, fmt.Sprintf("n%d", i), "Item", nil, false, false)
	}

	// Fractional limit should be rejected
	argsFrac := map[string]any{
		"query": "MATCH (n) RETURN n.id",
		"limit": json.Number("-0.5"),
	}
	_, err = executeToolCall(db, db, "graph_query", argsFrac, false)
	if err == nil {
		t.Fatalf("expected error for fractional limit -0.5, got nil")
	}

	// Override limit with max-rows=1 configured
	oldMax := defaultMaxRows
	defaultMaxRows = 1
	defer func() { defaultMaxRows = oldMax }()

	argsOverride := map[string]any{
		"query": "MATCH (n) RETURN n.id",
		"limit": 999,
	}
	out, err := executeToolCall(db, db, "graph_query", argsOverride, false)
	if err != nil {
		t.Fatalf("graph_query override: %v", err)
	}
	var res map[string]any
	_ = json.Unmarshal([]byte(out), &res)
	if res["count"].(float64) != 1 {
		t.Fatalf("expected limit to be clamped to defaultMaxRows=1, got count=%v", res["count"])
	}
}

func TestExecuteToolCall_InvalidTypes_Rejected(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := initDatabase(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("initDatabase: %v", err)
	}
	defer db.Close()

	// 1. Rename relation with wrong from_kind type should fail, not broaden scope
	argsRename := map[string]any{
		"action":    "rename_relation",
		"relation":  "KNOWS",
		"from_kind": 123,
	}
	_, err = executeToolCall(db, db, "graph_schema_define", argsRename, false)
	if err == nil {
		t.Fatalf("expected error for from_kind: 123, got nil")
	}

	// 2. Set node with properties: null should fail, not wipe properties
	_, _ = handleSetNode(db, "p1", "Person", map[string]any{"name": "Alice"}, false, false)
	argsNullProps := map[string]any{
		"id":         "p1",
		"kind":       "Person",
		"properties": nil,
		"merge":      false,
	}
	_, err = executeToolCall(db, db, "graph_set_node", argsNullProps, false)
	if err == nil {
		t.Fatalf("expected error for properties: null, got nil")
	}

	// Verify properties were NOT wiped
	var props string
	_ = db.QueryRow("SELECT properties FROM nodes WHERE id = 'p1'").Scan(&props)
	if !strings.Contains(props, "Alice") {
		t.Fatalf("properties were wiped: %s", props)
	}
}
