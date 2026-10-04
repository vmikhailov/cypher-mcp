# Cypher MCP Server: Remediation Roadmap & Backlog

> **Baseline:** Technical audit of HEAD `21b07a8d30eb744722b44b9f13d6eb5a1339d9cb`.  
> This document consolidates technical audit findings (F01–F20), engineering principles, step-by-step implementation phases (Stages 0–6), and acceptance criteria.

---

## Core Engineering Principles

1. **Preserve Core Architecture:** Embedded SQLite, WAL mode, and in-process OpenCypher compilation remain the right foundation for local agent memory. Do not replace the engine; focus on strict validation boundaries, connection isolation, and transactional integrity.
2. **Security & Data Safety First:** Prioritize data integrity guarantees and secret redaction (P1), followed by protocol compliance and search (P2), then operations, schema versioning, and benchmark credibility.
3. **Strict TDD (Invariant Tests):** Every defect must be accompanied by an invariant test asserting expected behavior, verified to FAIL against the defective baseline, then fixed and proven to PASS. Evidence probes from the audit (which pass on defective behavior) must not be repurposed as correctness criteria.
4. **Environment Isolation:** All tests and probes must execute against disposable, temporary databases. Never run verification against live user knowledge graphs, local `.env` files, or billable external APIs.
5. **Modular Delivery:** Deliver changes in focused, logically cohesive PRs/commits; do not interleave bulk reformatting, schema evolution, and core business logic.

---

## Audit Defect Matrix

| ID | Area | Summary | Priority | Stage |
|---|---|---|:---:|:---:|
| **F01** | SQLite / Storage | Connection-level PRAGMAs (`foreign_keys`, `query_only`, `busy_timeout`) not applied to new pool connections | **P1** | Stage 1 |
| **F02** | Security / Vector | Network error message (`url.Error`) leaks embedding API key via URL query string | **P1** | Stage 1 |
| **F03** | Mutation / Batch | `json.Unmarshal` errors ignored; malformed batch commits partial data | **P1** | Stage 1 |
| **F04** | Mutation / Input | Malformed `properties` type silently wipes or ignores properties with 200 OK | **P1** | Stage 1 |
| **F05** | Schema Governance | `remove_kind` with self-`migrate_to` deletes schema rule while leaving orphan nodes | **P1** | Stage 2 |
| **F06** | Schema Governance | `remove_relation` migrates edges to an unregistered schema relation | **P1** | Stage 2 |
| **F07** | Schema Governance | Incomplete scope (`from_kind` without `to_kind`) silently falls back to global rename | **P1** | Stage 2 |
| **F08** | Schema / Validation | Kind change validation crashes or falsely permits invalid transitions on self-loops | **P1** | Stage 2 |
| **F09** | Vector / Storage | `handleUpsertAlias` runs DELETE + INSERT outside transaction; loses vector on failure | **P1** | Stage 1 |
| **F10** | Batch / Mutation | `graph_batch_upsert` cannot coordinate simultaneous kind changes of connected nodes | **P2** | Stage 2 |
| **F11** | Schema Governance | Schema migrations lack collision policies (unhandled SQLite UNIQUE constraint crashes) | **P2** | Stage 2 |
| **F12** | Protocol / Transport | Loose JSON-RPC envelope, `float64` precision loss on large integer IDs, replies to notifications | **P2** | Stage 3 |
| **F13** | Vector Math | Missing dimension equality checks, non-finite values permitted, `float32` overflow in L2 norm | **P2** | Stage 3 |
| **F14** | Vector / Search | `handleResolveEntity` suppresses `rows.Err()` and overwrites explicit `min_score=0` with 0.5 | **P2** | Stage 3 |
| **F15** | Guardrails / AST | Backtick regex matches inside string literals; negative limit disables row truncation | **P2** | Stage 3 |
| **F16** | Benchmarks | Hardcoded baseline constants in README/reports presented as empirical measurements | Info | Stage 5 |
| **F17** | Benchmarks | Microbenchmark topology is trivial (all edges target `svc:0000`); assertion checks `len(res)>0` | Info | Stage 5 |
| **F18** | Agent Evals | Small sample size (3–4 tasks), prompt leaks Cypher examples, recall-only evaluation | Info | Stage 5 |
| **F19** | Tooling / Tests | Plain `go test` overwrites `BENCHMARKS.md`; unsafe script file deletions without confirmation | Info | Stage 0/5 |
| **F20** | Startup / Migrations | Startup executes full edge deduplication and complete FTS rebuild on every launch | **P2** | Stage 4 |

