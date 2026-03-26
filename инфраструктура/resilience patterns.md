#infrastructure #resilience #patterns #distributed-systems

Паттерны надёжности распределённых систем. Источники: **«Release It!»** (Michael Nygard, 2018), **«Designing Data-Intensive Applications»** (Kleppmann, 2017), **Netflix Tech Blog** (Hystrix, Resilience4j).

---

## Circuit Breaker

Предотвращает каскадный отказ: когда downstream сервис нездоров — быстро возвращаем ошибку вместо ожидания таймаута.

### Состояния автомата

```
                  threshold errors
  CLOSED ─────────────────────────▶ OPEN
    ▲                                  │
    │ success                          │ half-open timeout
    │                                  ▼
    └──────────────────────────── HALF-OPEN
                                (1 test request)
```

- **CLOSED** — нормальная работа, считаем ошибки
- **OPEN** — сразу возвращаем ошибку (fail-fast), не нагружаем downstream
- **HALF-OPEN** — пробуем один запрос; успех → CLOSED, ошибка → OPEN

### Реализация на Go

```go
type CircuitBreaker struct {
    mu           sync.Mutex
    state        State  // Closed, Open, HalfOpen
    failures     int
    threshold    int
    timeout      time.Duration
    lastFailure  time.Time
}

func (cb *CircuitBreaker) Call(fn func() error) error {
    cb.mu.Lock()
    switch cb.state {
    case Open:
        if time.Since(cb.lastFailure) > cb.timeout {
            cb.state = HalfOpen
        } else {
            cb.mu.Unlock()
            return ErrCircuitOpen  // fail fast
        }
    }
    cb.mu.Unlock()

    err := fn()

    cb.mu.Lock()
    defer cb.mu.Unlock()
    if err != nil {
        cb.failures++
        cb.lastFailure = time.Now()
        if cb.failures >= cb.threshold {
            cb.state = Open
        }
    } else {
        cb.failures = 0
        cb.state = Closed
    }
    return err
}
```

**Метрики для threshold:**
- Процент ошибок за окно (sliding window) — более устойчиво чем счётчик
- Или количество ошибок подряд (consecutive failures)

---

## Retry with Exponential Backoff + Jitter

Повторная попытка при временных ошибках. **Без jitter** все клиенты повторяют синхронно → thundering herd.

```go
type RetryConfig struct {
    MaxAttempts int
    BaseDelay   time.Duration
    MaxDelay    time.Duration
    Multiplier  float64
}

func Retry(ctx context.Context, cfg RetryConfig, fn func() error) error {
    var err error
    delay := cfg.BaseDelay

    for attempt := 0; attempt < cfg.MaxAttempts; attempt++ {
        if err = fn(); err == nil {
            return nil
        }

        // Не retry на non-retriable ошибки
        if !isRetriable(err) {
            return err
        }

        if attempt == cfg.MaxAttempts-1 {
            break
        }

        // Exponential backoff + full jitter
        jitter := time.Duration(rand.Int63n(int64(delay)))
        sleep := min(delay, cfg.MaxDelay)/2 + jitter/2

        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-time.After(sleep):
        }

        delay = time.Duration(float64(delay) * cfg.Multiplier)
    }
    return fmt.Errorf("after %d attempts: %w", cfg.MaxAttempts, err)
}

func isRetriable(err error) bool {
    // Retry: network errors, 503, 429 (с Retry-After)
    // NO retry: 400, 401, 403, 404
    var netErr *net.OpError
    if errors.As(err, &netErr) { return true }
    // ...
}
```

**Jitter стратегии** (AWS Architecture Blog, 2015):
- **Full Jitter:** `sleep = random(0, cap)` — лучший для снижения нагрузки на сервер
- **Equal Jitter:** `sleep = cap/2 + random(0, cap/2)`
- **Decorrelated Jitter:** `sleep = min(cap, random(base, prev*3))`

---

## Timeout

Каждый внешний вызов **должен** иметь таймаут. Без него одна медленная зависимость подвешивает все потоки.

```go
// HTTP клиент с таймаутами
client := &http.Client{
    Timeout: 5 * time.Second,  // полный таймаут запроса
    Transport: &http.Transport{
        DialContext:           (&net.Dialer{Timeout: 1 * time.Second}).DialContext,
        TLSHandshakeTimeout:   1 * time.Second,
        ResponseHeaderTimeout: 3 * time.Second,  // первый байт ответа
        IdleConnTimeout:       90 * time.Second,
        MaxIdleConns:          100,
    },
}

// Context timeout (более гибко — propagate через chain вызовов)
ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
defer cancel()

req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
resp, err := client.Do(req)
```

