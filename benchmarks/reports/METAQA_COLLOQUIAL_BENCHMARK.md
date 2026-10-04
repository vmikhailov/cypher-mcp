# MetaQA Colloquial & Multi-Hop Benchmark Report (English)

Empirical benchmark evaluating **`cypher-mcp` (Vector Entity Resolution + OpenCypher)** vs. **Text RAG (FTS5 on 134,741 Triples)** across real-world colloquial English questions with typos, nicknames, and abbreviations.

- **Dataset:** MetaQA Knowledge Graph (43,170 nodes, 116,434 edges, 134,741 facts)
- **Model:** `Google Gemini 3.8 Flash`
- **Embedding Model:** `gemini-embedding-001` (768 dimensions)

---

## 1. Final Scorecard

| Task Type | cypher-mcp (Precision / Recall / F1) | Text RAG (Precision / Recall / F1) | Winner |
| :--- | :--- | :--- | :--- |
| **1-Hop (Typo / Informal Mention)**<br>*What movies did Michel Trachtenberg star in?* | **F1: 100.0%** (P: 100.0%, R: 100.0%)<br>Turns: 2 \| Latency: 4.29s | **F1: 100.0%** (P: 100.0%, R: 100.0%)<br>Turns: 2 \| Latency: 5.32s | **TIE** |
| **2-Hop (Nickname / Diminutive)**<br>*Name all directors who directed movies starring Jonny Krasinski.* | **F1: 100.0%** (P: 100.0%, R: 100.0%)<br>Turns: 4 \| Latency: 6.86s | **F1: 100.0%** (P: 100.0%, R: 100.0%)<br>Turns: 3 \| Latency: 14.94s | **TIE** |
| **3-Hop (Abbreviated Title Multi-Hop)**<br>*What languages were spoken in movies directed by the same filmmaker who directed 'Catch Me If U Can'?* | **F1: 100.0%** (P: 100.0%, R: 100.0%)<br>Turns: 4 \| Latency: 7.66s | **F1: 72.7%** (P: 57.1%, R: 100.0%)<br>Turns: 2 \| Latency: 40.79s | **cypher-mcp** |
| **AVERAGE F1** | **100.0%** | **90.9%** | **cypher-mcp (+9.1%)** |

---

## 2. Detailed Task Breakdown

### 1-Hop (Typo / Informal Mention)

- **Prompt:** *"What movies did Michel Trachtenberg star in?"*
- **cypher-mcp Output (Recall: 100.0%):**

```text
Michelle Trachtenberg starred in the following movies:

* **Ice Princess**
* **The Scribbler**
* **Harriet the Spy**
* **Black Christmas**
* **Inspector Gadget**
```

- **Text RAG Output (Recall: 100.0%):**

```text
Assuming you are referring to the actress **Michelle Trachtenberg**, here are the notable movies she has starred in:

* **Harriet the Spy** (1996) – as Harriet M. Welsch
* **Inspector Gadget** (1999) – as Penny
* **EuroTrip** (2004) – as Jenny
* **Mysterious Skin** (2004) – as Wendy
* **Ice Princess** (2005) – as Casey Carlyle
* **Black Christmas** (2006) – as Melissa Kitt
* **17 Again** (2009) – as Maggie O'Donnell
* **Cop Out** (2010) – as Ava Monroe
* **Take Me Home Tonight** (2011) – as Ashley
* **Killing Kennedy** (2013, TV film) – as Marina Oswald
* **The Scribbler** (2014) – as Alice

*(She is also widely recognized for her major television roles, notably as Dawn Summers in **Buffy the Vampire Slayer** and Georgina Sparks in **Gossip Girl**).*
```

---

### 2-Hop (Nickname / Diminutive)

- **Prompt:** *"Name all directors who directed movies starring Jonny Krasinski."*
- **cypher-mcp Output (Recall: 100.0%):**

```text
The directors who directed movies starring John Krasinski (Jonny Krasinski) in the knowledge graph are:

- **George Clooney** (*Leatherheads*)
- **Ken Kwapis** (*License to Wed*)
- **Luke Greenfield** (*Something Borrowed*)
- **Nancy Meyers** (*It's Complicated*)
- **Sam Mendes** (*Away We Go*)
```

- **Text RAG Output (Recall: 100.0%):**

