# Benchmark Report: GraphRAG (cypher-mcp) vs. Plain Vector RAG

Evaluation comparing **GraphRAG (`cypher-mcp` with Vector Entity Resolution + OpenCypher)** against **Plain Vector RAG (Embedding Chunk Search)**.

Evaluator: `Google Gemini 3.8 Flash` | Embedding Model: `gemini-embedding-001`

## Summary Scorecard

| Paradigm | Avg Recall | Avg Turns | Avg Latency | Avg Tokens |
| :--- | :--- | :--- | :--- | :--- |
| **GraphRAG (cypher-mcp)** | **75.0%** | **8.0** | **19.2s** | **21643** |
| **Plain Vector RAG** | **87.5%** | **6.8** | **17.9s** | **18616** |

## Detailed Task Results

### Colloquial 1-Hop Discovery

**Prompt:** *"Кто у нас отвечает за шлюз авторизации (auth gateway)? Пользователь в чате пишет: 'Спроси у Саши, это его сервис?'. Назови полное имя ответственного инженера и его точную роль."*

| Metric | Plain Vector RAG | GraphRAG (cypher-mcp) |
| :--- | :--- | :--- |
| **Fact Recall** | **50.0%** | **100.0%** |
| **Turns to Answer** | 8 | 4 |
| **Tool Calls** | 7 | 5 |
| **Latency** | 14.51s | 6.93s |
| **Token Usage** | 13995 | 6627 |

**Plain RAG Answer:**
> Да, шлюз авторизации действительно находится в зоне ответственности Саши.

Согласно архитектурной документации сервиса (`svc:auth-gateway`):
* **Полное имя:** Александр Иванов (**Alexander Ivanov**, в Slack известен как *Саша* или *Alex*).
* **Точная роль по отношению к сервису:** Владелец и ответственный за сопровождение сервиса (**Owner and Maintainer** — *owned and maintained by Alexander Ivanov*). Также он управляет сопутствующими компонентами авторизационного контура: сервисом `svc:user-store` и базами данных `db:postgres-users` и `db:redis-session`.

**GraphRAG Answer:**
> Да, сервис **Auth Gateway** (`svc:auth-gateway`) действительно закреплен за Сашей.

* **Полное имя ответственного инженера:** Alexander Ivanov (Александр Иванов)
* **Точная роль:** Staff Architect

---

### Multi-Hop Transitive Dependency (2-Hop)

**Prompt:** *"В каком именно датацентре (город и название зоны) физически расположен сервер базы данных, от которой зависит платежный воркер (svc:payment-worker)?"*

| Metric | Plain Vector RAG | GraphRAG (cypher-mcp) |
| :--- | :--- | :--- |
| **Fact Recall** | **100.0%** | **0.0%** |
| **Turns to Answer** | 5 | 10 |
| **Tool Calls** | 4 | 10 |
| **Latency** | 14.23s | 25.02s |
| **Token Usage** | 5541 | 25580 |

**Plain RAG Answer:**
> Сервер базы данных, от которой зависит платежный воркер (`svc:payment-worker`), расположен в следующем датацентре:

* **Город:** Франкфурт-на-Майне (Frankfurt am Main, Германия)
* **Название зоны / датацентра:** `dc:frankfurt-zone-1` (Frankfurt Zone 1)

---

### Цепочка зависимостей:
1. Воркер **`svc:payment-worker`** напрямую зависит от сервиса **`svc:billing-engine`**.
2. Сервис **`svc:billing-engine`** зависит от базы данных **`db:postgres-orders`**.
3. База данных **`db:postgres-orders`** размещена на физическом сервере **`srv:db-node-01`**.
4. Сервер **`srv:db-node-01`** физически находится в датацентре **`dc:frankfurt-zone-1`** (Франкфурт-на-Майне).

**GraphRAG Answer:**
> TIMEOUT / MAX TURNS EXCEEDED

---

### Blast Radius Impact Chain (3-Hop)

**Prompt:** *"Инженеры зафиксировали отказ кэш-сервера srv:cache-01. Перечисли ВСЕ сервисы, чья работа будет затронута по цепочке зависимостей, и ВСЕХ инженеров (владельцев этих сервисов), которых нужно вызвать в war room."*

