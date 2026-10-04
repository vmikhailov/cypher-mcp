package main

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/vmikhailov/cypher-mcp/internal/storage"
	"github.com/vmikhailov/cypher-mcp/internal/transport"
	"github.com/vmikhailov/cypher-mcp/internal/vector"
)

// FuzzJSONRPCMessageFraming fuzzes JSON-RPC message parsing and ID validation
func FuzzJSONRPCMessageFraming(f *testing.F) {
	seeds := [][]byte{
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`),
		[]byte(`{"jsonrpc":"2.0","id":"abc-123","method":"tools/list"}`),
		[]byte(`{"jsonrpc":"2.0","id":null,"method":"notifications/test"}`),
		[]byte(`{"jsonrpc":"2.0","id":true,"method":"test"}`),
		[]byte(`[{"jsonrpc":"2.0"}]`),
		[]byte(`{"jsonrpc":"1.0","id":1}`),
		[]byte(``),
		[]byte(`{malformed`),
		[]byte(`{"jsonrpc":"2.0","id":9007199254740993,"method":"test"}`),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		var req transport.JSONRPCRequest
		if err := json.Unmarshal(data, &req); err != nil {
			return
		}
		// Validate ID - must never panic
		_ = transport.ValidateJSONRPCID(req.ID)
	})
}

// FuzzVectorBlobDecoding fuzzes vector binary decoding
func FuzzVectorBlobDecoding(f *testing.F) {
	seeds := [][]byte{
		{},
		{0, 0, 128, 63}, // 1.0f in little-endian
		{0, 0, 0, 0, 0, 0, 128, 63},
		{1, 2, 3},        // Invalid length (not multiple of 4)
		{0, 0, 192, 127}, // NaN
		{0, 0, 128, 127}, // +Inf
		{0, 0, 128, 255}, // -Inf
		{255, 255, 255, 255},
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, blob []byte) {
		vec, err := vector.DecodeEmbedding(blob)
		if err != nil {
			return
		}
		// If successfully decoded, encoding must succeed or return an error, never panic
		_, _ = vector.EncodeEmbedding(vec)
	})
}

func FuzzMutationArguments(f *testing.F) {
	dbPath := filepath.Join(f.TempDir(), "fuzz.db")
	db, err := storage.InitDatabase(dbPath)
	if err != nil {
		f.Fatalf("InitDatabase: %v", err)
	}
	defer db.Close()
	dbRO, err := storage.InitRODatabase(dbPath)
	if err != nil {
		f.Fatalf("InitRODatabase: %v", err)
	}
	defer dbRO.Close()

	f.Add("graph_set_node", `{"id":"n1","kind":"Person","properties":{"k":"v"}}`)
	f.Add("graph_set_edge", `{"from":"n1","to":"n2","kind":"KNOWS"}`)
	f.Add("graph_query", `{"query":"MATCH (n) RETURN n","limit":10}`)
	f.Add("graph_batch_upsert", `{"nodes":[],"edges":[]}`)
	f.Add("graph_search", `{"query":"test","limit":5}`)
	f.Add("graph_schema", `{}`)
	f.Add("graph_schema_define", `{"action":"allow_kind","kind":"Person"}`)
	f.Add("unknown_tool", `{}`)

	f.Fuzz(func(t *testing.T, toolName string, argsJSON string) {
		var args map[string]any
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return
		}
		if args == nil {
			args = make(map[string]any)
		}
		// Execute with nil DB handles - must return error, never panic
		_, _ = transport.ExecuteToolCall(nil, nil, toolName, args, false)

		// Execute with active DB handles - must never panic regardless of tool or args
		_, _ = transport.ExecuteToolCall(db, dbRO, toolName, args, false)
	})
}
