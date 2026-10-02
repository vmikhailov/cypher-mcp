# Cypher MCP

[![Release](https://img.shields.io/github/v/release/vmikhailov/cypher-mcp?color=blue)](https://github.com/vmikhailov/cypher-mcp/releases)
[![CI](https://github.com/vmikhailov/cypher-mcp/actions/workflows/ci.yml/badge.svg)](https://github.com/vmikhailov/cypher-mcp/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

A **Zero-CGO Model Context Protocol (MCP)** server that equips AI agents with an embedded SQLite Knowledge Graph
queried via declarative **OpenCypher** with sub-millisecond response times.

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

---

## MCP Tools

`cypher-mcp` exposes 5 standard MCP tools via JSON-RPC 2.0 (stdio):

| Tool | Mode | Description |
| :--- | :--- | :--- |
| `graph_query` | Read-only | Transpiles and executes OpenCypher against SQLite. Returns JSON records with compile and execution timing metrics. |
| `graph_schema` | Read-only | Summarizes graph topology: counts of nodes and edges by kind, and top property keys. |
| `graph_set_node` | Mutation | Upserts a node by unique `id`, `kind`, and dynamic JSON `properties`. |
| `graph_set_edge` | Mutation | Upserts a directed relationship between `from_id` and `to_id` with `kind` and `properties`. |
| `graph_delete_node` | Mutation | Deletes a node and cascade-cleans all its incoming and outgoing relationships. |

---

## Quick Start

### 1. Download Prebuilt Binary
Grab the latest static binary for your OS and architecture from [Releases](https://github.com/vmikhailov/cypher-mcp/releases):
* Linux (`x86_64`, `arm64`)
* macOS (`Apple Silicon arm64`, `Intel amd64`)
* Windows (`x86_64`)

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
mcp:
  servers:
    cypher_graph:
      command: "cypher-mcp"
      args: ["--db", "~/.hermes/knowledge_graph.db"]
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

## Building from Source

Requires Go 1.22+:

```bash
git clone https://github.com/vmikhailov/cypher-mcp.git
cd cypher-mcp
go build -ldflags="-s -w" -o bin/cypher-mcp .
```

To run tests:
```bash
go test -v ./...
```

---

## License

MIT License. Copyright (c) 2026 Viacheslav Mikhailov.
