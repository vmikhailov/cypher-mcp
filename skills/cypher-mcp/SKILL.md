---
name: cypher-mcp
description: Read, query, and maintain shared persistent memory in a local SQLite knowledge graph via cypher-mcp (MCP tools or CLI). Use when recalling people, projects, documents, contracts, preferences, or past decisions across tools and sessions, or when persisting durable structured facts and relationships.
---

# Cypher MCP Knowledge Graph Skill

Use `cypher-mcp` as a shared, structured memory layer across tools (IDE agents, CLI agents, personal assistants). Store connected entities and relationships in the local SQLite graph instead of flat markdown notes.

## When to Use

- **Recall before asking:** When the user mentions a person, project, company, document, contract, vehicle, account, policy, or past decision that may already exist in memory, check the graph first instead of asking the user to repeat themselves.
- **Persist durable facts:** When an important decision, contact, project milestone, preference, or document metadata is established and will matter in future sessions or other tools, save it to the graph.
- **Skip for ephemeral noise:** Do not store temporary scratch thoughts, raw log dumps, or one-off code snippets that belong in git.

## Read Protocol (Schema -> Locate -> Traverse)

Always follow this three-step sequence when reading from an unfamiliar graph:

1. **Inspect schema first (`graph_schema`):**
   - Call `graph_schema` once per session to see active node kinds, relationship types, counts, and whether strict schema enforcement is enabled.
   - Never guess node labels or edge types.

2. **Find entry-point node IDs (`graph_search` or `graph_resolve_entity`):**
   - Use `graph_search` for exact keywords, substrings, IDs, or property values (FTS5 trigram index).
   - Use `graph_resolve_entity` for nicknames, abbreviations, typos, or colloquial/multilingual mentions (vector similarity over registered aliases).

3. **Traverse with Cypher (`graph_query`):**
   - Anchor at least one endpoint by `id`, label, or property and pass `params` for user-supplied values.
   - Example:
     ```cypher
     MATCH (p:Person {id: $id})-[r*1..2]-(connected)
     RETURN p, connected
     LIMIT 50
     ```
   - `graph_query` is **read-only**. Queries time out after 15 seconds, and unbounded `[*]` caps at 10 hops. Fully unanchored traversals require an explicit `LIMIT` and depth `<= 3`.

## Write Protocol (Deduplicate -> Schema -> Upsert)

1. **Deduplicate before creating:**
   - Run `graph_search` (and `graph_resolve_entity` if aliases are used) before adding a new node so you reuse the existing canonical `id` instead of creating duplicates.

2. **Follow existing ID conventions:**
   - Match the repository/graph's naming style — typically lowercase `prefix:slug` (e.g., `person:alice_smith`, `project:billing_api`, `doc:lease_2026`).

3. **Respect and evolve the schema (`graph_schema_define`):**
   - Reuse existing node kinds and edge relations whenever they fit.
   - Only add a new kind (`add_kind`) or relation (`add_relation`) via `graph_schema_define` when modeling a genuinely distinct concept. Always include a short `description` explaining the rule.
   - Kind and relation identifiers must be ASCII alphanumeric + underscore (case-insensitive unique).

4. **Write atomically and preserve properties:**
   - Use `graph_batch_upsert` when creating multiple related nodes and edges together (executes in a single ACID transaction; rolls back completely on validation failure).
   - Use `graph_set_node` and `graph_set_edge` for single updates. `merge` defaults to `true`, preserving existing properties; pass `merge: false` only when intentionally replacing all properties.
   - Use `graph_delete_node` only when an entity is truly obsolete (cascades deletion of all connected edges).

5. **Index aliases for recall (`graph_upsert_alias`):**
   - When an entity has nicknames, acronyms, or alternate names (e.g., `"Alex"`, `"Al"` for `person:alexander`), register them with `graph_upsert_alias` so future lookups resolve reliably.

## CLI Fallback

If MCP tools are not connected in the current environment, use the `cypher-mcp` CLI directly against the same SQLite database (`--db <path>`):

```bash
cypher-mcp schema show --db graph.db
cypher-mcp search "Alice" --db graph.db
cypher-mcp resolve "Al" --db graph.db
cypher-mcp query --db graph.db "MATCH (p:Person)-[:WORKS_ON]->(prj) RETURN p.name, prj.name LIMIT 20"
cypher-mcp node set person:alice Person '{"name":"Alice"}' --db graph.db
cypher-mcp edge set person:alice project:api OWNS --db graph.db
```