---

## Stage 0. Safe Foundation

- [x] **0.1. Decouple Test Execution from Artifact Generation (F19)**
  - Remove automated overwrite of `benchmarks/reports/BENCHMARKS.md` from standard `go test` runs.
  - Require explicit opt-in via environment variable `CYPHER_MCP_EVAL_REPORT` to generate reports.
- [x] **0.2. Secure Benchmark Scripts & Runners (F19)**
  - Guard all Python modules with `if __name__ == '__main__':` (imports must not trigger network calls or processes).
  - Use safe, isolated temporary directories for databases; prevent deleting existing DB files without `--overwrite`.
  - Ensure child processes are reliably terminated within `try ... finally` blocks.
- [x] **0.3. SQLite Backup & Recovery Verification**
  - Implement and verify a WAL-safe online backup mechanism (`VACUUM INTO` or SQLite Online Backup API).
  - Validate integrity (`PRAGMA integrity_check`, `PRAGMA foreign_key_check`) on restored backups before running migrations.
- [x] **0.4. Disclose Illustrative vs Empirical Claims in Documentation (F16)**
  - Add explicit labels in `README.md` and evaluation reports distinguishing illustrative/estimated models from empirical local measurements.
- [x] **0.5. Establish Baseline CI Gates & Toolchain Hardening**
  - Pin supported Go compiler toolchain in `go.mod` (`toolchain go1.26.8+` or patched 1.22.x/1.23.x) to resolve stdlib advisories flagged by `govulncheck`.
  - Add standard CI checks: `go test -count=1 ./...`, `go vet ./...`, `gofmt -l`, `staticcheck ./...`.
  - Resolve `staticcheck` U1000 diagnostics (unused variables in `benchmark_test.go`).

> **Stage 0 Acceptance Criteria:**  
> Running `go test ./...` leaves git working tree completely clean; importing Python modules incurs no side effects; database backups are verified consistent; baseline CI passes cleanly.

---

## Stage 1. P1: Eliminate Data Loss & Credential Leakage Risks

### PR 1.1. SQLite Connection Pool Guarantees (F01)
- [x] **DSN PRAGMA Configuration for Writer Pool:**
  - Configure `initDatabase` to pass mandatory PRAGMAs via `modernc.org/sqlite` DSN connection parameters:  
    `_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)`.
- [x] **True Read-Only Pool for `graph_query`:**
  - Configure `initRODatabase` with a true SQLite read-only URI:  
    `file:<path>?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(5000)`.
- [x] **Connection Pool Invariant Tests:**
  - `TestReadOnlyPoolReplacement`: after dropping idle connections (`db.SetMaxIdleConns(0)`), subsequent connections still reject INSERT with a read-only error.
  - `TestReadOnlyConcurrentSecondConnection`: holding an open read-only connection forces second connection to remain read-only.
  - `TestForeignKeysPoolReplacement`: after reconnecting writer pool, foreign keys remain enabled and reject dangling edges.

### PR 1.2. Strict Mutation Input Validation & Decoding (F03, F04)
- [x] **Transport Boundary & Strict Argument Typing:**
  - Enforce strict JSON decoding for tool arguments; never discard `json.Unmarshal` errors.
  - Distinguish missing fields, explicit `null`, and invalid argument types.
  - Reject non-object `properties` (e.g., string, array, number) with a `-32602 Invalid params` or tool error instead of silently wiping or ignoring them.
