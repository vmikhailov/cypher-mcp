#!/usr/bin/env python3
"""
Zero-Shortcut Multi-Hop Depth Scaling Benchmark:
Testing GraphRAG (cypher-mcp) vs Plain Vector RAG across increasing hop depths (1 to 4 hops)
with branching factor = 2, where NO single document contains multiple hops.

Evaluator: Google Gemini 3.8 Flash
Embedding: gemini-embedding-001
"""

import os
import sys
import time
import json
import math
import subprocess
import urllib.request

sys.path.insert(0, os.path.dirname(__file__))
from common import get_api_key, get_paths

REPO_ROOT, BIN_PATH, DATA_DIR, REPORTS_DIR = get_paths()
DB_PATH = os.path.join(DATA_DIR, "depth_scaling.db")

api_key = get_api_key()
if not api_key:
    print("Error: GOOGLE_API_KEY not found in environment or .env file.")
    sys.exit(1)

GEMINI_URL = f"https://generativelanguage.googleapis.com/v1beta/models/gemini-3.8-flash:generateContent?key={api_key}"
EMBED_URL = f"https://generativelanguage.googleapis.com/v1beta/models/gemini-embedding-001:embedContent?key={api_key}"

def get_embedding(text):
    payload = {"model": "models/gemini-embedding-001", "content": {"parts": [{"text": text}]}}
    data = json.dumps(payload).encode("utf-8")
    req = urllib.request.Request(EMBED_URL, data=data, headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req) as resp:
        res = json.loads(resp.read().decode("utf-8"))
        vals = res["embedding"]["values"]
        norm = math.sqrt(sum(x*x for x in vals))
        return [x / (norm or 1.0) for x in vals]

# 1. Generate Strict Zero-Shortcut Branching Dataset
# Depth 0: Root (Entry Service)
# Depth 1: 2 Services
# Depth 2: 4 Services
# Depth 3: 8 Services
# Depth 4: 16 Target Components (with exact secret licenses / attributes)

print("Generating strict zero-shortcut synthetic topology...")

# Tree structure:
# Node L0_0 -> L1_0, L1_1
# L1_0 -> L2_0, L2_1; L1_1 -> L2_2, L2_3
# L2_0 -> L3_0, L3_1 ...
# L3_x -> L4_y (Leaf components with unique compliance tags)

nodes = [{"id": "L0_root", "kind": "Service", "properties": {"name": "Nexus Core Gateway"}}]
edges = []
raw_docs = [
    {
        "id": "doc_L0",
        "title": "Service Nexus Core Gateway Specification",
        "text": "The Nexus Core Gateway (L0_root) delegates downstream routing strictly to two sub-gateways: Service Alpha-1 (L1_0) and Service Beta-1 (L1_1). It has no direct knowledge of any deeper systems."
    }
]

# Generate Level 1
for i in [0, 1]:
    nid = f"L1_{i}"
    name = f"Service Sub-Gateway {chr(65+i)}"
    nodes.append({"id": nid, "kind": "Service", "properties": {"name": name}})
    edges.append({"from": "L0_root", "to": nid, "kind": "CALLS"})
    
    # Each L1 calls two L2s
    c1, c2 = f"L2_{i*2}", f"L2_{i*2+1}"
    raw_docs.append({
        "id": f"doc_{nid}",
        "title": f"Specification of {name}",
        "text": f"{name} ({nid}) receives traffic from Nexus Gateway and exclusively calls two mid-tier processors: Processor {c1} and Processor {c2}. No other systems are called directly."
    })

# Generate Level 2
for i in range(4):
    nid = f"L2_{i}"
    name = f"Processor Tier-2 {nid}"
    nodes.append({"id": nid, "kind": "Service", "properties": {"name": name}})
    p_id = f"L1_{i // 2}"
    edges.append({"from": p_id, "to": nid, "kind": "CALLS"})
    
    c1, c2 = f"L3_{i*2}", f"L3_{i*2+1}"
    raw_docs.append({
        "id": f"doc_{nid}",
        "title": f"Processor {nid} Architecture",
        "text": f"Component {nid} handles business validation. It calls data storage handlers {c1} and {c2}."
    })

