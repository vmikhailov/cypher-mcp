#!/usr/bin/env python3
"""
MetaQA Knowledge Graph Indexer for cypher-mcp
Downloads the MetaQA 135k-triple benchmark dataset and indexes it into
a SQLite graph database with `nodes` and `edges` compatible with cypher-mcp.
"""

import os
import sys
import time
import json
import sqlite3
import urllib.request

DATA_DIR = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "data")
DB_PATH = os.path.join(DATA_DIR, "metaqa.db")
KB_URL = "https://huggingface.co/datasets/camazlucas/MetaQA/raw/main/kb/kb.txt"

def index_metaqa():
    os.makedirs(DATA_DIR, exist_ok=True)
    if os.path.exists(DB_PATH):
        try:
            os.remove(DB_PATH)
        except Exception:
            pass

    print(f"Downloading MetaQA kb.txt from {KB_URL} ...")
    t0 = time.time()
    req = urllib.request.Request(KB_URL, headers={"User-Agent": "Mozilla/5.0"})
    with urllib.request.urlopen(req) as resp:
        data = resp.read().decode("utf-8")
    lines = [l.strip() for l in data.strip().split("\n") if l.strip()]
    dl_time = time.time() - t0
    print(f"Downloaded {len(lines):,} triples in {dl_time:.2f}s")

    print("Indexing into SQLite knowledge graph...")
    t1 = time.time()
    con = sqlite3.connect(DB_PATH)
    cur = con.cursor()
    cur.execute("PRAGMA synchronous = OFF;")
    cur.execute("PRAGMA journal_mode = MEMORY;")

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

    nodes = {}
    edges = []
    movie_props = {}

    def nid(kind, name):
        return f"{kind.lower()}:{name}"

    for line in lines:
        parts = line.split("|")
        if len(parts) != 3:
            continue
        head, rel, tail = parts[0].strip(), parts[1].strip(), parts[2].strip()

        m_id = nid("Movie", head)
        if m_id not in nodes:
            nodes[m_id] = ("Movie", head)
            movie_props[m_id] = {"name": head}

        if rel in ("release_year", "has_imdb_rating", "has_imdb_votes"):
            movie_props[m_id][rel] = tail
        elif rel in ("directed_by", "written_by", "starred_actors"):
            p_id = nid("Person", tail)
            if p_id not in nodes:
                nodes[p_id] = ("Person", tail)
            edges.append((m_id, p_id, rel, "{}"))
        elif rel == "has_genre":
            g_id = nid("Genre", tail)
            if g_id not in nodes:
                nodes[g_id] = ("Genre", tail)
            edges.append((m_id, g_id, rel, "{}"))
        elif rel == "in_language":
            l_id = nid("Language", tail)
            if l_id not in nodes:
                nodes[l_id] = ("Language", tail)
            edges.append((m_id, l_id, rel, "{}"))
        elif rel == "has_tags":
            t_id = nid("Tag", tail)
            if t_id not in nodes:
                nodes[t_id] = ("Tag", tail)
            edges.append((m_id, t_id, rel, "{}"))

    node_batch = []
    for n_id, (kind, name) in nodes.items():
        props = movie_props[n_id] if kind == "Movie" else {"name": name}
        node_batch.append((n_id, kind, json.dumps(props)))

    cur.executemany("INSERT INTO nodes (id, kind, properties) VALUES (?, ?, ?)", node_batch)
    cur.executemany("INSERT INTO edges (from_id, to_id, kind, properties) VALUES (?, ?, ?, ?)", edges)

    cur.execute("CREATE INDEX idx_nodes_kind ON nodes(kind);")
    cur.execute("CREATE INDEX idx_edges_from_id ON edges(from_id);")
    cur.execute("CREATE INDEX idx_edges_to_id ON edges(to_id);")
    cur.execute("CREATE INDEX idx_edges_kind ON edges(kind);")

    con.commit()
    con.close()

    idx_time = time.time() - t1
    size_mb = os.path.getsize(DB_PATH) / (1024 * 1024)
    print(f"Indexing complete in {idx_time:.2f}s!")
    print(f"  Nodes: {len(nodes):,}")
    print(f"  Edges: {len(edges):,}")
    print(f"  DB Size: {size_mb:.2f} MB")
    print(f"  Path: {DB_PATH}")

if __name__ == "__main__":
    index_metaqa()
