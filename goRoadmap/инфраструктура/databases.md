#infrastructure #databases #postgresql #sql #transactions

Источники: **PostgreSQL Documentation** (postgresql.org/docs/current), **«Database Internals»** (Alex Petrov, 2019), **«Designing Data-Intensive Applications»** (Martin Kleppmann, 2017).

---

## Транзакции

Транзакция — логическая единица работы, объединяющая несколько операций чтения и записи в один блок.

- **Цель:** упростить модель программирования — приложение может игнорировать часть сбоев и сложности конкурентного доступа.
- **Результат:** транзакция либо **commit** (всё применено), либо **abort/rollback** (частичных изменений не остаётся).

---

## Гарантии ACID

| Свойство | Суть | Механизм в PostgreSQL |
|---|---|---|
| **Atomicity** | «Всё или ничего» — при сбое все частичные изменения отбрасываются | WAL + rollback |
| **Consistency** | БД переходит из одного корректного состояния в другое | constraints, triggers, логика приложения |
| **Isolation** | Параллельные транзакции не мешают друг другу | MVCC |
| **Durability** | После commit данные не пропадут даже при сбое питания | WAL fsync |

> **Важно про Consistency:** за неё отвечает скорее _приложение_, чем сама БД — инварианты (например, «баланс не может быть отрицательным») задаёт разработчик.

---

## Аномалии конкурентности

Уровни изоляции нужны для защиты от следующих эффектов:

| Аномалия | Описание |
|---|---|
| **Dirty Read** | Транзакция видит незафиксированные данные другой транзакции (могут быть отменены) |
| **Dirty Write** | Транзакция перезаписывает ещё не зафиксированные изменения другой транзакции |
| **Read Skew** (Non-repeatable Read) | Повторное чтение тех же строк даёт другой результат из-за чужого commit между чтениями |
| **Phantom Read** | Повторный запрос возвращает другое количество строк (INSERT от другой транзакции) |
| **Write Skew** | Две транзакции читают одни данные, принимают решение и пишут в _разные_ объекты, нарушая инвариант. Пример: два дежурных врача одновременно берут отгул, думая, что второй останется |

---

## Уровни изоляции

| Уровень                         | Dirty Read  | Read Skew | Phantom Read | Write Skew |
| ------------------------------- | ----------- | --------- | ------------ | ---------- |
| READ UNCOMMITTED                | ✅ (PG: нет) | ✅         | ✅            | ✅          |
| **READ COMMITTED** (PG default) | ❌           | ✅         | ✅            | ✅          |
| REPEATABLE READ / Snapshot      | ❌           | ❌         | ✅ (PG: нет)  | ✅          |
| **SERIALIZABLE**                | ❌           | ❌         | ❌            | ❌          |

### READ UNCOMMITTED

Практически нет изоляции. Транзакция может прочитать **незафиксированные** изменения другой транзакции.

> В PostgreSQL этот уровень ведёт себя как READ COMMITTED — грязное чтение намеренно не реализовано.

```sql
-- Транзакция A:
BEGIN;
UPDATE accounts SET balance = 9999 WHERE id = 1;
-- ещё не COMMIT

-- Транзакция B (READ UNCOMMITTED в другой СУБД):
SELECT balance FROM accounts WHERE id = 1;
-- → 9999  (грязное чтение! A может сделать ROLLBACK)
```

**Когда использовать:** почти никогда. Только если допустима грязная статистика (счётчики, приблизительные отчёты) и нужна максимальная скорость.

---

### READ COMMITTED _(дефолт в PostgreSQL и Oracle)_

Каждый **отдельный оператор** внутри транзакции видит свежий snapshot — данные, зафиксированные на момент _его_ старта.

```sql
-- Транзакция A:
BEGIN;
SELECT balance FROM accounts WHERE id = 1;  -- → 1000

-- Транзакция B (параллельно):
BEGIN;
UPDATE accounts SET balance = 500 WHERE id = 1;
COMMIT;

-- Транзакция A продолжает:
SELECT balance FROM accounts WHERE id = 1;  -- → 500  ← Read Skew!
COMMIT;
-- Два SELECT в одной транзакции вернули разные значения.
```

**Защищает от:** Dirty Read, Dirty Write.
**Не защищает от:** Read Skew, Phantom Read, Write Skew.
**Когда использовать:** большинство OLTP-приложений, где не нужна полная изоляция.

---

### REPEATABLE READ / Snapshot Isolation

Транзакция видит **снимок (snapshot) данных на момент своего старта** и не замечает чужих commit'ов в процессе работы.

```sql
BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ;

SELECT balance FROM accounts WHERE id = 1;  -- → 1000

-- Транзакция B (параллельно): UPDATE ... SET balance = 500; COMMIT;

SELECT balance FROM accounts WHERE id = 1;  -- → 1000 (не 500!)
-- Snapshot "заморожен" на начало транзакции A.
COMMIT;
```

**Пример Write Skew** (этот уровень не защищает):

