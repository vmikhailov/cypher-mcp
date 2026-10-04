#!/usr/bin/env python3
"""
Benchmark: GraphRAG (cypher-mcp + Vector Entity Resolution) vs Plain Vector RAG.

Dataset: Synthetic Enterprise Infrastructure & Org Graph (Acme Cloud Platform)
Evaluator Model: Google Gemini 3.8 Flash
Embedding Model: Google Gemini Embedding 001
"""

import os
import sys
import time
import json
import math
import struct
import sqlite3
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

def get_embedding(text):
    payload = {
        "model": "models/gemini-embedding-001",
        "content": {"parts": [{"text": text}]}
    }
    data = json.dumps(payload).encode("utf-8")
    req = urllib.request.Request(EMBED_URL, data=data, headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req) as resp:
        res = json.loads(resp.read().decode("utf-8"))
        vals = res["embedding"]["values"]
        norm = math.sqrt(sum(x*x for x in vals))
        if norm == 0:
            norm = 1.0
        return [x / norm for x in vals]

# 2. Setup Benchmark Dataset
DB_PATH = r"C:\Work\Personal\cypher-mcp\data\benchmark_rag_vs_graph.db"
if os.path.exists(DB_PATH):
    try:
        os.remove(DB_PATH)
    except Exception:
        pass

# Documents for Plain Vector RAG
DOCS = [
    {
        "id": "doc:auth_gateway",
        "title": "Auth Gateway Service Architecture",
        "text": "The service 'svc:auth-gateway' handles all customer authentication and JWT issuance. It is owned and maintained by Alexander Ivanov (known as 'Саша' or 'Alex' on Slack). The service directly depends on 'svc:user-store' for identity records and 'db:redis-session' for session tokens."
    },
    {
        "id": "doc:billing_engine",
        "title": "Billing Engine Overview",
        "text": "The service 'svc:billing-engine' processes subscriptions and invoice generations. Owned by Dmitry Smirnov ('Дима' or 'Dima'). It depends directly on 'svc:auth-gateway' for user validation and on 'db:postgres-orders' for persistent billing records."
    },
    {
        "id": "doc:payment_worker",
        "title": "Payment Worker Processing",
        "text": "The background daemon 'svc:payment-worker' executes scheduled credit card transactions. Owned by Dmitry Smirnov ('Дима'). It depends directly on 'svc:billing-engine' to fetch invoices and emits events to 'mq:kafka-events'."
    },
    {
        "id": "doc:fraud_detector",
        "title": "Realtime Fraud Detection",
        "text": "The 'svc:fraud-detector' inspects live financial operations. Owned by Elena Kuznetsova ('Лена' or 'Helen'). It directly consumes transactions from 'svc:payment-worker' and checks historical profiles in 'svc:user-store'."
    },
    {
        "id": "doc:audit_logger",
        "title": "Compliance Audit Logger",
        "text": "The service 'svc:audit-logger' archives security and financial logs. Owned by Elena Kuznetsova ('Лена'). It consumes all stream messages from 'mq:kafka-events' and persists them into 'db:clickhouse-lake'."
    },
    {
        "id": "doc:analytics_api",
        "title": "Analytics and Reporting API",
        "text": "The 'svc:analytics-api' powers executive dashboards and BI reporting. Owned by Mikhail Popov ('Миша' or 'Mike'). It directly queries 'db:clickhouse-lake' and does not publish to Kafka."
    },
    {
        "id": "doc:notification_dispatcher",
        "title": "Notification Dispatcher Service",
        "text": "The service 'svc:notification-dispatcher' sends transactional SMS and emails. Owned by Olga Sokolova ('Оля'). It listens to events from 'mq:kafka-events' and dispatches alerts."
    },
    {
        "id": "doc:user_store",
        "title": "User Store Core Microservice",
        "text": "The service 'svc:user-store' manages user credentials and profiles. Owned by Alexander Ivanov ('Саша'). It depends on 'db:postgres-users' for durable storage."
    },
    {
        "id": "doc:infra_redis",
        "title": "Redis Session Cache Infrastructure",
        "text": "Database 'db:redis-session' provides sub-millisecond session validation. Managed by Alexander Ivanov ('Саша'). Hosted on physical server 'srv:cache-01'."
    },
    {
        "id": "doc:infra_pg_orders",
        "title": "Postgres Orders Database Infrastructure",
        "text": "Database 'db:postgres-orders' stores financial ledgers and invoices. Managed by Dmitry Smirnov ('Дима'). Hosted on physical server 'srv:db-node-01'."
    },
    {
        "id": "doc:infra_pg_users",
        "title": "Postgres Users Database Infrastructure",
        "text": "Database 'db:postgres-users' stores customer profiles. Managed by Alexander Ivanov ('Саша'). Hosted on physical server 'srv:db-node-02'."
    },
    {
        "id": "doc:infra_clickhouse",
        "title": "ClickHouse Analytics Data Lake",
        "text": "Database 'db:clickhouse-lake' stores columnar metrics and logs. Managed by Mikhail Popov ('Миша'). Hosted on server cluster 'srv:analytics-cluster'."
    },
    {
        "id": "doc:dc_frankfurt_1",
        "title": "Datacenter Frankfurt Zone 1",
        "text": "Datacenter 'dc:frankfurt-zone-1' is located in Frankfurt am Main, Germany. It hosts physical server 'srv:db-node-01' and 'srv:db-node-02'."
    },
    {
        "id": "doc:dc_frankfurt_2",
        "title": "Datacenter Frankfurt Zone 2",
        "text": "Datacenter 'dc:frankfurt-zone-2' is a secondary failover facility in Frankfurt. It hosts physical server 'srv:cache-01'."
    },
    {
        "id": "doc:dc_amsterdam_1",
        "title": "Datacenter Amsterdam Zone 1",
        "text": "Datacenter 'dc:amsterdam-zone-1' is located in Amsterdam, Netherlands. It hosts server 'srv:analytics-cluster'."
    }
]