| Metric | Plain Vector RAG | GraphRAG (cypher-mcp) |
| :--- | :--- | :--- |
| **Fact Recall** | **100.0%** | **100.0%** |
| **Turns to Answer** | 7 | 9 |
| **Tool Calls** | 6 | 8 |
| **Latency** | 24.99s | 22.61s |
| **Token Usage** | 27118 | 30853 |

**Plain RAG Answer:**
> При отказе сервера **`srv:cache-01`** цепочка зависимостей и список затронутых сервисов и инженеров выглядят следующим образом:

---

### 1. Первоисточник сбоя и инфраструктурный компонент
* **`db:redis-session`** — база данных сессионного кэша, размещенная непосредственно на физическом сервере `srv:cache-01`.
  * **Ответственный:** Александр Иванов (*Alexander Ivanov*, в Slack — «Саша» / *Alex*).

---

### 2. Цепочка затронутых сервисов (Downstream-зависимости)

1. **`svc:auth-gateway`**
   * **Связь:** Напрямую зависит от `db:redis-session` для валидации сессионных токенов и аутентификации.
   * **Владелец:** Александр Иванов (*Alexander Ivanov* / «Саша» / *Alex*).

2. **`svc:billing-engine`**
   * **Связь:** Напрямую зависит от `svc:auth-gateway` для валидации пользователей при обработке подписок и счетов.
   * **Владелец:** Дмитрий Смирнов (*Dmitry Smirnov* / «Дима» / *Dima*).

3. **`svc:payment-worker`**
   * **Связь:** Фоновый демон напрямую зависит от `svc:billing-engine` (получение счетов для выполнения транзакций) и отправляет события в `mq:kafka-events`.
   * **Владелец:** Дмитрий Смирнов (*Dmitry Smirnov* / «Дима»).

4. **`svc:fraud-detector`**
   * **Связь:** Напрямую потребляет поток транзакций от `svc:payment-worker` для антифрод-проверок в реальном времени.
   * **Владелец:** Елена Кузнецова (*Elena Kuznetsova* / «Лена» / *Helen*).

5. **`svc:notification-dispatcher`**
   * **Связь:** Подписан на очередь событий `mq:kafka-events`, куда публикует `svc:payment-worker`; при сбое генерации платежных событий перестанет получать и отправлять уведомления (SMS/Email).
   * **Владелец:** Ольга Соколова (*Olga Sokolova* / «Оля»).

6. **`svc:audit-logger`**
   * **Связь:** Вычитывает поток сообщений из `mq:kafka-events` для аудита и записи в `db:clickhouse-lake`.
   * **Владелец:** Елена Кузнецова (*Elena Kuznetsova* / «Лена»).

*(Косвенно также может быть затронут сервис **`svc:analytics-api`** под управлением Михаила Попова (*Mikhail Popov* / «Миша»), так как прекратится поступление свежих данных в `db:clickhouse-lake` через `svc:audit-logger`).*

---

### 3. Инженеры, которых необходимо вызвать в War Room

1. **Александр Иванов (Саша / Alex)** — владелец `db:redis-session` и `svc:auth-gateway`.
2. **Дмитрий Смирнов (Дима / Dima)** — владелец `svc:billing-engine` и `svc:payment-worker`.
3. **Елена Кузнецова (Лена / Helen)** — владелец `svc:fraud-detector` и `svc:audit-logger`.
4. **Ольга Соколова (Оля)** — владелец `svc:notification-dispatcher`.
5. *(Опционально)* **Михаил Попов (Миша / Mike)** — владелец хранилища аналитики `db:clickhouse-lake` и сервиса `svc:analytics-api` (для контроля задержек в отчетности).

**GraphRAG Answer:**
> При отказе кэш-сервера **`srv:cache-01`** выходит из строя размещённая на нём база данных **`db:redis-session`** (`Redis Session`).

### 1. Затронутые сервисы (по цепочке зависимостей):

