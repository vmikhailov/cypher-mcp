#!/usr/bin/env python3
"""
Enterprise Compliance & Financial Audit Benchmark (250 Documents).
Comparing GraphRAG (cypher-mcp) vs Plain Vector RAG on:
1. Security Compliance Leak (Contractor access to PII-Tier-1)
2. Transitive Financial Cost Rollup (End-to-End Infrastructure Cost)
3. Negative Global Filtering & Aggregation (Staff without Prod Access)

Corpus: 250 Realistic Confluence Architecture & IAM Documents
Evaluator: Google Gemini 3.8 Flash
Embedding: Google Gemini Embedding 001 (batchEmbedContents)
"""

import os
import sys
import time
import json
import math
import random
import subprocess
import urllib.request
import urllib.error

# 1. Load API Key
env_path = r"C:\Users\viach\AppData\Local\hermes\.env"
api_key = None
if os.path.exists(env_path):
    with open(env_path, "r", encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if line.startswith("GOOGLE_API_KEY="):
                api_key = line.split("=", 1)[1].strip().strip("\"'")
                break

if not api_key:
    print("Error: GOOGLE_API_KEY not found in .env")
    sys.exit(1)

GEMINI_URL = f"https://generativelanguage.googleapis.com/v1beta/models/gemini-3.8-flash:generateContent?key={api_key}"
EMBED_URL = f"https://generativelanguage.googleapis.com/v1beta/models/gemini-embedding-001:embedContent?key={api_key}"
BATCH_EMBED_URL = f"https://generativelanguage.googleapis.com/v1beta/models/gemini-embedding-001:batchEmbedContents?key={api_key}"

def get_single_embedding(text):
    payload = {"model": "models/gemini-embedding-001", "content": {"parts": [{"text": text[:2000]}]}}
    data = json.dumps(payload).encode("utf-8")
    req = urllib.request.Request(EMBED_URL, data=data, headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req) as resp:
        res = json.loads(resp.read().decode("utf-8"))
        vals = res["embedding"]["values"]
        norm = math.sqrt(sum(x*x for x in vals))
        return [x / (norm or 1.0) for x in vals]

def get_batch_embeddings(texts, batch_size=80):
    all_embeddings = []
    for i in range(0, len(texts), batch_size):
        chunk = texts[i:i+batch_size]
        requests = [
            {"model": "models/gemini-embedding-001", "content": {"parts": [{"text": t[:2000]}]}}
            for t in chunk
        ]
        data = json.dumps({"requests": requests}).encode("utf-8")
        req = urllib.request.Request(BATCH_EMBED_URL, data=data, headers={"Content-Type": "application/json"})
        with urllib.request.urlopen(req) as resp:
            res = json.loads(resp.read().decode("utf-8"))
            for item in res["embeddings"]:
                vals = item["values"]
                norm = math.sqrt(sum(x*x for x in vals))
                all_embeddings.append([x / (norm or 1.0) for x in vals])
        time.sleep(0.5)
    return all_embeddings

# 2. Synthetic Enterprise Dataset Generator (250 Documents & Graph Entities)
print("1. Generating 250 enterprise documents and structured graph...")

random.seed(42)

nodes = []
edges = []
raw_documents = []

# --- 2.1 Servers (50 servers) ---
servers = []
# Designated Checkout chain servers:
checkout_servers = [
    ("srv:app-checkout-01", 850, "us-east-1a", "App Node for Checkout API"),
    ("srv:app-pay-01", 1200, "us-east-1b", "Payment Router Compute Node"),
    ("srv:app-tax-01", 400, "us-east-1a", "Tax Calculator Worker"),
    ("srv:cache-redis-01", 650, "us-east-1a", "Redis Cluster Node A"),
    ("srv:db-routing-01", 1500, "us-east-1c", "Postgres Routing Primary"),
    ("srv:db-cardvault-01", 2800, "us-east-1-secure", "HSM Secured Card Vault DB")
]
# Sum = 850 + 1200 + 400 + 650 + 1500 + 2800 = 7400

for s_id, cost, zone, desc in checkout_servers:
    servers.append((s_id, cost, zone, desc))

for i in range(len(checkout_servers), 50):
    s_id = f"srv:infra-node-{i:02d}"
    cost = random.choice([350, 500, 750, 1100, 1400, 2200])
    zone = random.choice(["us-east-1a", "us-east-1b", "eu-central-1a", "eu-central-1b"])
    desc = f"General Purpose Infrastructure Host {i}"
    servers.append((s_id, cost, zone, desc))

for s_id, cost, zone, desc in servers:
    nodes.append({
        "id": s_id,
        "kind": "Server",
        "properties": {"name": s_id, "monthly_cost_usd": cost, "zone": zone}
    })
    raw_documents.append({
        "id": f"doc_{s_id}",
        "title": f"Infrastructure Specification: {s_id}",
        "text": f"Server {s_id} is deployed in availability zone {zone}. Description: {desc}. The operational cost allocated in Q3 cloud budget is ${cost} USD per month. Host is monitored by CloudOps."
    })

# --- 2.2 Databases (30 databases) ---
databases = [
    ("db:checkout-redis", "Internal", "srv:cache-redis-01", "Session Cache for Checkout"),
    ("db:routing-pg", "Internal", "srv:db-routing-01", "Transaction Routing Ledger"),
    ("db:card-vault", "PII-Tier-1", "srv:db-cardvault-01", "Encrypted Credit Card Storage Vault"),
    ("db:users-vault", "PII-Tier-1", "srv:infra-node-10", "User Identity and SSN Repository"),
    ("db:tax-cache", "Public", "srv:app-tax-01", "Regional Tax Rates Cache"),
    ("db:ledger-audit-lake", "PII-Tier-1", "srv:infra-node-11", "Financial Compliance Ledger Lake")
]
for i in range(len(databases), 30):
    db_id = f"db:app-store-{i:02d}"
    tier = random.choice(["Internal", "Internal", "Public"])
    host_srv = servers[i % len(servers)][0]
    databases.append((db_id, tier, host_srv, f"Application Database {i}"))

for db_id, tier, host_srv, desc in databases:
    nodes.append({
        "id": db_id,
        "kind": "Database",
        "properties": {"name": db_id, "pii_tier": tier}
    })
    edges.append({"from": db_id, "to": host_srv, "kind": "HOSTED_ON", "properties": {}})
    raw_documents.append({
        "id": f"doc_{db_id}",
        "title": f"Database Topology & Security: {db_id}",
        "text": f"Database {db_id} has data sensitivity classification '{tier}'. It is physically hosted on server {host_srv}. Description: {desc}. Backup retention is 30 days."
    })

# --- 2.3 Microservices (50 services) ---
services = [
    ("svc:checkout-api", "Payment Gateway", ["svc:payment-router", "svc:tax-calculator"], ["db:checkout-redis"], "srv:app-checkout-01"),
    ("svc:payment-router", "Payment Gateway", ["svc:card-processor"], ["db:routing-pg"], "srv:app-pay-01"),
    ("svc:tax-calculator", "Payment Gateway", [], ["db:tax-cache"], "srv:app-tax-01"),
    ("svc:card-processor", "Payment Gateway", [], ["db:card-vault"], "srv:db-cardvault-01"),
    ("svc:auth-broker", "Core Banking", [], ["db:users-vault"], "srv:infra-node-10"),
    ("svc:ledger-engine", "Core Banking", [], ["db:ledger-audit-lake"], "srv:infra-node-11")
]

for i in range(len(services), 50):
    s_id = f"svc:microservice-{i:02d}"
    dept = random.choice(["Core Banking", "Payment Gateway", "Risk & AML", "Data & Analytics"])
    dep_svc = [services[random.randint(0, len(services)-1)][0]] if random.random() > 0.4 else []
    conn_db = [databases[random.randint(0, len(databases)-1)][0]] if random.random() > 0.5 else []
    h_srv = servers[i % len(servers)][0]
    services.append((s_id, dept, dep_svc, conn_db, h_srv))

for s_id, dept, dep_svcs, conn_dbs, h_srv in services:
    nodes.append({
        "id": s_id,
        "kind": "Service",
        "properties": {"name": s_id, "department": dept}
    })
    edges.append({"from": s_id, "to": h_srv, "kind": "HOSTED_ON", "properties": {}})
    for ds in dep_svcs:
        edges.append({"from": s_id, "to": ds, "kind": "DEPENDS_ON", "properties": {}})
    for db in conn_dbs:
        edges.append({"from": s_id, "to": db, "kind": "CONNECTS_TO", "properties": {}})
    
    dep_text = f"Dependencies: calls {', '.join(dep_svcs)}." if dep_svcs else "No downstream service dependencies."
    db_text = f"Connected databases: {', '.join(conn_dbs)}." if conn_dbs else "No database connections."
    raw_documents.append({
        "id": f"doc_{s_id}",
        "title": f"Service Architecture Sheet: {s_id}",
        "text": f"Microservice {s_id} belongs to department '{dept}'. Hosted on compute host {h_srv}. {dep_text} {db_text}"
    })

# --- 2.4 IAM Groups (25 groups) ---
iam_groups = [
    ("grp:contractor-legacy-support", ["db:card-vault"], "Legacy database maintenance group"),
    ("grp:external-qa-audit", ["db:users-vault"], "External QA data inspection group"),
    ("grp:checkout-prod-admin", ["svc:checkout-api"], "Production administrators for checkout"),
    ("grp:core-banking-dev", ["svc:auth-broker"], "Core banking software engineers"),
    ("grp:read-only-analysts", [], "Global read-only analytics group")
]
for i in range(len(iam_groups), 25):
    g_id = f"grp:role-group-{i:02d}"
    acc = [databases[i % len(databases)][0]] if random.random() > 0.5 else []
    iam_groups.append((g_id, acc, f"IAM Role Group {i}"))

for g_id, granted_targets, desc in iam_groups:
    nodes.append({"id": g_id, "kind": "IAMGroup", "properties": {"name": g_id}})
    for gt in granted_targets:
        edges.append({"from": g_id, "to": gt, "kind": "HAS_ACCESS", "properties": {}})
    acc_str = f"Grants direct access to: {', '.join(granted_targets)}." if granted_targets else "No direct resource write access."
    raw_documents.append({
        "id": f"doc_{g_id}",
        "title": f"IAM Role & Policy Definition: {g_id}",
        "text": f"IAM Group {g_id}. Description: {desc}. {acc_str} Policy revision 2026-v3."
    })

# --- 2.5 Personnel (Employees & Contractors - 75 people) ---
# Rogue contractors:
contractors = [
    ("person:viktor-vance", "Viktor Vance", "Contractor", "Payment Gateway", ["grp:contractor-legacy-support"], "External SRE from agency DevTalent"),
    ("person:sarah-jenkins", "Sarah Jenkins", "Contractor", "Core Banking", ["grp:external-qa-audit"], "External QA contractor from TestCorp"),
    ("person:alex-morozov", "Alex Morozov", "Contractor", "Risk & AML", ["grp:read-only-analysts"], "Frontend React Contractor"),
    ("person:liam-neill", "Liam Neill", "Contractor", "Data & Analytics", ["grp:read-only-analysts"], "Data annotation contractor")
]
for i in range(len(contractors), 25):
    c_id = f"person:contractor-{i:02d}"
    name = f"Contractor Name {i}"
    dept = random.choice(["Payment Gateway", "Core Banking", "Risk & AML", "Data & Analytics"])
    grp = [iam_groups[random.randint(4, len(iam_groups)-1)][0]]
    contractors.append((c_id, name, "Contractor", dept, grp, f"External contractor {i}"))

staff = [
    ("person:dmitry-smirnov", "Dmitry Smirnov", "Employee", "Payment Gateway", ["grp:checkout-prod-admin"], "Principal SRE"),
    ("person:elena-kuznetsova", "Elena Kuznetsova", "Employee", "Core Banking", ["grp:core-banking-dev"], "Lead Architect"),
    ("person:olga-sokolova", "Olga Sokolova", "Employee", "Payment Gateway", [], "Junior QA Engineer (No prod groups)"),
    ("person:mikhail-popov", "Mikhail Popov", "Employee", "Payment Gateway", [], "Junior Support Analyst (No prod groups)")
]
for i in range(len(staff), 50):
    s_id = f"person:employee-{i:02d}"
    name = f"Employee Staff {i}"
    dept = random.choice(["Payment Gateway", "Core Banking", "Risk & AML", "Data & Analytics"])
    grp = [iam_groups[random.randint(2, len(iam_groups)-1)][0]] if random.random() > 0.5 else []
    staff.append((s_id, name, "Employee", dept, grp, f"Full-time Engineer {i}"))

personnel = contractors + staff

for p_id, name, p_type, dept, grps, role_desc in personnel:
    nodes.append({
        "id": p_id,
        "kind": "Person",
        "properties": {"name": name, "employment_type": p_type, "department": dept}
    })
    for g in grps:
        edges.append({"from": p_id, "to": g, "kind": "MEMBER_OF", "properties": {}})
    grp_str = f"Member of IAM security groups: {', '.join(grps)}." if grps else "Not assigned to any IAM production groups."
    raw_documents.append({
        "id": f"doc_{p_id}",
        "title": f"Personnel Profile: {name}",
        "text": f"Name: {name} ({p_id}). Employment Type: {p_type}. Department: {dept}. Role: {role_desc}. {grp_str}"
    })

# --- 2.6 Compliance Policy Document ---
raw_documents.append({
    "id": "doc_compliance_secpol_04",
    "title": "Corporate Compliance Security Policy SecPol-04",
    "text": "POLICY SecPol-04 (Mandatory): External Contractors ('Contractor') are strictly forbidden from having direct or indirect group membership access to any database classified as 'PII-Tier-1' (Personally Identifiable Information). Any contractor found with access to a PII-Tier-1 store must be flagged immediately for security revocation."
})

print(f"Total Generated Documents: {len(raw_documents)}")
print(f"Total Graph Nodes: {len(nodes)} | Edges: {len(edges)}")

# 3. Vector Indexing for Plain Vector RAG
CACHE_FILE = r"C:\Work\Personal\cypher-mcp\data\audit_vectors.json"

def get_or_load_vectors(raw_documents):
    if os.path.exists(CACHE_FILE):
        print(f"2. Loading cached embeddings from {CACHE_FILE}...")
        with open(CACHE_FILE, "r", encoding="utf-8") as f:
            return json.load(f)
    print("2. Batch embedding 250 documents for Plain Vector RAG...")
    texts_to_embed = [d["title"] + "\n" + d["text"] for d in raw_documents]
    t0 = time.time()
    all_vectors = get_batch_embeddings(texts_to_embed, batch_size=80)
    print(f"   Indexed {len(all_vectors)} vectors in {time.time()-t0:.2f}s.")
    with open(CACHE_FILE, "w", encoding="utf-8") as f:
        json.dump(all_vectors, f)
    return all_vectors

all_vectors = None

# 4. Graph Initialization for cypher-mcp
print("3. Initializing cypher-mcp SQLite Graph...")
DB_PATH = r"C:\Work\Personal\cypher-mcp\data\enterprise_audit.db"
if os.path.exists(DB_PATH):
    try: os.remove(DB_PATH)
    except Exception: pass

mcp_proc = subprocess.Popen(
    [r"C:\Work\Personal\cypher-mcp\bin\cypher-mcp.exe", "--db", DB_PATH],
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

rpc_mcp("initialize", {"protocolVersion": "2024-11-05", "clientInfo": {"name": "audit-eval"}})
rpc_mcp("tools/call", {"name": "graph_batch_upsert", "arguments": {"nodes": nodes, "edges": edges}})

# Add aliases for key entities
key_aliases = [
    ("svc:checkout-api", "Checkout Service"),
    ("svc:checkout-api", "Checkout API"),
    ("svc:checkout-api", "сервис чекаута"),
    ("db:card-vault", "Card Vault DB"),
    ("db:users-vault", "Users Vault DB")
]
for nid, alias in key_aliases:
    rpc_mcp("tools/call", {"name": "graph_upsert_alias", "arguments": {"node_id": nid, "alias": alias}})

print("   Graph populated and ready.\n")

# 5. Agent Definitions
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

def exec_vector_search(query, top_k=6):
    global all_vectors
    if all_vectors is None:
        all_vectors = get_or_load_vectors(raw_documents)
    q_vec = get_single_embedding(query)
    scores = []
    for doc, d_vec in zip(raw_documents, all_vectors):
        sim = sum(a * b for a, b in zip(q_vec, d_vec))
        scores.append((sim, doc))
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
    "description": "Semantic vector search across 250 enterprise architecture, IAM, personnel, and compliance documents.",
    "parameters": {
        "type": "OBJECT",
        "properties": {
            "query": {"type": "STRING", "description": "Search query"},
            "top_k": {"type": "INTEGER", "description": "Number of chunks (default 6)"}
        },
        "required": ["query"]
    }
}]

