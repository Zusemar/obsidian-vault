# `golang.org/x/sync/errgroup` — разбор исходников

> Пакет: `golang.org/x/sync/errgroup` Файл: `errgroup.go` (~150 строк) Источник: [github.com/golang/sync](https://github.com/golang/sync/blob/master/errgroup/errgroup.go)

---

## Зачем нужен errgroup

`errgroup.Group` — это **`sync.WaitGroup` + обработка ошибок + (опционально) `context` cancellation + (опционально) лимит concurrency**.

Ключевая идея: запускаем N горутин, дожидаемся всех, возвращаем **первую** ошибку.

---

## Структура `Group`

```go
type token struct{}

type Group struct {
    cancel  func(error)      // CancelCauseFunc от context.WithCancelCause
    wg      sync.WaitGroup   // ожидание завершения всех горутин
    sem     chan token        // семафор для ограничения concurrency (nil = без лимита)
    errOnce sync.Once        // гарантирует запись ошибки ровно один раз
    err     error            // первая non-nil ошибка
}
```

### Разбор полей

|Поле|Тип|Роль|
|---|---|---|
|`cancel`|`func(error)`|Если группа создана через `WithContext` — функция отмены контекста. У zero-value Group = `nil`|
|`wg`|`sync.WaitGroup`|Классический счётчик горутин. `Add(1)` при запуске, `Done()` при завершении|
|`sem`|`chan token`|Буферизированный канал размером N — работает как **counting semaphore**. `nil` = без лимита|
|`errOnce`|`sync.Once`|Защищает запись в `err` — только первая ошибка сохраняется|
|`err`|`error`|Хранит первую non-nil ошибку|

### Zero value is valid

```go
var g errgroup.Group  // cancel=nil, sem=nil → нет лимита, нет контекста
```

Zero-value группа полностью работоспособна: без лимита горутин, без отмены контекста, просто WaitGroup + первая ошибка.

---

## `WithContext` — конструктор с контекстом

```go
func WithContext(ctx context.Context) (*Group, context.Context) {
    ctx, cancel := context.WithCancelCause(ctx)
    return &Group{cancel: cancel}, ctx
}
```

### Что происходит

1. Создаётся **derived context** через `context.WithCancelCause` (не `WithCancel`!)
2. `cancel` — это `CancelCauseFunc`, принимает `error` как причину отмены
3. Возвращается **новая Group** (с `cancel`) и **derived context**

### Важный нюанс: `WithCancelCause` vs `WithCancel`

`WithCancelCause` (Go 1.20+) позволяет при отмене передать **причину** — ту самую первую ошибку из горутины. Клиент может достать причину через `context.Cause(ctx)`.

### Кто владеет контекстом

Контекст **не хранится** внутри `Group` — хранится только `cancel`. Контекст возвращается наружу, и вызывающий код передаёт его в горутины сам. Это важно: `Group` не знает о контексте, она знает только как его отменить.

---

## `Go` — запуск горутины

```go
func (g *Group) Go(f func() error) {
    if g.sem != nil {
        g.sem <- token{}       // (1) блокируемся если лимит исчерпан
    }
    g.wg.Add(1)                // (2) инкрементируем счётчик
    go func() {
        defer g.done()         // (4) освобождаем слот + wg.Done()

        if err := f(); err != nil {
            g.errOnce.Do(func() {
                g.err = err              // (3a) сохраняем первую ошибку
                if g.cancel != nil {
                    g.cancel(g.err)      // (3b) отменяем контекст с причиной
                }
            })
        }
    }()
}
```

### Пошаговый разбор

#### Шаг 1: Семафор (rate limiting)

```go
if g.sem != nil {
    g.sem <- token{}
}
```

Если установлен лимит (`SetLimit`), `sem` — буферизированный канал. Отправка в заполненный канал **блокирует вызывающую горутину** до тех пор, пока кто-то не завершится и не освободит слот.

> **Ключевой момент**: блокировка происходит **в вызывающей горутине**, а не в запускаемой. То есть `Go()` блокирует _caller_, пока не появится свободный слот.

#### Шаг 2: WaitGroup

```go
g.wg.Add(1)
```

Стандартный инкремент. Обрати внимание: `Add(1)` вызывается **после** прохождения семафора, но **до** запуска горутины — гарантирует, что `Wait()` точно дождётся.

#### Шаг 3: Выполнение + обработка ошибки

```go
if err := f(); err != nil {
    g.errOnce.Do(func() {
        g.err = err
        if g.cancel != nil {
            g.cancel(g.err)
        }
    })
}
```

- `errOnce.Do` — **только первая ошибка** записывается в `g.err`
- Все последующие ошибки **молча игнорируются**
- Если есть `cancel` — контекст отменяется **сразу при первой ошибке**, не дожидаясь `Wait()`
- Остальные горутины продолжают работать (если сами не слушают контекст)

#### Шаг 4: Cleanup (`done()`)

```go
func (g *Group) done() {
    if g.sem != nil {
        <-g.sem       // освобождаем слот в семафоре
    }
    g.wg.Done()       // декрементируем WaitGroup
}
```

Порядок: сначала освобождаем семафор (разблокируем следующий `Go()`), потом `wg.Done()`.

### Почему паники не перехватываются

В коде есть подробный комментарий (Issues #53757, #74275, #74304, #74306):

1. **Задержка паники** — паника произошла в горутине, но всплывёт только в `Wait()` → баги сложнее отлавливать
2. **Потеря стека** — стек паники превращается в обычное значение, crash-monitoring инструменты его не видят
3. **Риск дедлока** — если паника оставила программу в невалидном состоянии, `Wait()` может никогда не быть вызван → паника скрыта навсегда

---

## `Wait` — ожидание и финализация

```go
func (g *Group) Wait() error {
    g.wg.Wait()               // (1) дожидаемся всех горутин
    if g.cancel != nil {
        g.cancel(g.err)       // (2) отменяем контекст
    }
    return g.err              // (3) возвращаем первую ошибку (или nil)
}
```

### Зачем `cancel` в `Wait`?

Даже если **все горутины завершились успешно** (err == nil), `Wait()` всё равно вызывает `cancel(nil)`. Это **освобождает ресурсы контекста** (утечка goroutine в `context` пакете если не отменить).

> Из документации: _"The derived Context is canceled the first time a function passed to Go returns a non-nil error or the first time Wait returns, whichever occurs first."_

Два пути отмены:

1. **Первая ошибка** → `cancel(err)` внутри `errOnce.Do`
2. **`Wait()` возвращается** → `cancel(g.err)` (может быть `cancel(nil)`)

Повторный вызов `cancel` безопасен — `CancelCauseFunc` идемпотентна (second call is a no-op if already canceled).

---

## `TryGo` — неблокирующая попытка запуска

```go
func (g *Group) TryGo(f func() error) bool {
    if g.sem != nil {
        select {
        case g.sem <- token{}:
            // ok, получили слот
        default:
            return false      // лимит исчерпан, не блокируемся
        }
    }
    g.wg.Add(1)
    go func() {
        defer g.done()
        if err := f(); err != nil {
            g.errOnce.Do(func() {
                g.err = err
                if g.cancel != nil {
                    g.cancel(g.err)
                }
            })
        }
    }()
    return true
}
```

### Отличие от `Go`

||`Go`|`TryGo`|
|---|---|---|
|Лимит исчерпан|**Блокируется**|**Возвращает `false`**|
|Лимит не установлен|Запускает сразу|Запускает сразу (всегда `true`)|
|Механизм|`g.sem <- token{}`|`select { case g.sem <- token{}: ... default: return false }`|

### Комментарий про barging

```go
// Note: this allows barging iff channels in general allow barging.
```

**Barging** — ситуация когда горутина, вызвавшая `TryGo`, может "пролезть" мимо горутин, заблокированных в `Go()`. Это зависит от семантики каналов Go (каналы не гарантируют FIFO для отправителей, заблокированных на полном канале, хотя на практике обычно close to FIFO).

---

## `SetLimit` — установка лимита concurrency

```go
func (g *Group) SetLimit(n int) {
    if n < 0 {
        g.sem = nil           // отрицательное значение = без лимита
        return
    }
    if active := len(g.sem); active != 0 {
        panic(fmt.Errorf("errgroup: modify limit while %v goroutines in the group are still active", active))
    }
    g.sem = make(chan token, n)
}
```

### Ключевые моменты

1. **`n < 0`** → убирает лимит (`sem = nil`)
2. **`n == 0`** → лимит ноль, новые горутины **не смогут запуститься** (канал размера 0, `Go` заблокируется навсегда, `TryGo` всегда `false`)
3. **Паника при активных горутинах** — `len(g.sem)` показывает количество токенов в канале = количество активных горутин
4. **Не thread-safe** — документация: _"The limit must not be modified while any goroutines in the group are still active"_

### Почему `len(g.sem)` = число активных горутин?

- При запуске: `g.sem <- token{}` → `len(g.sem)++`
- При завершении: `<-g.sem` → `len(g.sem)--`
- Таким образом `len(g.sem)` в любой момент = количество работающих горутин

---

## Паттерн семафора на каналах

Это классический паттерн **counting semaphore** через буферизированный канал:

```
sem = make(chan token, N)

acquire: sem <- token{}   // блокируется когда N слотов заняты
release: <-sem            // освобождает слот
```

Отличие от `sync.Semaphore` (из `golang.org/x/sync/semaphore`):

- Канал проще, но не поддерживает weighted acquire
- `semaphore.Weighted` позволяет запрашивать разное количество ресурсов

---

## Happens-before отношения

### При записи ошибки

```
f() returns err  →(happens-before)→  errOnce.Do записывает g.err
                                     (sync.Once гарантирует HB)
```

### При Wait

```
все g.wg.Done()  →(HB)→  g.wg.Wait() возвращается  →(HB)→  чтение g.err
```

`sync.WaitGroup` гарантирует: все `Done()` happens-before `Wait()` return. Значит `g.err`, записанная внутри горутины (до `defer g.done()`), видна после `Wait()`.

### При cancel

```
errOnce.Do { g.err = err; cancel(err) }
```

`sync.Once` гарантирует: запись `g.err` happens-before `cancel(err)`. Контекст отменяется с корректной причиной.

---

## Data race safety

|Поле|Защита|Подробности|
|---|---|---|
|`g.err`|`sync.Once` + `sync.WaitGroup`|Запись через `errOnce.Do`, чтение после `wg.Wait()`|
|`g.sem`|Не модифицируется при активных горутинах|Паника в `SetLimit` если `len(sem) != 0`|
|`g.cancel`|Устанавливается в конструкторе, далее read-only|`CancelCauseFunc` сама thread-safe|
|`g.wg`|Внутренняя синхронизация `sync.WaitGroup`|—|
|`g.errOnce`|Внутренняя синхронизация `sync.Once`|—|

---

## Типичные паттерны использования

### Базовый (без контекста)

```go
var g errgroup.Group

g.Go(func() error {
    return fetchURL("https://example.com/a")
})
g.Go(func() error {
    return fetchURL("https://example.com/b")
})

if err := g.Wait(); err != nil {
    log.Fatal(err)
}
```

### С контекстом (отмена при первой ошибке)

```go
g, ctx := errgroup.WithContext(ctx)

g.Go(func() error {
    return fetchWithCtx(ctx, "/a")  // слушает ctx.Done()
})
g.Go(func() error {
    return fetchWithCtx(ctx, "/b")  // отменится если /a упал
})

if err := g.Wait(); err != nil {
    log.Fatal(err)
}
```

### С лимитом concurrency (bounded parallelism)

```go
g, ctx := errgroup.WithContext(ctx)
g.SetLimit(10)  // максимум 10 одновременных горутин

for _, url := range urls {
    url := url  // capture (не нужно с Go 1.22+)
    g.Go(func() error {
        return fetch(ctx, url)
    })
}

if err := g.Wait(); err != nil {
    log.Fatal(err)
}
```

---

## Частые вопросы на собеседованиях

### Q: Чем errgroup отличается от sync.WaitGroup?

**A:** `WaitGroup` — просто счётчик, не знает об ошибках. `errgroup.Group` добавляет: (1) сбор первой ошибки, (2) интеграцию с `context` для отмены, (3) лимит concurrency через семафор.

### Q: Почему сохраняется только первая ошибка?

**A:** Дизайн-решение: в большинстве случаев первая ошибка — корневая причина, остальные — следствие (например, отмены контекста). Если нужны все ошибки — используй кастомную обёртку с `[]error` + `sync.Mutex`.

### Q: Как устроен лимит concurrency внутри?

**A:** Буферизированный канал размером N работает как counting semaphore. `Go()` отправляет токен (блокируется если полный), `done()` забирает токен (освобождает слот).

### Q: Что произойдёт если горутина паникует?

**A:** Паника не перехватывается — горутина крашит всю программу. Это осознанное решение (см. комментарий в коде и issues).

### Q: Безопасно ли вызывать `Go` из нескольких горутин?

**A:** Да. `wg.Add`, `sem <-`, `errOnce.Do` — всё thread-safe. Но `SetLimit` нельзя вызывать при активных горутинах.

---

## Резюме: что стоит запомнить

1. **5 полей** — `cancel`, `wg`, `sem`, `errOnce`, `err` — каждое с чёткой ответственностью
2. **Zero value работает** — без контекста, без лимита, просто WaitGroup+ошибка
3. **`sync.Once`** — гарантирует сохранение ровно первой ошибки
4. **Семафор на канале** — элегантный counting semaphore для лимита горутин
5. **`WithCancelCause`** — причина отмены = первая ошибка, доступна через `context.Cause()`
6. **`Wait()` всегда отменяет контекст** — освобождает ресурсы даже при успехе
7. **Паники не перехватываются** — осознанный trade-off ради debuggability