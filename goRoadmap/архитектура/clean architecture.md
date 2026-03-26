#architecture #clean-architecture #design

Clean Architecture — архитектурный подход Robert C. Martin («Uncle Bob»), описан в книге **«Clean Architecture: A Craftsman's Guide to Software Structure and Design»** (2017). Цель: **независимость бизнес-логики от фреймворков, БД, UI и внешних сервисов**.

---

## Слои и Dependency Rule

```
        ┌─────────────────────────────────────────┐
        │           Frameworks & Drivers          │  ← Web, DB, UI, Devices
        │   ┌───────────────────────────────────┐ │
        │   │      Interface Adapters           │ │  ← Controllers, Presenters, Gateways
        │   │  ┌───────────────────────────┐    │ │
        │   │  │       Use Cases           │    │ │  ← Application Business Rules
        │   │  │  ┌───────────────────┐    │    │ │
        │   │  │  │    Entities       │    │    │ │  ← Enterprise Business Rules
        │   │  │  └───────────────────┘    │    │ │
        │   │  └───────────────────────────┘    │ │
        │   └───────────────────────────────────┘ │
        └─────────────────────────────────────────┘

Зависимости направлены ТОЛЬКО к центру:
Frameworks → Adapters → Use Cases → Entities
```

**Dependency Rule:** исходный код внешнего слоя может зависеть от кода внутреннего слоя. **Никогда — наоборот.**

---

## Слои в деталях

### 1. Entities (Сущности)

Объекты с **бизнес-правилами уровня предприятия**. Не зависят ни от чего.

```go
// domain/order.go
package domain

type Order struct {
    ID       OrderID
    UserID   UserID
    Items    []Item
    Status   OrderStatus
    Total    Money
}

// Бизнес-правила как методы сущности
func (o *Order) Cancel() error {
    if o.Status == StatusShipped {
        return ErrCannotCancelShippedOrder
    }
    o.Status = StatusCancelled
    return nil
}

func (o *Order) AddItem(item Item) error {
    if o.Status != StatusDraft {
        return ErrOrderNotEditable
    }
    o.Items = append(o.Items, item)
    o.Total = o.calcTotal()
    return nil
}
```

### 2. Use Cases (Сценарии использования)

**Бизнес-правила уровня приложения**. Оркестрируют поток данных между сущностями. Определяют интерфейсы к внешним зависимостям.

```go
// usecase/order.go
package usecase

// Интерфейсы определяются здесь — не в пакете инфраструктуры!
type OrderRepository interface {
    FindByID(ctx context.Context, id domain.OrderID) (*domain.Order, error)
    Save(ctx context.Context, order *domain.Order) error
}

type PaymentGateway interface {
    Charge(ctx context.Context, amount domain.Money, token string) (*Payment, error)
}

type EventPublisher interface {
    Publish(ctx context.Context, event domain.Event) error
}

type OrderService struct {
    orders   OrderRepository
    payments PaymentGateway
    events   EventPublisher
}

func (s *OrderService) PlaceOrder(ctx context.Context, req PlaceOrderRequest) (*domain.Order, error) {
    order := domain.NewOrder(req.UserID, req.Items)

    payment, err := s.payments.Charge(ctx, order.Total, req.PaymentToken)
    if err != nil {
        return nil, fmt.Errorf("payment failed: %w", err)
    }

    order.MarkPaid(payment.ID)

    if err := s.orders.Save(ctx, order); err != nil {
        return nil, err
    }

    s.events.Publish(ctx, domain.OrderPlacedEvent{OrderID: order.ID})
    return order, nil
}
```

### 3. Interface Adapters (Адаптеры)

Преобразуют данные между форматом Use Cases и внешним миром (HTTP, SQL, gRPC).

```go
// adapter/http/order_handler.go
package httphandler

type OrderHandler struct {
    orders usecase.OrderService  // зависит от usecase, не от БД
}

func (h *OrderHandler) PlaceOrder(w http.ResponseWriter, r *http.Request) {
    var body PlaceOrderRequest
    json.NewDecoder(r.Body).Decode(&body)

    order, err := h.orders.PlaceOrder(r.Context(), toUsecaseRequest(body))
    if err != nil {
        writeError(w, err)
        return
    }
    writeJSON(w, 201, toResponse(order))
}

// adapter/postgres/order_repo.go
package postgresrepo

type OrderRepo struct{ db *sql.DB }

func (r *OrderRepo) FindByID(ctx context.Context, id domain.OrderID) (*domain.Order, error) {
    // SQL запрос → маппинг в domain.Order
}
```

### 4. Frameworks & Drivers

Внешние детали: web-фреймворки, ORM, брокеры. Минимум логики.

---

## Структура пакетов в Go

```
myapp/
├── domain/              # Entities (нет зависимостей)
│   ├── order.go
│   ├── user.go
│   └── events.go
│
├── usecase/             # Use Cases (зависит только от domain)
│   ├── order_service.go
│   ├── user_service.go
│   └── ports.go         # интерфейсы (OrderRepository, etc.)
│
├── adapter/             # Interface Adapters
│   ├── http/            # HTTP handlers
│   ├── grpc/            # gRPC handlers
│   ├── postgres/        # DB repositories
│   └── kafka/           # event publisher
│
├── infrastructure/      # Frameworks & Drivers
│   ├── server.go
│   └── config.go
│
└── cmd/
    └── main.go          # Dependency injection / wiring
```

---

## Dependency Injection (wiring в main)

```go
// cmd/main.go
func main() {
    db := postgres.Connect(cfg.DSN)
    kafkaProducer := kafka.NewProducer(cfg.Kafka)

    // Инфраструктура реализует интерфейсы usecase
    orderRepo    := postgresrepo.NewOrderRepo(db)
    paymentGW    := stripe.NewGateway(cfg.StripeKey)
    eventPub     := kafkaadapter.NewPublisher(kafkaProducer)

    // Use cases получают зависимости через интерфейсы
    orderSvc := usecase.NewOrderService(orderRepo, paymentGW, eventPub)

    // HTTP слой получает use cases
    handler := httphandler.NewOrderHandler(orderSvc)

    http.ListenAndServe(":8080", handler)
}
```

---

## Сравнение с другими архитектурами

| | Clean Architecture | Hexagonal (Ports & Adapters) | Layered (N-tier) |
|---|---|---|---|
| Автор | Uncle Bob (2012) | Alistair Cockburn (2005) | классическая |
| Центр | Use Cases | Application Core | Data Layer |
| Dependency Rule | явная, слоями | порты + адаптеры | сверху вниз |
| Тестируемость | высокая | высокая | средняя |
| Сложность | высокая | высокая | низкая |

---

## Когда НЕ использовать

- **CRUD-сервисы** без бизнес-логики → чистая архитектура оверинжиниринг
- **Маленькие сервисы** (< 3 разработчика) → overhead превышает выгоды
- **Прототипы** → быстрая итерация важнее структуры

---

## Связанные темы

- [[go SOLID]] — DIP как основа Dependency Rule; ISP как основа маленьких интерфейсов
- [[interfaces (архитектура)]] — Ports & Adapters как альтернативная терминология
- [[design patterns]] — Repository, Factory, Adapter используются в каждом слое
- [[go Interfaces]] — интерфейсы Go как механизм реализации Dependency Rule
- [[L0 architecture]] — пример применения в проекте L0