- [x] **Atomic Pre-validation for `handleBatchUpsert`:**
  - Parse and validate types for all `nodes` and `edges` elements prior to initiating `tx.Begin()`.
  - Reject payloads with malformed edges (e.g., object instead of array) upfront with zero writes to disk.
- [x] **Input Validation Invariant Tests:**
  - `TestBatchMalformedEdges_RejectsWithoutCommit`: passing an object instead of array for `edges` rejects entire batch; no nodes committed.
  - `TestMalformedProperties_ReturnsError`: passing string or non-map properties returns an error without altering stored properties.

### PR 1.3. Credential Redaction & Atomic Alias Upsert (F02, F09)
- [x] **Sanitize Embedding API Key from Network Errors (F02):**
  - Update `fetchEmbedding` to pass the Google Gemini API key via HTTP header `x-goog-api-key` instead of query string.
  - Implement outbound error redaction: strip tokens, keys, and query parameters from `*url.Error` and network messages.
  - Test: `TestNetworkError_NoKeyLeak` using mock transport generating network failures.
- [x] **Atomic Alias Upsert Without Vector Loss (F09):**
  - Add `UNIQUE(node_id, alias)` constraint to `entity_embeddings` table.
  - Replace split DELETE + INSERT in `handleUpsertAlias` with atomic SQL:  
    `INSERT INTO entity_embeddings (node_id, alias, embedding) VALUES (?, ?, ?) ON CONFLICT(node_id, alias) DO UPDATE SET embedding = excluded.embedding`.
  - Wrap in a transaction; ensure that failure during embedding generation or insertion preserves existing vector.
  - Test: `TestAliasUpsert_FailurePreservesOldEmbedding`.

> **Stage 1 Acceptance Criteria:**  
> All connections in SQLite pools are governed by PRAGMAs; malformed batches/properties are rejected before commit; alias upsert failure leaves previous vectors intact; outbound errors never contain API keys.

---

## Stage 2. P1/P2: Graph Integrity & Schema Governance

### PR 2.1. Schema Migration Guardrails (F05, F06, F07, F11)
- [x] **Reject Self-Migration in `remove_kind` (F05):**
  - In `handleSchemaDefine`, reject migration if `migrate_to` matches target kind case-insensitively (`strings.EqualFold`).
  - Test: `TestRemoveKind_RejectSelfMigration`.
- [x] **Validate Migration Target in `remove_relation` (F06):**
  - Verify that `(migrate_to, from_kind, to_kind)` is an existing rule in `schema_relations` when schema governance is active.
  - Test: `TestRemoveRelation_ValidateTargetRelation`.
- [x] **Enforce Explicit Scope in `rename_relation` (F07):**
  - Reject partial scope filters (specifying only `from_kind` or only `to_kind`); require either both for scoped or neither for global rename.
  - Check `RowsAffected()`: return error and skip changelog if target rule does not exist.
  - Test: `TestRenameRelation_RejectPartialScope`.
- [x] **Pre-flight Conflict Checks for Schema Migrations (F11):**
  - Pre-flight unique checks: detect rule or edge collisions before running `UPDATE`.
  - Return informative client errors instead of crashing with SQLite constraint errors.
  - Test: `TestSchemaMigration_CollisionPreflight`.

### PR 2.2. Kind Transitions & Coordinated Batch Mutations (F08, F10)
- [x] **Correct Self-Loop Validation on Kind Change (F08):**
  - In `validateNodeKindChange`, explicitly handle `from_id == to_id`: validate schema rule as `(newKind, rel, newKind)`.
  - Test: `TestSelfLoop_KindChangeValidation`.
