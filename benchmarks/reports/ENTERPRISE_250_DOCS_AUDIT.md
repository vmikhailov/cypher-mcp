# Enterprise Audit Benchmark Report (250 Documents)

Evaluation on 250 enterprise architecture, IAM, and compliance documents comparing **GraphRAG (`cypher-mcp`)** vs **Plain Vector RAG**.

Evaluator: `Google Gemini 3.8 Flash` | Embeddings: `gemini-embedding-001` (250 vectors)

## Scorecard

| Audit Task | Metric | Plain Vector RAG | GraphRAG (cypher-mcp) |
| :--- | :--- | :--- | :--- |
| **Security Compliance Leak Audit (SecPol-04)** | Fact Recall | **0.0%** | **100.0%** |
| | False Accusations (Hallucinations) | **0** | **0** |
| | Turns to Complete | 10 | 3 |
| | Latency | 38.12s | 8.37s |
| | Tokens Consumed | 46730 | 4528 |
| **Transitive Infrastructure Financial Rollup** | Fact Recall | **0.0%** | **87.5%** |
| | False Accusations (Hallucinations) | **0** | **0** |
| | Turns to Complete | 10 | 11 |
| | Latency | 20.31s | 31.19s |
| | Tokens Consumed | 32802 | 49207 |

## Detailed Outputs

### Security Compliance Leak Audit (SecPol-04)

**Plain Vector RAG Answer:**
> TIMEOUT / MAX TURNS EXCEEDED

**GraphRAG Answer:**
> Based on an audit of personnel, IAM groups, and database access permissions in accordance with corporate policy **SecPol-04**, external contractors are strictly prohibited from accessing databases classified as **PII-Tier-1**.

### Audit Findings: Policy Violations

There are **2 contractors** currently violating SecPol-04:

| Contractor Name | IAM Group | Database Accessed | Classification |
| :--- | :--- | :--- | :--- |
| **Sarah Jenkins** | `grp:external-qa-audit` | `db:users-vault` | `PII-Tier-1` |
| **Viktor Vance** | `grp:contractor-legacy-support` | `db:card-vault` | `PII-Tier-1` |

---

### Remediation Recommendations
1. **Revoke Group Access / Membership:**
   - Remove **Sarah Jenkins** from IAM group `grp:external-qa-audit` (or revoke `grp:external-qa-audit`'s direct access to `db:users-vault`).
   - Remove **Viktor Vance** from IAM group `grp:contractor-legacy-support` (or revoke `grp:contractor-legacy-support`'s direct access to `db:card-vault`).
2. **Access Control Policy Enforcement:** Implement guardrails or automated IAM policy checks preventing any group containing `Contractor` identities from binding permissions to `PII-Tier-1` data stores.

---

### Transitive Infrastructure Financial Rollup

**Plain Vector RAG Answer:**
> TIMEOUT / MAX TURNS EXCEEDED

**GraphRAG Answer:**
> To determine the exact total monthly infrastructure cost for all physical servers that **`svc:checkout-api`** directly or transitively depends on, we map out the service and database dependency tree and their underlying host servers:

---

### 1. Dependency Tree Mapping

* **`svc:checkout-api`** (Root Service)
  * **Hosted on:** `srv:app-checkout-01` ($850/mo, Zone: `us-east-1a`)
  * **Connects to Database:** `db:checkout-redis`
    * **Hosted on:** `srv:cache-redis-01` ($650/mo, Zone: `us-east-1a`)
  * **Depends on Service:** `svc:payment-router`
    * **Hosted on:** `srv:app-pay-01` ($1,200/mo, Zone: `us-east-1b`)
    * **Connects to Database:** `db:routing-pg`
      * **Hosted on:** `srv:db-routing-01` ($1,500/mo, Zone: `us-east-1c`)
    * **Depends on Service:** `svc:card-processor`
      * **Hosted on:** `srv:db-cardvault-01` ($2,800/mo, Zone: `us-east-1-secure`)
      * **Connects to Database:** `db:card-vault`
        * **Hosted on:** `srv:db-cardvault-01` (co-hosted; counted once)
  * **Depends on Service:** `svc:tax-calculator`
    * **Hosted on:** `srv:app-tax-01` ($400/mo, Zone: `us-east-1a`)
    * **Connects to Database:** `db:tax-cache`
      * **Hosted on:** `srv:app-tax-01` (co-hosted; counted once)

---

### 2. Unique Servers & Monthly Costs

| Server Name | Hosted Services & Databases | Zone | Monthly Cost (USD) |
| :--- | :--- | :--- | :--- |
| **`srv:app-checkout-01`** | `svc:checkout-api` | `us-east-1a` | **$850** |
| **`srv:cache-redis-01`** | `db:checkout-redis` | `us-east-1a` | **$650** |
| **`srv:app-pay-01`** | `svc:payment-router` | `us-east-1b` | **$1,200** |
| **`srv:db-routing-01`** | `db:routing-pg` | `us-east-1c` | **$1,500** |
| **`srv:app-tax-01`** | `svc:tax-calculator`, `db:tax-cache` | `us-east-1a` | **$400** |
| **`srv:db-cardvault-01`** | `svc:card-processor`, `db:card-vault` | `us-east-1-secure` | **$2,800** |

---

### 3. Total Monthly Cost Calculation

$$\$850 + \$650 + \$1,200 + \$1,500 + \$400 + \$2,800 = \mathbf{\$7,400}$$

*(Note: If strictly excluding `svc:checkout-api`'s own host server `srv:app-checkout-01` and counting only downstream infrastructure dependencies, the sum is **$6,550**).*

**Exact Final Sum:** **$7,400 USD per month**

---

