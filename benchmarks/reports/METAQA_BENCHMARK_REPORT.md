# MetaQA Multi-Hop QA Benchmark: cypher-mcp vs Text RAG

Empirical benchmark on the standard **MetaQA** movie knowledge graph:
- **Corpus:** 43,170 nodes, 117,565 edges, 134,741 triples
- **Model:** Google Gemini 3.8 Flash (Function Calling)

## 1. Multi-Hop Performance Matrix

| Task | Agent | Recall / Completeness | Turns | Tokens (In / Out) | Latency |
| :--- | :--- | :---: | :---: | :---: | :---: |
| **1-Hop: Direct Filmography** | `cypher-mcp` | **100%** (5/5) | 1 | 896 / 81 | 2306 ms |
| **1-Hop: Direct Filmography** | `text-search` | **100%** (5/5) | 2 | 835 / 240 | 3929 ms |
| **2-Hop: Co-Stars to Directors** | `cypher-mcp` | **100%** (5/5) | 1 | 951 / 92 | 3175 ms |
| **2-Hop: Co-Stars to Directors** | `text-search` | **100%** (5/5) | 2 | 820 / 224 | 6579 ms |
| **3-Hop: Shared Director Cross-Language Reachability** | `cypher-mcp` | **100%** (4/4) | 2 | 2074 / 146 | 3876 ms |
| **3-Hop: Shared Director Cross-Language Reachability** | `text-search` | **75%** (3/4) | 2 | 921 / 150 | 12169 ms |

## 2. Quantitative Summary

| Agent | Overall Recall | Avg Turns | Avg Tokens (Total) | Avg Latency |
| :--- | :---: | :---: | :---: | :---: |
| **cypher-mcp** | **100.0%** | **1.3** | **1413** | **3119 ms** |
| **text-search** | **91.7%** | **2.0** | **1063** | **7559 ms** |

## 3. Detailed Transcripts

### 1-Hop: Direct Filmography

**Question:** `what films did [Michelle Trachtenberg] star in?`

**Ground Truth:** `inspector gadget, black christmas, ice princess, harriet the spy, the scribbler`

#### Agent: `cypher-mcp`

**Tool Calls:**
- `graph_query`: `{"query": "MATCH (m:Movie)-[:starred_actors]->(p:Person {name: 'Michelle Trachtenberg'}) RETURN m.name ORDER BY m.name"}`

**Answer:**

Michelle Trachtenberg starred in the following films:

- Black Christmas
- Harriet the Spy
- Ice Princess
- Inspector Gadget
- The Scribbler

---

#### Agent: `text-search`

**Tool Calls:**
- `search_facts`: `{"query": "Michelle Trachtenberg starred_actors"}`
- `search_facts`: `{"query": "Michelle Trachtenberg"}`

**Answer:**

Michelle Trachtenberg has starred in several notable films, including:

* ***Harriet the Spy* (1996)** – as Harriet M. Welsch
* ***Inspector Gadget* (1999)** – as Penny
* ***Can't Be Heaven* (1999)** – as Julie
* ***EuroTrip* (2004)** – as Jenny
* ***Ice Princess* (2005)** – as Casey Carlyle
* ***Black Christmas* (2006)** – as Melissa Kitt
* ***17 Again* (2009)** – as Maggie O'Donnell
* ***Against the Current* (2009)** – as Suzanne
* ***Cop Out* (2010)** – as Ava Monroe
* ***Take Me Home Tonight* (2011)** – as Ashley
* ***The Scribbler* (2014)** – as Alice

---

### 2-Hop: Co-Stars to Directors

**Question:** `which person directed the movies starred by [John Krasinski]?`

**Ground Truth:** `nancy meyers, sam mendes, george clooney, ken kwapis, luke greenfield`

#### Agent: `cypher-mcp`

**Tool Calls:**
- `graph_query`: `{"query": "MATCH (m:Movie)-[:starred_actors]->(a:Person {name: 'John Krasinski'})\nMATCH (m)-[:directed_by]->(d:Person)\nRETURN DISTINCT d.name AS director"}`

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
- `search_facts`: `{"query": "John Krasinski"}`

**Answer:**

The directors of movies starring John Krasinski include:

* **Sam Mendes** (*Away We Go*, *Jarhead*)
* **Ken Kwapis** (*License to Wed*, *Big Miracle*)
* **George Clooney** (*Leatherheads*)
* **Nancy Meyers** (*It's Complicated*)
* **Luke Greenfield** (*Something Borrowed*)
* **Gus Van Sant** (*Promised Land*)
* **Michael Bay** (*13 Hours: The Secret Soldiers of Benghazi*)
* **Cameron Crowe** (*Aloha*)
* **Kathryn Bigelow** (*Detroit*)
* **Gregg Araki** (*Smiley Face*)
* **Christopher Guest** (*For Your Consideration*)
* **John Krasinski** himself (*Brief Interviews with Hideous Men*, *The Hollars*, *A Quiet Place*, *A Quiet Place Part II*)

---

### 3-Hop: Shared Director Cross-Language Reachability

**Question:** `the films that share directors with the film [Catch Me If You Can] were in which languages?`

**Ground Truth:** `german, polish, mende, japanese`

#### Agent: `cypher-mcp`

**Tool Calls:**
- `graph_query`: `{"query": "MATCH (m1:Movie {name: 'Catch Me If You Can'})-[:directed_by]->(d:Person)<-[:directed_by]-(m2:Movie)-[:in_language]->(l:Language)\nRETURN DISTINCT l.name"}`
- `graph_query`: `{"query": "MATCH (m1:Movie {name: 'Catch Me If You Can'})-[:in_language]->(l:Language) RETURN l.name"}`

**Answer:**

The films that share directors with **Catch Me If You Can** were in the following languages:

* German
* Japanese
* Mende
* Polish

---

#### Agent: `text-search`

**Tool Calls:**
- `search_facts`: `{"query": "Catch Me If You Can directed_by"}`
- `search_facts`: `{"query": "Catch Me If You Can"}`

**Answer:**

Films that share a director with ***Catch Me If You Can*** (most notably directed by **Steven Spielberg**, as well as the 1989 film directed by **Stephen Sommers**) were released in the following languages:

* **English**
* **German** 
* **French**
* **Hebrew**
* **Polish**
* **Arabic**
* **Spanish**
* **Italian**
* **Russian**
* **Japanese**
* **Czech**
* **Latin**

---

