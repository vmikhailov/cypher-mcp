# Cypher MCP Server: Roadmap & TODO List

This document tracks security, architectural, and data integrity improvements for `cypher-mcp`.

---

## 1. Security & Integrity

### 1.1 Read-Only Enclosure for Queries (`graph_query`)
- **Status:** Completed
- **Description:** `handleGraphQuery` runs on a dedicated read-only `*sql.DB` connection with `PRAGMA query_only = ON;` and `_query_only=1`.

### 1.2 Parameter Binding in `graph_query`
- **Status:** Completed
- **Description:** Allow callers to pass parameters (`params`) in `graph_query`. Query is compiled with `cyphersql.CompileWithParams(query, params)` and bound to `db.Query` using `sql.Named`.

### 1.3 Atomic Edge Upserts & Unique Constraints
- **Status:** Completed
- **Description:** Edge mutations are wrapped in an atomic database transaction (`tx, err := db.Begin()`) and enforced by a `UNIQUE(from_id, to_id, kind)` index on `edges`.

### 1.4 Node Existence & Foreign Key Validation (Prevent Dangling Edges)
- **Status:** Completed
- **Description:** Both source (`from`) and target (`to`) nodes must exist in `nodes` before an edge can be created, returning descriptive errors if either node is missing.

### 1.5 Binary Path Alignment in MCP Configuration
- **Status:** Completed
- **Description:** Global MCP client configuration (`mcp_config.json`) points directly to `C:\Work\Personal\cypher-mcp\bin\cypher-mcp.exe`.

---

## 2. Checklist

- [x] 1. Forward and bind `params` in `handleGraphQuery` via `CompileWithParams` and `sql.Named`.
- [x] 2. Wrap `handleSetEdge` in an atomic database transaction.
- [x] 3. Validate existence of `from` and `to` nodes before creating edges.
- [x] 4. Add `UNIQUE(from_id, to_id, kind)` index on `edges`.
- [x] 5. Update `mcp_config.json` binary path to `C:\Work\Personal\cypher-mcp\bin\cypher-mcp.exe`.