# Generate Level 3
for i in range(8):
    nid = f"L3_{i}"
    name = f"Storage Handler {nid}"
    nodes.append({"id": nid, "kind": "Service", "properties": {"name": name}})
    p_id = f"L2_{i // 2}"
    edges.append({"from": p_id, "to": nid, "kind": "CALLS"})
    
    c1 = f"L4_{i}"
    raw_docs.append({
        "id": f"doc_{nid}",
        "title": f"Storage Handler {nid} Spec",
        "text": f"Storage Handler {nid} persists state into isolated compliance vault {c1}."
    })

# Generate Level 4 (Target Vaults)
compliance_codes = ["ALPHA", "BETA", "GAMMA", "DELTA", "EPSILON", "ZETA", "ETA", "THETA"]
for i in range(8):
    nid = f"L4_{i}"
    tag = compliance_codes[i]
    nodes.append({"id": nid, "kind": "Vault", "properties": {"name": f"Vault {nid}", "compliance_tier": tag}})
    p_id = f"L3_{i}"
    edges.append({"from": p_id, "to": nid, "kind": "STORES_IN"})
    raw_docs.append({
        "id": f"doc_{nid}",
        "title": f"Compliance Vault {nid} Configuration",
        "text": f"Vault {nid} is certified under Compliance Tier {tag}. Managed by team Security-{tag}."
    })

print(f"Graph generated: {len(nodes)} nodes, {len(edges)} edges, {len(raw_docs)} isolated docs.")

# Index Documents for Vector RAG
print("Computing embeddings for Plain Vector RAG...")
doc_vectors = []
for doc in raw_docs:
    v = get_embedding(doc["title"] + "\n" + doc["text"])
    doc_vectors.append((doc, v))
print(f"Indexed {len(doc_vectors)} text chunks.")

# Index Graph in cypher-mcp
if os.path.exists(DB_PATH):
    try:
        os.remove(DB_PATH)
    except Exception:
        pass

mcp_proc = subprocess.Popen(
    [BIN_PATH, "--db", DB_PATH],
    stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True
)

req_id_counter = 0
def rpc_mcp(method, params):
    global req_id_counter
    req_id_counter += 1
    req = {"jsonrpc": "2.0", "id": req_id_counter, "method": method, "params": params}
    mcp_proc.stdin.write(json.dumps(req) + "\n")
    mcp_proc.stdin.flush()
    line = mcp_proc.stdout.readline()
    return json.loads(line)

rpc_mcp("initialize", {"protocolVersion": "2024-11-05", "clientInfo": {"name": "eval"}})
rpc_mcp("tools/call", {"name": "graph_batch_upsert", "arguments": {"nodes": nodes, "edges": edges}})

# Add alias for root
rpc_mcp("tools/call", {"name": "graph_upsert_alias", "arguments": {"node_id": "L0_root", "alias": "Nexus Core Gateway"}})

# 2. Agent Execution Functions
def call_gemini(messages, tools_decl):
    payload = {
        "contents": messages,
        "tools": [{"functionDeclarations": tools_decl}] if tools_decl else [],
        "generationConfig": {"temperature": 0.0}
    }
    data = json.dumps(payload).encode("utf-8")
    req = urllib.request.Request(GEMINI_URL, data=data, headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req) as resp:
        return json.loads(resp.read().decode("utf-8"))

def exec_vector_search(query, top_k=4):
    q_vec = get_embedding(query)
    scores = [(sum(a * b for a, b in zip(q_vec, d_vec)), doc) for doc, d_vec in doc_vectors]
    scores.sort(reverse=True, key=lambda x: x[0])
    return json.dumps([{"title": d["title"], "text": d["text"]} for _, d in scores[:top_k]], ensure_ascii=False)

