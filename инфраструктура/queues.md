#infrastructure #queues #kafka #messaging

Источники: **Apache Kafka Documentation** (kafka.apache.org), **«Designing Data-Intensive Applications»** (Kleppmann, 2017), **AMQP 1.0 Spec**.

---

## Message Queue vs Pub-Sub

| | Message Queue | Pub-Sub |
|---|---|---|
| Получатель | один (competing consumers) | все подписчики |
| Примеры | RabbitMQ (queue), SQS | Kafka topic, Redis Pub/Sub |
| Retention | удаляется после получения | хранится (offset-based) |

---

## Apache Kafka

### Архитектура

```
Producers
   │
   ▼
┌──────────────────────────────────────────────┐
│              Kafka Cluster                   │
│  ┌────────────────────────────────────────┐  │
│  │           Topic: orders                │  │
│  │  ┌──────────┐ ┌──────────┐ ┌────────┐ │  │
│  │  │Partition0│ │Partition1│ │Part..2 │ │  │
│  │  │[0][1][2] │ │[0][1]    │ │[0][1]  │ │  │
│  │  │  Leader  │ │  Leader  │ │ Leader │ │  │
│  │  └──────────┘ └──────────┘ └────────┘ │  │
│  └────────────────────────────────────────┘  │
└──────────────────────────────────────────────┘
   │
   ▼
Consumer Groups
   ├── Group A: [Consumer1(P0), Consumer2(P1), Consumer3(P2)]
   └── Group B: [Consumer1(P0,P1,P2)]  (один consumer — все партиции)
```

### Partition

- Упорядоченный, неизменяемый лог сообщений
- Каждое сообщение имеет **offset** (монотонно растёт)
- Одна партиция = один consumer в группе = гарантированный порядок
- Количество партиций = максимальный параллелизм в группе

### Retention

```
# По времени
log.retention.hours=168  # 7 дней

# По размеру
log.retention.bytes=1073741824  # 1 GB per partition

# Компактный лог (последнее значение по ключу)
cleanup.policy=compact  # для CDC, state snapshots
```

---

## Producer

### Partitioning

```go
// По умолчанию:
// 1. Если key != nil: hash(key) % num_partitions
// 2. Если key == nil: round-robin (Kafka 2.4+: sticky partitioning)

// Пример с ключом (гарантия порядка для одного user_id):
msg := &sarama.ProducerMessage{
    Topic: "orders",
    Key:   sarama.StringEncoder(order.UserID),  // ← один user → одна partition
    Value: sarama.ByteEncoder(orderJSON),
}
producer.SendMessage(msg)
```

### Delivery Guarantees (acks)

```
acks=0:  не ждать подтверждения (максимальная скорость, возможна потеря)
acks=1:  ждать leader (потеря при смерти leader до репликации)
acks=-1: ждать all ISR replicas (максимальная надёжность)
         min.insync.replicas=2  ← минимум реплик в ISR
```

### Idempotent Producer (Exactly Once в рамках одного producer)

```
enable.idempotence=true
# → acks=-1 автоматически
# → Producer ID + Sequence Number → дедупликация на брокере
```

---

## Consumer Group

```go
// Каждый consumer group независимо читает все сообщения
// Kafka хранит committed offsets в топике __consumer_offsets

config := sarama.NewConfig()
config.Consumer.Offsets.Initial = sarama.OffsetNewest  // или OffsetOldest
config.Consumer.Group.Rebalance.Strategy = sarama.NewBalanceStrategyRoundRobin()

group, _ := sarama.NewConsumerGroup(brokers, "my-group", config)

// Commit offset (at-least-once):
session.MarkMessage(msg, "")  // помечаем для commit
// Auto-commit по интервалу или ручной commitSync
```

### Rebalancing

При добавлении/удалении consumer в группу происходит **rebalance** — перераспределение партиций:

- **Eager (Stop-the-World):** все consumer останавливаются, партиции перераспределяются заново
- **Cooperative (Kafka 2.4+):** инкрементальный rebalance, минимальный downtime

---

## Delivery Semantics

| Семантика | Как | Потери | Дубли |
|---|---|---|---|
| At-most-once | commit до обработки | возможны | нет |
| **At-least-once** | commit после обработки | нет | **возможны** |
| Exactly-once | транзакции Kafka | нет | нет |

**At-least-once** — стандарт. Consumer должен быть **идемпотентным**:

```go
// Обработка с дедупликацией
func processOrder(ctx context.Context, msg *sarama.ConsumerMessage) error {
    var order Order
    json.Unmarshal(msg.Value, &order)

    // Idempotency key в БД
    _, err := db.ExecContext(ctx, `
        INSERT INTO processed_events (event_id, processed_at)
        VALUES ($1, NOW())
        ON CONFLICT (event_id) DO NOTHING
    `, order.ID)
    if err != nil { return err }

    return processOrder(ctx, order)
}
```

---

## Kafka Transactions (Exactly-once)

```
Producer транзакция:
1. initTransactions()
2. beginTransaction()
3. send(topic-A, msg1), send(topic-B, msg2)
4. commitTransaction() / abortTransaction()

Транзакционные записи видны consumer'у только после commit.
Consumer должен: isolation.level=read_committed
```

**Transactional Outbox Pattern** (without Kafka transactions):

```sql
-- В одной транзакции БД:
INSERT INTO orders VALUES (...);
INSERT INTO outbox (event_type, payload, processed) VALUES ('order.created', '...', false);
COMMIT;

-- Отдельный процесс (outbox poller) читает outbox → публикует в Kafka → помечает processed=true
```

---

## Lag Monitoring

```bash
# Kafka CLI
kafka-consumer-groups.sh --bootstrap-server kafka:9092 \
    --group my-group --describe

# Вывод:
GROUP     TOPIC  PARTITION  CURRENT-OFFSET  LOG-END-OFFSET  LAG
my-group  orders 0          1000            1050            50
my-group  orders 1          800             800             0
```

**Consumer lag** = разница между LOG-END-OFFSET и CURRENT-OFFSET. Критичный lag → добавить consumers (до числа партиций).

---

## RabbitMQ vs Kafka

| | RabbitMQ | Kafka |
|---|---|---|
| Модель | Smart broker, dumb consumer | Dumb broker, smart consumer |
| Порядок | per-queue | per-partition |
| Retention | до получения (TTL настраиваемый) | настраиваемое время/размер |
| Replay | нет | да (по offset) |
| Throughput | ~50k msg/s | ~1M msg/s |
| Routing | Flexible (exchanges) | нет |
| Use case | task queues, routing | event streaming, CDC |

---

## Связанные темы

- [[databases]] — Transactional Outbox использует БД; Kafka как альтернатива PostgreSQL LISTEN/NOTIFY
- [[resilience patterns]] — dead letter queue, retry при ошибках обработки
- [[clean architecture]] — EventPublisher как secondary port
- [[go concurrency patterns]] — consumer как worker pool; fan-out для параллельной обработки
- [[TCP]] — Kafka использует TCP; persistent connections к брокерам
