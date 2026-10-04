# Academic Benchmark Report: 2WikiMultihopQA (Multi-Hop with Distractors)

Comparison of **GraphRAG (`cypher-mcp` with Vector Entity Resolution + OpenCypher)** vs. **Plain Vector RAG (gemini-embedding-001)** on the official `2WikiMultihopQA` dataset.

Evaluator: `Google Gemini 3.8 Flash` | Embedding: `gemini-embedding-001`

## Scorecard

| Metric | Plain Vector RAG | GraphRAG (cypher-mcp) |
| :--- | :--- | :--- |
| **Accuracy (Exact Match / Fact Match)** | **100.0%** | **100.0%** |
| **Average Turns to Answer** | 2.2 | 4.2 |
| **Average Latency** | 3.2s | 6.1s |
| **Average Token Usage** | 1472 | 4164 |

## Detailed Task Breakdown

### Compositional (3-Hop)

**Question:** *"Who is the mother of the director of film Polish-Russian War (Film)?"*

**Ground Truth:** `Małgorzata Braunek`

| Engine | Correct | Turns | Latency | Tokens |
| :--- | :--- | :--- | :--- | :--- |
| Plain Vector RAG | PASS | 2 | 2.64s | 1099 |
| GraphRAG (cypher-mcp) | PASS | 4 | 5.99s | 3262 |

**Plain RAG Answer:**
> The director of the film *Polish-Russian War* is Xawery Żuławski, and his mother is Polish actress **Małgorzata Braunek**.

**GraphRAG Answer:**
> The director of the film *Polish-Russian War* is Xawery Żuławski. His mother is **Małgorzata Braunek**.

---

### Genealogy / Temporal (2-Hop with Distractors)

**Question:** *"When did John V, Prince Of Anhalt-Zerbst's father die?"*

**Ground Truth:** `12 June 1516`

| Engine | Correct | Turns | Latency | Tokens |
| :--- | :--- | :--- | :--- | :--- |
| Plain Vector RAG | PASS | 3 | 4.44s | 2598 |
| GraphRAG (cypher-mcp) | PASS | 4 | 5.31s | 3731 |

**Plain RAG Answer:**
> John V, Prince of Anhalt-Zerbst's father, Ernest I, Prince of Anhalt-Dessau, died on **12 June 1516**.

**GraphRAG Answer:**
> John V, Prince of Anhalt-Zerbst's father was Ernest I, Prince of Anhalt-Dessau, who died on **12 June 1516**.

---

### Inference (Paternal Grandfather across French Dynasty)

**Question:** *"Who is Charles Bretagne Marie De La Trémoille's paternal grandfather?"*

**Ground Truth:** `Charles Armand René de La Trémoille`

| Engine | Correct | Turns | Latency | Tokens |
| :--- | :--- | :--- | :--- | :--- |
| Plain Vector RAG | PASS | 2 | 2.88s | 1210 |
| GraphRAG (cypher-mcp) | PASS | 6 | 8.54s | 7131 |

**Plain RAG Answer:**
> Charles Bretagne Marie de La Trémoille's paternal grandfather was **Charles Armand René de La Trémoille**, 6th Duke of Thouars.

**GraphRAG Answer:**
> Charles Bretagne Marie de La Trémoille's paternal grandfather is **Charles Armand René de La Trémoille**.

---

### Comparison (Release Dates across Distractor Franchise)

**Question:** *"Which film came out first, Blind Shaft or The Mask Of Fu Manchu?"*

**Ground Truth:** `The Mask Of Fu Manchu`

| Engine | Correct | Turns | Latency | Tokens |
| :--- | :--- | :--- | :--- | :--- |
| Plain Vector RAG | PASS | 2 | 2.71s | 981 |
| GraphRAG (cypher-mcp) | PASS | 3 | 4.72s | 2534 |

**Plain RAG Answer:**
> **The Mask of Fu Manchu** came out first (1932), compared to *Blind Shaft* (2003).

**GraphRAG Answer:**
> To determine which film came out first, we compare their release years:

* **The Mask of Fu Manchu**: 1932
* **Blind Shaft**: 2003

Therefore, **The Mask of Fu Manchu** came out first.

---

