#golang #concurrency #patterns

Паттерны конкурентного программирования в Go. Источник: «Concurrency in Go» (Katherine Cox-Buday), Go blog, `golang.org/x/sync`.

---

## 1. Pipeline (Конвейер)

Цепочка горутин, где каждая стадия читает из входного канала и пишет в выходной:

```go
func generate(nums ...int) <-chan int {
    out := make(chan int)
    go func() {
        defer close(out)
        for _, n := range nums {
            out <- n
        }
    }()
    return out
}

func square(in <-chan int) <-chan int {
    out := make(chan int)
    go func() {
        defer close(out)
        for n := range in {
            out <- n * n
        }
    }()
    return out
}

// Использование:
c := generate(2, 3, 4)
sq := square(square(c))
for v := range sq { fmt.Println(v) }  // 16, 81, 256
```

**Правило:** каждая стадия закрывает свой выходной канал через `defer close(out)`.

---

## 2. Fan-out / Fan-in

**Fan-out** — несколько горутин читают из одного канала (параллельная обработка).
**Fan-in** — объединение нескольких каналов в один.

```go
// Fan-out: запускаем N воркеров на одном канале
func fanOut(in <-chan Job, n int) []<-chan Result {
    channels := make([]<-chan Result, n)
    for i := range n {
        channels[i] = worker(in)
    }
    return channels
}

// Fan-in: merge нескольких каналов в один
func merge(cs ...<-chan Result) <-chan Result {
    var wg sync.WaitGroup
    out := make(chan Result)

    output := func(c <-chan Result) {
        defer wg.Done()
        for r := range c {
            out <- r
        }
    }

    wg.Add(len(cs))
    for _, c := range cs {
        go output(c)
    }

    go func() {
        wg.Wait()
        close(out)
    }()
    return out
}
```

---

## 3. Worker Pool (Пул воркеров)

Фиксированное число горутин-воркеров, читающих из общей очереди задач:

```go
func workerPool(ctx context.Context, jobs <-chan Job, numWorkers int) <-chan Result {
    results := make(chan Result, numWorkers)
    var wg sync.WaitGroup

    for range numWorkers {
        wg.Add(1)
        go func() {
            defer wg.Done()
            for {
                select {
                case <-ctx.Done():
                    return
                case job, ok := <-jobs:
                    if !ok {
                        return
                    }
                    results <- process(job)
                }
            }
        }()
    }

    go func() {
        wg.Wait()
        close(results)
    }()
    return results
}
```

---

## 4. Done Channel (Отмена через контекст)

Стандартный паттерн остановки горутин — `context.Context` или done-канал:

```go
func producer(ctx context.Context) <-chan int {
    out := make(chan int)
    go func() {
        defer close(out)
        for i := 0; ; i++ {
            select {
            case <-ctx.Done():
                return
            case out <- i:
            }
        }
    }()
    return out
}

ctx, cancel := context.WithCancel(context.Background())
defer cancel()

for v := range producer(ctx) {
    if v > 5 { cancel() }
}
```

---

## 5. errgroup — Fan-out с обработкой ошибок

`golang.org/x/sync/errgroup` — стандарт для параллельных задач с отменой:

```go
import "golang.org/x/sync/errgroup"

func fetchAll(ctx context.Context, urls []string) ([][]byte, error) {
    g, ctx := errgroup.WithContext(ctx)
    results := make([][]byte, len(urls))

    for i, url := range urls {
        i, url := i, url  // Go < 1.22: захват переменной
        g.Go(func() error {
            body, err := fetch(ctx, url)
            if err != nil {
                return fmt.Errorf("fetch %s: %w", url, err)
            }
            results[i] = body
            return nil
        })
    }

    if err := g.Wait(); err != nil {
        return nil, err  // первая ошибка, остальные отменены через ctx
    }
    return results, nil
}

// Ограничение параллелизма:
g.SetLimit(10)  // не более 10 горутин одновременно
```

---

