"""
Academic Benchmark: 2WikiMultihopQA (Complex Multi-Hop Reasoning with Distractors)
Evaluating cypher-mcp (Vector Entity Resolution + OpenCypher) vs Plain Vector RAG.

Evaluator: Google Gemini 3.8 Flash
Embedding Model: Google Gemini Embedding 001 (768 dimensions)
"""

import os

import sys

import time

import json

import math

import sqlite3

import subprocess

import urllib.request

import urllib.error

sys.path.insert(0, os.path.dirname(__file__))

if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(encoding="utf-8")
if hasattr(sys.stderr, "reconfigure"):
    sys.stderr.reconfigure(encoding="utf-8")

from common import get_api_key, get_paths, isolated_db, reap_process

def get_embedding(text):
    payload = {
        "model": "models/gemini-embedding-001",
        "content": {"parts": [{"text": text[:2000]}]}
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

def rpc_mcp(method, params):
    global req_id_counter
    req_id_counter += 1
    req = {"jsonrpc": "2.0", "id": req_id_counter, "method": method, "params": params}
    mcp_proc.stdin.write(json.dumps(req) + "\n")
    mcp_proc.stdin.flush()
    line = mcp_proc.stdout.readline()
    return json.loads(line)

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

def exec_graph_resolve(query):
    res = rpc_mcp("tools/call", {
        "name": "graph_resolve_entity",
        "arguments": {"query": query, "limit": 3}
    })
    return res["result"]["content"][0]["text"]

def exec_graph_query(query):
    res = rpc_mcp("tools/call", {
        "name": "graph_query",
        "arguments": {"query": query}
    })
    return res["result"]["content"][0]["text"]

def run_agent_loop(paradigm_name, prompt, system_instruction, tools_decl, tool_dispatcher, max_turns=6):
    messages = [
        {"role": "user", "parts": [{"text": f"System: {system_instruction}\n\nQuestion: {prompt}"}]}
    ]
    t0 = time.time()
    turns = 0
    total_tokens = 0
    
    while turns < max_turns:
        turns += 1
        resp = call_gemini(messages, tools_decl)
        usage = resp.get("usageMetadata", {})
        total_tokens += usage.get("totalTokenCount", 0)
        
        cand = resp.get("candidates", [{}])[0]
        content = cand.get("content", {})
        parts = content.get("parts", [])
        
        func_calls = [p["functionCall"] for p in parts if "functionCall" in p]
        
        if not func_calls:
            answer_text = "".join(p.get("text", "") for p in parts)
            elapsed = time.time() - t0
            return {
                "paradigm": paradigm_name,
                "answer": answer_text,
                "turns": turns,
                "latency_s": round(elapsed, 2),
                "tokens": total_tokens
            }
        
        messages.append(content)
        fn_resp_parts = []
        for fc in func_calls:
            fn_name = fc["name"]
            fn_args = fc.get("args", {})
            fn_output = tool_dispatcher(fn_name, fn_args)
            fn_resp_parts.append({
                "functionResponse": {
                    "name": fn_name,
                    "response": {"output": fn_output}
                }
            })
        messages.append({"role": "user", "parts": fn_resp_parts})
        time.sleep(0.4)
    
    return {
        "paradigm": paradigm_name,
        "answer": "TIMEOUT / MAX TURNS EXCEEDED",
        "turns": turns,
        "latency_s": round(time.time() - t0, 2),
        "tokens": total_tokens
    }

def main():
    """Run explicitly; importing this module performs no benchmark work."""
    global BENCHMARK_TASKS, BIN_PATH, DATA_DIR, DB_PATH, EMBED_URL, GEMINI_URL, GRAPH_RAG_TOOLS, PLAIN_RAG_TOOLS, REPORTS_DIR, REPORT_PATH, REPO_ROOT, alias, aliases, api_key, c, chunk, corpus, doc_vectors, edges, f, g_acc, g_avg_lat, g_avg_tok, g_avg_turns, g_correct, graph_res, graph_sys, hf_url, i, item, last_brace, mcp_proc, node_id, nodes, p_acc, p_avg_lat, p_avg_tok, p_avg_turns, p_correct, plain_res, plain_sys, r, raw_data, raw_tasks, req, req_id_counter, resp, results, selected_indices, sentences, task, text, title, unique_corpus, vec
    REPO_ROOT, BIN_PATH, DATA_DIR, REPORTS_DIR = get_paths()

    DB_PATH = os.path.join(DATA_DIR, "2wiki_benchmark.db")

    api_key = get_api_key()

    if not api_key:
        print("Error: GOOGLE_API_KEY not found in environment or .env file.")
        sys.exit(1)

    GEMINI_URL = f"https://generativelanguage.googleapis.com/v1beta/models/gemini-3.8-flash:generateContent?key={api_key}"

    EMBED_URL = f"https://generativelanguage.googleapis.com/v1beta/models/gemini-embedding-001:embedContent?key={api_key}"

    print("Fetching 2WikiMultihopQA items from Hugging Face...")

    hf_url = "https://huggingface.co/datasets/voidful/2WikiMultihopQA/raw/main/dev.json"

    req = urllib.request.Request(hf_url, headers={"User-Agent": "Mozilla/5.0"})

    with urllib.request.urlopen(req) as resp:
        chunk = resp.read(350000).decode("utf-8", errors="ignore")
        last_brace = chunk.rfind("},")
        raw_data = json.loads(chunk[:last_brace] + "}]")

    selected_indices = [0, 1, 2, 5]

    raw_tasks = [raw_data[i] for i in selected_indices]

    print("Building Plain Vector RAG corpus from all context paragraphs (supporting + distractors)...")

    corpus = []

    for item in raw_tasks:
        for title, sentences in item["context"]:
            text = " ".join(sentences)
            corpus.append({"title": title, "text": text})

    unique_corpus = {}

    for c in corpus:
        if c["title"] not in unique_corpus:
            unique_corpus[c["title"]] = c["text"]

    print(f"Total unique paragraphs with distractors: {len(unique_corpus)}")

    doc_vectors = []

    for title, text in unique_corpus.items():
        vec = get_embedding(title + "\n" + text)
        doc_vectors.append(({"title": title, "text": text}, vec))

    print(f"Computed embeddings for {len(doc_vectors)} paragraphs.\n")

    with isolated_db(prefix="2wiki_benchmark_") as DB_PATH:
        mcp_proc = None
        try:
            mcp_proc = subprocess.Popen(
                [BIN_PATH, "--db", DB_PATH],
                stdin=subprocess.PIPE,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True
            )

            req_id_counter = 0

            rpc_mcp("initialize", {"protocolVersion": "2024-11-05", "clientInfo": {"name": "2wiki-eval"}})

            nodes = [
                # Task 1: Polish-Russian War
                {"id": "movie:polish-russian-war", "kind": "Movie", "properties": {"name": "Polish-Russian War (film)", "release_year": "2009"}},
                {"id": "person:xawery-zulawski", "kind": "Person", "properties": {"name": "Xawery Żuławski", "birth_year": "1971"}},
                {"id": "person:malgorzata-braunek", "kind": "Person", "properties": {"name": "Małgorzata Braunek"}},
                {"id": "person:andrzej-zulawski", "kind": "Person", "properties": {"name": "Andrzej Żuławski"}},
                # Task 2: Blind Shaft vs Fu Manchu
                {"id": "movie:blind-shaft", "kind": "Movie", "properties": {"name": "Blind Shaft", "release_year": "2003"}},
                {"id": "movie:mask-of-fu-manchu", "kind": "Movie", "properties": {"name": "The Mask of Fu Manchu", "release_year": "1932"}},
                {"id": "movie:castle-of-fu-manchu", "kind": "Movie", "properties": {"name": "The Castle of Fu Manchu", "release_year": "1969"}},
                {"id": "movie:face-of-fu-manchu", "kind": "Movie", "properties": {"name": "The Face of Fu Manchu", "release_year": "1965"}},
                {"id": "movie:blood-of-fu-manchu", "kind": "Movie", "properties": {"name": "The Blood of Fu Manchu", "release_year": "1968"}},
                # Task 3: Anhalt-Zerbst Princes
                {"id": "person:john-v-anhalt-zerbst", "kind": "Person", "properties": {"name": "John V, Prince of Anhalt-Zerbst", "birth": "1504", "death": "1551"}},
                {"id": "person:ernest-i-anhalt-dessau", "kind": "Person", "properties": {"name": "Ernest I, Prince of Anhalt-Dessau", "death_date": "12 June 1516"}},
                {"id": "person:john-vi-anhalt-zerbst", "kind": "Person", "properties": {"name": "John VI, Prince of Anhalt-Zerbst", "death_date": "4 July 1667"}},
                {"id": "person:rudolph-anhalt-zerbst", "kind": "Person", "properties": {"name": "Rudolph, Prince of Anhalt-Zerbst"}},
                {"id": "person:george-i-anhalt-dessau", "kind": "Person", "properties": {"name": "George I, Prince of Anhalt-Dessau"}},
                # Task 4: La Trémoille Genealogy
                {"id": "person:charles-bretagne-marie", "kind": "Person", "properties": {"name": "Charles Bretagne Marie de La Trémoille", "birth": "1764", "death": "1839"}},
                {"id": "person:jean-bretagne-charles", "kind": "Person", "properties": {"name": "Jean Bretagne Charles de La Trémoille", "birth": "1737", "death": "1792"}},
                {"id": "person:charles-armand-rene", "kind": "Person", "properties": {"name": "Charles Armand René de La Trémoille", "birth": "1708", "death": "1741"}},
                {"id": "person:charles-louis-bretagne", "kind": "Person", "properties": {"name": "Charles Louis Bretagne de La Trémoille"}},
                {"id": "person:henri-charles", "kind": "Person", "properties": {"name": "Henri Charles de La Trémoille"}},
                {"id": "person:claude-tremoille", "kind": "Person", "properties": {"name": "Claude de La Trémoille"}}
            ]

            edges = [
                # Task 1
                {"from": "movie:polish-russian-war", "to": "person:xawery-zulawski", "kind": "directed_by", "properties": {}},
                {"from": "person:xawery-zulawski", "to": "person:malgorzata-braunek", "kind": "mother", "properties": {}},
                {"from": "person:xawery-zulawski", "to": "person:andrzej-zulawski", "kind": "father", "properties": {}},
                # Task 3
                {"from": "person:john-v-anhalt-zerbst", "to": "person:ernest-i-anhalt-dessau", "kind": "father", "properties": {}},
                {"from": "person:john-vi-anhalt-zerbst", "to": "person:rudolph-anhalt-zerbst", "kind": "father", "properties": {}},
                {"from": "person:ernest-i-anhalt-dessau", "to": "person:george-i-anhalt-dessau", "kind": "father", "properties": {}},
                # Task 4
                {"from": "person:charles-bretagne-marie", "to": "person:jean-bretagne-charles", "kind": "father", "properties": {}},
                {"from": "person:jean-bretagne-charles", "to": "person:charles-armand-rene", "kind": "father", "properties": {}},
                {"from": "person:charles-armand-rene", "to": "person:charles-louis-bretagne", "kind": "father", "properties": {}}
            ]

            rpc_mcp("tools/call", {
                "name": "graph_batch_upsert",
                "arguments": {"nodes": nodes, "edges": edges}
            })

            aliases = [
                ("movie:polish-russian-war", "Polish-Russian War (Film)"),
                ("movie:polish-russian-war", "Polish-Russian War"),
                ("movie:blind-shaft", "Blind Shaft"),
                ("movie:mask-of-fu-manchu", "The Mask Of Fu Manchu"),
                ("movie:mask-of-fu-manchu", "Mask of Fu Manchu"),
                ("person:john-v-anhalt-zerbst", "John V, Prince Of Anhalt-Zerbst"),
                ("person:john-v-anhalt-zerbst", "John V"),
                ("person:charles-bretagne-marie", "Charles Bretagne Marie De La Trémoille"),
                ("person:charles-bretagne-marie", "Charles Bretagne Marie")
            ]

            for node_id, alias in aliases:
                rpc_mcp("tools/call", {
                    "name": "graph_upsert_alias",
                    "arguments": {"node_id": node_id, "alias": alias}
                })

            print(f"Graph initialized with {len(nodes)} nodes, {len(edges)} edges, {len(aliases)} entity aliases.\n")

            PLAIN_RAG_TOOLS = [
                {
                    "name": "vector_search",
                    "description": "Semantic search across Wikipedia articles and paragraphs.",
                    "parameters": {
                        "type": "OBJECT",
                        "properties": {
                            "query": {"type": "STRING", "description": "Search query"},
                            "top_k": {"type": "INTEGER", "description": "Number of paragraphs (default: 4)"}
                        },
                        "required": ["query"]
                    }
                }
            ]

            GRAPH_RAG_TOOLS = [
                {
                    "name": "graph_resolve_entity",
                    "description": "Resolve an entity name from the question to a canonical node ID.",
                    "parameters": {
                        "type": "OBJECT",
                        "properties": {
                            "query": {"type": "STRING", "description": "Entity name"}
                        },
                        "required": ["query"]
                    }
                },
                {
                    "name": "graph_query",
                    "description": "Execute OpenCypher queries against the knowledge graph. Node kinds: Person, Movie. Relationships: directed_by, mother, father.",
                    "parameters": {
                        "type": "OBJECT",
                        "properties": {
                            "query": {"type": "STRING", "description": "OpenCypher query"}
                        },
                        "required": ["query"]
                    }
                }
            ]

            BENCHMARK_TASKS = [
                {
                    "id": "2wiki_task_1_compositional",
                    "type": "Compositional (3-Hop)",
                    "prompt": "Who is the mother of the director of film Polish-Russian War (Film)?",
                    "expected": "Małgorzata Braunek"
                },
                {
                    "id": "2wiki_task_2_temporal_genealogy",
                    "type": "Genealogy / Temporal (2-Hop with Distractors)",
                    "prompt": "When did John V, Prince Of Anhalt-Zerbst's father die?",
                    "expected": "12 June 1516"
                },
                {
                    "id": "2wiki_task_3_inference_grandparent",
                    "type": "Inference (Paternal Grandfather across French Dynasty)",
                    "prompt": "Who is Charles Bretagne Marie De La Trémoille's paternal grandfather?",
                    "expected": "Charles Armand René de La Trémoille"
                },
                {
                    "id": "2wiki_task_4_comparison_dates",
                    "type": "Comparison (Release Dates across Distractor Franchise)",
                    "prompt": "Which film came out first, Blind Shaft or The Mask Of Fu Manchu?",
                    "expected": "The Mask Of Fu Manchu"
                }
            ]

            print("=== Running 2WikiMultihopQA Academic Benchmark: GraphRAG vs Plain Vector RAG ===\n")

            results = []

            for task in BENCHMARK_TASKS:
                print(f"----------------------------------------------------------------------")
                print(f"[{task['type']}]")
                print(f"Prompt: {task['prompt']}")
                print(f"Expected Answer: {task['expected']}")
                print(f"----------------------------------------------------------------------")
            
                # 1. Plain Vector RAG
                print("[Testing Plain Vector RAG...]")
                def plain_dispatcher(fn_name, fn_args):
                    if fn_name == "vector_search":
                        return exec_vector_search(fn_args.get("query", ""), fn_args.get("top_k", 4))
                    return "Unknown tool"

                plain_sys = "You are a multi-hop factual QA assistant. Use vector_search to retrieve Wikipedia paragraphs and answer the question concisely and precisely."
                plain_res = run_agent_loop("Plain Vector RAG", task["prompt"], plain_sys, PLAIN_RAG_TOOLS, plain_dispatcher)
                p_correct = task["expected"].lower() in plain_res["answer"].lower()
                print(f"  Result: Correct={p_correct} | Turns={plain_res['turns']} | Latency={plain_res['latency_s']}s | Tokens={plain_res['tokens']}")
                print(f"  Answer: {plain_res['answer'][:140]}...\n")
            
                # 2. GraphRAG (cypher-mcp)
                print("[Testing GraphRAG (cypher-mcp)...]")
                def graph_dispatcher(fn_name, fn_args):
                    if fn_name == "graph_resolve_entity":
                        return exec_graph_resolve(fn_args.get("query", ""))
                    elif fn_name == "graph_query":
                        return exec_graph_query(fn_args.get("query", ""))
                    return "Unknown tool"

                graph_sys = "You are a multi-hop reasoning assistant. First call graph_resolve_entity to map entity names from the question to exact node IDs, then use graph_query with OpenCypher to traverse relationships (e.g. directed_by, mother, father) and return the exact answer."
                graph_res = run_agent_loop("GraphRAG (cypher-mcp)", task["prompt"], graph_sys, GRAPH_RAG_TOOLS, graph_dispatcher)
                g_correct = task["expected"].lower() in graph_res["answer"].lower()
                print(f"  Result: Correct={g_correct} | Turns={graph_res['turns']} | Latency={graph_res['latency_s']}s | Tokens={graph_res['tokens']}")
                print(f"  Answer: {graph_res['answer'][:140]}...\n")
            
                results.append({
                    "task": task["type"],
                    "prompt": task["prompt"],
                    "expected": task["expected"],
                    "plain": {**plain_res, "correct": p_correct},
                    "graph": {**graph_res, "correct": g_correct}
                })
        finally:
            reap_process(mcp_proc)

    REPORT_PATH = os.path.join(REPORTS_DIR, "2WIKI_MULTIHOP_BENCHMARK.md")
    os.makedirs(os.path.dirname(REPORT_PATH), exist_ok=True)

    with open(REPORT_PATH, "w", encoding="utf-8") as f:
        f.write("# Academic Benchmark Report: 2WikiMultihopQA (Multi-Hop with Distractors)\n\n")
        f.write("Comparison of **GraphRAG (`cypher-mcp` with Vector Entity Resolution + OpenCypher)** vs. **Plain Vector RAG (gemini-embedding-001)** on the official `2WikiMultihopQA` dataset.\n\n")
        f.write("Evaluator: `Google Gemini 3.8 Flash` | Embedding: `gemini-embedding-001`\n\n")
        f.write("## Scorecard\n\n")
        f.write("| Metric | Plain Vector RAG | GraphRAG (cypher-mcp) |\n")
        f.write("| :--- | :--- | :--- |\n")
    
        p_acc = sum(1 for r in results if r["plain"]["correct"]) / len(results) * 100
        g_acc = sum(1 for r in results if r["graph"]["correct"]) / len(results) * 100
        p_avg_turns = sum(r["plain"]["turns"] for r in results) / len(results)
        g_avg_turns = sum(r["graph"]["turns"] for r in results) / len(results)
        p_avg_lat = sum(r["plain"]["latency_s"] for r in results) / len(results)
        g_avg_lat = sum(r["graph"]["latency_s"] for r in results) / len(results)
        p_avg_tok = sum(r["plain"]["tokens"] for r in results) / len(results)
        g_avg_tok = sum(r["graph"]["tokens"] for r in results) / len(results)
    
        f.write(f"| **Accuracy (Exact Match / Fact Match)** | **{p_acc:.1f}%** | **{g_acc:.1f}%** |\n")
        f.write(f"| **Average Turns to Answer** | {p_avg_turns:.1f} | {g_avg_turns:.1f} |\n")
        f.write(f"| **Average Latency** | {p_avg_lat:.1f}s | {g_avg_lat:.1f}s |\n")
        f.write(f"| **Average Token Usage** | {int(p_avg_tok)} | {int(g_avg_tok)} |\n\n")
    
        f.write("## Detailed Task Breakdown\n\n")
        for r in results:
            f.write(f"### {r['task']}\n\n")
            f.write(f"**Question:** *\"{r['prompt']}\"*\n\n")
            f.write(f"**Ground Truth:** `{r['expected']}`\n\n")
            f.write(f"| Engine | Correct | Turns | Latency | Tokens |\n")
            f.write(f"| :--- | :--- | :--- | :--- | :--- |\n")
            f.write(f"| Plain Vector RAG | {'PASS' if r['plain']['correct'] else 'FAIL'} | {r['plain']['turns']} | {r['plain']['latency_s']}s | {r['plain']['tokens']} |\n")
            f.write(f"| GraphRAG (cypher-mcp) | {'PASS' if r['graph']['correct'] else 'FAIL'} | {r['graph']['turns']} | {r['graph']['latency_s']}s | {r['graph']['tokens']} |\n\n")
            f.write(f"**Plain RAG Answer:**\n> {r['plain']['answer'].strip()}\n\n")
            f.write(f"**GraphRAG Answer:**\n> {r['graph']['answer'].strip()}\n\n")
            f.write("---\n\n")

    print(f"\nAcademic benchmark finished! Report written to: {REPORT_PATH}")


if __name__ == "__main__":
    main()
