# Zero-Shortcut Depth Scaling Benchmark (1 to 4 Hops)

Evaluation comparing **GraphRAG (`cypher-mcp`)** vs. **Plain Vector RAG** across branching tree depths (1 to 4 hops) where intermediate documents contain zero shortcut links.

- **Evaluator:** Google Gemini 3.8 Flash
- **Embedding:** `gemini-embedding-001` (768 dimensions)

---

## 1. Final Scorecard

| Depth / Hops | Plain Vector RAG Recall | GraphRAG Recall | Latency Ratio | Winner |
| :--- | :--- | :--- | :--- | :--- |
| **1-Hop Dependency** | 100.0% (3 turns, 1659 tokens) | **100.0%** (5 turns, 3702 tokens) | 0.9x | **TIE** |
| **2-Hop Branching** | 100.0% (3 turns, 2018 tokens) | **100.0%** (4 turns, 5828 tokens) | 1.0x | **TIE** |
| **3-Hop Branching** | 100.0% (6 turns, 5220 tokens) | **100.0%** (4 turns, 6116 tokens) | 1.3x | **TIE** |
| **4-Hop Transitive Attribute Extraction** | 0.0% (8 turns, 9646 tokens) | **100.0%** (7 turns, 10767 tokens) | 1.6x | **GraphRAG** |

---