```text
Assuming the question refers to **John Krasinski** (as there is no recognized actor credited under the name "Jonny Krasinski"), here are the directors who have directed movies starring him:

### Key Feature Film Directors
* **John Krasinski** – *Brief Interviews with Hideous Men* (2009), *The Hollars* (2016), *A Quiet Place* (2018), *A Quiet Place Part II* (2020), *IF* (2024)
* **Sam Mendes** – *Jarhead* (2005), *Away We Go* (2009)
* **Ken Kwapis** – *License to Wed* (2007), *Big Miracle* (2012)
* **George Clooney** – *Leatherheads* (2008)
* **Nancy Meyers** – *The Holiday* (2006), *It's Complicated* (2009)
* **Luke Greenfield** – *Something Borrowed* (2011)
* **Gus Van Sant** – *Promised Land* (2012)
* **Cameron Crowe** – *Aloha* (2015)
* **Michael Bay** – *13 Hours: The Secret Soldiers of Benghazi* (2016)
* **Kathryn Bigelow** – *Detroit* (2017)
* **Bill Condon** – *Kinsey* (2004), *Dreamgirls* (2006)
* **Tim Story** – *Taxi* (2004)
* **Matt Mulhern** – *Duane Hopwood* (2005)
* **Christopher Guest** – *For Your Consideration* (2006)
* **Gregg Araki** – *Smiley Face* (2007)
* **Sam Raimi** – *Doctor Strange in the Multiverse of Madness* (2022)

### Animated / Voice Roles
* **Chris Miller** & **Raman Hui** – *Shrek the Third* (2007)
* **Dan Scanlon** – *Monsters University* (2013)
* **Hayao Miyazaki** – *The Wind Rises* (2013, English dub)
* **Roger Allers** – *The Prophet* (2014)
* **Scott Christian Sava** & **Tony Bancroft** – *Animal Crackers* (2017)
* **Kevin R. Adams** & **Joe Ksander** – *Next Gen* (2018)
* **Jared Stern** – *DC League of Super-Pets* (2022)

*(Note: If strictly searching for an individual credited under the exact name **"Jonny Krasinski"**, there are no known feature films or directors in standard film databases).*
```

---

### 3-Hop (Abbreviated Title Multi-Hop)

- **Prompt:** *"What languages were spoken in movies directed by the same filmmaker who directed 'Catch Me If U Can'?"*
- **cypher-mcp Output (Recall: 100.0%):**

```text
The movie **Catch Me If You Can** was directed by **Steven Spielberg**. 

The languages spoken across movies directed by Steven Spielberg in the database are:
- **German**
- **Japanese**
- **Mende**
- **Polish**
```

- **Text RAG Output (Recall: 100.0%):**

```text
The database search encountered a temporary technical error, but based on the film title **"Catch Me If You Can"** (frequently colloquially abbreviated as *Catch Me If U Can*), this most likely refers to the acclaimed 2002 film directed by **Steven Spielberg** (or alternatively, the 1989 film directed by **Stephen Sommers**). 

Here are the languages spoken in movies directed by each filmmaker:

---

### 1. If referring to **Steven Spielberg** (*Catch Me If You Can*, 2002)
Steven Spielberg has directed a vast filmography across numerous cultures and historical periods (e.g., *Schindler's List*, *Saving Private Ryan*, *Munich*, *Raiders of the Lost Ark*, *The Terminal*, *Amistad*, *Empire of the Sun*, *Close Encounters of the Third Kind*, *War of the Worlds*). 

The languages spoken across movies he has directed include:
* **English** *(primary)*
* **French** *(featured in Catch Me If You Can, Saving Private Ryan, Munich, The Terminal)*
* **German** *(Schindler's List, Saving Private Ryan, Raiders of the Lost Ark, Munich, The Last Crusade)*
* **Hebrew** *(Schindler's List, Munich, Raiders of the Lost Ark)*
* **Polish** *(Schindler's List)*
* **Spanish** *(Jurassic Park, The Terminal, Raiders of the Lost Ark, Close Encounters)*
* **Arabic** *(Raiders of the Lost Ark, Munich, Indiana Jones and the Last Crusade)*
* **Russian** & **Bulgarian** *(The Terminal)*
* **Italian** *(Munich)*
* **Japanese** *(Empire of the Sun, 1941)*
* **Mandarin Chinese** *(Empire of the Sun)*
* **Hindi** *(Close Encounters of the Third Kind, Indiana Jones and the Temple of Doom)*
* **Sinhala** *(Indiana Jones and the Temple of Doom)*
* **Czech** *(Saving Private Ryan)*
* **Latin** *(Schindler's List)*
* **Mende** & **Portuguese** *(Amistad)*
* **Nepali** *(Raiders of the Lost Ark)*

*(Note: Within the 2002 film **Catch Me If You Can** itself, the spoken languages are **English** and **French**.)*

---

### 2. If referring to **Stephen Sommers** (*Catch Me If You Can*, 1989)
Stephen Sommers directed the 1989 film as well as titles such as *The Mummy*, *The Mummy Returns*, *Van Helsing*, *Deep Rising*, *G.I. Joe: The Rise of Cobra*, and *The Adventures of Huck Finn*. 

The languages spoken across his directed films include:
* **English**
* **Arabic** *(The Mummy, The Mummy Returns, G.I. Joe)*
* **Ancient Egyptian** *(The Mummy, The Mummy Returns)*
* **Hebrew** *(The Mummy)*
* **Hungarian** *(The Mummy, Van Helsing)*
* **Latin** & **Romanian** *(Van Helsing)*
* **French** & **Scottish Gaelic** *(G.I. Joe: The Rise of Cobra)*
```

---