- [x] **Coordinated Kind Changes in `handleBatchUpsert` (F10):**
  - Pre-calculate target kinds for all modified nodes in the batch (`batchTargetKinds map[string]string`).
  - Validate edges against the intended final graph state rather than intermediate uncommitted DB states, enabling order-independent migrations.
  - Test: `TestBatchUpsert_CoordinatedKindChange`.

> **Stage 2 Acceptance Criteria:**  
> All integrity probes pass; schema operations cannot produce orphaned nodes or unregistered relations; self-loops validate properly; multi-node kind migrations succeed atomically regardless of item order.

---

## Stage 3. P2: Robust MCP Protocol & Semantic Search

### PR 3.1. Strict JSON-RPC 2.0 Compliance & Query Guardrails (F12, F15)
- [x] **Strict JSON-RPC 2.0 Envelope (F12):**
  - Enforce `jsonrpc == "2.0"`.
  - Store request `ID` as `json.RawMessage` to preserve 64-bit integer precision (`float64` roundtrip flaw on `9007199254740993`).
  - Reject boolean IDs.
  - **Notifications:** Never send responses to notification requests (requests without an `id`).
  - Correctly classify JSON array batches (return `-32600 Invalid Request` rather than `-32700 Parse error` if batch is unsupported).
- [x] **Refine Query Guardrails & Arguments (F15):**
  - Validate Cypher identifiers at token/AST level rather than raw string regex (eliminate false rejections on string literals like `'Use `git push`'`).
  - Validate `limit`: reject negative values (`limit < 0`) and enforce upper bounds.
  - Propagate `context.Context` to SQLite and HTTP operations for cancellation/timeout.

### PR 3.2. Vector Invariants & Entity Resolver Reliability (F13, F14)
- [x] **Vector Mathematical Invariants (F13):**
  - In `dotProduct`, require exact dimension equality (`len(v1) == len(v2)`); reject mismatches.
  - Enforce finite numbers (`!math.IsNaN(x) && !math.IsInf(x, 0)`).
  - Prevent `float32` overflow during L2 normalization by performing sum and norm in `float64` before casting components.
  - Validate BLOB length is a multiple of 4 bytes.
  - Strictly parse explicit embedding arrays (reject non-numeric values; no silent skipping).
- [x] **Robust Candidate Resolution in `handleResolveEntity` (F14):**
  - Check `rows.Err()` following the `rows.Next()` loop; propagate iteration errors.
  - Preserve explicit `min_score = 0.0` (do not coerce to default `0.5`).
  - Validate `min_score` range `[0.0, 1.0]`.

> **Stage 3 Acceptance Criteria:**  
> Server conforms strictly to JSON-RPC 2.0; correlation IDs are preserved losslessly; vector calculations are mathematically sound and overflow-safe; backticks in string literals execute cleanly.

---

## Stage 4. Operations, Schema Versioning & Refactoring

### PR 4.1. Versioned Migrations Replacing Startup Overhead (F20)
- [x] **Versioned Migrations via `PRAGMA user_version`:**
  - Transition legacy edge deduplication, trigger creation, and FTS initialization into one-time versioned migrations.
  - Server startup performs a lightweight version check (eliminating write amplification and cold-start latency).
  - Implement a crash-safe FTS rebuild policy if a migration is interrupted.
- [x] **Migration Test Suite:**
  - Test migration from legacy schemas ensuring preservation of nodes, properties, edges, and embeddings.

### PR 4.2. Clean Architectural Boundaries
- [x] **Separate Internal Responsibilities (Without Framework Bloat):**
  - `transport/`: JSON-RPC protocol handling, strict tool argument validation.
  - `storage/`: SQLite connection pool, versioned migrations.
  - `graph/`: Cypher compilation, query execution, taxonomy & governance validation.
  - `vector/`: Vector math, embedding storage, cosine similarity, contextual HTTP client.
- [x] Return typed domain models internally; serialize to JSON exclusively at the protocol boundary.