print("1. Indexing Plain Vector RAG chunks...")
doc_vectors = []
for doc in DOCS:
    vec = get_embedding(doc["title"] + "\n" + doc["text"])
    doc_vectors.append((doc, vec))
print(f"   Indexed {len(doc_vectors)} text chunks.")

# 3. Setup cypher-mcp Graph & Vector Aliases
print("2. Starting cypher-mcp server and initializing graph...")
mcp_proc = subprocess.Popen(
    [r"C:\Work\Personal\cypher-mcp\bin\cypher-mcp.exe", "--db", DB_PATH],
    stdin=subprocess.PIPE,
    stdout=subprocess.PIPE,
    stderr=subprocess.PIPE,
    text=True
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

# Construct graph nodes & edges
nodes = [
    # People
    {"id": "person:alexander", "kind": "Person", "properties": {"name": "Alexander Ivanov", "role": "Staff Architect"}},
    {"id": "person:dmitry", "kind": "Person", "properties": {"name": "Dmitry Smirnov", "role": "SRE Lead"}},
    {"id": "person:elena", "kind": "Person", "properties": {"name": "Elena Kuznetsova", "role": "Security Lead"}},
    {"id": "person:mikhail", "kind": "Person", "properties": {"name": "Mikhail Popov", "role": "Data Lead"}},
    {"id": "person:olga", "kind": "Person", "properties": {"name": "Olga Sokolova", "role": "Infra Lead"}},
    # Services
    {"id": "svc:auth-gateway", "kind": "Service", "properties": {"name": "Auth Gateway"}},
    {"id": "svc:billing-engine", "kind": "Service", "properties": {"name": "Billing Engine"}},
    {"id": "svc:payment-worker", "kind": "Service", "properties": {"name": "Payment Worker"}},
    {"id": "svc:fraud-detector", "kind": "Service", "properties": {"name": "Fraud Detector"}},
    {"id": "svc:audit-logger", "kind": "Service", "properties": {"name": "Audit Logger"}},
    {"id": "svc:analytics-api", "kind": "Service", "properties": {"name": "Analytics API"}},
    {"id": "svc:notification-dispatcher", "kind": "Service", "properties": {"name": "Notification Dispatcher"}},
    {"id": "svc:user-store", "kind": "Service", "properties": {"name": "User Store"}},
    # Queues & DBs
    {"id": "mq:kafka-events", "kind": "Queue", "properties": {"name": "Kafka Event Bus"}},
    {"id": "db:redis-session", "kind": "Database", "properties": {"name": "Redis Session", "engine": "Redis"}},
    {"id": "db:postgres-orders", "kind": "Database", "properties": {"name": "Postgres Orders", "engine": "PostgreSQL"}},
    {"id": "db:postgres-users", "kind": "Database", "properties": {"name": "Postgres Users", "engine": "PostgreSQL"}},
    {"id": "db:clickhouse-lake", "kind": "Database", "properties": {"name": "ClickHouse Lake", "engine": "ClickHouse"}},
    # Servers
    {"id": "srv:cache-01", "kind": "Server", "properties": {"hostname": "srv:cache-01"}},
    {"id": "srv:db-node-01", "kind": "Server", "properties": {"hostname": "srv:db-node-01"}},
    {"id": "srv:db-node-02", "kind": "Server", "properties": {"hostname": "srv:db-node-02"}},
    {"id": "srv:analytics-cluster", "kind": "Server", "properties": {"hostname": "srv:analytics-cluster"}},
    # Datacenters
    {"id": "dc:frankfurt-zone-1", "kind": "Datacenter", "properties": {"name": "Frankfurt Zone 1", "city": "Frankfurt"}},
    {"id": "dc:frankfurt-zone-2", "kind": "Datacenter", "properties": {"name": "Frankfurt Zone 2", "city": "Frankfurt"}},
    {"id": "dc:amsterdam-zone-1", "kind": "Datacenter", "properties": {"name": "Amsterdam Zone 1", "city": "Amsterdam"}}
]

edges = [
    # Ownership
    {"from": "person:alexander", "to": "svc:auth-gateway", "kind": "OWNS", "properties": {}},
    {"from": "person:alexander", "to": "svc:user-store", "kind": "OWNS", "properties": {}},
    {"from": "person:alexander", "to": "db:redis-session", "kind": "OWNS", "properties": {}},
    {"from": "person:alexander", "to": "db:postgres-users", "kind": "OWNS", "properties": {}},
    {"from": "person:dmitry", "to": "svc:billing-engine", "kind": "OWNS", "properties": {}},
    {"from": "person:dmitry", "to": "svc:payment-worker", "kind": "OWNS", "properties": {}},
    {"from": "person:dmitry", "to": "db:postgres-orders", "kind": "OWNS", "properties": {}},
    {"from": "person:elena", "to": "svc:fraud-detector", "kind": "OWNS", "properties": {}},
    {"from": "person:elena", "to": "svc:audit-logger", "kind": "OWNS", "properties": {}},
    {"from": "person:mikhail", "to": "svc:analytics-api", "kind": "OWNS", "properties": {}},
    {"from": "person:mikhail", "to": "db:clickhouse-lake", "kind": "OWNS", "properties": {}},
    {"from": "person:olga", "to": "svc:notification-dispatcher", "kind": "OWNS", "properties": {}},

    # Dependencies (Service -> Service / DB / Queue)
    {"from": "svc:auth-gateway", "to": "svc:user-store", "kind": "DEPENDS_ON", "properties": {}},
    {"from": "svc:auth-gateway", "to": "db:redis-session", "kind": "DEPENDS_ON", "properties": {}},
    {"from": "svc:user-store", "to": "db:postgres-users", "kind": "DEPENDS_ON", "properties": {}},
    {"from": "svc:billing-engine", "to": "svc:auth-gateway", "kind": "DEPENDS_ON", "properties": {}},
    {"from": "svc:billing-engine", "to": "db:postgres-orders", "kind": "DEPENDS_ON", "properties": {}},
    {"from": "svc:payment-worker", "to": "svc:billing-engine", "kind": "DEPENDS_ON", "properties": {}},
    {"from": "svc:payment-worker", "to": "mq:kafka-events", "kind": "DEPENDS_ON", "properties": {}},
    {"from": "svc:fraud-detector", "to": "svc:payment-worker", "kind": "DEPENDS_ON", "properties": {}},
    {"from": "svc:fraud-detector", "to": "svc:user-store", "kind": "DEPENDS_ON", "properties": {}},
    {"from": "svc:audit-logger", "to": "mq:kafka-events", "kind": "DEPENDS_ON", "properties": {}},
    {"from": "svc:audit-logger", "to": "db:clickhouse-lake", "kind": "DEPENDS_ON", "properties": {}},
    {"from": "svc:analytics-api", "to": "db:clickhouse-lake", "kind": "DEPENDS_ON", "properties": {}},
    {"from": "svc:notification-dispatcher", "to": "mq:kafka-events", "kind": "DEPENDS_ON", "properties": {}},

    # Infrastructure Hosting & Locations
    {"from": "db:postgres-orders", "to": "srv:db-node-01", "kind": "HOSTED_ON", "properties": {}},
    {"from": "db:postgres-users", "to": "srv:db-node-02", "kind": "HOSTED_ON", "properties": {}},
    {"from": "db:redis-session", "to": "srv:cache-01", "kind": "HOSTED_ON", "properties": {}},
    {"from": "db:clickhouse-lake", "to": "srv:analytics-cluster", "kind": "HOSTED_ON", "properties": {}},

    {"from": "srv:db-node-01", "to": "dc:frankfurt-zone-1", "kind": "LOCATED_IN", "properties": {}},
    {"from": "srv:db-node-02", "to": "dc:frankfurt-zone-1", "kind": "LOCATED_IN", "properties": {}},
    {"from": "srv:cache-01", "to": "dc:frankfurt-zone-2", "kind": "LOCATED_IN", "properties": {}},
    {"from": "srv:analytics-cluster", "to": "dc:amsterdam-zone-1", "kind": "LOCATED_IN", "properties": {}}
]

rpc_mcp("tools/call", {
    "name": "graph_batch_upsert",
    "arguments": {"nodes": nodes, "edges": edges}
})

# Index Aliases into graph
aliases = [
    ("person:alexander", "Саша"),
    ("person:alexander", "Александр"),
    ("person:alexander", "Alex"),
    ("person:dmitry", "Дима"),
    ("person:dmitry", "Дмитрий"),
    ("person:dmitry", "Dima"),
    ("person:elena", "Лена"),
    ("person:elena", "Алёна"),
    ("person:elena", "Helen"),
    ("person:mikhail", "Миша"),
    ("person:mikhail", "Михаил"),
    ("person:olga", "Оля"),
    ("person:olga", "Ольга"),
    ("svc:payment-worker", "платежный воркер"),
    ("svc:payment-worker", "payment worker"),
    ("srv:cache-01", "кэш-сервер")
]

print("3. Indexing entity aliases into Graph vector index...")
for node_id, alias in aliases:
    rpc_mcp("tools/call", {
        "name": "graph_upsert_alias",
        "arguments": {"node_id": node_id, "alias": alias}
    })
print(f"   Indexed {len(aliases)} entity aliases in cypher-mcp.\n")

# 4. Agent Execution Framework

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

# Tools for Plain Vector RAG
PLAIN_RAG_TOOLS = [
    {
        "name": "vector_search",
        "description": "Semantic vector search across architecture documents and system specifications.",
        "parameters": {
            "type": "OBJECT",
            "properties": {
                "query": {"type": "STRING", "description": "Natural language search query"},
                "top_k": {"type": "INTEGER", "description": "Number of relevant chunks to retrieve (default: 5)"}
            },
            "required": ["query"]
        }
    }
]

def exec_vector_search(query, top_k=5):
    q_vec = get_embedding(query)
    scores = []
    for doc, d_vec in doc_vectors:
        sim = sum(a * b for a, b in zip(q_vec, d_vec))
        scores.append((sim, doc))
    scores.sort(reverse=True, key=lambda x: x[0])
    results = []
    for sim, doc in scores[:top_k]:
        results.append({
            "title": doc["title"],
            "score": round(sim, 4),
            "text": doc["text"]
        })
    return json.dumps(results, ensure_ascii=False, indent=2)

# Tools for GraphRAG
GRAPH_RAG_TOOLS = [
    {
        "name": "graph_schema",
        "description": "Inspect available node kinds, edge relationship kinds, and topology statistics.",
        "parameters": {
            "type": "OBJECT",
            "properties": {}
        }
    },
    {
        "name": "graph_resolve_entity",
        "description": "Resolve colloquial names, nicknames, or terms to canonical graph node IDs using vector similarity.",
        "parameters": {
            "type": "OBJECT",
            "properties": {
                "query": {"type": "STRING", "description": "Entity name or phrase (e.g. 'Саша', 'платежный воркер')"},
                "limit": {"type": "INTEGER", "description": "Max candidates (default: 3)"}
            },
            "required": ["query"]
        }
    },
    {
        "name": "graph_query",
        "description": "Execute OpenCypher queries against the knowledge graph. Nodes: Person, Service, Database, Server, Datacenter, Queue. Edges: OWNS, DEPENDS_ON, HOSTED_ON, LOCATED_IN.",
        "parameters": {
            "type": "OBJECT",
            "properties": {
                "query": {"type": "STRING", "description": "OpenCypher query"}
            },
            "required": ["query"]
        }
    }
]

def exec_graph_schema():
    res = rpc_mcp("tools/call", {
        "name": "graph_schema",
        "arguments": {}
    })
    return res["result"]["content"][0]["text"]

def exec_graph_resolve(query, limit=3):
    res = rpc_mcp("tools/call", {
        "name": "graph_resolve_entity",
        "arguments": {"query": query, "limit": limit}
    })
    return res["result"]["content"][0]["text"]

def exec_graph_query(query):
    res = rpc_mcp("tools/call", {
        "name": "graph_query",
        "arguments": {"query": query}
    })
    return res["result"]["content"][0]["text"]

def run_agent_loop(paradigm_name, prompt, system_instruction, tools_decl, tool_dispatcher, max_turns=10):
    messages = [
        {"role": "user", "parts": [{"text": f"System: {system_instruction}\n\nTask: {prompt}"}]}
    ]
    t0 = time.time()
    turns = 0
    total_tokens = 0
    tool_calls_count = 0
    
    while turns < max_turns:
        turns += 1
        resp = call_gemini(messages, tools_decl)
        
        usage = resp.get("usageMetadata", {})
        total_tokens += usage.get("totalTokenCount", 0)
        
        cand = resp.get("candidates", [{}])[0]
        content = cand.get("content", {})
        parts = content.get("parts", [])
        
        # Check for function calls
        func_calls = [p["functionCall"] for p in parts if "functionCall" in p]
        
        if not func_calls:
            # Model gave final answer
            answer_text = "".join(p.get("text", "") for p in parts)
            elapsed = time.time() - t0
            return {
                "paradigm": paradigm_name,
                "answer": answer_text,
                "turns": turns,
                "tool_calls": tool_calls_count,
                "latency_s": round(elapsed, 2),
                "tokens": total_tokens
            }
        
        # Add model response to history
        messages.append(content)
        
        # Execute tool calls
        fn_resp_parts = []
        for fc in func_calls:
            tool_calls_count += 1
            fn_name = fc["name"]
            fn_args = fc.get("args", {})
            fn_output = tool_dispatcher(fn_name, fn_args)
            fn_resp_parts.append({
                "functionResponse": {
                    "name": fn_name,
                    "response": {"output": fn_output}
                }
            })
        
        # Role must be 'user' for Gemini REST functionResponse
        messages.append({"role": "user", "parts": fn_resp_parts})
        time.sleep(0.5)
    
    return {
        "paradigm": paradigm_name,
        "answer": "TIMEOUT / MAX TURNS EXCEEDED",
        "turns": turns,
        "tool_calls": tool_calls_count,
        "latency_s": round(time.time() - t0, 2),
        "tokens": total_tokens
    }

# 5. Benchmark Tasks
TASKS = [
    {
        "id": "task_1_colloquial_1hop",
        "name": "Colloquial 1-Hop Discovery",
        "prompt": "Кто у нас отвечает за шлюз авторизации (auth gateway)? Пользователь в чате пишет: 'Спроси у Саши, это его сервис?'. Назови полное имя ответственного инженера и его точную роль.",
        "expected_facts": ["Alexander Ivanov", "Staff Architect"],
        "graph_start_hint": "Resolve entity 'Саша' or service 'auth gateway'"
    },
    {
        "id": "task_2_multihop_2hop",
        "name": "Multi-Hop Transitive Dependency (2-Hop)",
        "prompt": "В каком именно датацентре (город и название зоны) физически расположен сервер базы данных, от которой зависит платежный воркер (svc:payment-worker)?",
        "expected_facts": ["Frankfurt", "dc:frankfurt-zone-1"],
        "graph_start_hint": "Trace payment-worker -> billing-engine -> postgres-orders -> db-node-01 -> frankfurt-zone-1"
    },
    {
        "id": "task_3_blast_radius_3hop",
        "name": "Blast Radius Impact Chain (3-Hop)",
        "prompt": "Инженеры зафиксировали отказ кэш-сервера srv:cache-01. Перечисли ВСЕ сервисы, чья работа будет затронута по цепочке зависимостей, и ВСЕХ инженеров (владельцев этих сервисов), которых нужно вызвать в war room.",
        "expected_facts": ["svc:auth-gateway", "svc:billing-engine", "svc:payment-worker", "svc:fraud-detector", "Alexander Ivanov", "Dmitry Smirnov", "Elena Kuznetsova"],
        "graph_start_hint": "srv:cache-01 -> db:redis-session <-DEPENDS_ON*1..3-"
    },
    {
        "id": "task_4_negation_aggregation",
        "name": "Global Negation & Aggregation",
        "prompt": "Сколько ВСЕГО микросервисов (kind: 'Service') в нашей инфраструктуре НЕ зависят от брокера сообщений Kafka (mq:kafka-events)? Назови их точное количество и перечисли их идентификаторы.",
        "expected_facts": ["svc:auth-gateway", "svc:billing-engine", "svc:analytics-api", "svc:user-store", "4"],
        "graph_start_hint": "MATCH (s:Service) WHERE NOT (s)-[:DEPENDS_ON]->(:Queue {id: 'mq:kafka-events'}) RETURN count(s), collect(s.id)"
    }
]

print("=== Running Head-to-Head Benchmark: GraphRAG vs Plain Vector RAG ===\n")

results = []

for task in TASKS:
    print(f"----------------------------------------------------------------------")
    print(f"TASK: {task['name']}")
    print(f"Prompt: {task['prompt']}")
    print(f"----------------------------------------------------------------------")
    
    # 1. Run Plain Vector RAG
    print("[Testing Plain Vector RAG...]")
    def plain_rag_dispatcher(fn_name, fn_args):
        if fn_name == "vector_search":
            return exec_vector_search(fn_args.get("query", ""), fn_args.get("top_k", 3))
        return "Unknown tool"

    plain_sys = "You are a technical platform assistant. Answer user questions using vector_search to retrieve documentation chunks. Be precise and cite facts."
    plain_res = run_agent_loop("Plain Vector RAG", task["prompt"], plain_sys, PLAIN_RAG_TOOLS, plain_rag_dispatcher)
    print(f"  Result: {plain_res['turns']} turns, {plain_res['tool_calls']} calls, {plain_res['latency_s']}s, {plain_res['tokens']} tokens")
    print(f"  Answer preview: {plain_res['answer'][:160]}...\n")
    
    # 2. Run GraphRAG (cypher-mcp)
    print("[Testing GraphRAG (cypher-mcp)...]")
    def graph_rag_dispatcher(fn_name, fn_args):
        if fn_name == "graph_schema":
            return exec_graph_schema()
        elif fn_name == "graph_resolve_entity":
            return exec_graph_resolve(fn_args.get("query", ""), fn_args.get("limit", 3))
        elif fn_name == "graph_query":
            return exec_graph_query(fn_args.get("query", ""))
        return "Unknown tool"

    graph_sys = "You are a platform architecture assistant with access to a live knowledge graph. First use graph_resolve_entity to resolve colloquial names or nicknames to exact node IDs, then use graph_query with OpenCypher to traverse relationships, dependencies, and count/filter nodes."
    graph_res = run_agent_loop("GraphRAG (cypher-mcp)", task["prompt"], graph_sys, GRAPH_RAG_TOOLS, graph_rag_dispatcher)
    print(f"  Result: {graph_res['turns']} turns, {graph_res['tool_calls']} calls, {graph_res['latency_s']}s, {graph_res['tokens']} tokens")
    print(f"  Answer preview: {graph_res['answer'][:160]}...\n")
    
    # Evaluate recall against expected facts
    def eval_facts(answer, facts):
        found = [f for f in facts if f.lower() in answer.lower()]
        return round(len(found) / len(facts) * 100, 1), found, [f for f in facts if f not in found]

    plain_recall, p_found, p_miss = eval_facts(plain_res["answer"], task["expected_facts"])
    graph_recall, g_found, g_miss = eval_facts(graph_res["answer"], task["expected_facts"])
    
    print(f"EVALUATION: Task '{task['name']}'")
    print(f"  Plain Vector RAG : Recall: {plain_recall}% | Missed: {p_miss}")
    print(f"  GraphRAG MCP     : Recall: {graph_recall}% | Missed: {g_miss}\n")
    
    results.append({
        "task": task["name"],
        "prompt": task["prompt"],
        "plain_rag": {**plain_res, "recall": plain_recall, "missed": p_miss},
        "graph_rag": {**graph_res, "recall": graph_recall, "missed": g_miss}
    })

mcp_proc.kill()

# Save Report
REPORT_PATH = r"C:/Work/Personal/cypher-mcp/benchmarks/reports/VECTOR_GRAPH_VS_PLAIN_RAG.md"
with open(REPORT_PATH, "w", encoding="utf-8") as f:
    f.write("# Benchmark Report: GraphRAG (cypher-mcp) vs. Plain Vector RAG\n\n")
    f.write("Evaluation comparing **GraphRAG (`cypher-mcp` with Vector Entity Resolution + OpenCypher)** against **Plain Vector RAG (Embedding Chunk Search)**.\n\n")
    f.write("Evaluator: `Google Gemini 3.8 Flash` | Embedding Model: `gemini-embedding-001`\n\n")
    f.write("## Summary Scorecard\n\n")
    f.write("| Paradigm | Avg Recall | Avg Turns | Avg Latency | Avg Tokens |\n")
    f.write("| :--- | :--- | :--- | :--- | :--- |\n")
    
    avg_p_rec = sum(r["plain_rag"]["recall"] for r in results) / len(results)
    avg_p_turns = sum(r["plain_rag"]["turns"] for r in results) / len(results)
    avg_p_lat = sum(r["plain_rag"]["latency_s"] for r in results) / len(results)
    avg_p_tok = sum(r["plain_rag"]["tokens"] for r in results) / len(results)

    avg_g_rec = sum(r["graph_rag"]["recall"] for r in results) / len(results)
    avg_g_turns = sum(r["graph_rag"]["turns"] for r in results) / len(results)
    avg_g_lat = sum(r["graph_rag"]["latency_s"] for r in results) / len(results)
    avg_g_tok = sum(r["graph_rag"]["tokens"] for r in results) / len(results)

    f.write(f"| **GraphRAG (cypher-mcp)** | **{avg_g_rec:.1f}%** | **{avg_g_turns:.1f}** | **{avg_g_lat:.1f}s** | **{int(avg_g_tok)}** |\n")
    f.write(f"| **Plain Vector RAG** | **{avg_p_rec:.1f}%** | **{avg_p_turns:.1f}** | **{avg_p_lat:.1f}s** | **{int(avg_p_tok)}** |\n\n")
    
    f.write("## Detailed Task Results\n\n")
    for r in results:
        f.write(f"### {r['task']}\n\n")
        f.write(f"**Prompt:** *\"{r['prompt']}\"*\n\n")
        f.write(f"| Metric | Plain Vector RAG | GraphRAG (cypher-mcp) |\n")
        f.write(f"| :--- | :--- | :--- |\n")
        f.write(f"| **Fact Recall** | **{r['plain_rag']['recall']}%** | **{r['graph_rag']['recall']}%** |\n")
        f.write(f"| **Turns to Answer** | {r['plain_rag']['turns']} | {r['graph_rag']['turns']} |\n")
        f.write(f"| **Tool Calls** | {r['plain_rag']['tool_calls']} | {r['graph_rag']['tool_calls']} |\n")
        f.write(f"| **Latency** | {r['plain_rag']['latency_s']}s | {r['graph_rag']['latency_s']}s |\n")
        f.write(f"| **Token Usage** | {r['plain_rag']['tokens']} | {r['graph_rag']['tokens']} |\n\n")
        f.write(f"**Plain RAG Answer:**\n> {r['plain_rag']['answer'].strip()}\n\n")
        f.write(f"**GraphRAG Answer:**\n> {r['graph_rag']['answer'].strip()}\n\n")
        f.write("---\n\n")

print(f"\nBenchmark finished! Report written to: {REPORT_PATH}")
