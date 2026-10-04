#!/usr/bin/env python3
"""
Prototype: Vector Entity Resolution (Linking) on SQLite for cypher-mcp.

Maps colloquial forms, aliases, diminutives, and declensions:
  "Саша", "со Сашей", "Санька", "Александр", "Alex" -> "person:alexander"
Directly in SQLite using cosine similarity.
"""

import os
import sys
import json
import math
import struct
import sqlite3
import urllib.request

sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "benchmarks")))
from common import get_api_key, get_paths, isolated_db

def get_embedding(text, embed_url):
    payload = {
        "model": "models/gemini-embedding-001",
        "content": {"parts": [{"text": text}]}
    }
    data = json.dumps(payload).encode("utf-8")
    req = urllib.request.Request(embed_url, data=data, headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req) as resp:
        res = json.loads(resp.read().decode("utf-8"))
        vals = res["embedding"]["values"]
        # Normalize vector for fast dot-product cosine similarity
        norm = math.sqrt(sum(x*x for x in vals))
        return [x / norm for x in vals]

def floats_to_blob(float_list):
    return struct.pack(f"{len(float_list)}f", *float_list)

def blob_to_floats(blob):
    count = len(blob) // 4
    return struct.unpack(f"{count}f", blob)

def resolve_entity(cur, embed_url, query_text, top_k=2):
    q_vec = get_embedding(query_text, embed_url)
    
    cur.execute("SELECT node_id, alias, embedding FROM entity_embeddings")
    candidates = []
    for node_id, alias, blob in cur.fetchall():
        cand_vec = blob_to_floats(blob)
        # Cosine similarity is simple dot product because vectors are normalized
        score = sum(a * b for a, b in zip(q_vec, cand_vec))
        candidates.append((score, node_id, alias))
    
    candidates.sort(reverse=True, key=lambda x: x[0])
    return candidates[:top_k]

def main():
    api_key = get_api_key()
    if not api_key:
        print("Error: GOOGLE_API_KEY not found in environment or .env file.")
        sys.exit(1)

    REPO_ROOT, BIN_PATH, DATA_DIR, REPORTS_DIR = get_paths()
    embed_url = f"https://generativelanguage.googleapis.com/v1beta/models/gemini-embedding-001:embedContent?key={api_key}"

    with isolated_db(prefix="vector_entity_demo_") as db_path:
        con = sqlite3.connect(db_path)
        cur = con.cursor()
        try:
            # 1. Graph tables
            cur.execute("""
            CREATE TABLE nodes (
                id TEXT PRIMARY KEY,
                kind TEXT NOT NULL,
                properties JSON NOT NULL
            );
            """)
            cur.execute("""
            CREATE TABLE edges (
                from_id TEXT NOT NULL,
                to_id TEXT NOT NULL,
                kind TEXT NOT NULL,
                properties JSON NOT NULL
            );
            """)

            # 2. Vector Entity Resolution table
            cur.execute("""
            CREATE TABLE entity_embeddings (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                node_id TEXT NOT NULL,
                alias TEXT NOT NULL,
                embedding BLOB NOT NULL
            );
            """)
            cur.execute("CREATE INDEX idx_vec_node_id ON entity_embeddings(node_id);")

            # 3. Populate sample graph
            nodes_data = [
                ("person:alexander", "Person", json.dumps({"name": "Alexander Ivanov", "role": "Architect"})),
                ("person:elena", "Person", json.dumps({"name": "Elena Ivanova", "role": "Engineer"})),
                ("person:dmitry", "Person", json.dumps({"name": "Dmitry Ivanov", "role": "Analyst"})),
                ("vehicle:car-01", "Vehicle", json.dumps({"plate": "M-AB 123", "model": "BMW 3 Series"}))
            ]
            cur.executemany("INSERT INTO nodes (id, kind, properties) VALUES (?, ?, ?)", nodes_data)

            edges_data = [
                ("person:alexander", "vehicle:car-01", "OWNS", json.dumps({"since": "2023"})),
                ("person:alexander", "person:elena", "WORKS_WITH", "{}"),
                ("person:alexander", "person:dmitry", "COLLABORATES", "{}")
            ]
            cur.executemany("INSERT INTO edges (from_id, to_id, kind, properties) VALUES (?, ?, ?, ?)", edges_data)

            # 4. Populate Aliases with Embeddings
            aliases_to_index = [
                ("person:alexander", "Alexander Ivanov"),
                ("person:alexander", "Александр"),
                ("person:alexander", "Саша"),
                ("person:alexander", "Alex"),
                ("person:elena", "Elena Ivanova"),
                ("person:elena", "Лена"),
                ("person:elena", "Елена"),
                ("person:dmitry", "Dmitry"),
                ("person:dmitry", "Дима"),
                ("vehicle:car-01", "BMW M-AB 123"),
                ("vehicle:car-01", "БМВ тройка")
            ]

            print("Indexing entity vectors into SQLite...")
            for node_id, alias in aliases_to_index:
                vec = get_embedding(alias, embed_url)
                blob = floats_to_blob(vec)
                cur.execute("INSERT INTO entity_embeddings (node_id, alias, embedding) VALUES (?, ?, ?)", (node_id, alias, blob))
            con.commit()
            print(f"Indexed {len(aliases_to_index)} aliases.\n")

            # 6. Test various colloquial inputs
            test_inputs = [
                "Саша",
                "со Сашей",
                "Санька",
                "Александр",
                "Леночка",
                "Димка",
                "Бэха"
            ]

            print("=== Vector Entity Resolution Results ===")
            for test_input in test_inputs:
                results = resolve_entity(cur, embed_url, test_input)
                top_score, top_node, top_alias = results[0]
                
                # Fetch canonical node from graph
                cur.execute("SELECT properties FROM nodes WHERE id = ?", (top_node,))
                props = json.loads(cur.fetchone()[0])
                name = props.get("name") or props.get("model") or top_node
                
                print(f"Input: \"{test_input}\"")
                print(f"  -> Matched Node: [{top_node}] ({name})")
                print(f"  -> Best Alias: \"{top_alias}\" | Similarity: {top_score:.4f}")
                print()
        finally:
            con.close()

if __name__ == "__main__":
    main()
