#!/usr/bin/env python3
"""
Prototype: Vector Entity Resolution (Linking) on SQLite for cypher-mcp.

Maps colloquial forms, aliases, diminutives, and declensions:
  "Слава", "со Славой", "Славика", "Вячеслав", "Viacheslav" -> "person:viacheslav"
Directly in SQLite using cosine similarity.
"""

import os
import json
import math
import struct
import sqlite3
import urllib.request

env_path = r"C:\Users\viach\AppData\Local\hermes\.env"
with open(env_path, "r", encoding="utf-8") as f:
    for line in f:
        if line.startswith("GOOGLE_API_KEY="):
            api_key = line.split("=", 1)[1].strip().strip("\"'")
            break

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
        # Normalize vector for fast dot-product cosine similarity
        norm = math.sqrt(sum(x*x for x in vals))
        return [x / norm for x in vals]

def floats_to_blob(float_list):
    return struct.pack(f"{len(float_list)}f", *float_list)

def blob_to_floats(blob):
    count = len(blob) // 4
    return struct.unpack(f"{count}f", blob)

# Setup SQLite DB in memory or file
db_path = r"C:\Work\Personal\cypher-mcp\data\vector_entity_demo.db"
if os.path.exists(db_path):
    os.remove(db_path)

con = sqlite3.connect(db_path)
cur = con.cursor()

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
    ("person:viacheslav", "Person", json.dumps({"name": "Viacheslav Mikhailov", "role": "Architect"})),
    ("person:ekaterina", "Person", json.dumps({"name": "Ekaterina Mikhailova", "role": "Wife"})),
    ("person:timur", "Person", json.dumps({"name": "Timur Mikhailov", "role": "Son"})),
    ("vehicle:m-ew-330", "Vehicle", json.dumps({"plate": "M-EW 330", "model": "BMW 3 Series"}))
]
cur.executemany("INSERT INTO nodes (id, kind, properties) VALUES (?, ?, ?)", nodes_data)

edges_data = [
    ("person:viacheslav", "vehicle:m-ew-330", "OWNS", json.dumps({"since": "2023"})),
    ("person:viacheslav", "person:ekaterina", "MARRIED_TO", "{}"),
    ("person:viacheslav", "person:timur", "PARENT_OF", "{}")
]
cur.executemany("INSERT INTO edges (from_id, to_id, kind, properties) VALUES (?, ?, ?, ?)", edges_data)

# 4. Populate Aliases with Embeddings
aliases_to_index = [
    ("person:viacheslav", "Viacheslav Mikhailov"),
    ("person:viacheslav", "Вячеслав"),
    ("person:viacheslav", "Слава"),
    ("person:viacheslav", "Slava"),
    ("person:ekaterina", "Ekaterina Mikhailova"),
    ("person:ekaterina", "Катя"),
    ("person:ekaterina", "Екатерина"),
    ("person:timur", "Timur"),
    ("person:timur", "Тимур"),
    ("vehicle:m-ew-330", "BMW M-EW 330"),
    ("vehicle:m-ew-330", "БМВ тройка")
]

print("Indexing entity vectors into SQLite...")
for node_id, alias in aliases_to_index:
    vec = get_embedding(alias)
    blob = floats_to_blob(vec)
    cur.execute("INSERT INTO entity_embeddings (node_id, alias, embedding) VALUES (?, ?, ?)", (node_id, alias, blob))
con.commit()
print(f"Indexed {len(aliases_to_index)} aliases.\n")

# 5. Entity Resolution Query Function
def resolve_entity(query_text, top_k=2):
    q_vec = get_embedding(query_text)
    
    cur.execute("SELECT node_id, alias, embedding FROM entity_embeddings")
    candidates = []
    for node_id, alias, blob in cur.fetchall():
        cand_vec = blob_to_floats(blob)
        # Cosine similarity is simple dot product because vectors are normalized
        score = sum(a * b for a, b in zip(q_vec, cand_vec))
        candidates.append((score, node_id, alias))
    
    candidates.sort(reverse=True, key=lambda x: x[0])
    return candidates[:top_k]

# 6. Test various colloquial inputs
test_inputs = [
    "Слава",
    "со Славой",
    "Славика",
    "Вячеслав",
    "Катенька",
    "Тимурка",
    "Бэха"
]

print("=== Vector Entity Resolution Results ===")
for test_input in test_inputs:
    results = resolve_entity(test_input)
    top_score, top_node, top_alias = results[0]
    
    # Fetch canonical node from graph
    cur.execute("SELECT properties FROM nodes WHERE id = ?", (top_node,))
    props = json.loads(cur.fetchone()[0])
    name = props.get("name") or props.get("model") or top_node
    
    print(f"Input: \"{test_input}\"")
    print(f"  -> Matched Node: [{top_node}] ({name})")
    print(f"  -> Best Alias: \"{top_alias}\" | Similarity: {top_score:.4f}")
    print()

con.close()
