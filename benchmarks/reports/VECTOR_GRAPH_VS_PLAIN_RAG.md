# Benchmark Report: GraphRAG (cypher-mcp) vs. Plain Vector RAG

Evaluation comparing **GraphRAG (`cypher-mcp` with Vector Entity Resolution + OpenCypher)** against **Plain Vector RAG (Embedding Chunk Search)**.

Evaluator: `Google Gemini 3.8 Flash` | Embedding Model: `gemini-embedding-001`

## Summary Scorecard

| Paradigm | Avg Recall | Avg Turns | Avg Latency | Avg Tokens |
| :--- | :--- | :--- | :--- | :--- |
| **GraphRAG (cypher-mcp)** | **50.0%** | **8.5** | **16.5s** | **23569** |
| **Plain Vector RAG** | **76.8%** | **6.8** | **18.0s** | **19066** |

## Detailed Task Results

### Colloquial 1-Hop Discovery

**Prompt:** *"Кто у нас отвечает за шлюз авторизации (auth gateway)? Пользователь в чате пишет: 'Спроси у Саши, это его сервис?'. Назови полное имя ответственного инженера и его точную роль."*

| Metric | Plain Vector RAG | GraphRAG (cypher-mcp) |
| :--- | :--- | :--- |
| **Fact Recall** | **50.0%** | **100.0%** |
| **Turns to Answer** | 10 | 4 |
| **Tool Calls** | 9 | 7 |
| **Latency** | 17.76s | 7.08s |
| **Token Usage** | 18570 | 8032 |

**Plain RAG Answer:**
> Да, пользователь в чате прав — это действительно сервис Саши.

* **Полное имя ответственного инженера:** Александр Иванов (*Alexander Ivanov*, в Slack упоминается как «Саша» или «Alex»).
* **Точная роль по отношению к сервису:** Владелец и мейнтейнер (*Owner and Maintainer*) сервиса `svc:auth-gateway`.

**GraphRAG Answer:**
> Да, шлюз авторизации (**Auth Gateway** / `svc:auth-gateway`) действительно находится в зоне ответственности Саши.

* **Полное имя ответственного:** Alexander Ivanov (Александр Иванов)
* **Точная роль:** Staff Architect

---

### Multi-Hop Transitive Dependency (2-Hop)

**Prompt:** *"В каком именно датацентре (город и название зоны) физически расположен сервер базы данных, от которой зависит платежный воркер (svc:payment-worker)?"*

| Metric | Plain Vector RAG | GraphRAG (cypher-mcp) |
| :--- | :--- | :--- |
| **Fact Recall** | **100.0%** | **0.0%** |
| **Turns to Answer** | 4 | 10 |
| **Tool Calls** | 3 | 10 |
| **Latency** | 11.79s | 20.17s |
| **Token Usage** | 3633 | 24195 |

**Plain RAG Answer:**
> Сервер базы данных, от которой зависит платежный воркер (`svc:payment-worker`), расположен в следующем датацентре:

* **Город:** Франкфурт-на-Майне (Frankfurt am Main, Германия)
* **Название зоны (датацентра):** `dc:frankfurt-zone-1`

### Цепочка зависимостей:
1. Сервис **`svc:payment-worker`** напрямую зависит от **`svc:billing-engine`** для получения инвойсов.
2. Сервис **`svc:billing-engine`** использует базу данных **`db:postgres-orders`** для хранения счетов и финансовых записей.
3. База данных **`db:postgres-orders`** размещена на физическом сервере **`srv:db-node-01`**.
4. Физический сервер **`srv:db-node-01`** находится в датацентре **`dc:frankfurt-zone-1`** (г. Франкфурт-на-Майне).

**GraphRAG Answer:**
> TIMEOUT / MAX TURNS EXCEEDED

---

### Blast Radius Impact Chain (3-Hop)

**Prompt:** *"Инженеры зафиксировали отказ кэш-сервера srv:cache-01. Перечисли ВСЕ сервисы, чья работа будет затронута по цепочке зависимостей, и ВСЕХ инженеров (владельцев этих сервисов), которых нужно вызвать в war room."*

| Metric | Plain Vector RAG | GraphRAG (cypher-mcp) |
| :--- | :--- | :--- |
| **Fact Recall** | **57.1%** | **100.0%** |
| **Turns to Answer** | 9 | 10 |
| **Tool Calls** | 8 | 9 |
| **Latency** | 31.9s | 21.51s |
| **Token Usage** | 43581 | 31105 |

**Plain RAG Answer:**
> При отказе кэш-сервера **`srv:cache-01`** цепочка зависимостей и список затронутых компонентов формируются следующим образом:

---

### Цепочка зависимостей и затронутые сервисы/компоненты

1. **`db:redis-session`** (Кэш сессий)
   * **Размещение:** Физически развернут на упавшем сервере `srv:cache-01`.
   * **Владелец / ответственный:** Александр Иванов («Саша»).

2. **`svc:auth-gateway`** (Шлюз аутентификации и выдачи JWT)
   * **Зависимость:** Напрямую зависит от `db:redis-session` для проверки токенов сессий.
   * **Владелец:** Александр Иванов («Саша» / «Alex»).

3. **`svc:billing-engine`** (Движок подписок и биллинга)
   * **Зависимость:** Напрямую зависит от `svc:auth-gateway` для валидации пользователей.
   * **Владелец:** Дмитрий Смирнов («Дима» / «Dima»).