```sql
-- Инвариант: хотя бы один врач должен быть на дежурстве.
-- Сейчас дежурят: Иван и Мария.

-- Транзакция A (Иван берёт отгул):
BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ;
SELECT COUNT(*) FROM doctors WHERE on_duty = true;  -- → 2, можно
UPDATE doctors SET on_duty = false WHERE name = 'Иван';
COMMIT;

-- Транзакция B (Мария берёт отгул, читает snapshot ДО commit A):
BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ;
SELECT COUNT(*) FROM doctors WHERE on_duty = true;  -- → 2 (snapshot!), можно
UPDATE doctors SET on_duty = false WHERE name = 'Мария';
COMMIT;

-- Итог: никто не дежурит — инвариант нарушен.
```

**Защищает от:** Dirty Read, Dirty Write, Read Skew, Phantom Read (в PostgreSQL).
**Не защищает от:** Write Skew.
**Когда использовать:** отчёты, аналитика, длинные read-only транзакции, бэкапы онлайн.

---

### SERIALIZABLE _(SSI в PostgreSQL)_

Самый строгий уровень. Гарантирует, что результат параллельного выполнения **эквивалентен** какому-либо последовательному порядку. Устраняет Write Skew.

В PostgreSQL реализован через **SSI (Serializable Snapshot Isolation)** — оптимистичный подход: транзакции работают параллельно, БД отслеживает зависимости и при конфликте прерывает одну из них с ошибкой `ERROR: could not serialize access`.

```sql
-- Тот же пример с врачами — теперь на SERIALIZABLE:

-- Транзакция A:
BEGIN TRANSACTION ISOLATION LEVEL SERIALIZABLE;
SELECT COUNT(*) FROM doctors WHERE on_duty = true;  -- → 2
UPDATE doctors SET on_duty = false WHERE name = 'Иван';
COMMIT;  -- ОК

-- Транзакция B (параллельно):
BEGIN TRANSACTION ISOLATION LEVEL SERIALIZABLE;
SELECT COUNT(*) FROM doctors WHERE on_duty = true;  -- → 2
UPDATE doctors SET on_duty = false WHERE name = 'Мария';
COMMIT;
-- ERROR: could not serialize access due to read/write dependencies
-- Приложение должно повторить транзакцию B.
```

**Цена:** небольшой overhead на отслеживание зависимостей + необходимость retry при сериализационных конфликтах.
**Защищает от:** всех аномалий.
**Когда использовать:** финансовые операции, инварианты целостности между несколькими строками/таблицами.

---

## Механизмы реализации

### MVCC (Multi-Version Concurrency Control)

> Мантра MVCC: **читатели не блокируют писателей, писатели не блокируют читателей.**

PostgreSQL хранит **несколько версий** каждого кортежа вместо блокировок на чтение:

```
Tuple:  xmin | xmax | data
        ─────────────────────
        100  | ∞    | v1      ← активная версия, создана транзакцией 100
        100  | 150  | v1      ← устарела: удалена транзакцией 150
        150  | ∞    | v2      ← новая версия
```

- **xmin** — ID транзакции, создавшей версию
- **xmax** — ID транзакции, удалившей/обновившей версию (0 = ещё живая)
- Читатель видит версию, если `xmin <= snapshot_xid && xmax > snapshot_xid`

#### Следствие: VACUUM

Устаревшие версии (dead tuples) не удаляются немедленно — нужен **VACUUM**:

```sql
VACUUM ANALYZE orders;
-- Autovacuum работает автоматически
-- Проблема: долгая транзакция блокирует vacuum → bloat таблицы
```

### Блокировки (Locks)

Самый простой способ:
- **Shared lock** — читающая транзакция; несколько shared-блокировок совместимы
- **Exclusive lock** — пишущая транзакция; несовместима ни с чем

### 2PL (Двухфазная блокировка)

Традиционный способ обеспечения сериализуемости: если транзакция A читает данные, транзакция B должна ждать с их записью — и наоборот. Создаёт высокую конкуренцию при нагрузке.

### SSI (Serializable Snapshot Isolation)

Современный **оптимистичный** подход, используемый в PostgreSQL:
1. Транзакции выполняются параллельно без жёстких блокировок.
2. БД отслеживает зависимости между транзакциями.
3. При обнаружении нарушения сериализуемости одна из транзакций прерывается.

---

## Индексы

### B-Tree (основной тип в PostgreSQL)

Сбалансированное дерево. Высота = O(log N). Все данные в листовых узлах:

```
                    ┌─────────────┐
                    │   [50, 75]  │  ← внутренний узел (ключи-сепараторы)
                    └──┬──────┬───┘
          ┌────────────┘      └────────────┐
     ┌────┴────┐                    ┌──────┴────┐
     │ [10,30] │                    │ [60,70,80]│  ← листовые узлы
     └─────────┘                    └──────────-┘
          │                              │
   ┌──────────────┐              ┌──────────────┐
   │ heap туплы   │              │ heap туплы   │
   └──────────────┘              └──────────────┘
```