def exec_graph_query(query):
    res = rpc_mcp("tools/call", {"name": "graph_query", "arguments": {"query": query}})
    return res["result"]["content"][0]["text"]

def exec_graph_resolve(query):
    res = rpc_mcp("tools/call", {"name": "graph_resolve_entity", "arguments": {"query": query}})
    return res["result"]["content"][0]["text"]

PLAIN_TOOLS = [{
    "name": "vector_search",
    "description": "Semantic search across architecture and component documentation.",
    "parameters": {"type": "OBJECT", "properties": {"query": {"type": "STRING"}}, "required": ["query"]}
}]

GRAPH_TOOLS = [
    {
        "name": "graph_resolve_entity",
        "description": "Resolve component name to node ID.",
        "parameters": {"type": "OBJECT", "properties": {"query": {"type": "STRING"}}, "required": ["query"]}
    },
    {
        "name": "graph_query",
        "description": "Execute OpenCypher queries against topology graph. Nodes: Service, Vault. Edges: CALLS, STORES_IN.",
        "parameters": {"type": "OBJECT", "properties": {"query": {"type": "STRING"}}, "required": ["query"]}
    }
]

def run_test(paradigm, prompt, tools_decl, dispatcher, max_turns=8):
    messages = [{"role": "user", "parts": [{"text": f"Question: {prompt}"}]}]
    t0 = time.time()
    turns = 0
    tokens = 0
    
    while turns < max_turns:
        turns += 1
        resp = call_gemini(messages, tools_decl)
        tokens += resp.get("usageMetadata", {}).get("totalTokenCount", 0)
        cand = resp["candidates"][0]["content"]
        parts = cand.get("parts", [])
        fcs = [p["functionCall"] for p in parts if "functionCall" in p]
        
        if not fcs:
            return {"answer": "".join(p.get("text", "") for p in parts), "turns": turns, "latency": round(time.time()-t0, 2), "tokens": tokens}
            
        messages.append(cand)
        resp_parts = []
        for fc in fcs:
            fn = fc["name"]
            args = fc.get("args", {})
            out = dispatcher(fn, args)
            resp_parts.append({"functionResponse": {"name": fn, "response": {"output": out}}})
        messages.append({"role": "user", "parts": resp_parts})
        time.sleep(0.3)
        
    return {"answer": "TIMEOUT", "turns": turns, "latency": round(time.time()-t0, 2), "tokens": tokens}

# Tasks scaling from 1 to 4 hops
TESTS = [
    {
        "name": "1-Hop Dependency",
        "prompt": "Which direct downstream sub-gateways does 'Nexus Core Gateway' call?",
        "expected": ["L1_0", "L1_1"]
    },
    {
        "name": "2-Hop Branching",
        "prompt": "List ALL 4 mid-tier processors (L2) reachable downstream from 'Nexus Core Gateway'.",
        "expected": ["L2_0", "L2_1", "L2_2", "L2_3"]
    },
    {
        "name": "3-Hop Branching",
        "prompt": "List ALL 8 storage handlers (L3) reachable downstream from 'Nexus Core Gateway'.",
        "expected": ["L3_0", "L3_1", "L3_2", "L3_3", "L3_4", "L3_5", "L3_6", "L3_7"]
    },
    {
        "name": "4-Hop Transitive Attribute Extraction",
        "prompt": "List ALL distinct compliance tiers of the vaults (L4) where data from 'Nexus Core Gateway' is transitively stored.",
        "expected": compliance_codes
    }
]

print("\n=== Running Depth Scaling Benchmark (1 to 4 Hops) ===\n")
results = []

