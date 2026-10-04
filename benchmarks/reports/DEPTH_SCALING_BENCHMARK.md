# Zero-Shortcut Depth Scaling Benchmark (1 to 4 Hops)

Evaluation comparing **GraphRAG (`cypher-mcp`)** vs. **Plain Vector RAG** across branching tree depths (1 to 4 hops) where intermediate documents contain zero shortcut links.

- **Evaluator:** Google Gemini 3.8 Flash
- **Embedding:** `gemini-embedding-001` (768 dimensions)

---

## 1. Final Scorecard

| Depth / Hops | Plain Vector RAG Recall | GraphRAG Recall | Latency Ratio | Winner |
| :--- | :--- | :--- | :--- | :--- |
| **1-Hop Dependency** | 100.0% (3 turns, 1675 tokens) | **0.0%** (4 turns, 2502 tokens) | 0.8x | **Plain RAG** |
| **2-Hop Branching** | 100.0% (3 turns, 2050 tokens) | **100.0%** (4 turns, 7371 tokens) | 1.5x | **TIE** |
| **3-Hop Branching** | 100.0% (5 turns, 3726 tokens) | **100.0%** (4 turns, 7218 tokens) | 1.1x | **TIE** |
| **4-Hop Transitive Attribute Extraction** | 0.0% (8 turns, 8869 tokens) | **0.0%** (8 turns, 10018 tokens) | 1.3x | **TIE** |

---
