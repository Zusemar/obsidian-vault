#architecture #interfaces #ports-adapters #design

Архитектурные интерфейсы — механизм инверсии зависимостей и изоляции модулей. Источники: **Alistair Cockburn «Hexagonal Architecture» (2005)**, **Robert Martin «Clean Architecture» (2017)**, **Martin Fowler «Patterns of Enterprise Application Architecture» (2002)**.

---

## Hexagonal Architecture (Ports & Adapters)

Alistair Cockburn, 2005. Цель: приложение должно **одинаково работать** с разными внешними системами и быть тестируемым в изоляции.

```
                    ┌──────────────────────────────┐
  HTTP Request  ──▶ │                              │ ──▶  PostgreSQL
  gRPC Call     ──▶ │   Application Core           │ ──▶  Kafka
  CLI Command   ──▶ │   (Domain + Use Cases)       │ ──▶  Email Service
  Test Driver   ──▶ │                              │ ──▶  In-Memory (test)
                    └──────────────────────────────┘
                         ▲                  ▲
                   Primary Ports      Secondary Ports
                   (Driving)          (Driven)
```

### Primary Ports (Driving Side)

Входящие интерфейсы — то, что **вызывает** приложение. Определяются в core:

```go
// port/input/order.go — определяется в application core
package port

type OrderUseCase interface {
    PlaceOrder(ctx context.Context, req PlaceOrderInput) (*PlaceOrderOutput, error)
    GetOrder(ctx context.Context, id OrderID) (*Order, error)
    CancelOrder(ctx context.Context, id OrderID) error
}
```

Адаптеры к primary ports (HTTP, gRPC, CLI):
```go
// adapter/primary/http/order_handler.go
type OrderHTTPHandler struct {
    uc port.OrderUseCase  // зависит от порта, не от реализации
}
```

### Secondary Ports (Driven Side)

Исходящие интерфейсы — то, что приложение **вызывает** во внешнем мире:

```go
// port/output/storage.go
package port

type OrderRepository interface {
    FindByID(ctx context.Context, id OrderID) (*Order, error)
    Save(ctx context.Context, order *Order) error
    FindByUser(ctx context.Context, userID UserID, filter Filter) ([]*Order, error)
}

type PaymentGateway interface {
    Charge(ctx context.Context, req ChargeRequest) (*ChargeResult, error)
    Refund(ctx context.Context, paymentID string, amount Money) error
}

type EventPublisher interface {
    Publish(ctx context.Context, topic string, event Event) error
}
```

Адаптеры к secondary ports:
```go
// adapter/secondary/postgres/order_repo.go
type PostgresOrderRepo struct{ db *pgxpool.Pool }
func (r *PostgresOrderRepo) FindByID(...) (*Order, error) { /* SQL */ }

// adapter/secondary/memory/order_repo.go (для тестов)
type MemoryOrderRepo struct{ orders map[OrderID]*Order }
func (r *MemoryOrderRepo) FindByID(...) (*Order, error) { /* in-memory */ }
```

---

## Interface Segregation в архитектуре

Принцип Роберта Мартина: **клиент не должен зависеть от методов, которые не использует**.

Архитектурный следствие — **интерфейсы определяются рядом с потребителем**:

```go
// ❌ Широкий интерфейс в репозитории
type OrderRepository interface {
    FindByID(...)
    FindByUser(...)
    FindByStatus(...)
    Save(...)
    Delete(...)
    Count() int
    Stats() RepoStats
    Backup(io.Writer) error
}

// ✅ Раздробленные интерфейсы по потребителям
// Нужен только read для cache middleware:
type OrderFinder interface {
    FindByID(ctx context.Context, id OrderID) (*Order, error)
}

// Нужен только write для event handler:
type OrderSaver interface {
    Save(ctx context.Context, order *Order) error
}

// Полный интерфейс только там, где нужно всё:
type OrderRepository interface {
    OrderFinder
    OrderSaver
    FindByUser(ctx context.Context, userID UserID) ([]*Order, error)
}
```

---

## Interface Discovery (идиома Go)

В Go интерфейсы **имплицитны** — тип удовлетворяет интерфейсу без явного объявления. Это меняет процесс проектирования:

```
Java/C#: Define interface → Implement class → Use interface
Go:      Implement struct → Discover interface → Extract interface
```

```go
// 1. Пишем конкретную реализацию
type PostgresOrderRepo struct{ db *sql.DB }
func (r *PostgresOrderRepo) FindByID(ctx context.Context, id string) (*Order, error) { ... }
func (r *PostgresOrderRepo) Save(ctx context.Context, o *Order) error { ... }

// 2. В usecase — определяем МИНИМАЛЬНО необходимый интерфейс
type orderRepo interface {  // unexported — локален для пакета
    FindByID(ctx context.Context, id string) (*Order, error)
    Save(ctx context.Context, o *Order) error
}

// 3. Проверка совместимости в compile time
var _ orderRepo = (*PostgresOrderRepo)(nil)
```

---

## API Design Principles

### Минимальный публичный интерфейс

```go
// Экспортируй минимум — расширить можно всегда, сузить — никогда
package cache

// Публичный интерфейс — только то, что нужно потребителям
type Cache interface {
    Get(ctx context.Context, key string) ([]byte, bool)
    Set(ctx context.Context, key string, val []byte, ttl time.Duration)
    Delete(ctx context.Context, key string)
}

// Всё остальное — детали реализации
type redisCache struct {
    client    *redis.Client
    keyPrefix string
    metrics   *cacheMetrics
}
```

### Accept Interfaces, Return Structs

Идиома Go: функции принимают интерфейсы (для гибкости), возвращают конкретные типы (для богатого API):

```go
// ✅ Принимаем интерфейс — легко тестировать, легко заменить реализацию
func NewOrderService(repo OrderRepository, gw PaymentGateway) *OrderService {
    return &OrderService{repo: repo, gw: gw}
}

// ✅ Возвращаем конкретный тип — вызывающий код получает весь API
// Если нужен интерфейс — вызывающий определит его сам (минимально необходимый)
```

---

## Versioning интерфейсов

```go
// Версионирование через пакеты
package storage/v1
package storage/v2  // breaking change → новая версия

// Расширение без breaking change — новые опциональные методы
type Closer interface{ Close() error }

// Клиент проверяет поддержку через type assertion
if c, ok := repo.(io.Closer); ok {
    defer c.Close()
}
```

---

## Связанные темы

- [[go SOLID]] — ISP и DIP как теоретическая база
- [[clean architecture]] — конкретная реализация ports & adapters
- [[design patterns]] — Adapter, Facade, Proxy — паттерны на уровне интерфейсов
- [[go Interfaces]] — имплицитные интерфейсы Go как инструмент
- [[gRPC]] — service definition в .proto как явное описание порта