for t in TESTS:
    print(f"--------------------------------------------------")
    print(f"TEST: {t['name']}")
    print(f"Prompt: {t['prompt']}")
    print(f"--------------------------------------------------")
    
    # 1. Plain RAG
    print("  -> Plain Vector RAG...")
    p_res = run_test("Plain RAG", t["prompt"], PLAIN_TOOLS, lambda fn, a: exec_vector_search(a.get("query", "")))
    p_matched = [e for e in t["expected"] if e.lower() in p_res["answer"].lower()]
    p_rec = round(len(p_matched) / len(t["expected"]) * 100, 1)
    print(f"     Recall: {p_rec}% ({len(p_matched)}/{len(t['expected'])}) | Turns: {p_res['turns']} | Time: {p_res['latency']}s | Tokens: {p_res['tokens']}")
    
    # 2. GraphRAG
    print("  -> GraphRAG (cypher-mcp)...")
    def g_dispatch(fn, a):
        if fn == "graph_resolve_entity": return exec_graph_resolve(a.get("query", ""))
        return exec_graph_query(a.get("query", ""))
    g_res = run_test("GraphRAG", t["prompt"], GRAPH_TOOLS, g_dispatch)
    g_matched = [e for e in t["expected"] if e.lower() in g_res["answer"].lower()]
    g_rec = round(len(g_matched) / len(t["expected"]) * 100, 1)
    print(f"     Recall: {g_rec}% ({len(g_matched)}/{len(t['expected'])}) | Turns: {g_res['turns']} | Time: {g_res['latency']}s | Tokens: {g_res['tokens']}\n")
    
    results.append({
        "name": t["name"],
        "plain": {**p_res, "recall": p_rec},
        "graph": {**g_res, "recall": g_rec}
    })

mcp_proc.kill()

# Save Report
REPORT_PATH = os.path.join(REPORTS_DIR, "DEPTH_SCALING_BENCHMARK.md")
os.makedirs(os.path.dirname(REPORT_PATH), exist_ok=True)
with open(REPORT_PATH, "w", encoding="utf-8") as f:
    f.write("# Zero-Shortcut Depth Scaling Benchmark (1 to 4 Hops)\n\n")
    f.write("Evaluation comparing **GraphRAG (`cypher-mcp`)** vs. **Plain Vector RAG** across branching tree depths (1 to 4 hops) where intermediate documents contain zero shortcut links.\n\n")
    f.write("- **Evaluator:** Google Gemini 3.8 Flash\n")
    f.write("- **Embedding:** `gemini-embedding-001` (768 dimensions)\n\n")
    f.write("---\n\n")
    f.write("## 1. Final Scorecard\n\n")
    f.write("| Depth / Hops | Plain Vector RAG Recall | GraphRAG Recall | Latency Ratio | Winner |\n")
    f.write("| :--- | :--- | :--- | :--- | :--- |\n")
    for r in results:
        lat_ratio = f"{r['plain']['latency'] / (r['graph']['latency'] or 1.0):.1f}x"
        winner = "**GraphRAG**" if r['graph']['recall'] > r['plain']['recall'] else ("**TIE**" if r['graph']['recall'] == r['plain']['recall'] else "**Plain RAG**")
        f.write(f"| **{r['name']}** | {r['plain']['recall']:.1f}% ({r['plain']['turns']} turns, {r['plain']['tokens']} tokens) | **{r['graph']['recall']:.1f}%** ({r['graph']['turns']} turns, {r['graph']['tokens']} tokens) | {lat_ratio} | {winner} |\n")
    f.write("\n---\n")

print(f"\nReport saved to: {REPORT_PATH}")

print("================ FINAL DEPTH SCALING SCORECARD ================")
print(f"{'Task Depth':<25} | {'Plain RAG Recall':<18} | {'GraphRAG Recall':<18} | {'Latency Ratio':<12}")
print("-" * 80)
for r in results:
    lat_ratio = f"{r['plain']['latency'] / (r['graph']['latency'] or 1.0):.1f}x"
    print(f"{r['name']:<25} | {r['plain']['recall']:<5}% ({r['plain']['turns']} turns)     | {r['graph']['recall']:<5}% ({r['graph']['turns']} turns)     | {lat_ratio}")