**Поддерживает:** `=`, `<`, `>`, `<=`, `>=`, `BETWEEN`, `LIKE 'foo%'`.
**Не поддерживает:** `LIKE '%foo'`, JSON операции, full-text search.

### Hash Index

O(1) lookup, **только** `=`. В PostgreSQL WAL-safe с версии 10.

### GIN (Generalized Inverted Index)

Для многозначных данных: массивы, JSONB, tsvector (full-text search):

```sql
CREATE INDEX idx_tags ON articles USING GIN(tags);
SELECT * FROM articles WHERE tags @> ARRAY['go', 'backend'];
```

### BRIN (Block Range INdex)

Для очень больших таблиц с **физической корреляцией** данных (time-series, append-only):

```sql
CREATE INDEX idx_created ON events USING BRIN(created_at);
-- Хранит только min/max значение для каждого диапазона блоков
-- Размер: ~1000x меньше B-Tree
```

### Полезные приёмы с индексами

```sql
-- Partial index: только для подмножества данных
CREATE INDEX idx_pending_orders ON orders (created_at)
WHERE status = 'pending';

-- Covering index: содержит все нужные данные (index-only scan)
CREATE INDEX idx_orders_covering ON orders (user_id, created_at)
INCLUDE (status, total);

-- Composite: порядок важен! (user_id, status) ≠ (status, user_id)
-- Работает для: WHERE user_id=? AND status=?
-- Работает для: WHERE user_id=?
-- НЕ работает для: WHERE status=?  (без leading column)
```

---

## WAL (Write-Ahead Log)

Основа durability и репликации в PostgreSQL:

```
Транзакция:
1. Запись изменений в WAL (sequential write = быстро)
2. WAL fsync (гарантия durability)
3. Обновление heap/index pages (может быть отложено)

При сбое:
- Replay WAL → heap pages восстанавливаются
```

**Репликация:** standby читает WAL от primary в режиме streaming.

---

## EXPLAIN ANALYZE

```sql
EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT)
SELECT o.*, u.name
FROM orders o
JOIN users u ON u.id = o.user_id
WHERE o.status = 'pending'
  AND o.created_at > NOW() - INTERVAL '7 days'
ORDER BY o.created_at DESC
LIMIT 100;
```

**Читать снизу вверх** (inner → outer):

```
Limit  (cost=... rows=100)
  -> Sort  (cost=...)
    -> Hash Join  (cost=...)
         Hash Cond: (o.user_id = u.id)
         -> Seq Scan on orders  ← нет индекса на (status, created_at)!
         -> Hash
              -> Seq Scan on users
```

**Ключевые метрики:**
- `actual time=X..Y` — реальное время
- `rows=X` vs `rows=Y` — оценка vs реальность (большое расхождение → ANALYZE)
- `Seq Scan` на большой таблице → нужен индекс
- `Buffers: hit=X read=Y` — X из кэша, Y с диска

---

## Connection Pooling

TCP handshake + TLS + PostgreSQL auth = ~5–10 мс на новое соединение. Connection pool решает это:

```go
// pgx + pgxpool
pool, err := pgxpool.New(ctx, dsn)

cfg, _ := pgxpool.ParseConfig(dsn)
cfg.MaxConns = 20
cfg.MinConns = 5
cfg.MaxConnLifetime = 30 * time.Minute
cfg.MaxConnIdleTime = 10 * time.Minute
```

**Правило размера пула:** `num_cpus * 2 + num_disks` (рекомендация pgBouncer). Слишком большой пул → конкуренция на уровне БД.

**PgBouncer** — отдельный connection pooler перед PostgreSQL:
- **Transaction mode:** соединение возвращается в пул после каждой транзакции
- **Session mode:** соединение на всю сессию

---

## N+1 Problem

```go
// ПЛОХО: 1 запрос за users + N запросов за orders
users, _ := db.Query("SELECT * FROM users")
for _, user := range users {
    orders, _ := db.Query("SELECT * FROM orders WHERE user_id=$1", user.ID)
    user.Orders = orders
}

// ХОРОШО: 2 запроса
users, _ := db.Query("SELECT * FROM users")
userIDs := extractIDs(users)
orders, _ := db.Query("SELECT * FROM orders WHERE user_id = ANY($1)", userIDs)
// группируем orders по user_id в памяти

// ИЛИ JOIN:
rows, _ := db.Query(`
    SELECT u.*, o.*
    FROM users u
    LEFT JOIN orders o ON o.user_id = u.id
`)
```

---

## Связанные темы

- [[TCP]] — каждое DB-соединение = TCP соединение; handshake = latency
- [[queues]] — Kafka vs PostgreSQL как очередь; transactional outbox pattern
- [[resilience patterns]] — retry при DB failover, circuit breaker для медленных запросов
- [[clean architecture]] — Repository pattern как абстракция над БД