4. **`svc:payment-worker`** (Фоновый сервис обработки платежей)
   * **Зависимость:** Напрямую зависит от `svc:billing-engine` для получения счетов и отправляет события в `mq:kafka-events`.
   * **Владелец:** Дмитрий Смирнов («Дима»).

5. **`svc:fraud-detector`** (Сервис антифрода в реальном времени)
   * **Зависимость:** Напрямую потребляет транзакции от `svc:payment-worker`.
   * **Владелец:** Елена Кузнецова («Лена» / «Helen»).

6. **Сервисы-подписчики очереди событий (`mq:kafka-events`)**, затронутые из-за нарушения потока транзакций от `svc:payment-worker`:
   * **`svc:notification-dispatcher`** (Транзакционные SMS и email) — слушает события из `mq:kafka-events`.  
     *Владелец:* Ольга Соколова («Оля»).
   * **`svc:audit-logger`** (Логирование финансовых операций и безопасности) — потребляет события из `mq:kafka-events`.  
     *Владелец:* Елена Кузнецова («Лена»).

---

### Инженеры, которых необходимо вызвать в war room

1. **Александр Иванов** (`Саша` / `Alex`) — владелец упавшей инфраструктуры кэша `db:redis-session` и сервиса `svc:auth-gateway`.
2. **Дмитрий Смирнов** (`Дима` / `Dima`) — владелец сервисов `svc:billing-engine` и `svc:payment-worker`.
3. **Елена Кузнецова** (`Лена` / `Helen`) — владелец сервисов `svc:fraud-detector` и `svc:audit-logger`.
4. **Ольга Соколова** (`Оля`) — владелец сервиса `svc:notification-dispatcher`.

**GraphRAG Answer:**
> В результате отказа кэш-сервера **`srv:cache-01`** выходит из строя размещенная на нем база сессий **`db:redis-session` (Redis Session)**.

---

### 1. Цепочка влияния и затронутые сервисы

По графу зависимостей (`DEPENDS_ON`) сбой распространяется следующим образом:

1. **`svc:auth-gateway` (Auth Gateway)** — *прямая зависимость*: сервис напрямую зависит от `db:redis-session`.
2. **`svc:billing-engine` (Billing Engine)** — *транзитивная зависимость 1-го уровня*: сервис зависит от `svc:auth-gateway`.
3. **`svc:payment-worker` (Payment Worker)** — *транзитивная зависимость 2-го уровня*: сервис зависит от `svc:billing-engine`.
4. **`svc:fraud-detector` (Fraud Detector)** — *транзитивная зависимость 3-го уровня*: сервис зависит от `svc:payment-worker`.

**Всего затронуто сервисов: 4**
* `svc:auth-gateway` (Auth Gateway)
* `svc:billing-engine` (Billing Engine)
* `svc:payment-worker` (Payment Worker)
* `svc:fraud-detector` (Fraud Detector)

---

### 2. Инженеры (владельцы затронутых сервисов) для вызова в War Room

1. **Alexander Ivanov** (`person:alexander`, *Staff Architect*)
   * Владелец сервиса: **Auth Gateway** (`svc:auth-gateway`)
   * *(Также является владельцем отказавшей базы данных `db:redis-session`)*
2. **Dmitry Smirnov** (`person:dmitry`, *SRE Lead*)
   * Владелец сервисов: **Billing Engine** (`svc:billing-engine`) и **Payment Worker** (`svc:payment-worker`)
3. **Elena Kuznetsova** (`person:elena`, *Security Lead*)
   * Владелец сервиса: **Fraud Detector** (`svc:fraud-detector`)

---

### Global Negation & Aggregation

**Prompt:** *"Сколько ВСЕГО микросервисов (kind: 'Service') в нашей инфраструктуре НЕ зависят от брокера сообщений Kafka (mq:kafka-events)? Назови их точное количество и перечисли их идентификаторы."*

| Metric | Plain Vector RAG | GraphRAG (cypher-mcp) |
| :--- | :--- | :--- |
| **Fact Recall** | **100.0%** | **0.0%** |
| **Turns to Answer** | 4 | 10 |
| **Tool Calls** | 3 | 10 |
| **Latency** | 10.41s | 17.18s |
| **Token Usage** | 10481 | 30946 |

**Plain RAG Answer:**
> В инфраструктуре зарегистрировано **8 микросервисов** (`kind: 'Service'`), из которых **3** зависят от брокера сообщений `mq:kafka-events` (`svc:notification-dispatcher`, `svc:audit-logger`, `svc:payment-worker`).

**НЕ зависят** от `mq:kafka-events` ровно **5 микросервисов**:

1. **`svc:analytics-api`** — напрямую опрашивает `db:clickhouse-lake` и не публикует события в Kafka.
2. **`svc:auth-gateway`** — зависит напрямую от `svc:user-store` и базы `db:redis-session`.
3. **`svc:billing-engine`** — зависит от `svc:auth-gateway` и базы `db:postgres-orders`.
4. **`svc:fraud-detector`** — напрямую потребляет транзакции из `svc:payment-worker` и обращается к `svc:user-store`.
5. **`svc:user-store`** — зависит только от базы `db:postgres-users`.

**GraphRAG Answer:**
> TIMEOUT / MAX TURNS EXCEEDED

---