## 6. Semaphore (Семафор через канал)

Буферизированный канал как счётный семафор:

```go
sem := make(chan struct{}, 10)  // не более 10 параллельных

for _, item := range items {
    sem <- struct{}{}  // acquire
    go func(item Item) {
        defer func() { <-sem }()  // release
        process(item)
    }(item)
}

// Дождаться завершения всех: заполнить семафор
for range cap(sem) {
    sem <- struct{}{}
}
```

Или через `golang.org/x/sync/semaphore`:
```go
s := semaphore.NewWeighted(10)
s.Acquire(ctx, 1)
defer s.Release(1)
```

---

## 7. Rate Limiting (Ограничение частоты)

```go
// Простой rate limiter через time.Ticker
limiter := time.NewTicker(100 * time.Millisecond)  // 10 req/s
defer limiter.Stop()

for req := range requests {
    <-limiter.C  // ждём тик
    go handle(req)
}
```

Для продакшена — `golang.org/x/time/rate` (token bucket):
```go
rl := rate.NewLimiter(rate.Every(100*time.Millisecond), 5)  // 10/s, burst=5

func handle(ctx context.Context, req Request) error {
    if err := rl.Wait(ctx); err != nil {
        return err  // ctx отменён
    }
    return doWork(req)
}
```

---

## 8. Or-Done Channel

Обёртка для чтения из канала с одновременным отслеживанием отмены:

```go
func orDone[T any](ctx context.Context, in <-chan T) <-chan T {
    out := make(chan T)
    go func() {
        defer close(out)
        for {
            select {
            case <-ctx.Done():
                return
            case v, ok := <-in:
                if !ok {
                    return
                }
                select {
                case out <- v:
                case <-ctx.Done():
                    return
                }
            }
        }
    }()
    return out
}
```

---

## 9. Tee Channel

Дублирование потока данных в два канала:

```go
func tee[T any](in <-chan T) (<-chan T, <-chan T) {
    out1 := make(chan T)
    out2 := make(chan T)
    go func() {
        defer close(out1)
        defer close(out2)
        for v := range in {
            out1, out2 := out1, out2  // локальные копии для select
            for range 2 {
                select {
                case out1 <- v:
                    out1 = nil
                case out2 <- v:
                    out2 = nil
                }
            }
        }
    }()
    return out1, out2
}
```

---

## 10. Heartbeat Pattern

Горутина периодически сигнализирует, что жива:

```go
func worker(ctx context.Context, pulse time.Duration) (<-chan struct{}, <-chan Result) {
    heartbeat := make(chan struct{}, 1)
    results := make(chan Result)

    go func() {
        defer close(heartbeat)
        defer close(results)
        ticker := time.NewTicker(pulse)
        defer ticker.Stop()

        for {
            select {
            case <-ctx.Done():
                return
            case <-ticker.C:
                select {
                case heartbeat <- struct{}{}:
                default:  // не блокировать если никто не слушает
                }
            case results <- doWork():
            }
        }
    }()
    return heartbeat, results
}
```

---

## Таблица выбора паттерна

| Задача | Паттерн |
|---|---|
| Параллельная обработка N задач | Worker Pool |
| Параллельные HTTP-запросы с отменой | errgroup |
| Трансформация потока данных | Pipeline |
| Ограничение параллелизма | Semaphore |
| Ограничение частоты запросов | Rate Limiter |
| Несколько источников → один поток | Fan-in / merge |
| Graceful shutdown | Done Channel / context |

---

## Связанные темы

- [[go Channel]] — фундамент всех паттернов; select, buffered channels
- [[go goroutine]] — утечки горутин при неправильной реализации паттернов
- [[go context]] — отмена и дедлайны; `errgroup.WithContext`
- [[go sync.Pool]] — снижение аллокаций в worker pool
- [[Go — sync atomic (атомики)]] — lock-free счётчики для rate limiter
- [[go scheduler]] — work stealing оптимизирует fan-out без дополнительных настроек
