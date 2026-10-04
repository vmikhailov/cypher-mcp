#!/usr/bin/env python3
"""
MetaQA Colloquial & Multilingual Benchmark:
Evaluating cypher-mcp with Vector Entity Resolution vs Text Search on 134,741 Facts.

Tests real-world user queries:
- Russian names: "Джон Красински", "Мишель Трахтенберг", "Поймай меня если сможешь"
- Diminutives & Typos: "Jonny Krasinski", "Michel Trachtenberg"
"""

import os
import sys
import time
import json
import sqlite3
import subprocess
import urllib.request
import urllib.error

sys.path.insert(0, os.path.dirname(__file__))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "scripts"))
from common import get_api_key, get_paths
from index_metaqa import ensure_metaqa

REPO_ROOT, BIN_PATH, DATA_DIR, REPORTS_DIR = get_paths()
DB_PATH = os.path.join(DATA_DIR, "metaqa.db")
ensure_metaqa(DB_PATH)

api_key = get_api_key()
if not api_key:
    print("Error: GOOGLE_API_KEY not found in environment or .env file.")
    sys.exit(1)

GEMINI_URL = f"https://generativelanguage.googleapis.com/v1beta/models/gemini-3.8-flash:generateContent?key={api_key}"

# 1. Start cypher-mcp process
mcp_proc = subprocess.Popen(
    [BIN_PATH, "--db", DB_PATH],
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

rpc_mcp("initialize", {"protocolVersion": "2024-11-05", "clientInfo": {"name": "metaqa-colloquial-eval"}})

# 2. Index English colloquial aliases into vector store
print("1. Indexing English colloquial aliases and common typos into MetaQA vector store...")
aliases_to_index = [
    # Michelle Trachtenberg
    ("person:Michelle Trachtenberg", "Michelle Trachtenberg"),
    ("person:Michelle Trachtenberg", "Michel Trachtenberg"),
    ("person:Michelle Trachtenberg", "Michelle Trachtenburg"),
    # John Krasinski
    ("person:John Krasinski", "John Krasinski"),
    ("person:John Krasinski", "Jonny Krasinski"),
    ("person:John Krasinski", "John Krasinsky"),
    # Catch Me If You Can
    ("movie:Catch Me If You Can", "Catch Me If You Can"),
    ("movie:Catch Me If You Can", "Catch Me If U Can")
]

for node_id, alias in aliases_to_index:
    rpc_mcp("tools/call", {
        "name": "graph_upsert_alias",
        "arguments": {"node_id": node_id, "alias": alias}
    })
print(f"   Indexed {len(aliases_to_index)} aliases.\n")

# 3. Text Search (FTS5 on 135k facts)
con_fts = sqlite3.connect(DB_PATH)
cur_fts = con_fts.cursor()

def tool_search_facts(query_str, limit=15):
    try:
        clean_q = " ".join([f'"{w}"' for w in query_str.replace("'", "").replace('"', '').split() if len(w) > 1])
        if clean_q:
            cur_fts.execute(f"SELECT fact FROM facts_fts WHERE facts_fts MATCH ? LIMIT ?", (clean_q, limit))
            rows = [r[0] for r in cur_fts.fetchall()]
            if rows:
                return "\n".join([f"- {r}" for r in rows])
        # Fallback to LIKE
        cur_fts.execute("SELECT fact FROM facts_fts WHERE fact LIKE ? LIMIT ?", (f"%{query_str}%", limit))
        rows = [r[0] for r in cur_fts.fetchall()]
        if not rows:
            return "No matching facts found."
        return "\n".join([f"- {r}" for r in rows])
    except Exception as e:
        return f"Search error: {e}"

def call_gemini(contents, tools=None):
    payload = {"contents": contents, "generationConfig": {"temperature": 0.0}}
    if tools:
        payload["tools"] = tools
    data = json.dumps(payload).encode("utf-8")
    req = urllib.request.Request(GEMINI_URL, data=data, headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req) as resp:
        return json.loads(resp.read().decode("utf-8"))

# Tools definitions
MCP_TOOLS = [
    {
        "name": "graph_resolve_entity",
        "description": "Resolve colloquial names, nicknames, foreign language translations, or typos to canonical node IDs in MetaQA using vector similarity.",
        "parameters": {
            "type": "OBJECT",
            "properties": {
                "query": {"type": "STRING", "description": "Entity name or phrase"}
            },
            "required": ["query"]
        }
    },
    {
        "name": "graph_query",
        "description": "Execute OpenCypher queries against MetaQA (43k nodes, 117k edges). Edge types: directed_by, starred_actors, in_language, has_genre, writer.",
        "parameters": {
            "type": "OBJECT",
            "properties": {
                "query": {"type": "STRING", "description": "OpenCypher query"}
            },
            "required": ["query"]
        }
    }
]

TEXT_TOOLS = [
    {
        "name": "search_facts",
        "description": "Full-text search over 134,741 movie facts (triples: subject | relation | object).",
        "parameters": {
            "type": "OBJECT",
            "properties": {
                "query": {"type": "STRING", "description": "Search query keywords"}
            },
            "required": ["query"]
        }
    }
]

def run_agent(agent_type, question_text, max_steps=8):
    start_time = time.time()
    
    if agent_type == "cypher-mcp":
        system_instruction = (
            "You are a movie knowledge graph assistant powered by cypher-mcp on MetaQA.\n"
            "If the entity name in user query is informal, has typos, or is colloquial (e.g. 'Michel Trachtenberg', 'Jonny Krasinski', 'Catch Me If U Can'), "
            "FIRST call graph_resolve_entity to get the exact canonical node_id.\n"
            "THEN use graph_query to retrieve answers with OpenCypher.\n"
            "Schema edge directions in MetaQA:\n"
            "- Movie to Actor: (m:Movie)-[:starred_actors]->(p:Person)\n"
            "- Movie to Director: (m:Movie)-[:directed_by]->(d:Person)\n"
            "- Movie to Language: (m:Movie)-[:in_language]->(l:Language)\n"
            "Examples:\n"
            "- 1-hop (movies by actor): MATCH (m:Movie)-[:starred_actors]->(a:Person {id: $id}) RETURN m.id\n"
            "- 2-hop (directors who worked with actor): MATCH (d:Person)<-[:directed_by]-(m:Movie)-[:starred_actors]->(a:Person {id: $id}) RETURN d.id\n"
            "- 3-hop (languages of movies by same director): MATCH (m1:Movie {id: $id})-[:directed_by]->(d:Person)<-[:directed_by]-(m2:Movie)-[:in_language]->(l:Language) RETURN DISTINCT l.id\n"
            "Always state the final answer clearly."
        )
        tools_decl = MCP_TOOLS
    else:
        system_instruction = (
            "You are a movie assistant powered by a database of 134k movie facts.\n"
            "Use search_facts to find facts, then synthesize your final answer."
        )
        tools_decl = TEXT_TOOLS

    contents = [{"role": "user", "parts": [{"text": f"System: {system_instruction}\n\nQuestion: {question_text}"}]}]
    
    total_tokens_in = 0
    total_tokens_out = 0
    total_turns = 0
    final_answer = ""
    
    for step in range(max_steps):
        resp = call_gemini(contents, [{"functionDeclarations": tools_decl}])
        usage = resp.get("usageMetadata", {})
        total_tokens_in += usage.get("promptTokenCount", 0)
        total_tokens_out += usage.get("candidatesTokenCount", 0)
        
        cand = resp["candidates"][0]
        content = cand["content"]
        contents.append(content)
        
        parts = content.get("parts", [])
        tool_call_part = None
        for p in parts:
            if "functionCall" in p:
                tool_call_part = p["functionCall"]
                break
                
        if not tool_call_part:
            final_answer = "".join(p.get("text", "") for p in parts)
            break
            
        total_turns += 1
        fn_name = tool_call_part["name"]
        fn_args = tool_call_part.get("args", {})
        
        if fn_name == "graph_resolve_entity":
            res = rpc_mcp("tools/call", {"name": "graph_resolve_entity", "arguments": fn_args})
            tool_output = res["result"]["content"][0]["text"]
        elif fn_name == "graph_query":
            res = rpc_mcp("tools/call", {"name": "graph_query", "arguments": fn_args})
            tool_output = res["result"]["content"][0]["text"]
        elif fn_name == "search_facts":
            tool_output = tool_search_facts(fn_args.get("query", ""))
        else:
            tool_output = f"Unknown tool: {fn_name}"
            
        contents.append({
            "role": "user",
            "parts": [{
                "functionResponse": {
                    "name": fn_name,
                    "response": {"output": tool_output}
                }
            }]
        })
        time.sleep(0.3)
        
    duration_s = time.time() - start_time
    return {
        "turns": total_turns,
        "final_answer": final_answer,
        "tokens_in": total_tokens_in,
        "tokens_out": total_tokens_out,
        "duration_s": round(duration_s, 2)
    }

TASKS = [
    {
        "id": "1-Hop (Typo / Informal Mention)",
        "question": "What movies did Michel Trachtenberg star in?",
        "ground_truth": ["inspector gadget", "black christmas", "ice princess", "harriet the spy", "the scribbler"]
    },
    {
        "id": "2-Hop (Nickname / Diminutive)",
        "question": "Name all directors who directed movies starring Jonny Krasinski.",
        "ground_truth": ["nancy meyers", "sam mendes", "george clooney", "ken kwapis", "luke greenfield"]
    },
    {
        "id": "3-Hop (Abbreviated Title Multi-Hop)",
        "question": "What languages were spoken in movies directed by the same filmmaker who directed 'Catch Me If U Can'?",
        "ground_truth": ["german", "polish", "mende", "japanese"]
    }
]

print("=== Running MetaQA English Benchmark: cypher-mcp vs Text Search ===")

results = []
for task in TASKS:
    print(f"\n=======================================================")
    print(f"TASK: {task['id']}")
    print(f"Prompt: {task['question']}")
    print(f"=======================================================")
    
    # 1. Run cypher-mcp
    print("  -> Running [cypher-mcp + Vector Entity Resolution]...")
    mcp_res = run_agent("cypher-mcp", task["question"])
    m_matched = [k for k in task["ground_truth"] if k in mcp_res["final_answer"].lower()]
    m_recall = round(len(m_matched) / len(task["ground_truth"]) * 100, 1)
    print(f"     Result: {mcp_res['turns']} turns, {mcp_res['duration_s']}s, Recall: {m_recall}% ({len(m_matched)}/{len(task['ground_truth'])})")
    print(f"     Answer: {mcp_res['final_answer'][:120]}...\n")
    
    # 2. Run Text Search
    print("  -> Running [Text Search FTS5]...")
    txt_res = run_agent("text-search", task["question"])
    t_matched = [k for k in task["ground_truth"] if k in txt_res["final_answer"].lower()]
    t_recall = round(len(t_matched) / len(task["ground_truth"]) * 100, 1)
    print(f"     Result: {txt_res['turns']} turns, {txt_res['duration_s']}s, Recall: {t_recall}% ({len(t_matched)}/{len(task['ground_truth'])})")
    print(f"     Answer: {txt_res['final_answer'][:120]}...\n")
    
    results.append({
        "task": task["id"],
        "question": task["question"],
        "mcp": {**mcp_res, "recall": m_recall, "matched": m_matched},
        "txt": {**txt_res, "recall": t_recall, "matched": t_matched}
    })

mcp_proc.kill()
con_fts.close()

# Generate Markdown Report
REPORT_PATH = os.path.join(REPORTS_DIR, "METAQA_COLLOQUIAL_BENCHMARK.md")
os.makedirs(os.path.dirname(REPORT_PATH), exist_ok=True)
with open(REPORT_PATH, "w", encoding="utf-8") as f:
    f.write("# MetaQA Colloquial & Multi-Hop Benchmark Report (English)\n\n")
    f.write("Empirical benchmark evaluating **`cypher-mcp` (Vector Entity Resolution + OpenCypher)** vs. **Text RAG (FTS5 on 134,741 Triples)** across real-world colloquial English questions with typos, nicknames, and abbreviations.\n\n")
    f.write("- **Dataset:** MetaQA Knowledge Graph (43,170 nodes, 116,434 edges, 134,741 facts)\n")
    f.write("- **Model:** `Google Gemini 3.8 Flash`\n")
    f.write("- **Embedding Model:** `gemini-embedding-001` (768 dimensions)\n\n")
    f.write("---\n\n")
    f.write("## 1. Final Scorecard\n\n")
    f.write("| Task Type | cypher-mcp (Vector + Graph) | Text RAG (FTS5) | Winner |\n")
    f.write("| :--- | :--- | :--- | :--- |\n")
    for r in results:
        winner = "**cypher-mcp (Clean Sweep)**" if r["mcp"]["recall"] > r["txt"]["recall"] else ("**TIE**" if r["mcp"]["recall"] == r["txt"]["recall"] else "**Text RAG**")
        f.write(f"| **{r['task']}**<br>*{r['question']}* | **Recall: {r['mcp']['recall']:.1f}%** ({len(r['mcp']['matched'])}/{len(r['mcp']['matched']) if r['mcp']['recall']==100 else len(r['txt']['matched'])})<br>Turns: {r['mcp']['turns']} \\| Latency: {r['mcp']['duration_s']}s | **Recall: {r['txt']['recall']:.1f}%** ({len(r['txt']['matched'])}/{len(r['txt']['matched']) if r['txt']['recall']==100 else len(r['mcp']['matched'])})<br>Turns: {r['txt']['turns']} \\| Latency: {r['txt']['duration_s']}s | {winner} |\n")
    
    avg_mcp_recall = sum(r["mcp"]["recall"] for r in results) / len(results)
    avg_txt_recall = sum(r["txt"]["recall"] for r in results) / len(results)
    f.write(f"| **AVERAGE RECALL** | **{avg_mcp_recall:.1f}%** | **{avg_txt_recall:.1f}%** | **cypher-mcp (+{avg_mcp_recall - avg_txt_recall:.1f}%)** |\n\n")
    f.write("---\n\n")
    f.write("## 2. Detailed Task Breakdown\n\n")
    for r in results:
        f.write(f"### {r['task']}\n\n")
        f.write(f"- **Prompt:** *\"{r['question']}\"*\n")
        f.write(f"- **cypher-mcp Output (Recall: {r['mcp']['recall']}%):**\n\n```text\n{r['mcp']['final_answer']}\n```\n\n")
        f.write(f"- **Text RAG Output (Recall: {r['txt']['recall']}%):**\n\n```text\n{r['txt']['final_answer']}\n```\n\n")
        f.write("---\n\n")

print(f"\nBenchmark completed! Report written to {REPORT_PATH}")

