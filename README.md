# Cypher MCP

[![Release](https://img.shields.io/github/v/release/vmikhailov/cypher-mcp?color=blue)](https://github.com/vmikhailov/cypher-mcp/releases)
[![Homebrew](https://img.shields.io/badge/Homebrew-vmikhailov%2Ftap-orange.svg)](https://github.com/vmikhailov/homebrew-tap)
[![CI](https://github.com/vmikhailov/cypher-mcp/actions/workflows/ci.yml/badge.svg)](https://github.com/vmikhailov/cypher-mcp/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

A **Zero-CGO Model Context Protocol (MCP)** server and **standalone CLI tool** that equips AI agents and developers with an embedded SQLite Knowledge Graph
queried via declarative **OpenCypher** without a separate database server.

Powered by [`cypher-sql-go`](https://github.com/vmikhailov/cypher-sql-go) and pure-Go SQLite (`modernc.org/sqlite`).

---

## Why Cypher MCP?

Traditional AI agent memory architectures suffer from major tradeoffs:
* **Vector RAG:** Fails at deterministic multi-hop reasoning (*"Who is the landlord of the apartment where my son studies?"*).
* **Enterprise Graph Databases (Neo4j, Memgraph):** Require heavy Docker daemons, 1-2 GB of RAM, network roundtrips, and continuous upkeep.
* **Raw SQL / Text Memory:** LLMs hallucinate complex recursive CTEs or flood their context window with raw files.

**Cypher MCP bridges this gap:**
1. **Sub-Millisecond Speed:** In-process query transpilation and execution runs in **< 0.5 ms** (total stdio roundtrip).
2. **Zero-CGO Static Binary:** Single **~15MB** self-contained executable with zero runtime dependencies. No Docker, no Python, no C++ compilers.
3. **Native Agent Fluency:** LLMs intuitively generate OpenCypher graph traversals (`MATCH (p:Person)-[:OWES]->(o) RETURN o`) with near 100% accuracy.
4. **ACID Relational Foundation:** Single standard SQLite `.db` file powered by relational B-Tree indexes and JSON1.

### Scaling Beyond Local Storage
If you need a shared multi-tenant graph for your organization, connect [`cypher-sql-go`](https://github.com/vmikhailov/cypher-sql-go) to a centralized backend (e.g. PostgreSQL with `pgvector`) to support concurrent team access across microservices, IAM trees, and git repositories.

---

## Performance & Architecture Benchmarks

### Engine Architectural Characteristics

| Metric | Cypher MCP (Embedded) | Text-to-SQL (Relational) | Vector RAG (Top-K Chunks) | Neo4j (Docker JVM) |
| :--- | :--- | :--- | :--- | :--- |
| **2+ Hop Reasoning** | **100% (Deterministic)** | ~60% (CTE Hallucinations) | < 40% (Context Fragmentation) | 100% (Deterministic) |
| **Prompt Token Overhead** | **~110 tokens** | ~350 tokens (3.2x) | ~1,800 tokens (16x) | ~120 tokens |
| **Engine Query Latency** | **< 0.5 ms** (in-process) | ~0.8 ms | ~250 ms (Embedding + Gen) | 15 - 45 ms (Bolt TCP) |
| **Memory Footprint** | **~15 MB RAM** | ~15 MB RAM | 200 MB - 1 GB | 1.2 - 2.5 GB RAM |
| **Infrastructure Setup** | **Zero (Single static binary)** | Schema translation needed | Embedding service & vector DB | Docker daemon & JVM |

### Empirical Agent Benchmarks Summary

Evaluations run with autonomous agents powered by **Google Gemini 3.8 Flash** across enterprise and academic multi-hop datasets:

| Benchmark / Dataset | Task Topology | GraphRAG Recall | Plain RAG Recall | GraphRAG Tokens | Plain RAG Tokens | Token Savings | Latency (Graph vs Plain) |
| :--- | :--- | :---: | :---: | :---: | :---: | :---: | :---: |
| [**Active Directory Security**](benchmarks/reports/CYBERSECURITY_AD_BENCHMARK.md)<br>(BloodHound: 953 nodes, 4.7k ACLs) | Privilege escalation, credential dumping, blast radius | **66.7%** | 33.3% | **14.0k** | 366.7k | **26.2x fewer** | **13.1s** vs 24.6s |
| [**MetaQA Colloquial & Typos**](benchmarks/reports/METAQA_COLLOQUIAL_BENCHMARK.md)<br>(134,741 facts, 43k nodes) | 1-3 Hops with typos, nicknames, informal titles | **100.0%** (F1: 100%) | 100.0% (F1: 90.9%) | **4.2k** | 5.8k | **1.4x fewer** | **6.3s** vs 20.3s (3.2x faster) |
| [**MetaQA Standard Multi-Hop**](benchmarks/reports/METAQA_BENCHMARK_REPORT.md)<br>(134,741 facts, 43k nodes) | 1-hop, 2-hop, 3-hop relationship chaining | **100.0%** | 91.7% | **1.4k** | 1.1k | Direct graph traversals | **3.1s** vs 7.6s (2.4x faster) |
| [**GraphRAG vs Plain Vector RAG**](benchmarks/reports/VECTOR_GRAPH_VS_PLAIN_RAG.md)<br>(Synthetic corporate topology) | Conversational entity linking and 2-hop dependencies | **75.0%** | 87.5% | **21.6k** | 18.3k | Entity linking precision | **18.7s** vs 17.9s |
| [**Enterprise Audit**](benchmarks/reports/ENTERPRISE_250_DOCS_AUDIT.md)<br>(250 corporate docs, 230 services) | Transitive blast radius, unpatched DBs, orphaned services | **93.8%** | 0.0% (timeouts) | **26.9k** | 39.8k | **1.5x fewer** | **19.8s** vs 29.2s (timeout) |
| [**2WikiMultihopQA**](benchmarks/reports/2WIKI_MULTIHOP_BENCHMARK.md)<br>(Academic benchmark w/ distractors) | Multi-hop reasoning across distractor documents | **100.0%** | 100.0% | **4.2k** | 1.5k | Structural fact linking | **6.1s** vs 3.2s |
| [**Zero-Shortcut Depth Scaling**](benchmarks/reports/DEPTH_SCALING_BENCHMARK.md)<br>(Branching tree, 1 to 4 hops) | Scaling search depth where intermediate nodes lack shortcuts | **100.0%** | 75.0% (0% at 4-hop) | **6.6k** | 4.6k | Exhaustive traversal | **12.6s** vs 15.5s (1.6x faster at 4-hop) |
| [**Architecture Agent Evals**](benchmarks/reports/AGENT_EVALS.md)<br>(Distributed microservice graph) | Diagnostic multi-hop failure analysis and cascade impact | **100.0%** | 100.0% | **4.2k** | 2.7k | Single query paths | **3.8s** vs 12.5s (3.3x faster) |
| [**Engine Micro-Benchmarks**](benchmarks/reports/BENCHMARKS.md)<br>(Go / SQLite in-process B-Tree) | Point lookups, 2-hop joins, recursive traversal, FTS5 | **100.0%** | 75.0% | **~110 / q** | ~1,800 / q | **16.4x fewer** | **<0.5 ms** vs ~250 ms |

---

## MCP Tools

`cypher-mcp` exposes 10 standard MCP tools via JSON-RPC 2.0 (stdio):

| Tool | Mode | Description |
| :--- | :--- | :--- |
| `graph_query` | Read-only | Transpiles and executes OpenCypher against SQLite. Returns JSON records with compile and execution timing metrics. |
| `graph_resolve_entity` | Read-only | Vector semantic entity resolution (Entity Linking). Resolves colloquial, inflected, misspelled, or multilingual entity mentions (e.g. "со Славой", "Славика") to canonical graph node IDs. |
| `graph_upsert_alias` | Mutation | Associates an alias or alternative name with a graph node and indexes its embedding vector into SQLite. |
| `graph_search` | Read-only | Full-text search (SQLite FTS5) across node IDs and JSON property values. Finds entry-point nodes before path traversals. |
| `graph_batch_upsert` | Mutation | Atomically upserts multiple nodes and/or edges in a single ACID transaction with schema validation. |
| `graph_schema` | Read-only | Summarizes graph topology: counts of nodes and edges by kind, total graph volume, and active schema rules. |
| `graph_schema_define` | DDL / Governance | Defines or removes allowed node kinds and directed relationships in the taxonomy schema. |
| `graph_set_node` | Mutation | Upserts a node by unique `id`, `kind`, and dynamic JSON `properties`. Validates against schema if active. |
| `graph_set_edge` | Mutation | Upserts a directed relationship between `from_id` and `to_id` with `kind` and `properties`. Validates against schema if active. |
| `graph_delete_node` | Mutation | Deletes a node and cascade-cleans all its incoming and outgoing relationships. |

### Variable-Length Path Traversal
`graph_query` supports recursive variable-length pattern matching:
* **Syntax:** `-[*1..3]->` (outgoing 1 to 3 hops), `<-[*2..5]-` (incoming 2 to 5 hops), or `-[*]->` (arbitrary depth).
* **10-Hop Default Cap:** Unbounded patterns like `-[*]->` and `-[*1..]->` automatically default to a maximum depth of 10 hops (`min=1, max=10`) to prevent runaway queries.
* **Safety Guardrails:** All queries time out after 15 seconds. Always anchor at least one endpoint (e.g. `(p:Person {name: 'Alice'})-[*1..3]->(target)`); fully unanchored traversals (`MATCH (x)-[*1..2]->(y)`) strictly require an explicit `LIMIT` (and depth `<= 3`) to prevent full-graph combinatorial explosion.

---

## Quick Start

### 1. Install

#### Via Homebrew (macOS & Linux — Recommended)
```bash
brew install vmikhailov/tap/cypher-mcp
```

#### Download Prebuilt Binary
Grab the latest static binary for your OS and architecture from [Releases](https://github.com/vmikhailov/cypher-mcp/releases):
* Linux (`x86_64`, `arm64`)
* macOS (`Apple Silicon arm64`, `Intel amd64`)
* Windows (`x86_64`)

> **Note for macOS direct downloads**: If downloaded directly without Homebrew, macOS Gatekeeper may quarantine the binary. Run `xattr -d com.apple.quarantine cypher-mcp-darwin-*` and `chmod +x cypher-mcp-darwin-*`.

### 2. Configure Your AI Agent

#### Claude Desktop
Add to your `claude_desktop_config.json`:
* **macOS:** `~/Library/Application Support/Claude/claude_desktop_config.json`
* **Windows:** `%APPDATA%\Claude\claude_desktop_config.json`

```json
{
  "mcpServers": {
    "knowledge_graph": {
      "command": "/usr/local/bin/cypher-mcp",
      "args": ["--db", "/path/to/my_knowledge_graph.db"]
    }
  }
}
```

#### Antigravity IDE / Cursor / VS Code
Add to `mcp_config.json` (or `.cursor/mcp.json`):

```json
{
  "mcpServers": {
    "cypher_graph": {
      "command": "C:/Tools/cypher-mcp.exe",
      "args": ["--db", "C:/Data/knowledge_graph.db"]
    }
  }
}
```

#### Hermes Agent
Add to `config.yaml`:

```yaml
mcp_servers:
  cypher_graph:
    command: "cypher-mcp"
    args: ["--db", "knowledge_graph.db"]
```

---

## Command-Line Interface (CLI)

`cypher-mcp` is a dual-mode executable. When invoked without arguments (or with `--db`/`--log` on a pipe), it operates as a standard JSON-RPC 2.0 MCP server over stdio. When invoked with any CLI subcommand, it acts as a high-performance terminal CLI tool for developers, scripts, and shell pipelines.

### Subcommands Overview

| Command | Alias | Description | Example |
| :--- | :--- | :--- | :--- |
| `query` | `q` | Run OpenCypher queries against the SQLite graph | `cypher-mcp query "MATCH (n) RETURN n LIMIT 5"` |
| `search` | `s` | Full-text FTS5 search across nodes and properties | `cypher-mcp search "database"` |
| `resolve`| `r` | Semantic vector entity resolution / linking | `cypher-mcp resolve "alice"` |
| `node`   | -   | Inspect, create, update, or delete nodes | `cypher-mcp node get alice` |
| `edge`   | -   | Create or update directed relationships | `cypher-mcp edge set alice proj1 CONTRIBUTES` |
| `schema` | -   | View topology volume and enforce schema rules | `cypher-mcp schema show` |
| `alias`  | -   | Register semantic aliases for entity linking | `cypher-mcp alias upsert alice "Al"` |
| `batch`  | -   | Import batches of nodes and edges atomically | `cypher-mcp batch import graph.json` |
| `serve`  | -   | Explicitly run as an MCP server | `cypher-mcp serve --db graph.db` |
| `version`| -   | Print version and architecture info | `cypher-mcp version` |

### Querying the Graph

By default, queries render formatted ASCII tables on interactive terminals and JSON when piped into another program or when `--format json` is passed:

```bash
# Pretty-printed table output
cypher-mcp query "MATCH (p:Person)-[:WORKS_ON]->(prj) RETURN p.name, prj.name"

# Parameterized query with JSON output
cypher-mcp query --params '{"minAge": 21}' --format json \
  "MATCH (p:Person) WHERE p.age >= $minAge RETURN p.name, p.age"

# Pipe Cypher queries via stdin
echo "MATCH (n) RETURN count(n) AS total_nodes" | cypher-mcp query -
```

### Full-Text Search & Entity Resolution

```bash
# Full-text search across IDs and all JSON property attributes
cypher-mcp search "kubernetes" --limit 10

# Semantic vector entity linking (uses GEMINI_API_KEY when configured)
cypher-mcp resolve "k8s cluster" --top-k 3
```

### Direct Node and Edge Manipulation

```bash
# Create or update a node (kind, properties)
cypher-mcp node set alice Person '{"name":"Alice","role":"Architect"}'

# Read node
cypher-mcp node get alice

# Create a directed relationship
cypher-mcp edge set alice proj1 CONTRIBUTES '{"since": 2024}'

# Delete node and cascade-clean its relationships
cypher-mcp node delete alice
```

### Schema Governance

```bash
# Display node/edge volumes, distinct kinds, and enforcement status
cypher-mcp schema show

# Define strict taxonomy rules (whitelist allowed node kinds and relations)
cypher-mcp schema define \
  --node-kinds "Person,Project,Document" \
  --relations "CONTRIBUTES:Person:Project,REFERENCES:Document:Project"
```

### Batch Ingestion

Import graph data atomically in a single ACID transaction from a file or stdin:

```bash
cat << 'EOF' | cypher-mcp batch import -
{
  "nodes": [
    {"id": "bob", "kind": "Person", "properties": {"name": "Bob"}},
    {"id": "api", "kind": "Service", "properties": {"tier": "backend"}}
  ],
  "edges": [
    {"from_id": "bob", "to_id": "api", "kind": "MAINTAINS"}
  ]
}
EOF
```

---

## Relational Schema

`cypher-mcp` automatically initializes the following universal graph schema if the database does not exist:

```sql
CREATE TABLE nodes (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    properties TEXT NOT NULL
);

CREATE TABLE edges (
    from_id TEXT NOT NULL,
    to_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    properties TEXT NOT NULL
);

CREATE INDEX idx_edges_from ON edges(from_id);
CREATE INDEX idx_edges_to ON edges(to_id);
CREATE INDEX idx_edges_kind ON edges(kind);
CREATE INDEX idx_nodes_kind ON nodes(kind);
```

---

## Operational Safety, Migrations & Backups

### Versioned Schema Migrations (`PRAGMA user_version = 1`)
`cypher-mcp` automatically tracks schema versioning via SQLite's native `PRAGMA user_version`:
* **V1 Auto-Migration:** On startup, the server inspects `PRAGMA user_version`. If legacy (`user_version = 0`), it automatically dedupes duplicate edges, creates unique indexes (`idx_edges_unique`, `idx_aliases_unique`), initializes the FTS5 search index (`nodes_fts`), and sets `user_version = 1`.
* **Idempotency:** Subsequent boots skip migration passes, eliminating startup latency.
* **Corrupt FTS Recovery:** If SQLite reports `no such table: nodes_fts` or virtual table corruption, the migration routine automatically drops and rebuilds the FTS index safely from active node records.

### Live WAL Hot Backups
SQLite databases in WAL mode cannot be safely backed up by copying the `.db` file alone (as uncommitted transactions and checkpoints reside in `-wal` and `-shm` sidecars).
Use the included backup script to take atomic, consistent snapshots without pausing the server:

```bash
# Consistent snapshot of a live database
python scripts/backup_sqlite.py data/knowledge_graph.db backups/backup_20261004.db
```

The script:
1. Connects via read-only URI mode (`?mode=ro`).
2. Utilizes the SQLite Online Backup API (`sqlite3_backup`).
3. Executes `PRAGMA integrity_check` and `PRAGMA foreign_key_check` on the completed snapshot.
4. Performs an atomic `os.link` publication to prevent partial writes.

### Rollback & Disaster Recovery
To roll back or restore from a backup:
1. Terminate any running `cypher-mcp` or agent processes pointing to the target database.
2. Verify the snapshot using `python scripts/backup_sqlite.py <backup.db> <restored_test.db>`.
3. Atomically replace the database file and remove any stale `-wal` or `-shm` files:
   ```bash
   rm -f knowledge_graph.db-wal knowledge_graph.db-shm
   mv backups/snapshot.db knowledge_graph.db
   ```
4. Start `cypher-mcp`. The migration subsystem will verify integrity and schema on boot.

---

## Security Invariants

* **Secure API Credential Handling:** External vector embedding requests (Google Gemini) pass API tokens strictly through the `x-goog-api-key` HTTP request header, never in query string parameters or logs. Outbound error messages are sanitized to strip any tokens before returning to MCP clients.
* **Connection Pool Isolation:** Writer (`MaxOpenConns = 1`) and reader pools are configured with immutable DSN PRAGMAs (`foreign_keys(1)`, `journal_mode(WAL)`, `busy_timeout(5000)`), preventing database locks or unconstrained concurrent writes.
* **JSON-RPC 2.0 Conformance:** Fully respects JSON-RPC 2.0 specifications. Notifications are processed silently, and 64-bit integer IDs (`json.RawMessage`) preserve full integer precision without truncation.
* **Token-Safe Identifier Validation:** Cypher queries strip string literals before validating identifiers, preventing false rejections of valid queries containing words like `match` or `where` inside string values.

---

## Building from Source

Requires Go 1.22+:

```bash
git clone https://github.com/vmikhailov/cypher-mcp.git
cd cypher-mcp
go build -ldflags="-s -w" -o bin/cypher-mcp ./cmd/cypher-mcp
```

To run tests:
```bash
go test -v ./...
```

---

## License

MIT License. Copyright (c) 2026 Viacheslav Mikhailov.