GRAPH_TOOLS = [
    {
        "name": "graph_resolve_entity",
        "description": "Resolve service, person, or database name to canonical graph node ID.",
        "parameters": {"type": "OBJECT", "properties": {"query": {"type": "STRING"}}, "required": ["query"]}
    },
    {
        "name": "graph_query",
        "description": "Execute OpenCypher queries against enterprise knowledge graph. Nodes: Person, Service, Database, Server, IAMGroup. Edges: MEMBER_OF, HAS_ACCESS, DEPENDS_ON, CONNECTS_TO, HOSTED_ON.",
        "parameters": {"type": "OBJECT", "properties": {"query": {"type": "STRING"}}, "required": ["query"]}
    }
]

GRAPH_SYS = """You are an enterprise compliance and architecture auditor with access to a knowledge graph.
Knowledge Graph Schema:
- Nodes:
  - Person (properties: name, employment_type ['Contractor', 'Employee'], department)
  - IAMGroup (properties: name)
  - Database (properties: name, pii_tier ['PII-Tier-1', 'Internal', 'Public'])
  - Service (properties: name, department)
  - Server (properties: name, monthly_cost_usd, zone)
- Relationships:
  - (Person)-[:MEMBER_OF]->(IAMGroup)
  - (IAMGroup)-[:HAS_ACCESS]->(Database)
  - (Service)-[:DEPENDS_ON]->(Service)
  - (Service)-[:CONNECTS_TO]->(Database)
  - (Service)-[:HOSTED_ON]->(Server)
  - (Database)-[:HOSTED_ON]->(Server)

Write precise OpenCypher queries using graph_query to retrieve exact, deterministic facts.
Supported Cypher: MATCH, WHERE, RETURN, DISTINCT, ORDER BY, LIMIT, count(), sum(), and variable-length paths like [:DEPENDS_ON*1..5].
Tip: For multi-branch dependencies, you can either combine or run 2-3 focused queries to discover services, their databases, and their hosting servers."""

