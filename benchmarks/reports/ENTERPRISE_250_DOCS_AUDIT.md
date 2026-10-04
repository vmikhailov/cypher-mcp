# Enterprise Audit Benchmark Report (250 Documents)

Evaluation on 250 enterprise architecture, IAM, and compliance documents comparing **GraphRAG (`cypher-mcp`)** vs **Plain Vector RAG**.

Evaluator: `Google Gemini 3.8 Flash` | Embeddings: `gemini-embedding-001` (250 vectors)

## Scorecard

| Audit Task | Metric | Plain Vector RAG | GraphRAG (cypher-mcp) |
| :--- | :--- | :--- | :--- |
| **Security Compliance Leak Audit (SecPol-04)** | Fact Recall | **0.0%** | **100.0%** |
| | False Accusations (Hallucinations) | **0** | **0** |
| | Turns to Complete | 10 | 4 |
| | Latency | 23.94s | 9.89s |
| | Tokens Consumed | 36940 | 6142 |
| **Transitive Infrastructure Financial Rollup** | Fact Recall | **87.5%** | **0.0%** |
| | False Accusations (Hallucinations) | **0** | **0** |
| | Turns to Complete | 10 | 10 |
| | Latency | 29.35s | 22.61s |
| | Tokens Consumed | 34825 | 40685 |

## Detailed Outputs

### Security Compliance Leak Audit (SecPol-04)

**Plain Vector RAG Answer:**
> TIMEOUT / MAX TURNS EXCEEDED

**GraphRAG Answer:**
> Based on an audit of personnel, IAM group memberships, and database permissions against compliance policy **SecPol-04**, external contractors are strictly prohibited from holding access to any database classified as `PII-Tier-1`.

### Audit Findings

Two contractors were found to be in violation of SecPol-04:

| Contractor Name | IAM Group | Database Accessed | Database Classification |
| :--- | :--- | :--- | :--- |
| **Sarah Jenkins** | `grp:external-qa-audit` | `db:users-vault` | `PII-Tier-1` |
| **Viktor Vance** | `grp:contractor-legacy-support` | `db:card-vault` | `PII-Tier-1` |

---

### Audit Query Used
```cypher
MATCH (p:Person {employment_type: 'Contractor'})-[:MEMBER_OF]->(g:IAMGroup)-[:HAS_ACCESS]->(d:Database {pii_tier: 'PII-Tier-1'})
RETURN p.name AS contractor_name, g.name AS iam_group, d.name AS database_name
ORDER BY p.name, d.name
```

### Remediation Recommendation
- Revoke `grp:external-qa-audit`'s access permissions to `db:users-vault`, or remove Sarah Jenkins from `grp:external-qa-audit`.
- Revoke `grp:contractor-legacy-support`'s access permissions to `db:card-vault`, or remove Viktor Vance from `grp:contractor-legacy-support`.

---

### Transitive Infrastructure Financial Rollup

**Plain Vector RAG Answer:**
> To determine the total monthly infrastructure cost for all physical servers that **`svc:checkout-api`** directly or transitively depends on, we trace its complete dependency chain across services, databases, and compute hosts:

---

### 1. Dependency Tree & Server Mapping

1. **Direct Compute Host of `svc:checkout-api`**:
   * **Host**: `srv:app-checkout-01`
   * **Monthly Cost**: **$850 USD**

2. **Direct Connected Database**:
   * **`db:checkout-redis`**:
     * **Host**: `srv:cache-redis-01`
     * **Monthly Cost**: **$650 USD**

3. **Direct Service Dependency 1**:
   * **`svc:tax-calculator`**:
     * **Host**: `srv:app-tax-01`
     * **Monthly Cost**: **$400 USD**
     * **Downstream Services**: None
     * **Connected Database**: `db:tax-cache` (physically hosted on `srv:app-tax-01` — already counted)

4. **Direct Service Dependency 2**:
   * **`svc:payment-router`**:
     * **Host**: `srv:app-pay-01`
     * **Monthly Cost**: **$1,200 USD**
     * **Connected Database**: `db:routing-pg`
       * **Host**: `srv:db-routing-01`
       * **Monthly Cost**: **$1,500 USD**
     * **Downstream Service Dependency**: `svc:card-processor`

5. **Transitive Service Dependency (via `svc:payment-router`)**:
   * **`svc:card-processor`**:
     * **Host**: `srv:db-cardvault-01`
     * **Monthly Cost**: **$2,800 USD**
     * **Downstream Services**: None
     * **Connected Database**: `db:card-vault` (physically hosted on `srv:db-cardvault-01` — already counted)

---

### 2. Breakdown of Servers and Monthly Costs

| Server Host | Role / Associated Components | Monthly Cost (USD) |
| :--- | :--- | :--- |
| **`srv:app-checkout-01`** | Compute node for `svc:checkout-api` | $850 |
| **`srv:cache-redis-01`** | Hosts `db:checkout-redis` (Session Cache) | $650 |
| **`srv:app-tax-01`** | Compute node for `svc:tax-calculator` & hosts `db:tax-cache` | $400 |
| **`srv:app-pay-01`** | Compute node for `svc:payment-router` | $1,200 |
| **`srv:db-routing-01`** | Hosts `db:routing-pg` (Postgres Routing Primary) | $1,500 |
| **`srv:db-cardvault-01`** | Compute node for `svc:card-processor` & hosts `db:card-vault` | $2,800 |

---

### 3. Total Monthly Infrastructure Cost

* **Total (including its direct host `srv:app-checkout-01` and all transitive infrastructure):**
  $$\$850 + \$650 + \$400 + \$1,200 + \$1,500 + \$2,800 = \mathbf{\$7,400\text{ USD per month}}$$

*(Note: If strictly excluding the primary host `srv:app-checkout-01` and only summing downstream dependencies, the total is **$6,550 USD per month**).*

**GraphRAG Answer:**
> TIMEOUT / MAX TURNS EXCEEDED

---

