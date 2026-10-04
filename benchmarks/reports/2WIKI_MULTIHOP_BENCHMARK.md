# Academic Benchmark Report: 2WikiMultihopQA (Multi-Hop with Distractors)

Comparison of **GraphRAG (`cypher-mcp` with Vector Entity Resolution + OpenCypher)** vs. **Plain Vector RAG (gemini-embedding-001)** on the official `2WikiMultihopQA` dataset.

Evaluator: `Google Gemini 3.8 Flash` | Embedding: `gemini-embedding-001`

## Scorecard

| Metric | Plain Vector RAG | GraphRAG (cypher-mcp) |
| :--- | :--- | :--- |
| **Accuracy (Exact Match / Fact Match)** | **100.0%** | **100.0%** |
| **Average Turns to Answer** | 2.2 | 3.2 |
| **Average Latency** | 3.0s | 4.3s |
| **Average Token Usage** | 1582 | 2596 |

## Detailed Task Breakdown

### Compositional (3-Hop)

**Question:** *"Who is the mother of the director of film Polish-Russian War (Film)?"*

**Ground Truth:** `Małgorzata Braunek`

| Engine | Correct | Turns | Latency | Tokens |
| :--- | :--- | :--- | :--- | :--- |
| Plain Vector RAG | PASS | 2 | 2.45s | 1181 |
| GraphRAG (cypher-mcp) | PASS | 3 | 4.04s | 1930 |

**Plain RAG Answer:**
> The director of the film *Polish-Russian War* is Xawery Żuławski. His mother is actress **Małgorzata Braunek**.

**GraphRAG Answer:**
> The director of the film *Polish-Russian War* is Xawery Żuławski, and his mother is **Małgorzata Braunek**.

---

### Genealogy / Temporal (2-Hop with Distractors)

**Question:** *"When did John V, Prince Of Anhalt-Zerbst's father die?"*

**Ground Truth:** `12 June 1516`

| Engine | Correct | Turns | Latency | Tokens |
| :--- | :--- | :--- | :--- | :--- |
| Plain Vector RAG | PASS | 3 | 4.36s | 2623 |
| GraphRAG (cypher-mcp) | PASS | 4 | 5.45s | 3618 |

**Plain RAG Answer:**
> John V, Prince of Anhalt-Zerbst's father was Ernest I, Prince of Anhalt-Dessau, who died on **12 June 1516**.

**GraphRAG Answer:**
> John V, Prince of Anhalt-Zerbst's father was Ernest I, Prince of Anhalt-Dessau, who died on **12 June 1516**.

---

### Inference (Paternal Grandfather across French Dynasty)

**Question:** *"Who is Charles Bretagne Marie De La Trémoille's paternal grandfather?"*

**Ground Truth:** `Charles Armand René de La Trémoille`

| Engine | Correct | Turns | Latency | Tokens |
| :--- | :--- | :--- | :--- | :--- |
| Plain Vector RAG | PASS | 2 | 2.65s | 1223 |
| GraphRAG (cypher-mcp) | PASS | 3 | 4.35s | 2280 |

**Plain RAG Answer:**
> The paternal grandfather of Charles Bretagne Marie de La Trémoille is **Charles Armand René de La Trémoille** (the father of Jean Bretagne Charles de La Trémoille).

**GraphRAG Answer:**
> Charles Bretagne Marie de La Trémoille's paternal grandfather is **Charles Armand René de La Trémoille**.

---

### Comparison (Release Dates across Distractor Franchise)

**Question:** *"Which film came out first, Blind Shaft or The Mask Of Fu Manchu?"*

**Ground Truth:** `The Mask Of Fu Manchu`

| Engine | Correct | Turns | Latency | Tokens |
| :--- | :--- | :--- | :--- | :--- |
| Plain Vector RAG | PASS | 2 | 2.47s | 1302 |
| GraphRAG (cypher-mcp) | PASS | 3 | 3.5s | 2556 |

**Plain RAG Answer:**
> ***The Mask of Fu Manchu*** came out first, having been released in **1932**, whereas *Blind Shaft* was released in **2003**.

**GraphRAG Answer:**
> *The Mask of Fu Manchu* came out first (released in 1932, compared to *Blind Shaft* in 2003).

---