1. **`svc:auth-gateway`** (*Auth Gateway*) — напрямую зависит от `db:redis-session`.
2. **`svc:billing-engine`** (*Billing Engine*) — зависит от `svc:auth-gateway`.
3. **`svc:payment-worker`** (*Payment Worker*) — зависит от `svc:billing-engine`.
4. **`svc:fraud-detector`** (*Fraud Detector*) — зависит от `svc:payment-worker`.

*Цепочка распространения сбоя:*  
`srv:cache-01` → `db:redis-session` → `svc:auth-gateway` → `svc:billing-engine` → `svc:payment-worker` → `svc:fraud-detector`.

---

### 2. Инженеры для вызова в war room:

1. **Alexander Ivanov** (`person:alexander`, *Staff Architect*)  
   — Владелец сервиса **`svc:auth-gateway`** (а также самой базы данных **`db:redis-session`**).

2. **Dmitry Smirnov** (`person:dmitry`, *SRE Lead*)  
   — Владелец сервисов **`svc:billing-engine`** и **`svc:payment-worker`**.

3. **Elena Kuznetsova** (`person:elena`, *Security Lead*)  
   — Владелец сервиса **`svc:fraud-detector`**.

---

### Global Negation & Aggregation

**Prompt:** *"Сколько ВСЕГО микросервисов (kind: 'Service') в нашей инфраструктуре НЕ зависят от брокера сообщений Kafka (mq:kafka-events)? Назови их точное количество и перечисли их идентификаторы."*

| Metric | Plain Vector RAG | GraphRAG (cypher-mcp) |
| :--- | :--- | :--- |
| **Fact Recall** | **100.0%** | **100.0%** |
| **Turns to Answer** | 7 | 9 |
| **Tool Calls** | 6 | 8 |
| **Latency** | 17.84s | 22.36s |
| **Token Usage** | 27813 | 23513 |

**Plain RAG Answer:**
> В инфраструктуре представлено всего 8 микросервисов (`kind: 'Service'`), из которых **5** напрямую **НЕ зависят** от брокера сообщений Kafka (`mq:kafka-events`).

### Точное количество:
**5 микросервисов**

### Идентификаторы микросервисов:
1. **`svc:user-store`** — зависит только от базы данных `db:postgres-users`.
2. **`svc:auth-gateway`** — зависит напрямую от `svc:user-store` и `db:redis-session`.
3. **`svc:billing-engine`** — зависит напрямую от `svc:auth-gateway` и `db:postgres-orders`.
4. **`svc:fraud-detector`** — напрямую получает данные от `svc:payment-worker` и обращается к `svc:user-store`.
5. **`svc:analytics-api`** — напрямую опрашивает `db:clickhouse-lake` и явным образом не публикует данные в Kafka.

---
*(Остальные 3 сервиса зависят от `mq:kafka-events`: `svc:payment-worker` отправляет в него события, а `svc:notification-dispatcher` и `svc:audit-logger` считывают сообщения).*

**GraphRAG Answer:**
> В инфраструктуре всего **8** микросервисов (`kind: 'Service'`). В зависимости от того, учитываются ли только прямые связи или также транзитивные (косвенные), результат следующий:

---

### 1. С точки зрения прямых зависимостей (`NOT (s)-[:DEPENDS_ON]->(kafka)`)
Прямой связи с брокером `mq:kafka-events` не имеют **5 микросервисов**:

1. `svc:auth-gateway` (Auth Gateway)
2. `svc:billing-engine` (Billing Engine)
3. `svc:fraud-detector` (Fraud Detector)
4. `svc:analytics-api` (Analytics API)
5. `svc:user-store` (User Store)

*(Напрямую зависят 3 сервиса: `svc:payment-worker`, `svc:audit-logger`, `svc:notification-dispatcher`).*

---

### 2. С точки зрения транзитивных (косвенных) зависимостей (`NOT (s)-[:DEPENDS_ON*]->(kafka)`)
Если учитывать цепочки зависимостей, сервис `svc:fraud-detector` зависит от `svc:payment-worker`, который обращается к Kafka. 

С учётом этого полностью независимыми (ни прямо, ни транзитивно) являются **4 микросервиса**:

1. `svc:auth-gateway`
2. `svc:billing-engine`
3. `svc:analytics-api`
4. `svc:user-store`

---