**Hedged Requests** (Tail Latency Reduction):

```go
// Если первый запрос медленный — отправляем второй через 95-й перцентиль
// Берём ответ того, кто ответил первым
func hedgedRequest(ctx context.Context, fn func(context.Context) (*Response, error)) (*Response, error) {
    ctx, cancel := context.WithCancel(ctx)
    defer cancel()

    results := make(chan result, 2)
    sendRequest := func() {
        r, err := fn(ctx)
        select {
        case results <- result{r, err}:
        default:
        }
    }

    go sendRequest()
    time.AfterFunc(p95latency, func() { go sendRequest() })

    r := <-results
    return r.resp, r.err
}
```

---

## Bulkhead (Переборки)

Изоляция ресурсов для разных типов запросов — поломка одного не топит всё:

```go
// Отдельные пулы goroutine/соединений для разных клиентов
type BulkheadExecutor struct {
    sem chan struct{}  // ограничение параллелизма
}

func NewBulkhead(limit int) *BulkheadExecutor {
    return &BulkheadExecutor{sem: make(chan struct{}, limit)}
}

func (b *BulkheadExecutor) Execute(ctx context.Context, fn func() error) error {
    select {
    case b.sem <- struct{}{}:
        defer func() { <-b.sem }()
        return fn()
    case <-ctx.Done():
        return ctx.Err()
    default:
        return ErrBulkheadFull  // fast rejection
    }
}

// Разные пулы для разных сервисов:
paymentsBulkhead  := NewBulkhead(20)  // не более 20 параллельных запросов к payments
inventoryBulkhead := NewBulkhead(10)
notifyBulkhead    := NewBulkhead(5)
```

---

## Rate Limiting

### Token Bucket (golang.org/x/time/rate)

```go
// 100 req/s с burst до 200
limiter := rate.NewLimiter(100, 200)

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    if !limiter.Allow() {
        http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
        return
    }
    h.next.ServeHTTP(w, r)
}

// Или с ожиданием (подходит для producer):
if err := limiter.Wait(ctx); err != nil {
    return err  // ctx отменён
}
```

### Distributed Rate Limiting (Redis)

```lua
-- Redis скрипт (атомарный через EVAL)
local key = KEYS[1]
local limit = tonumber(ARGV[1])
local window = tonumber(ARGV[2])

local current = redis.call("INCR", key)
if current == 1 then
    redis.call("EXPIRE", key, window)
end
if current > limit then
    return 0
end
return 1
```

---

## Fallback

```go
func getProductPrice(ctx context.Context, id string) (Price, error) {
    price, err := priceService.Get(ctx, id)
    if err != nil {
        // Fallback 1: локальный кэш
        if cached, ok := priceCache.Get(id); ok {
            return cached, nil
        }
        // Fallback 2: дефолтная цена
        return defaultPrice, nil
    }
    priceCache.Set(id, price)
    return price, nil
}
```

---

## Dead Letter Queue (DLQ)

Сообщения, которые не удалось обработать после N попыток → отдельная очередь для анализа:

```
Normal Queue → Consumer → Успех: commit offset
                       → Ошибка (retry 1..3): nack + delay
                       → Ошибка (retry > 3): → DLQ
                                                │
                                          Monitoring alert
                                          Manual review
                                          Replay after fix
```

---

## Паттерны в комбинации (типичный HTTP клиент)

```go
type ResilientClient struct {
    http     *http.Client       // timeout
    breaker  *CircuitBreaker    // circuit breaker
    bulkhead *BulkheadExecutor  // параллелизм
    limiter  *rate.Limiter      // rate limit
}

func (c *ResilientClient) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
    // 1. Rate limit
    if err := c.limiter.Wait(ctx); err != nil { return nil, err }

    // 2. Bulkhead
    var resp *http.Response
    err := c.bulkhead.Execute(ctx, func() error {
        // 3. Circuit Breaker
        return c.breaker.Call(func() error {
            // 4. Retry
            return Retry(ctx, defaultRetryConfig, func() error {
                // 5. Timeout (через context)
                var err error
                resp, err = c.http.Do(req.WithContext(ctx))
                return err
            })
        })
    })
    return resp, err
}
```

---

## Связанные темы

- [[queues]] — DLQ реализуется через отдельный топик/очередь; retry с backoff
- [[databases]] — circuit breaker для медленных DB запросов; connection pool как bulkhead
- [[gRPC]] — retry policy в gRPC service config; deadline propagation
- [[go context]] — timeout через context; cancellation propagation
- [[go concurrency patterns]] — semaphore как bulkhead; rate limiter паттерны
- [[HTTP]] — 429 Too Many Requests; Retry-After заголовок; 503 Service Unavailable
