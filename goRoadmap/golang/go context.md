#golang #concurrency #context #stdlib

`context.Context` — интерфейс для распространения сигналов отмены, дедлайнов и значений через дерево вызовов. Источник: `src/context/context.go`.

---

## Интерфейс

```go
type Context interface {
    Deadline() (deadline time.Time, ok bool)
    Done() <-chan struct{}
    Err() error          // nil | context.Canceled | context.DeadlineExceeded
    Value(key any) any
}
```

`Done()` возвращает канал, который **закрывается** при отмене. Это позволяет использовать его в `select`.

---

## Дерево контекстов

Контексты образуют **дерево**: дочерний наследует отмену родителя, но не наоборот.

```
context.Background()
    └── WithCancel → ctx1
            ├── WithTimeout(5s) → ctx2
            │       └── WithValue("user", u) → ctx3
            └── WithCancel → ctx4  (можно отменить независимо)
```

При отмене `ctx1` — отменяются `ctx2`, `ctx3`, `ctx4` и все их потомки.

---

## Корневые контексты

```go
context.Background()  // корень дерева, никогда не отменяется
context.TODO()        // заглушка: "контекст добавлю позже"
```

`TODO()` — семантическая метка для grep: `grep -r "context.TODO"` покажет места, требующие внимания.

---

## WithCancel

```go
ctx, cancel := context.WithCancel(parent)
defer cancel()  // ВСЕГДА вызывать cancel, иначе утечка горутин

go func() {
    select {
    case <-ctx.Done():
        // получили сигнал отмены
        cleanup()
        return
    case result := <-work():
        process(result)
    }
}()

cancel() // явная отмена
```

Вызов `cancel()` дважды — безопасен (идемпотентен).

---

## WithTimeout / WithDeadline

```go
// WithTimeout = WithDeadline(parent, time.Now().Add(timeout))
ctx, cancel := context.WithTimeout(parent, 5*time.Second)
defer cancel()

req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
resp, err := http.DefaultClient.Do(req)
if err != nil {
    if errors.Is(err, context.DeadlineExceeded) {
        // таймаут
    }
}
```

```go
ctx, cancel := context.WithDeadline(parent, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
defer cancel()
```

---

## WithValue

Передача метаданных запроса (трейс ID, user ID, логгер):

```go
// Ключ — всегда unexported тип, чтобы избежать коллизий
type ctxKey string
const userKey ctxKey = "user"

ctx = context.WithValue(ctx, userKey, user)

// Получение
if u, ok := ctx.Value(userKey).(*User); ok {
    ...
}
```

**Правила для Value:**
- Не используй для передачи обязательных параметров функций
- Только для данных уровня запроса: trace ID, auth token, logger
- Ключ — всегда unexported тип (не строка напрямую)

---

## Паттерн: распространение отмены

```go
func fetchAll(ctx context.Context, urls []string) ([]Result, error) {
    g, ctx := errgroup.WithContext(ctx)  // golang.org/x/sync/errgroup

    results := make([]Result, len(urls))
    for i, url := range urls {
        i, url := i, url
        g.Go(func() error {
            r, err := fetch(ctx, url)
            if err != nil {
                return err  // отменяет все остальные горутины через ctx
            }
            results[i] = r
            return nil
        })
    }
    return results, g.Wait()
}
```

---

## Паттерн: context в select

```go
func worker(ctx context.Context, jobs <-chan Job) {
    for {
        select {
        case <-ctx.Done():
            log.Println("worker stopping:", ctx.Err())
            return
        case job, ok := <-jobs:
            if !ok {
                return  // канал закрыт
            }
            process(ctx, job)
        }
    }
}
```

---

## Внутреннее устройство (src/context/context.go)

### cancelCtx

```go
type cancelCtx struct {
    Context                        // родительский контекст
    mu       sync.Mutex
    done     atomic.Value          // chan struct{}, лениво создаётся
    children map[canceler]struct{} // дочерние контексты
    err      error
    cause    error
}
```

При отмене `cancelCtx`:
1. Закрывает канал `done`
2. Рекурсивно отменяет все `children`
3. Отвязывается от родителя

### propagateCancel

При создании дочернего контекста рантайм ищет ближайший `cancelCtx` в цепочке родителей и регистрирует дочерний в его `children`. Это обеспечивает O(1) нотификацию.

### WithCancelCause (Go 1.20+)

```go
ctx, cancel := context.WithCancelCause(parent)
cancel(errors.New("rate limit exceeded"))

// Получение причины:
context.Cause(ctx)  // → errors.New("rate limit exceeded")
ctx.Err()           // → context.Canceled
```

### AfterFunc (Go 1.21+)

```go
// Выполняет f в новой горутине когда ctx отменён
stop := context.AfterFunc(ctx, func() {
    conn.Close()  // cleanup при отмене
})
defer stop()  // отменить регистрацию если не нужно
```

---

## Антипаттерны

```go
// ПЛОХО: хранить контекст в структуре
type Server struct {
    ctx context.Context  // ← неправильно
}

// ХОРОШО: передавать как первый аргумент
func (s *Server) Handle(ctx context.Context, req *Request) error { ... }
```

```go
// ПЛОХО: игнорировать ctx.Done() в долгой операции
func process(ctx context.Context, items []Item) {
    for _, item := range items {
        heavyWork(item)  // ctx не проверяется
    }
}

// ХОРОШО
func process(ctx context.Context, items []Item) error {
    for _, item := range items {
        if err := ctx.Err(); err != nil {
            return err
        }
        heavyWork(item)
    }
    return nil
}
```

```go
// ПЛОХО: не вызывать cancel → утечка горутин и памяти
ctx, _ = context.WithTimeout(parent, time.Second)

// ХОРОШО
ctx, cancel := context.WithTimeout(parent, time.Second)
defer cancel()
```

---

## Связанные темы

- [[go goroutine]] — каждая горутина должна слушать `ctx.Done()` для graceful shutdown
- [[go Channel]] — `ctx.Done()` возвращает `<-chan struct{}`, используется в select
- [[go concurrency patterns]] — errgroup, pipeline с отменой через context
- [[go sync atomic (атомики)]] — `done atomic.Value` внутри cancelCtx
- [[go sync package]] — `mu sync.Mutex` защищает children map в cancelCtx
