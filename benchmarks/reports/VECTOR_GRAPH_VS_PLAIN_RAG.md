# Benchmark Report: GraphRAG (cypher-mcp) vs. Plain Vector RAG

Evaluation comparing **GraphRAG (`cypher-mcp` with Vector Entity Resolution + OpenCypher)** against **Plain Vector RAG (Embedding Chunk Search)**.

Evaluator: `Google Gemini 3.8 Flash` | Embedding Model: `gemini-embedding-001`

## Summary Scorecard

| Paradigm | Avg Recall | Avg Turns | Avg Latency | Avg Tokens |
| :--- | :--- | :--- | :--- | :--- |
| **GraphRAG (cypher-mcp)** | **75.0%** | **8.0** | **19.2s** | **21643** |
| **Plain Vector RAG** | **87.5%** | **6.8** | **17.9s** | **18616** |

## Detailed Task Results

### Colloquial 1-Hop Discovery

**Prompt:** *"Who owns the auth gateway? Someone in chat asked: 'Ask Al, is that his service?'. Give the full name of the responsible engineer and their exact role."*

| Metric | Plain Vector RAG | GraphRAG (cypher-mcp) |
| :--- | :--- | :--- |
| **Fact Recall** | **50.0%** | **100.0%** |
| **Turns to Answer** | 8 | 4 |
| **Tool Calls** | 7 | 5 |
| **Latency** | 14.51s | 6.93s |
| **Token Usage** | 13995 | 6627 |

**Plain RAG Answer (translated):**
> Yes, the auth gateway is indeed owned by Alex.

According to the service architecture documentation (`svc:auth-gateway`):
* **Full name:** Alexander Ivanov (known on Slack as *Al* or *Alex*).
* **Role relative to the service:** Owner and Maintainer (*owned and maintained by Alexander Ivanov*). He also manages related auth components: `svc:user-store`, `db:postgres-users`, and `db:redis-session`.

**GraphRAG Answer (translated):**
> Yes, **Auth Gateway** (`svc:auth-gateway`) is indeed owned by Alex.

* **Full name:** Alexander Ivanov
* **Exact role:** Staff Architect

---

### Multi-Hop Transitive Dependency (2-Hop)

**Prompt:** *"In which datacenter (city and zone name) is the database server physically located that the payment worker (svc:payment-worker) depends on?"*

| Metric | Plain Vector RAG | GraphRAG (cypher-mcp) |
| :--- | :--- | :--- |
| **Fact Recall** | **100.0%** | **0.0%** |
| **Turns to Answer** | 5 | 10 |
| **Tool Calls** | 4 | 10 |
| **Latency** | 14.23s | 25.02s |
| **Token Usage** | 5541 | 25580 |

**Plain RAG Answer (translated):**
> The database server that `svc:payment-worker` depends on is located in the following datacenter:

* **City:** Frankfurt am Main, Germany
* **Datacenter / Zone:** `dc:frankfurt-zone-1` (Frankfurt Zone 1)

### Dependency chain:
1. **`svc:payment-worker`** directly depends on **`svc:billing-engine`**.
2. **`svc:billing-engine`** depends on **`db:postgres-orders`**.
3. **`db:postgres-orders`** is hosted on physical server **`srv:db-node-01`**.
4. **`srv:db-node-01`** is physically located in **`dc:frankfurt-zone-1`** (Frankfurt am Main).

**GraphRAG Answer:**
> TIMEOUT / MAX TURNS EXCEEDED

---

### Blast Radius Impact Chain (3-Hop)

**Prompt:** *"Engineers reported a failure on cache server srv:cache-01. List ALL services affected along the dependency chain, and ALL engineers (owners of those services) who need to be paged into the war room."*

| Metric | Plain Vector RAG | GraphRAG (cypher-mcp) |
| :--- | :--- | :--- |
| **Fact Recall** | **100.0%** | **100.0%** |
| **Turns to Answer** | 7 | 9 |
| **Tool Calls** | 6 | 8 |
| **Latency** | 24.99s | 22.61s |
| **Token Usage** | 27118 | 30853 |

**Plain RAG Answer (translated):**
> When server **`srv:cache-01`** fails, the affected components, downstream services, and engineers are:

### 1. Root Cause Component
* **`db:redis-session`** — session cache database hosted directly on `srv:cache-01`.
  * **Owner:** Alexander Ivanov (*Alex*).

