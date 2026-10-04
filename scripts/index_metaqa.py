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

from contextlib import closing
import os
from pathlib import Path
import tempfile


def check_wal_safety(target_path: Path):
    wal_file = target_path.with_name(target_path.name + "-wal")
    if wal_file.exists() and wal_file.stat().st_size > 0:
        raise RuntimeError(f"Cannot overwrite database with active WAL journal: {wal_file}")


def validate_metaqa(target_db):
    """Check graph schema and populated MetaQA content without writing to it."""
    uri = Path(target_db).absolute().as_uri() + "?mode=ro"
    try:
        with closing(sqlite3.connect(uri, uri=True)) as con:
            if con.execute("PRAGMA quick_check").fetchone() != ("ok",):
                raise ValueError("SQLite integrity check failed")
            con.execute("SELECT id, kind, properties FROM nodes LIMIT 1")
            con.execute("SELECT from_id, to_id, kind, properties FROM edges LIMIT 1")
            if not con.execute("SELECT 1 FROM nodes WHERE kind = 'Movie' LIMIT 1").fetchone():
                raise ValueError("MetaQA graph has no movies")
            if not con.execute("SELECT 1 FROM edges LIMIT 1").fetchone():
                raise ValueError("MetaQA graph has no relationships")
            if con.execute("SELECT 1 FROM sqlite_master WHERE type='table' AND name='facts_fts'").fetchone():
                con.execute("SELECT fact FROM facts_fts LIMIT 1")
    except sqlite3.Error as exc:
        raise ValueError(f"Invalid MetaQA database at {target_db}: {exc}") from exc


def ensure_metaqa(target_db=None, *, overwrite=False):
    target = target_db or DB_PATH
    if os.path.exists(target):
        if not overwrite:
            validate_metaqa(target)
            return target
    print(f"MetaQA database not found or rebuild requested at {target}. Downloading and building...")
    index_metaqa(target, overwrite=overwrite)
    return target

def index_metaqa(target_db=None, *, overwrite=False):
    target_path = Path(target_db or DB_PATH).resolve()
    target = str(target_path)
    target_path.parent.mkdir(parents=True, exist_ok=True)
    if os.path.lexists(target):
        if not overwrite:
            raise FileExistsError(f"Refusing to overwrite {target}; use overwrite=True explicitly")
        check_wal_safety(target_path)

    fd, tmp_file = tempfile.mkstemp(prefix=f".{target_path.name}.", suffix=".tmp", dir=str(target_path.parent))
    os.close(fd)

    try:
        print(f"Downloading MetaQA kb.txt from {KB_URL} ...")
        t0 = time.time()
        req = urllib.request.Request(KB_URL, headers={"User-Agent": "Mozilla/5.0"})
        with urllib.request.urlopen(req, timeout=30.0) as resp:
            data = resp.read().decode("utf-8")
        lines = [l.strip() for l in data.strip().split("\n") if l.strip()]
        dl_time = time.time() - t0
        print(f"Downloaded {len(lines):,} triples in {dl_time:.2f}s")

        print("Indexing into SQLite knowledge graph...")
        t1 = time.time()
        with closing(sqlite3.connect(tmp_file)) as con:
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

            cur.execute("CREATE VIRTUAL TABLE IF NOT EXISTS facts_fts USING fts5(fact);")
            cur.executemany("INSERT INTO facts_fts (fact) VALUES (?)", [(line.strip(),) for line in lines if line.strip()])

            con.commit()

        validate_metaqa(tmp_file)
        if not overwrite and os.path.lexists(target):
            raise FileExistsError(f"Refusing to overwrite concurrently created target {target}")
        os.replace(tmp_file, target)
    finally:
        if os.path.exists(tmp_file):
            try:
                os.remove(tmp_file)
            except Exception:
                pass

    idx_time = time.time() - t1
    size_mb = os.path.getsize(target) / (1024 * 1024)
    print(f"Indexing complete in {idx_time:.2f}s!")
    print(f"  Nodes: {len(nodes):,}")
    print(f"  Edges: {len(edges):,}")
    print(f"  DB Size: {size_mb:.2f} MB")
    print(f"  Path: {target}")
    return target

def main(argv=None):
    import argparse
    parser = argparse.ArgumentParser(description="MetaQA Knowledge Graph Indexer for cypher-mcp")
    parser.add_argument("target_db", nargs="?", default=None, help="Target SQLite database path")
    parser.add_argument("--target", dest="target_flag", default=None, help="Explicit target database path")
    parser.add_argument("--overwrite", action="store_true", help="Overwrite existing target database")
    args = parser.parse_args(argv)
    target = args.target_flag or args.target_db
    return index_metaqa(target, overwrite=args.overwrite)

if __name__ == "__main__":
    main()