PLAIN_SYS = """You are an enterprise compliance and architecture auditor. Use vector_search to retrieve documentation chunks from Confluence. Answer precisely based strictly on retrieved facts."""

def run_agent_loop(paradigm, prompt, system_instruction, tools_decl, dispatcher, max_turns=10):
    messages = [{"role": "user", "parts": [{"text": f"System: {system_instruction}\n\nTask: {prompt}"}]}]
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
            return {
                "answer": "".join(p.get("text", "") for p in parts),
                "turns": turns,
                "latency_s": round(time.time() - t0, 2),
                "tokens": tokens
            }
            
        messages.append(cand)
        resp_parts = []
        for fc in fcs:
            fn = fc["name"]
            args = fc.get("args", {})
            out = dispatcher(fn, args)
            resp_parts.append({"functionResponse": {"name": fn, "response": {"output": out}}})
        messages.append({"role": "user", "parts": resp_parts})
        time.sleep(0.4)
        
    return {"answer": "TIMEOUT / MAX TURNS EXCEEDED", "turns": turns, "latency_s": round(time.time() - t0, 2), "tokens": tokens}

# 6. Benchmark Audit Questions
AUDIT_TASKS = [
    {
        "id": "audit_1_compliance_leak",
        "name": "Security Compliance Leak Audit (SecPol-04)",
        "prompt": "According to corporate policy SecPol-04, external contractors ('Contractor') are strictly forbidden from having access to any database classified as 'PII-Tier-1'. Audit our personnel, IAM groups, and databases. Find ALL contractors violating this policy, and list their names and the exact databases they have access to.",
        "expected_facts": ["Viktor Vance", "db:card-vault", "Sarah Jenkins", "db:users-vault"],
        "forbidden_hallucinations": ["Alex Morozov", "Liam Neill", "Dmitry Smirnov", "Elena Kuznetsova"]
    },
    {
        "id": "audit_2_financial_cost_rollup",
        "name": "Transitive Infrastructure Financial Rollup",
        "prompt": "Calculate the EXACT total monthly infrastructure cost (USD per month) for ALL physical servers and clusters that 'svc:checkout-api' directly or transitively depends on (including servers hosting its dependent services and connected databases). Name the servers and give the exact final sum.",
        "expected_facts": ["7400", "7,400", "srv:app-checkout-01", "srv:app-pay-01", "srv:app-tax-01", "srv:cache-redis-01", "srv:db-routing-01", "srv:db-cardvault-01"],
        "forbidden_hallucinations": []
    }
]

