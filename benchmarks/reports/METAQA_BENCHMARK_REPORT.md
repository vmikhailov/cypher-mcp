# MetaQA Multi-Hop QA Benchmark: cypher-mcp vs Text RAG

Empirical benchmark on the standard **MetaQA** movie knowledge graph:
- **Corpus:** 43,170 nodes, 117,565 edges, 134,741 triples
- **Model:** Google Gemini 3.8 Flash (Function Calling)

## 1. Multi-Hop Performance Matrix

| Task | Agent | Recall / Completeness | Turns | Tokens (In / Out) | Latency |
| :--- | :--- | :---: | :---: | :---: | :---: |
| **1-Hop: Direct Filmography** | `cypher-mcp` | **100%** (5/5) | 1 | 954 / 76 | 4831 ms |
| **1-Hop: Direct Filmography** | `text-search` | **100%** (5/5) | 3 | 1565 / 110 | 3592 ms |
| **2-Hop: Co-Stars to Directors** | `cypher-mcp` | **100%** (5/5) | 1 | 975 / 85 | 4074 ms |
| **2-Hop: Co-Stars to Directors** | `text-search` | **100%** (5/5) | 3 | 1966 / 217 | 7255 ms |
| **3-Hop: Shared Director Cross-Language Reachability** | `cypher-mcp` | **100%** (4/4) | 3 | 4394 / 228 | 5437 ms |
| **3-Hop: Shared Director Cross-Language Reachability** | `text-search` | **50%** (2/4) | 3 | 2851 / 178 | 9039 ms |

## 2. Quantitative Summary

| Agent | Overall Recall | Avg Turns | Avg Tokens (Total) | Avg Latency |
| :--- | :---: | :---: | :---: | :---: |
| **cypher-mcp** | **100.0%** | **1.7** | **2237** | **4781 ms** |
| **text-search** | **83.3%** | **3.0** | **2296** | **6629 ms** |

## 3. Detailed Transcripts

### 1-Hop: Direct Filmography

**Question:** `what films did [Michelle Trachtenberg] star in?`

**Ground Truth:** `inspector gadget, black christmas, ice princess, harriet the spy, the scribbler`

#### Agent: `cypher-mcp`

**Tool Calls:**
- `graph_query`: `{"query": "MATCH (m:Movie)-[:starred_actors]->(p:Person {name: 'Michelle Trachtenberg'}) RETURN m.name"}`

**Answer:**

Michelle Trachtenberg starred in the following films:

- Ice Princess
- The Scribbler
- Harriet the Spy
- Black Christmas
- Inspector Gadget

---

#### Agent: `text-search`

**Tool Calls:**
- `search_facts`: `{"query": "Michelle Trachtenberg starred_actors"}`
- `search_facts`: `{"query": "Michelle Trachtenberg"}`
- `search_facts`: `{"query": "EuroTrip starred_actors"}`

**Answer:**

Based on the facts, Michelle Trachtenberg starred in the following films:

* **Harriet the Spy**
* **Inspector Gadget**
* **Ice Princess**
* **Black Christmas**
* **The Scribbler**

---

### 2-Hop: Co-Stars to Directors

**Question:** `which person directed the movies starred by [John Krasinski]?`

**Ground Truth:** `nancy meyers, sam mendes, george clooney, ken kwapis, luke greenfield`

#### Agent: `cypher-mcp`

**Tool Calls:**
- `graph_query`: `{"query": "MATCH (p:Person {name: 'John Krasinski'})<-[:starred_actors]-(m:Movie)-[:directed_by]->(d:Person) RETURN DISTINCT d.name"}`

**Answer:**

The directors of movies starring John Krasinski are:

- George Clooney
- Ken Kwapis
- Luke Greenfield
- Nancy Meyers
- Sam Mendes

---

#### Agent: `text-search`

**Tool Calls:**
- `search_facts`: `{"query": "John Krasinski starred_actors"}`
- `search_facts`: `{"query": "Leatherheads directed_by Away We Go directed_by License to Wed directed_by"}`
- `search_facts`: `{"query": "directed_by Leatherheads It's Complicated Away We Go Something Borrowed", "limit": 20}`

**Answer:**

The movies starring **John Krasinski** in the dataset and their directors are:

- **Leatherheads** – directed by **George Clooney**
- **Away We Go** – directed by **Sam Mendes**
- **License to Wed** – directed by **Ken Kwapis**
- **Something Borrowed** – directed by **Luke Greenfield**
- **It's Complicated** – directed by **Nancy Meyers**
- **Nobody Walks** – directed by **Ry Russo-Young**

**Directors:** George Clooney, Ken Kwapis, Luke Greenfield, Nancy Meyers, Ry Russo-Young, Sam Mendes.

---

### 3-Hop: Shared Director Cross-Language Reachability

**Question:** `the films that share directors with the film [Catch Me If You Can] were in which languages?`

**Ground Truth:** `german, polish, mende, japanese`

#### Agent: `cypher-mcp`

**Tool Calls:**
- `graph_query`: `{"query": "MATCH (m1:Movie {name: 'Catch Me If You Can'})-[:directed_by]->(d:Person)<-[:directed_by]-(m2:Movie)-[:in_language]->(l:Language) WHERE m2 <> m1 RETURN DISTINCT l.name"}`
- `graph_query`: `{"query": "MATCH (m:Movie {name: 'Catch Me If You Can'})-[:in_language]->(l:Language) RETURN l.name"}`
- `graph_query`: `{"query": "MATCH (m1:Movie {name: 'Catch Me If You Can'})-[:directed_by]->(d:Person)<-[:directed_by]-(m2:Movie)-[:in_language]->(l:Language) RETURN m2.name, l.name"}`

**Answer:**

The films that share directors with *Catch Me If You Can* (directed by Steven Spielberg) were in the following languages:

* German
* Japanese
* Mende
* Polish

---

#### Agent: `text-search`

**Tool Calls:**
- `search_facts`: `{"query": "Catch Me If You Can directed_by"}`
- `search_facts`: `{"limit": 30, "query": "Steven Spielberg directed_by"}`
- `search_facts`: `{"query": "Schindler's List in_language", "limit": 20}`

**Answer:**

The director of *Catch Me If You Can* is **Steven Spielberg**. 

The films directed by Steven Spielberg in the database are associated with the following languages:
- **English** (e.g., *Jaws*, *Raiders of the Lost Ark*, *Jurassic Park*, *Catch Me If You Can*, etc.)
- **German** (e.g., *Schindler's List*)
- **Polish** (e.g., *Schindler's List*)

---

