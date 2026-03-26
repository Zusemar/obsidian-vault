#architecture #design-patterns #gof

Паттерны проектирования — повторяемые решения для типичных проблем. Источник: **«Design Patterns: Elements of Reusable Object-Oriented Software»** (Gamma, Helm, Johnson, Vlissides — «Gang of Four», 1994). Примеры на Go.

---

## Creational (Порождающие)

### Factory Method

```go
type Notifier interface {
    Send(ctx context.Context, msg Message) error
}

func NewNotifier(kind string) (Notifier, error) {
    switch kind {
    case "email": return &EmailNotifier{}, nil
    case "sms":   return &SMSNotifier{}, nil
    case "push":  return &PushNotifier{}, nil
    default:      return nil, fmt.Errorf("unknown notifier: %s", kind)
    }
}
```

### Abstract Factory

```go
// Фабрика создаёт семейство связанных объектов
type StorageFactory interface {
    NewUserRepo()  UserRepository
    NewOrderRepo() OrderRepository
}

type PostgresFactory struct{ db *sql.DB }
func (f *PostgresFactory) NewUserRepo()  UserRepository { return &pgUserRepo{f.db} }
func (f *PostgresFactory) NewOrderRepo() OrderRepository { return &pgOrderRepo{f.db} }

type InMemoryFactory struct{}
func (f *InMemoryFactory) NewUserRepo()  UserRepository { return &memUserRepo{} }
func (f *InMemoryFactory) NewOrderRepo() OrderRepository { return &memOrderRepo{} }
```

### Builder

```go
type QueryBuilder struct {
    table  string
    wheres []string
    limit  int
    offset int
}

func (b *QueryBuilder) From(table string) *QueryBuilder {
    b.table = table; return b
}
func (b *QueryBuilder) Where(cond string) *QueryBuilder {
    b.wheres = append(b.wheres, cond); return b
}
func (b *QueryBuilder) Limit(n int) *QueryBuilder {
    b.limit = n; return b
}
func (b *QueryBuilder) Build() string {
    q := "SELECT * FROM " + b.table
    if len(b.wheres) > 0 {
        q += " WHERE " + strings.Join(b.wheres, " AND ")
    }
    if b.limit > 0 { q += fmt.Sprintf(" LIMIT %d", b.limit) }
    return q
}

// Использование:
q := new(QueryBuilder).From("orders").Where("status='paid'").Limit(10).Build()
```

### Singleton

```go
// В Go — через sync.Once
type Config struct{ DSN string }

var (
    instance *Config
    once     sync.Once
)

func GetConfig() *Config {
    once.Do(func() {
        instance = &Config{DSN: os.Getenv("DATABASE_URL")}
    })
    return instance
}
```

### Prototype (Clone)

```go
type Template struct {
    Name    string
    Headers map[string]string
}

func (t *Template) Clone() *Template {
    headers := make(map[string]string, len(t.Headers))
    for k, v := range t.Headers {
        headers[k] = v
    }
    return &Template{Name: t.Name, Headers: headers}
}
```

---

## Structural (Структурные)

### Adapter

```go
// Существующий интерфейс (ожидаем)
type Logger interface {
    Log(level, msg string)
}

// Внешняя библиотека с другим интерфейсом
type ZapLogger struct{ z *zap.Logger }

// Адаптер
type ZapAdapter struct{ zap *ZapLogger }

func (a *ZapAdapter) Log(level, msg string) {
    switch level {
    case "info":  a.zap.z.Info(msg)
    case "error": a.zap.z.Error(msg)
    }
}
```

### Decorator (Wrapper)

```go
type Handler interface {
    Handle(ctx context.Context, req Request) (Response, error)
}

// Добавляем логирование не меняя бизнес-логику
type LoggingHandler struct {
    next   Handler
    logger *slog.Logger
}

func (h *LoggingHandler) Handle(ctx context.Context, req Request) (Response, error) {
    h.logger.Info("request", "method", req.Method)
    resp, err := h.next.Handle(ctx, req)
    h.logger.Info("response", "status", resp.Status, "err", err)
    return resp, err
}

// Стекирование декораторов:
var h Handler = &BusinessHandler{}
h = &LoggingHandler{next: h, logger: logger}
h = &MetricsHandler{next: h, metrics: metrics}
h = &AuthHandler{next: h, verifier: verifier}
```

### Proxy

```go
// Кэширующий прокси для репозитория
type CachedOrderRepo struct {
    repo  OrderRepository
    cache map[OrderID]*Order
    mu    sync.RWMutex
}

func (r *CachedOrderRepo) FindByID(ctx context.Context, id OrderID) (*Order, error) {
    r.mu.RLock()
    if o, ok := r.cache[id]; ok {
        r.mu.RUnlock()
        return o, nil
    }
    r.mu.RUnlock()

    o, err := r.repo.FindByID(ctx, id)
    if err != nil { return nil, err }

    r.mu.Lock()
    r.cache[id] = o
    r.mu.Unlock()
    return o, nil
}
```

### Facade

