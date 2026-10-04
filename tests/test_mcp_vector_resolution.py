#!/usr/bin/env python3
"""
End-to-End Test for cypher-mcp Vector Entity Resolution Tools:
- graph_resolve_entity
- graph_upsert_alias
Communicates with cypher-mcp.exe over stdio JSON-RPC.
"""

import os
import sys
import json
import subprocess
import time

sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "benchmarks")))
from common import get_paths

REPO_ROOT, BINARY_PATH, DATA_DIR, REPORTS_DIR = get_paths()
TEST_DB = os.path.join(DATA_DIR, "mcp_vector_test.db")

if os.path.exists(TEST_DB):
    try:
        os.remove(TEST_DB)
    except Exception:
        pass

print("=== Starting cypher-mcp Vector Entity Resolution E2E Test ===")

proc = subprocess.Popen(
    [BINARY_PATH, "--db", TEST_DB],
    stdin=subprocess.PIPE,
    stdout=subprocess.PIPE,
    stderr=subprocess.PIPE,
    text=True
)

req_counter = 0

def call_rpc(method, params):
    global req_counter
    req_counter += 1
    req = {
        "jsonrpc": "2.0",
        "id": req_counter,
        "method": method,
        "params": params
    }
    proc.stdin.write(json.dumps(req) + "\n")
    proc.stdin.flush()
    line = proc.stdout.readline()
    if not line:
        err = proc.stderr.read()
        raise RuntimeError(f"Server closed connection. Stderr: {err}")
    return json.loads(line)

# 1. Initialize
init_resp = call_rpc("initialize", {
    "protocolVersion": "2024-11-05",
    "clientInfo": {"name": "vector-e2e-tester"}
})
print("[1] Initialized successfully:", init_resp.get("result", {}).get("serverInfo", {}).get("name"))

# 2. Check tools/list for new tools
tools_resp = call_rpc("tools/list", {})
tools = tools_resp["result"]["tools"]
tool_names = [t["name"] for t in tools]
print("[2] Tools available:", len(tools))
assert "graph_resolve_entity" in tool_names, "graph_resolve_entity not in tools/list"
assert "graph_upsert_alias" in tool_names, "graph_upsert_alias not in tools/list"
print("    -> Found 'graph_resolve_entity' and 'graph_upsert_alias' in tools/list!")

# 3. Create nodes in graph
nodes = [
    {
        "id": "person:alexander",
        "kind": "Person",
        "properties": {"name": "Alexander Ivanov", "role": "Architect", "city": "Munich"}
    },
    {
        "id": "person:elena",
        "kind": "Person",
        "properties": {"name": "Elena Ivanova", "role": "Engineer"}
    },
    {
        "id": "vehicle:car-01",
        "kind": "Vehicle",
        "properties": {"plate": "M-AB 123", "model": "BMW 3 Series"}
    }
]

edges = [
    {
        "from": "person:alexander",
        "to": "vehicle:car-01",
        "kind": "OWNS",
        "properties": {"primary": True}
    }
]

upsert_resp = call_rpc("tools/call", {
    "name": "graph_batch_upsert",
    "arguments": {"nodes": nodes, "edges": edges}
})
print("[3] Nodes & edges created:", upsert_resp["result"]["content"][0]["text"])

# 4. Upsert aliases with auto-embeddings
print("[4] Upserting aliases into vector index...")
for node_id, alias in [
    ("person:alexander", "Саша"),
    ("person:alexander", "Александр"),
    ("person:alexander", "Alex"),
    ("person:elena", "Лена"),
    ("vehicle:car-01", "БМВ тройка")
]:
    t0 = time.time()
    res = call_rpc("tools/call", {
        "name": "graph_upsert_alias",
        "arguments": {"node_id": node_id, "alias": alias}
    })
    dt = (time.time() - t0) * 1000
    print(f"    - Alias '{alias}' -> {node_id} (embed & index took {dt:.1f}ms)")

# 5. Test Entity Resolution with colloquial inputs
test_cases = [
    ("со Сашей", "person:alexander"),
    ("Санька", "person:alexander"),
    ("Александру", "person:alexander"),
    ("Леночка", "person:elena"),
    ("Бэха", "vehicle:car-01")
]

print("\n[5] Testing graph_resolve_entity:")
all_passed = True
for query, expected_node in test_cases:
    t0 = time.time()
    res = call_rpc("tools/call", {
        "name": "graph_resolve_entity",
        "arguments": {"query": query, "limit": 2, "min_score": 0.50}
    })
    dt = (time.time() - t0) * 1000
    text = res["result"]["content"][0]["text"]
    data = json.loads(text)
    
    if data["count"] > 0:
        top = data["results"][0]
        matched_node = top["node_id"]
        score = top["score"]
        alias = top["alias"]
        status = "PASS" if matched_node == expected_node else "FAIL"
        if status == "FAIL":
            all_passed = False
        print(f"    [{status}] Query: '{query}' -> [{matched_node}] (alias: '{alias}', score: {score:.4f}, latency: {dt:.1f}ms)")
    else:
        print(f"    [FAIL] Query: '{query}' -> No candidates found (latency: {dt:.1f}ms)")
        all_passed = False

# 6. Execute Cypher using resolved entity
print("\n[6] Combining Resolution + Cypher Query:")
res = call_rpc("tools/call", {
    "name": "graph_resolve_entity",
    "arguments": {"query": "со Сашей", "limit": 1}
})
resolved_node = json.loads(res["result"]["content"][0]["text"])["results"][0]["node_id"]

cypher_q = f"MATCH (p:Person {{id: '{resolved_node}'}})-[:OWNS]->(v:Vehicle) RETURN p.name AS owner, v.model AS car, v.plate AS plate"
cypher_res = call_rpc("tools/call", {
    "name": "graph_query",
    "arguments": {"query": cypher_q}
})
print("    Cypher query:", cypher_q)
print("    Result:", json.loads(cypher_res["result"]["content"][0]["text"])["results"])

proc.kill()

if all_passed:
    print("\n>>> ALL TESTS PASSED! Vector Entity Resolution fully verified on cypher-mcp! <<<")
else:
    print("\n>>> Some tests failed! Check output above. <<<")
