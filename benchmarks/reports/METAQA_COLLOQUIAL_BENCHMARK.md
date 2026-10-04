# MetaQA Colloquial & Multi-Hop Benchmark Report (English)

Empirical benchmark evaluating **`cypher-mcp` (Vector Entity Resolution + OpenCypher)** vs. **Text RAG (FTS5 on 134,741 Triples)** across real-world colloquial English questions with typos, nicknames, and abbreviations.

- **Dataset:** MetaQA Knowledge Graph (43,170 nodes, 116,434 edges, 134,741 facts)
- **Model:** `Google Gemini 3.8 Flash`
- **Embedding Model:** `gemini-embedding-001` (768 dimensions)

---

## 1. Final Scorecard

| Task Type | cypher-mcp (Vector + Graph) | Text RAG (FTS5) | Winner |
| :--- | :--- | :--- | :--- |
| **1-Hop (Typo / Informal Mention)**<br>*What movies did Michel Trachtenberg star in?* | **Recall: 100.0%** (5/5)<br>Turns: 3 \| Latency: 5.58s | **Recall: 100.0%** (5/5)<br>Turns: 5 \| Latency: 6.29s | **TIE** |
| **2-Hop (Nickname / Diminutive)**<br>*Name all directors who directed movies starring Jonny Krasinski.* | **Recall: 100.0%** (5/5)<br>Turns: 5 \| Latency: 8.73s | **Recall: 0.0%** (0/5)<br>Turns: 8 \| Latency: 10.05s | **cypher-mcp (Clean Sweep)** |
| **3-Hop (Abbreviated Title Multi-Hop)**<br>*What languages were spoken in movies directed by the same filmmaker who directed 'Catch Me If U Can'?* | **Recall: 100.0%** (4/4)<br>Turns: 6 \| Latency: 8.93s | **Recall: 0.0%** (0/4)<br>Turns: 8 \| Latency: 9.99s | **cypher-mcp (Clean Sweep)** |
| **AVERAGE RECALL** | **100.0%** | **33.3%** | **cypher-mcp (+66.7%)** |

---

## 2. Detailed Task Breakdown

### 1-Hop (Typo / Informal Mention)

- **Prompt:** *"What movies did Michel Trachtenberg star in?"*
- **cypher-mcp Output (Recall: 100.0%):**

```text
Michelle Trachtenberg starred in the following movies:

- **Ice Princess**
- **The Scribbler**
- **Harriet the Spy**
- **Black Christmas**
- **Inspector Gadget**
```

- **Text RAG Output (Recall: 100.0%):**

```text
According to the database, Michelle Trachtenberg starred in the following movies:

* **Harriet the Spy**
* **Inspector Gadget**
* **Ice Princess**
* **Black Christmas**
* **The Scribbler**

*(She also appeared in other notable films such as **EuroTrip** and **17 Again**).*
```

---

### 2-Hop (Nickname / Diminutive)

- **Prompt:** *"Name all directors who directed movies starring Jonny Krasinski."*
- **cypher-mcp Output (Recall: 100.0%):**

```text
To find the directors who directed movies starring **Jonny Krasinski** (resolved to canonical entity **John Krasinski**), we query the knowledge graph for directors of movies in which he starred:

### Directed Movies Starring John Krasinski:
1. **George Clooney** (*Leatherheads*)
2. **Sam Mendes** (*Away We Go*)
3. **Ken Kwapis** (*License to Wed*)
4. **Luke Greenfield** (*Something Borrowed*)
5. **Nancy Meyers** (*It's Complicated*)

### Final Answer:
- **George Clooney**
- **Ken Kwapis**
- **Luke Greenfield**
- **Nancy Meyers**
- **Sam Mendes**
```

- **Text RAG Output (Recall: 0.0%):**

```text

```

---

### 3-Hop (Abbreviated Title Multi-Hop)

- **Prompt:** *"What languages were spoken in movies directed by the same filmmaker who directed 'Catch Me If U Can'?"*
- **cypher-mcp Output (Recall: 100.0%):**

```text
To find the languages spoken in movies directed by the same filmmaker who directed **Catch Me If You Can** (Steven Spielberg), we resolve the movie entity and traverse the graph:

1. **Entity Resolution**: "Catch Me If U Can" resolves to **Catch Me If You Can** (`movie:Catch Me If You Can`).
2. **Director**: Steven Spielberg (`person:Steven Spielberg`).
3. **Languages**:
   - **German** (from *Schindler's List*)
   - **Japanese** (from *1941*, *Empire of the Sun*)
   - **Mende** (from *Amistad*)
   - **Polish** (from *Schindler's List*)

**Answer:**
German, Japanese, Mende, and Polish.
```

- **Text RAG Output (Recall: 0.0%):**

```text

```

---