```go
// Скрывает сложность нескольких подсистем за простым интерфейсом
type OrderFacade struct {
    orders   OrderService
    payments PaymentService
    shipping ShippingService
    notify   NotificationService
}

func (f *OrderFacade) PlaceOrder(ctx context.Context, req PlaceOrderRequest) error {
    order, _ := f.orders.Create(ctx, req)
    payment, _ := f.payments.Charge(ctx, order.Total, req.CardToken)
    shipment, _ := f.shipping.Schedule(ctx, order)
    f.notify.SendConfirmation(ctx, order, payment, shipment)
    return nil
}
```

### Composite

```go
// Дерево компонентов с одинаковым интерфейсом
type FileSystemNode interface {
    Size() int64
    Name() string
}

type File struct{ name string; size int64 }
func (f *File) Size() int64  { return f.size }
func (f *File) Name() string { return f.name }

type Directory struct {
    name     string
    children []FileSystemNode
}
func (d *Directory) Size() int64 {
    var total int64
    for _, c := range d.children { total += c.Size() }
    return total
}
```

---

## Behavioral (Поведенческие)

### Strategy

```go
type SortStrategy interface {
    Sort(data []int)
}

type Sorter struct{ strategy SortStrategy }

func (s *Sorter) SetStrategy(st SortStrategy) { s.strategy = st }
func (s *Sorter) Sort(data []int)              { s.strategy.Sort(data) }

// Можно использовать функцию как стратегию (идиома Go)
type SortFunc func([]int)
func (f SortFunc) Sort(data []int) { f(data) }

sorter := &Sorter{}
sorter.SetStrategy(SortFunc(sort.Ints))
```

### Observer

```go
type EventBus struct {
    mu          sync.RWMutex
    subscribers map[string][]func(Event)
}

func (b *EventBus) Subscribe(event string, fn func(Event)) {
    b.mu.Lock()
    defer b.mu.Unlock()
    b.subscribers[event] = append(b.subscribers[event], fn)
}

func (b *EventBus) Publish(event Event) {
    b.mu.RLock()
    subs := b.subscribers[event.Type]
    b.mu.RUnlock()
    for _, fn := range subs {
        go fn(event)  // асинхронно
    }
}
```

### Command

```go
type Command interface {
    Execute() error
    Undo() error
}

type MoveCommand struct {
    piece     *ChessPiece
    from, to  Position
}

func (c *MoveCommand) Execute() error { return c.piece.MoveTo(c.to) }
func (c *MoveCommand) Undo() error    { return c.piece.MoveTo(c.from) }

type CommandHistory struct{ history []Command }
func (h *CommandHistory) Execute(cmd Command) error {
    if err := cmd.Execute(); err != nil { return err }
    h.history = append(h.history, cmd)
    return nil
}
func (h *CommandHistory) Undo() error {
    if len(h.history) == 0 { return nil }
    last := h.history[len(h.history)-1]
    h.history = h.history[:len(h.history)-1]
    return last.Undo()
}
```

### Iterator (Go 1.23+ range-over-func)

```go
// Go 1.23+: функция-итератор
func (t *BinaryTree) All() iter.Seq[int] {
    return func(yield func(int) bool) {
        var traverse func(n *Node) bool
        traverse = func(n *Node) bool {
            if n == nil { return true }
            return traverse(n.Left) && yield(n.Val) && traverse(n.Right)
        }
        traverse(t.root)
    }
}

// Использование через range:
for v := range tree.All() {
    fmt.Println(v)
}
```

### Template Method (через embedding)

```go
type ReportGenerator struct{}

func (r *ReportGenerator) Generate(header, body, footer func() string) string {
    return header() + "\n" + body() + "\n" + footer()
}

// «Переопределяем» шаги через функции (аналог abstract methods)
pdfReport := gen.Generate(
    func() string { return "=== PDF REPORT ===" },
    func() string { return fetchData() },
    func() string { return "Generated: " + time.Now().String() },
)
```

### Chain of Responsibility

```go
// Цепочка middleware — классический пример в Go
type Middleware func(http.Handler) http.Handler

func Chain(h http.Handler, middlewares ...Middleware) http.Handler {
    for i := len(middlewares) - 1; i >= 0; i-- {
        h = middlewares[i](h)
    }
    return h
}

handler := Chain(
    businessHandler,
    authMiddleware,
    loggingMiddleware,
    rateLimitMiddleware,
)
```

---

## Go-специфичные паттерны

### Functional Options (Builder без Builder)

```go
type Server struct {
    port    int
    timeout time.Duration
    logger  *slog.Logger
}

type Option func(*Server)

func WithPort(p int) Option           { return func(s *Server) { s.port = p } }
func WithTimeout(d time.Duration) Option { return func(s *Server) { s.timeout = d } }
func WithLogger(l *slog.Logger) Option { return func(s *Server) { s.logger = l } }

func NewServer(opts ...Option) *Server {
    s := &Server{port: 8080, timeout: 30 * time.Second}  // defaults
    for _, opt := range opts { opt(s) }
    return s
}
```

---

## Связанные темы

- [[go SOLID]] — паттерны как реализация принципов SOLID
- [[clean architecture]] — Repository, Factory, Adapter — слои архитектуры
- [[interfaces (архитектура)]] — паттерны реализуются через интерфейсы
- [[go Interfaces]] — интерфейсы Go как основа структурных и поведенческих паттернов
- [[resilience patterns]] — Circuit Breaker, Retry — паттерны надёжности