### 2. Downstream Affected Services
1. **`svc:auth-gateway`** — directly depends on `db:redis-session` (Owner: Alexander Ivanov).
2. **`svc:billing-engine`** — directly depends on `svc:auth-gateway` (Owner: Dmitry Smirnov).
3. **`svc:payment-worker`** — directly depends on `svc:billing-engine` and emits to `mq:kafka-events` (Owner: Dmitry Smirnov).
4. **`svc:fraud-detector`** — consumes live transactions from `svc:payment-worker` (Owner: Elena Kuznetsova).
5. **`svc:notification-dispatcher`** — subscribes to `mq:kafka-events` (Owner: Olga Sokolova).
6. **`svc:audit-logger`** — consumes `mq:kafka-events` and persists to `db:clickhouse-lake` (Owner: Elena Kuznetsova).

*(Indirectly, **`svc:analytics-api`** owned by Mikhail Popov may also see stale data in `db:clickhouse-lake`.)*

### 3. Engineers to Page into War Room
1. **Alexander Ivanov** — `db:redis-session`, `svc:auth-gateway`.
2. **Dmitry Smirnov** — `svc:billing-engine`, `svc:payment-worker`.
3. **Elena Kuznetsova** — `svc:fraud-detector`, `svc:audit-logger`.
4. **Olga Sokolova** — `svc:notification-dispatcher`.
5. *(Optional)* **Mikhail Popov** — `db:clickhouse-lake`, `svc:analytics-api`.

**GraphRAG Answer (translated):**
> When cache server **`srv:cache-01`** fails, **`db:redis-session`** goes down.

### 1. Affected Services (dependency chain):
1. **`svc:auth-gateway`** — directly depends on `db:redis-session`.
2. **`svc:billing-engine`** — depends on `svc:auth-gateway`.
3. **`svc:payment-worker`** — depends on `svc:billing-engine`.
4. **`svc:fraud-detector`** — depends on `svc:payment-worker`.

*Failure chain:* `srv:cache-01` -> `db:redis-session` -> `svc:auth-gateway` -> `svc:billing-engine` -> `svc:payment-worker` -> `svc:fraud-detector`.

### 2. Engineers to Page into War Room:
1. **Alexander Ivanov** (`person:alexander`, *Staff Architect*) — owns `svc:auth-gateway` and `db:redis-session`.
2. **Dmitry Smirnov** (`person:dmitry`, *SRE Lead*) — owns `svc:billing-engine` and `svc:payment-worker`.
3. **Elena Kuznetsova** (`person:elena`, *Security Lead*) — owns `svc:fraud-detector`.

---

### Global Negation & Aggregation

**Prompt:** *"How many microservices (kind: 'Service') in total in our infrastructure do NOT depend on the Kafka message broker (mq:kafka-events)? Give the exact count and list their IDs."*

| Metric | Plain Vector RAG | GraphRAG (cypher-mcp) |
| :--- | :--- | :--- |
| **Fact Recall** | **100.0%** | **100.0%** |
| **Turns to Answer** | 7 | 9 |
| **Tool Calls** | 6 | 8 |
| **Latency** | 17.84s | 22.36s |
| **Token Usage** | 27813 | 23513 |

**Plain RAG Answer (translated):**
> Out of 8 total microservices (`kind: 'Service'`), **5** do NOT directly depend on Kafka (`mq:kafka-events`):

1. **`svc:user-store`** — depends only on `db:postgres-users`.
2. **`svc:auth-gateway`** — depends directly on `svc:user-store` and `db:redis-session`.
3. **`svc:billing-engine`** — depends directly on `svc:auth-gateway` and `db:postgres-orders`.
4. **`svc:fraud-detector`** — consumes from `svc:payment-worker` and queries `svc:user-store`.
5. **`svc:analytics-api`** — queries `db:clickhouse-lake` and does not publish to Kafka.

*(The remaining 3 depend on `mq:kafka-events`: `svc:payment-worker`, `svc:notification-dispatcher`, `svc:audit-logger`.)*

**GraphRAG Answer (translated):**
> There are **8** microservices (`kind: 'Service'`) in total. Depending on direct vs transitive dependencies:

### 1. Direct dependencies (`NOT (s)-[:DEPENDS_ON]->(kafka)`)
**5 microservices** have no direct dependency on `mq:kafka-events`:
1. `svc:auth-gateway`
2. `svc:billing-engine`
3. `svc:fraud-detector`
4. `svc:analytics-api`
5. `svc:user-store`

### 2. Transitive dependencies (`NOT (s)-[:DEPENDS_ON*]->(kafka)`)
Accounting for transitive chains, `svc:fraud-detector` depends on `svc:payment-worker`, which publishes to Kafka. That leaves **4 microservices** completely independent of Kafka:
1. `svc:auth-gateway`
2. `svc:billing-engine`
3. `svc:analytics-api`
4. `svc:user-store`

---

