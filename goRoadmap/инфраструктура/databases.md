#infrastructure #databases #postgresql #sql

Источники: **PostgreSQL Documentation** (postgresql.org/docs/current), **«Database Internals»** (Alex Petrov, 2019), **«Designing Data-Intensive Applications»** (Martin Kleppmann, 2017).

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

---

## ACID

| Свойство | Описание | Механизм в PostgreSQL |
|---|---|---|
| **Atomicity** | транзакция выполняется целиком или не выполняется | WAL + rollback |
| **Consistency** | транзакция переводит БД из одного валидного состояния в другое | constraints, triggers |
| **Isolation** | транзакции не видят промежуточных результатов друг друга | MVCC |
| **Durability** | зафиксированные данные не теряются при сбое | WAL fsync |

---

## MVCC (Multi-Version Concurrency Control)

PostgreSQL хранит **несколько версий** каждого кортежа вместо блокировок на чтение:

```
Tuple:  xmin | xmax | data
        ─────────────────────
        100  | ∞    | v1      ← активная версия, создана транзакцией 100
        100  | 150  | v1      ← версия устарела: удалена транзакцией 150
        150  | ∞    | v2      ← новая версия
```

- **xmin** — ID транзакции, создавшей версию
- **xmax** — ID транзакции, удалившей/обновившей версию (0 = ещё живая)
- Читающая транзакция видит версию, если `xmin <= snapshot_xid && xmax > snapshot_xid`

### Следствие: VACUUM

Устаревшие версии (dead tuples) не удаляются немедленно — нужен **VACUUM**:

```sql
-- Ручной vacuum
VACUUM ANALYZE orders;

-- Autovacuum работает автоматически
-- Проблема: долгая транзакция блокирует vacuum → bloat таблицы
```

---

## Уровни изоляции (ISO SQL + PostgreSQL)

| Уровень | Dirty Read | Non-Repeatable Read | Phantom Read | Serialization Anomaly |
|---|---|---|---|---|
| READ UNCOMMITTED | ✅ (PG: нет) | ✅ | ✅ | ✅ |
| **READ COMMITTED** (PG default) | ❌ | ✅ | ✅ | ✅ |
| REPEATABLE READ | ❌ | ❌ | ✅ (PG: нет) | ✅ |
| **SERIALIZABLE** | ❌ | ❌ | ❌ | ❌ |

**READ COMMITTED:** каждый оператор видит snapshot на момент своего начала.
**REPEATABLE READ:** транзакция видит snapshot на момент своего начала.
**SERIALIZABLE (SSI в PG):** транзакции ведут себя так, будто выполняются строго последовательно.

```sql
BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ;
SELECT balance FROM accounts WHERE id = 42;  -- 1000
-- Другая транзакция: UPDATE accounts SET balance = 500 WHERE id = 42; COMMIT;
SELECT balance FROM accounts WHERE id = 42;  -- 1000 (не 500!)
COMMIT;
```

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

TCP handshake + TLS + PostgreSQL auth = ~5-10мс на новое соединение. Connection pool решает это:

```go
// pgx + pgxpool
pool, err := pgxpool.New(ctx, dsn)
// или конфигурация:
cfg, _ := pgxpool.ParseConfig(dsn)
cfg.MaxConns = 20
cfg.MinConns = 5
cfg.MaxConnLifetime = 30 * time.Minute
cfg.MaxConnIdleTime = 10 * time.Minute
```

**Правило размера пула:** `num_cpus * 2 + num_disks` (рекомендация pgBouncer). Слишком большой пул → конкуренция на уровне БД.

**PgBouncer** — отдельный connection pooler перед PostgreSQL:
- Transaction mode: соединение возвращается в пул после каждой транзакции
- Session mode: соединение на всю сессию

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

## Полезные индексы

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

## Связанные темы

- [[TCP]] — каждое DB-соединение = TCP соединение; handshake = latency
- [[queues]] — Kafka vs PostgreSQL как очередь; transactional outbox pattern
- [[resilience patterns]] — retry при DB failover, circuit breaker для медленных запросов
- [[clean architecture]] — Repository pattern как абстракция над БД
- [[L0 database]] — пример схемы PostgreSQL


package main

import (
    "fmt"
    "runtime"
    "time"
)

func main() {
    runtime.GOMAXPROCS(1)
    go func() {
        fmt.Println(1)
    }()
    go func() {
        fmt.Println(2)
    }()
    go func() {
        fmt.Println(3)
    }()
    go func() {
        fmt.Println(4)
    }()

    <-time.After(10 * time.Millisecond)
}


