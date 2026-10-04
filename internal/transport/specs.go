package transport

var ServerTools = []map[string]any{
	{
		"name":        "graph_query",
		"description": "Execute an OpenCypher query against the knowledge graph and return structured results. Supports variable-length paths like -[*1..3]-> or <-[*1..5]-. Unbounded [*] and [*1..] default to a 10-hop maximum. Always anchor at least one endpoint (with labels or properties) or use LIMIT to avoid full-graph scans (queries time out after 15s).",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "OpenCypher query (e.g., MATCH (p:Person)-[:LIVES_AT]->(a:Apartment) RETURN p, a)",
				},
				"params": map[string]any{
					"type":        "object",
					"description": "Optional parameters to pass to the query (e.g., {\"name\": \"Alice\"})",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Optional maximum number of rows to return (default: server limit of 1000, 0 = unlimited/default)",
				},
			},
			"required": []string{"query"},
		},
	},
	{
		"name":        "graph_search",
		"description": "Full-text keyword and substring search across nodes using SQLite FTS5 (trigram-indexed). Searches node IDs and JSON property values (names, addresses, notes, etc.) across languages without strict syntax restrictions. Returns an empty array if no match.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Keywords or substrings to search for (e.g. 'Франциска', 'Михайлов', 'BMW', 'M-EW 330', 'KIT Timur')",
				},
				"kind": map[string]any{
					"type":        "string",
					"description": "Optional node kind/label filter (e.g. 'Person', 'Apartment', 'Vehicle')",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of results to return (default: 10, max: 50)",
				},
			},
			"required": []string{"query"},
		},
	},
	{
		"name":        "graph_resolve_entity",
		"description": "Resolve colloquial, misspelled, inflected, or foreign language entity names (e.g. 'Слава', 'со Славой', 'Славику', 'Beemer') to exact canonical graph node IDs using vector semantic similarity.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Entity name, alias, nickname, or phrase to resolve",
				},
				"kind": map[string]any{
					"type":        "string",
					"description": "Optional node kind filter (e.g. 'Person', 'Vehicle')",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of resolved candidates (default: 5)",
				},
				"min_score": map[string]any{
					"type":        "number",
					"description": "Minimum cosine similarity threshold (default: 0.50)",
				},
			},
			"required": []string{"query"},
		},
	},
	{
		"name":        "graph_upsert_alias",
		"description": "Associate an alias or alternative name with a graph node and store its embedding vector for semantic entity resolution.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"node_id": map[string]any{
					"type":        "string",
					"description": "Target canonical node ID (e.g. 'person:alexander')",
				},
				"alias": map[string]any{
					"type":        "string",
					"description": "Alias text to index (e.g. 'Слава', 'Вячеслав', 'Slava')",
				},
				"embedding": map[string]any{
					"type":        "array",
					"description": "Optional precomputed float32 embedding vector. If omitted, server will auto-embed via Gemini API.",
					"items":       map[string]any{"type": "number"},
				},
			},
			"required": []string{"node_id", "alias"},
		},
	},
	{
		"name":        "graph_batch_upsert",
		"description": "Atomically upsert multiple nodes and/or edges in a single ACID transaction. If any node or edge fails validation, the entire batch is rolled back.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"nodes": map[string]any{
					"type":        "array",
					"description": "List of nodes to upsert",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"id": map[string]any{
								"type":        "string",
								"description": "Unique identifier (e.g. 'person:slava')",
							},
							"kind": map[string]any{
								"type":        "string",
								"description": "Node kind / label (e.g. 'Person')",
							},
							"properties": map[string]any{
								"type":        "object",
								"description": "Key-value attributes for the node",
							},
						},
						"required": []string{"id", "kind"},
					},
				},
				"edges": map[string]any{
					"type":        "array",
					"description": "List of edges to upsert",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"from": map[string]any{
								"type":        "string",
								"description": "Source node ID",
							},
							"to": map[string]any{
								"type":        "string",
								"description": "Target node ID",
							},
							"kind": map[string]any{
								"type":        "string",
								"description": "Relationship type (e.g. 'LIVES_AT')",
							},
							"properties": map[string]any{
								"type":        "object",
								"description": "Optional attributes for the edge",
							},
						},
						"required": []string{"from", "to", "kind"},
					},
				},
			},
		},
	},
	{
		"name":        "graph_set_node",
		"description": "Create or update a graph node with a unique ID, kind/label, and properties. Properties are merged with existing properties by default; set merge=false to overwrite completely.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "Unique identifier for the node (e.g., 'person:slava', 'apt:franziska_6')",
				},
				"kind": map[string]any{
					"type":        "string",
					"description": "Node kind / label (e.g., 'Person', 'Apartment', 'BankAccount', 'Service')",
				},
				"properties": map[string]any{
					"type":        "object",
					"description": "Key-value attributes for the node",
				},
				"merge": map[string]any{
					"type":        "boolean",
					"description": "If true (default), merges properties with existing properties. If false, completely overwrites properties.",
				},
			},
			"required": []string{"id", "kind"},
		},
	},
	{
		"name":        "graph_set_edge",
		"description": "Create or update a directed relationship/edge between two nodes. Properties are merged with existing properties by default; set merge=false to overwrite completely.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"from": map[string]any{
					"type":        "string",
					"description": "Source node ID",
				},
				"to": map[string]any{
					"type":        "string",
					"description": "Target node ID",
				},
				"kind": map[string]any{
					"type":        "string",
					"description": "Relationship type (e.g., 'LIVES_AT', 'RENTED_FROM', 'HAS_ACCOUNT', 'CALLS')",
				},
				"properties": map[string]any{
					"type":        "object",
					"description": "Optional attributes for the edge",
				},
				"merge": map[string]any{
					"type":        "boolean",
					"description": "If true (default), merges properties with existing properties. If false, completely overwrites properties.",
				},
			},
			"required": []string{"from", "to", "kind"},
		},
	},
	{
		"name":        "graph_delete_node",
		"description": "Delete a node and all its connected relationships from the graph.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "Node ID to remove",
				},
			},
			"required": []string{"id"},
		},
	},
	{
		"name":        "graph_schema",
		"description": "Inspect graph metrics and active taxonomy schema: node and edge counts by kind, total volume, and enforced schema rules.",
		"inputSchema": map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	},
	{
		"name":        "graph_schema_define",
		"description": "Governance tool for defining or modifying allowed node kinds and relationship rules in the knowledge graph schema.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"add_kind", "remove_kind", "add_relation", "remove_relation", "rename_kind", "rename_relation"},
					"description": "Schema action to perform",
				},
				"kind": map[string]any{
					"type":        "string",
					"description": "Node kind name (for add_kind / remove_kind / rename_kind)",
				},
				"relation": map[string]any{
					"type":        "string",
					"description": "Relationship type (for add_relation / remove_relation / rename_relation)",
				},
				"from_kind": map[string]any{
					"type":        "string",
					"description": "Source node kind (for add_relation / remove_relation / rename_relation)",
				},
				"to_kind": map[string]any{
					"type":        "string",
					"description": "Target node kind (for add_relation / remove_relation / rename_relation)",
				},
				"new_kind": map[string]any{
					"type":        "string",
					"description": "New node kind name (for rename_kind)",
				},
				"new_relation": map[string]any{
					"type":        "string",
					"description": "New relationship type (for rename_relation)",
				},
				"cascade": map[string]any{
					"type":        "boolean",
					"description": "If true, cascades deletion of nodes or edges associated with the removed kind/relation",
				},
				"migrate_to": map[string]any{
					"type":        "string",
					"description": "Target kind or relationship type to migrate existing data to before removal",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "Human-readable description / rationale for this schema rule",
				},
			},
			"required": []string{"action"},
		},
	},
}