> **Stage 4 Acceptance Criteria:**  
> Warm/cold startup on large graphs takes sub-milliseconds with zero disk writes; internal responsibilities are separated into clean, testable packages.

---

## Stage 5. Rigorous & Honest Benchmark Methodology (F16–F19)

### PR 5.1. Realistic Microbenchmark Topologies (F17)
- [x] Replace trivial star fixtures (`(i*7)%i`) with multi-hop DAGs, branching trees, and cycles.
- [x] Assert exact returned row counts, IDs, and query results rather than merely `len(res) > 0`.
- [x] Disentangle benchmark timing: isolate compile time, SQLite execution, serialization, and stdio roundtrip.

### PR 5.2. Credible Comparative Benchmarks & Agent Evaluations (F16, F18)
- [x] Generate published reports exclusively from raw artifacts of reproducible runs.
- [x] Remove Cypher cheat-sheet examples from agent prompt comparisons against baselines.
- [x] Use held-out evaluation tasks and aliases; remove test typos from training indexes.
- [x] Measure Precision, Recall, and F1; penalize hallucinated, contradictory, or negated entities.
- [x] Clearly separate keyword search (FTS) from vector RAG in benchmark comparisons.

> **Stage 5 Acceptance Criteria:**  
> All figures in `README.md` are backed by reproducible raw outputs; fixtures reflect real graph topologies; agent scoring accounts for precision as well as recall.

---

## Stage 6. Release, Security & CI Hardening

- [x] Execute `go test -race` in CI (on Linux/macOS runners with CGO enabled).
- [x] Add fuzz testing for JSON-RPC message framing, mutation arguments, and vector BLOB decoding.
- [x] Pin all GitHub Actions to commit SHAs with least-privilege permissions.
- [x] Perform `govulncheck` on release artifacts and verify SHA256 checksums.
- [x] Update release notes, compatibility guidelines, and rollback procedures.

> **Stage 6 Acceptance Criteria:**  
> CI runs race detection and fuzz seeds; actions are pinned to immutable commit SHAs with least-privilege permissions; release builds generate and verify SHA256 checksums alongside vulnerability scans; operations, migration, and rollback procedures are fully documented.

---

## Execution Flowchart

```
┌────────────────────────────────────────────────────────┐
│ Stage 0: Safe Foundation                               │
│ (Decouple reports, secure scripts, backup & CI gates)  │
└──────────────────────────┬─────────────────────────────┘
                           ▼
┌────────────────────────────────────────────────────────┐
│ Stage 1: P1 — Eliminate Data Loss & Credential Leaks   │
│ (DSN PRAGMAs, strict batch/props, leak fix, alias lock)│
└──────────────────────────┬─────────────────────────────┘
                           ▼
┌────────────────────────────────────────────────────────┐
│ Stage 2: P1/P2 — Graph Integrity & Schema Governance   │
│ (Self-loops, batch co-migration, scope & targets)      │
└──────────────────────────┬─────────────────────────────┘
                           ▼
┌────────────────────────────────────────────────────────┐
│ Stage 3: P2 — Robust MCP Protocol & Semantic Search    │
│ (JSON-RPC 2.0, vector math, query guardrails, context) │
└──────────────────────────┬─────────────────────────────┘
                           ▼
┌────────────────────────────────────────────────────────┐
│ Stage 4: Versioned Migrations & Architecture           │
│ (PRAGMA user_version, clean package boundaries)        │
└──────────────────────────┬─────────────────────────────┘
                           ▼
┌────────────────────────────────────────────────────────┐
│ Stage 5: Rigorous Benchmarks & Honest Evaluations      │
│ (Realistic DAGs, precision/recall scoring, raw traces) │
└──────────────────────────┬─────────────────────────────┘
                           ▼
┌────────────────────────────────────────────────────────┐
│ Stage 6: Release, Fuzzing & CI Hardening               │
└────────────────────────────────────────────────────────┘
```

