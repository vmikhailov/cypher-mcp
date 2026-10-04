package main

import (
	"bytes"
	"encoding/json"
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