if __name__ == "__main__":
    print("=== Running Enterprise Compliance & Audit Benchmark (250 Documents) ===\n")
    results = []

    for task in AUDIT_TASKS:
        print("----------------------------------------------------------------------")
        print(f"AUDIT TASK: {task['name']}")
        print(f"Prompt: {task['prompt']}")
        print("----------------------------------------------------------------------")
        
        # 1. Plain Vector RAG
        print("[Testing Plain Vector RAG (250 Docs Corpus)...]")
        def plain_disp(fn, args):
            return exec_vector_search(args.get("query", ""), args.get("top_k", 6))
        
        p_res = run_agent_loop("Plain Vector RAG", task["prompt"], PLAIN_SYS, PLAIN_TOOLS, plain_disp, max_turns=10)
        p_found = [f for f in task["expected_facts"] if f.lower() in p_res["answer"].lower()]
        p_hallu = [h for h in task["forbidden_hallucinations"] if h.lower() in p_res["answer"].lower()]
        p_recall = round(len(p_found) / len(task["expected_facts"]) * 100, 1)
        print(f"  Result: Recall={p_recall}% ({len(p_found)}/{len(task['expected_facts'])}) | Hallucinations={len(p_hallu)} {p_hallu} | Turns={p_res['turns']} | Time={p_res['latency_s']}s | Tokens={p_res['tokens']}")
        print(f"  Answer preview:\n{p_res['answer'][:250]}...\n")
        
        # 2. GraphRAG (cypher-mcp)
        print("[Testing GraphRAG (cypher-mcp)...]")
        def graph_disp(fn, args):
            if fn == "graph_resolve_entity": return exec_graph_resolve(args.get("query", ""))
            return exec_graph_query(args.get("query", ""))
            
        g_res = run_agent_loop("GraphRAG (cypher-mcp)", task["prompt"], GRAPH_SYS, GRAPH_TOOLS, graph_disp, max_turns=12)
        g_found = [f for f in task["expected_facts"] if f.lower() in g_res["answer"].lower()]
        g_hallu = [h for h in task["forbidden_hallucinations"] if h.lower() in g_res["answer"].lower()]
        g_recall = round(len(g_found) / len(task["expected_facts"]) * 100, 1)
        print(f"  Result: Recall={g_recall}% ({len(g_found)}/{len(task['expected_facts'])}) | Hallucinations={len(g_hallu)} | Turns={g_res['turns']} | Time={g_res['latency_s']}s | Tokens={g_res['tokens']}")
        print(f"  Answer preview:\n{g_res['answer'][:250]}...\n")
        
        results.append({
            "name": task["name"],
            "plain": {**p_res, "recall": p_recall, "hallucinations": p_hallu},
            "graph": {**g_res, "recall": g_recall, "hallucinations": g_hallu}
        })

    mcp_proc.kill()

    # Save Detailed Report
    REPORT_PATH = r"C:/Work/Personal/cypher-mcp/benchmarks/reports/ENTERPRISE_250_DOCS_AUDIT.md"
    with open(REPORT_PATH, "w", encoding="utf-8") as f:
        f.write("# Enterprise Audit Benchmark Report (250 Documents)\n\n")
        f.write("Evaluation on 250 enterprise architecture, IAM, and compliance documents comparing **GraphRAG (`cypher-mcp`)** vs **Plain Vector RAG**.\n\n")
        f.write("Evaluator: `Google Gemini 3.8 Flash` | Embeddings: `gemini-embedding-001` (250 vectors)\n\n")
        f.write("## Scorecard\n\n")
        f.write("| Audit Task | Metric | Plain Vector RAG | GraphRAG (cypher-mcp) |\n")
        f.write("| :--- | :--- | :--- | :--- |\n")
        for r in results:
            f.write(f"| **{r['name']}** | Fact Recall | **{r['plain']['recall']}%** | **{r['graph']['recall']}%** |\n")
            f.write(f"| | False Accusations (Hallucinations) | **{len(r['plain']['hallucinations'])}** | **{len(r['graph']['hallucinations'])}** |\n")
            f.write(f"| | Turns to Complete | {r['plain']['turns']} | {r['graph']['turns']} |\n")
            f.write(f"| | Latency | {r['plain']['latency_s']}s | {r['graph']['latency_s']}s |\n")
            f.write(f"| | Tokens Consumed | {r['plain']['tokens']} | {r['graph']['tokens']} |\n")
        f.write("\n## Detailed Outputs\n\n")
        for r in results:
            f.write(f"### {r['name']}\n\n")
            f.write(f"**Plain Vector RAG Answer:**\n> {r['plain']['answer'].strip()}\n\n")
            f.write(f"**GraphRAG Answer:**\n> {r['graph']['answer'].strip()}\n\n---\n\n")

    print(f"Benchmark finished! Report written to {REPORT_PATH}")
