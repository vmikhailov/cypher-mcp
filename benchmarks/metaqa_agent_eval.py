#!/usr/bin/env python3
"""
MetaQA Multi-Hop QA Benchmark:
Comparing cypher-mcp (Knowledge Graph) vs Text Search RAG on 134k Real Facts.

Dataset: MetaQA (Movie Knowledge Graph: 43,170 nodes, 117,565 edges, 134,741 facts)
Evaluator Model: Google Gemini 3.8 Flash
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
from common import get_api_key, get_paths

REPO_ROOT, BIN_PATH, DATA_DIR, REPORTS_DIR = get_paths()
DB_PATH = os.path.join(DATA_DIR, "metaqa.db")

# 1. Load API Key
api_key = get_api_key()
if not api_key:
    print("Error: GOOGLE_API_KEY not found in environment or .env file.")
    sys.exit(1)

GEMINI_URL = f"https://generativelanguage.googleapis.com/v1beta/models/gemini-3.8-flash:generateContent?key={api_key}"

# 2. Tool Implementations

# A. Graph Agent: cypher-mcp process
mcp_proc = subprocess.Popen(
    [BIN_PATH, "--db", DB_PATH],
    stdin=subprocess.PIPE,
    stdout=subprocess.PIPE,
    stderr=subprocess.PIPE,
    text=True
)

def rpc_mcp(method, params, req_id=1):
    req = {"jsonrpc": "2.0", "id": req_id, "method": method, "params": params}
    mcp_proc.stdin.write(json.dumps(req) + "\n")
    mcp_proc.stdin.flush()
    line = mcp_proc.stdout.readline()
    return json.loads(line)

rpc_mcp("initialize", {"protocolVersion": "2024-11-05", "clientInfo": {"name": "eval"}}, 1)

def tool_graph_query(query_str):
    resp = rpc_mcp("tools/call", {
        "name": "graph_query",
        "arguments": {"query": query_str}
    }, 99)
    try:
        res = resp["result"]["content"][0]["text"]
        if len(res) > 3000:
            res = res[:3000] + "\n... [TRUNCATED]"
        return res
    except Exception as e:
        return f"Error: {e}"

# B. Text Search / RAG Agent: FTS5 on 135k facts
con_fts = sqlite3.connect(DB_PATH)
cur_fts = con_fts.cursor()

def tool_search_facts(query_str, limit=15):
    try:
        # Clean query for FTS5
        clean_q = " ".join([f'"{w}"' for w in query_str.replace("'", "").replace('"', '').split() if len(w) > 1])
        if not clean_q:
            clean_q = f'"{query_str}"'
        cur_fts.execute(f"SELECT fact FROM facts_fts WHERE facts_fts MATCH ? LIMIT ?", (clean_q, limit))
        rows = [r[0] for r in cur_fts.fetchall()]
        if not rows:
            # Fallback to simple LIKE
            cur_fts.execute("SELECT fact FROM facts_fts WHERE fact LIKE ? LIMIT ?", (f"%{query_str}%", limit))
            rows = [r[0] for r in cur_fts.fetchall()]
        if not rows:
            return "No matching facts found."
        return "\n".join([f"- {r}" for r in rows])
    except Exception as e:
        return f"Search error: {e}"

# 3. Gemini Helper
def call_gemini(contents, tools=None):
    payload = {"contents": contents}
    if tools:
        payload["tools"] = tools
    data = json.dumps(payload).encode("utf-8")
    req = urllib.request.Request(GEMINI_URL, data=data, headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req) as resp:
        return json.loads(resp.read().decode("utf-8"))

# 4. Agent Session Runner
def run_metaqa_agent(agent_type, question_text):
    start_time = time.time()

    if agent_type == "cypher-mcp":
        system_instruction = (
            "You are a movie knowledge graph assistant powered by cypher-mcp.\n"
            "You have tool `graph_query(query: string)`.\n"
            "Graph schema: Nodes(Movie, Person, Genre, Language, Tag). Properties: {name: '...'}.\n"
            "Edges: directed_by, written_by, starred_actors, has_genre, in_language, has_tags.\n"
            "Relationships go FROM Movie TO Person/Genre/Language/Tag (e.g. (m:Movie)-[:directed_by]->(p:Person)).\n"
            "Write precise OpenCypher queries to retrieve the exact answers, then deliver a direct list."
        )
        tools_def = [{
            "functionDeclarations": [{
                "name": "graph_query",
                "description": "Execute OpenCypher query against the movie knowledge graph.",
                "parameters": {
                    "type": "OBJECT",
                    "properties": {
                        "query": {"type": "STRING", "description": "OpenCypher query"}
                    },
                    "required": ["query"]
                }
            }]
        }]
    else: # text-search
        system_instruction = (
            "You are a movie knowledge assistant using a document / fact search engine.\n"
            "You have tool `search_facts(query: string, limit: int)`.\n"
            "Search for relevant facts across the 135,000-fact corpus, follow any required hops, then deliver your answer."
        )
        tools_def = [{
            "functionDeclarations": [{
                "name": "search_facts",
                "description": "Search 135,000 movie facts (e.g. 'Inception directed_by', 'John Krasinski starred_actors').",
                "parameters": {
                    "type": "OBJECT",
                    "properties": {
                        "query": {"type": "STRING", "description": "Search terms or entity name"},
                        "limit": {"type": "INTEGER", "description": "Maximum facts to return (default 15)"}
                    },
                    "required": ["query"]
                }
            }]
        }]

    contents = [
        {"role": "user", "parts": [{"text": f"{system_instruction}\n\nQuestion: {question_text}\nIMPORTANT: Use up to 3 tool calls, then provide the concise answer."}]}
    ]

    total_turns = 0
    executed_tools = []
    total_tokens_in = 0
    total_tokens_out = 0
    final_answer = ""
    max_steps = 5

    for step in range(max_steps):
        pass_tools = tools_def if step < (max_steps - 1) else None
        resp = call_gemini(contents, pass_tools)
        usage = resp.get("usageMetadata", {})
        total_tokens_in += usage.get("promptTokenCount", 0)
        total_tokens_out += usage.get("candidatesTokenCount", 0)

        candidate = resp["candidates"][0]
        content = candidate["content"]
        contents.append(content)

        parts = content.get("parts", [])
        tool_call_part = None
        for p in parts:
            if "functionCall" in p:
                tool_call_part = p["functionCall"]
                break

        if not tool_call_part:
            text_parts = [p.get("text", "") for p in parts if "text" in p]
            final_answer = " ".join(text_parts).strip()
            break

        total_turns += 1
        fn_name = tool_call_part["name"]
        fn_args = tool_call_part.get("args", {})
        executed_tools.append({"name": fn_name, "args": fn_args})

        if fn_name == "graph_query":
            tool_output = tool_graph_query(fn_args.get("query", ""))
        elif fn_name == "search_facts":
            tool_output = tool_search_facts(fn_args.get("query", ""), int(fn_args.get("limit", 15)))
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

    duration_ms = (time.time() - start_time) * 1000.0
    return {
        "turns": total_turns,
        "tool_calls": executed_tools,
        "final_answer": final_answer,
        "tokens_in": total_tokens_in,
        "tokens_out": total_tokens_out,
        "duration_ms": duration_ms
    }

# 5. Define MetaQA Benchmark Suite (1-hop, 2-hop, 3-hop)
TASKS = [
    {
        "id": "1-Hop: Direct Filmography",
        "question": "what films did [Michelle Trachtenberg] star in?",
        "ground_truth": ["inspector gadget", "black christmas", "ice princess", "harriet the spy", "the scribbler"]
    },
    {
        "id": "2-Hop: Co-Stars to Directors",
        "question": "which person directed the movies starred by [John Krasinski]?",
        "ground_truth": ["nancy meyers", "sam mendes", "george clooney", "ken kwapis", "luke greenfield"]
    },
    {
        "id": "3-Hop: Shared Director Cross-Language Reachability",
        "question": "the films that share directors with the film [Catch Me If You Can] were in which languages?",
        "ground_truth": ["german", "polish", "mende", "japanese"]
    }
]

print("=== Starting MetaQA Multi-Hop Benchmark (Gemini 3.8 Flash) ===")
AGENTS = ["cypher-mcp", "text-search"]
results = {t["id"]: {} for t in TASKS}

for task in TASKS:
    print(f"\n==========================================")
    print(f"EVALUATING: {task['id']}")
    print(f"Question: {task['question']}")
    print(f"==========================================")

    for ag in AGENTS:
        print(f"  Running Agent: [{ag}] ...")
        res = run_metaqa_agent(ag, task["question"])

        ans_lower = res["final_answer"].lower()
        matched = [k for k in task["ground_truth"] if k in ans_lower]
        score = len(matched) / len(task["ground_truth"])
        res["score"] = score
        res["matched"] = matched

        print(f"    -> Turns: {res['turns']} | Tokens: {res['tokens_in']}+{res['tokens_out']} | Latency: {res['duration_ms']:.1f}ms | Recall: {len(matched)}/{len(task['ground_truth'])} ({score*100:.0f}%)")
        results[task["id"]][ag] = res

# Cleanup
mcp_proc.kill()
con_fts.close()

# 6. Generate Markdown Report
report_path = os.path.join(REPORTS_DIR, "METAQA_BENCHMARK_REPORT.md")
with open(report_path, "w", encoding="utf-8") as f:
    f.write("# MetaQA Multi-Hop QA Benchmark: cypher-mcp vs Text RAG\n\n")
    f.write("Empirical benchmark on the standard **MetaQA** movie knowledge graph:\n")
    f.write("- **Corpus:** 43,170 nodes, 117,565 edges, 134,741 triples\n")
    f.write("- **Model:** Google Gemini 3.8 Flash (Function Calling)\n\n")

    f.write("## 1. Multi-Hop Performance Matrix\n\n")
    f.write("| Task | Agent | Recall / Completeness | Turns | Tokens (In / Out) | Latency |\n")
    f.write("| :--- | :--- | :---: | :---: | :---: | :---: |\n")

    for task in TASKS:
        t_id = task["id"]
        for ag in AGENTS:
            r = results[t_id][ag]
            f.write(f"| **{t_id}** | `{ag}` | **{r['score']*100:.0f}%** ({len(r['matched'])}/{len(task['ground_truth'])}) | {r['turns']} | {r['tokens_in']} / {r['tokens_out']} | {r['duration_ms']:.0f} ms |\n")

    f.write("\n## 2. Quantitative Summary\n\n")
    f.write("| Agent | Overall Recall | Avg Turns | Avg Tokens (Total) | Avg Latency |\n")
    f.write("| :--- | :---: | :---: | :---: | :---: |\n")

    for ag in AGENTS:
        avg_rec = sum(results[t["id"]][ag]["score"] for t in TASKS) / len(TASKS) * 100
        avg_turns = sum(results[t["id"]][ag]["turns"] for t in TASKS) / len(TASKS)
        avg_toks = sum(results[t["id"]][ag]["tokens_in"] + results[t["id"]][ag]["tokens_out"] for t in TASKS) / len(TASKS)
        avg_lat = sum(results[t["id"]][ag]["duration_ms"] for t in TASKS) / len(TASKS)
        f.write(f"| **{ag}** | **{avg_rec:.1f}%** | **{avg_turns:.1f}** | **{avg_toks:.0f}** | **{avg_lat:.0f} ms** |\n")

    f.write("\n## 3. Detailed Transcripts\n\n")
    for task in TASKS:
        t_id = task["id"]
        f.write(f"### {t_id}\n\n")
        f.write(f"**Question:** `{task['question']}`\n\n")
        f.write(f"**Ground Truth:** `{', '.join(task['ground_truth'])}`\n\n")
        for ag in AGENTS:
            r = results[t_id][ag]
            f.write(f"#### Agent: `{ag}`\n\n")
            f.write("**Tool Calls:**\n")
            for tc in r["tool_calls"]:
                f.write(f"- `{tc['name']}`: `{json.dumps(tc['args'])}`\n")
            f.write(f"\n**Answer:**\n\n{r['final_answer']}\n\n---\n\n")

print(f"\n[DONE] MetaQA benchmark finished! Report written to {report_path}")
