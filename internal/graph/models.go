package graph

// Node represents a graph node with its kind and properties.
type Node struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind"`
	Properties map[string]any `json:"properties"`
}

// Edge represents a directed relationship between two nodes.
type Edge struct {
	FromID     string         `json:"from_id"`
	ToID       string         `json:"to_id"`
	Kind       string         `json:"kind"`
	Properties map[string]any `json:"properties"`
}

// BatchNodeItem represents a single node in a batch upsert operation.
type BatchNodeItem struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind"`
	Properties map[string]any `json:"properties"`
	Overwrite  bool           `json:"overwrite,omitempty"`
}

// BatchEdgeItem represents a single edge in a batch upsert operation.
type BatchEdgeItem struct {
	From       string         `json:"from"`
	To         string         `json:"to"`
	Kind       string         `json:"kind"`
	Properties map[string]any `json:"properties"`
}

// BatchUpsertResult represents the outcome of a batch mutation.
type BatchUpsertResult struct {
	NodesCount int    `json:"nodes_count"`
	EdgesCount int    `json:"edges_count"`
	Status     string `json:"status"`
}

// DeleteResult represents the outcome of a node deletion.
type DeleteResult struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// SearchMatch represents a single full-text search result.
type SearchMatch struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind"`
	Content    string         `json:"content,omitempty"`
	Properties map[string]any `json:"properties,omitempty"`
	Score      float64        `json:"score,omitempty"`
}

// SearchResult represents the aggregated full-text search response.
type SearchResult struct {
	Query string        `json:"query"`
	Kind  string        `json:"kind,omitempty"`
	Count int           `json:"count"`
	Nodes []SearchMatch `json:"nodes"`
}

// KindDefinition represents a registered node kind in the schema taxonomy.
type KindDefinition struct {
	Kind        string `json:"kind"`
	Description string `json:"description,omitempty"`
}

// RelationDefinition represents a registered edge relation in the schema taxonomy.
type RelationDefinition struct {
	RelType     string `json:"rel_type"`
	FromKind    string `json:"from_kind"`
	ToKind      string `json:"to_kind"`
	Description string `json:"description,omitempty"`
}

// SchemaOverview represents the current taxonomy governance state.
type SchemaOverview struct {
	Strict    bool                 `json:"strict"`
	Kinds     []KindDefinition     `json:"kinds"`
	Relations []RelationDefinition `json:"relations"`
}

// SchemaMutationResult represents the outcome of a schema modification.
type SchemaMutationResult struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

// QueryResult represents the execution result of an OpenCypher query.
type QueryResult struct {
	Results       []map[string]any `json:"results"`
	Count         int              `json:"count"`
	CompiledSQL   string           `json:"compiled_sql"`
	CompileTimeUs int64            `json:"compile_time_us"`
	ExecuteTimeUs int64            `json:"execute_time_us"`
	Truncated     bool             `json:"truncated,omitempty"`
	Warning       string           `json:"warning,omitempty"`
}
