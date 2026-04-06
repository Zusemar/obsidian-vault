# Постулаты и философия Go

#go #fundamentals #philosophy

---

## Философия дизайна

Go создавался в Google (Robert Griesemer, Rob Pike, Ken Thompson, 2009) как ответ на конкретную боль: медленная компиляция C++, сложность управления зависимостями в больших кодовых базах, трудность написания concurrent-кода.

Ключевые принципы:
- **Simplicity over cleverness** — язык намеренно маленький. Нет наследования, нет дженериков (до 1.18), нет исключений, нет перегрузки операторов.
- **Composition over inheritance** — вместо иерархий классов используется embedding и интерфейсы.
- **Explicit over implicit** — ошибки возвращаются явно, преобразования типов явные, нет неявных конверсий между числовыми типами.
- **One way to do it** — `gofmt` как единый стиль, минимум синтаксического сахара.
- **Readability matters more than writability** — код читается чаще, чем пишется.

> "A little copying is better than a little dependency." — Go Proverbs

---

## Система типов

### Базовые типы
- Числовые: `int8/16/32/64`, `uint8/16/32/64`, `float32/64`, `complex64/128`
- `int` и `uint` — платформо-зависимые (32 или 64 бит)
- `byte` = alias для `uint8`, `rune` = alias для `int32` (Unicode code point)
- `string` — immutable последовательность байт (не рун!), внутренне `{pointer, length}`
- Zero values: `0` для чисел, `""` для строк, `nil` для указателей/слайсов/map/chan/func/interface, `false` для bool

### Составные типы
- **Array** `[N]T` — value type, размер — часть типа, `[3]int ≠ [4]int`
- **Slice** `[]T` — `{pointer, length, capacity}`, ссылается на underlying array → [[go slices]]
- **Map** `map[K]V` — hash-таблица, reference type, не потокобезопасна → [[go map]]
- **Struct** — value type, поля размещаются в памяти с выравниванием (padding)
- **Pointer** `*T` — нет арифметики указателей (только через `unsafe`)

### Интерфейсы
- Implicit satisfaction — нет `implements`, достаточно иметь нужные методы
- Внутреннее представление: `iface = {itab*, data*}` для непустых, `eface = {type*, data*}` для `interface{}`/`any`
- Nil interface ≠ interface с nil-значением — классическая ловушка → [[go Interfaces]]
- Маленькие интерфейсы (`io.Reader`, `io.Writer`, `error`) — идиоматичны

### Дженерики (Go 1.18+)
- Type parameters: `func Map[T any, R any](s []T, f func(T) R) []R`
- Constraints через интерфейсы: `comparable`, `constraints.Ordered`
- `~T` — underlying type constraint
- Нет specialization, нет variadic type parameters → [[go generics]]

---

## Управление памятью

### Stack vs Heap
- Компилятор решает через **escape analysis** (`go build -gcflags="-m"`) → [[go escape analysis]]
- Если переменная "убегает" из функции — аллоцируется на heap
- Goroutine stack начинается маленьким (~2-8 KB) и растёт динамически (segmented → contiguous stacks с Go 1.4)

### Garbage Collector
- **Tri-color mark-and-sweep**, concurrent, non-generational → [[go gc]]
- Write barrier включается во время GC-цикла
- Цель: пауза <500μs на GC цикл
- `GOGC` — контролирует частоту (по умолчанию 100, т.е. запуск GC при удвоении live heap)
- `GOMEMLIMIT` (Go 1.19+) — soft memory limit, GC становится агрессивнее при приближении к лимиту
- `runtime.SetFinalizer` — weak guarantee, не заменяет explicit cleanup

---

## Пакеты и модули

### Модульная система (Go Modules, с 1.11, default с 1.16)
- `go.mod` — объявление модуля, зависимости, версия Go
- `go.sum` — криптографические хеши зависимостей
- **Semantic Import Versioning**: `v2+` меняет import path (`github.com/foo/bar/v2`)
- **MVS (Minimal Version Selection)** — выбирается минимальная версия, удовлетворяющая всем constraints (в отличие от npm/pip, которые берут latest)
- `GONOSUMDB`, `GONOSUMCHECK`, `GOPRIVATE` — для приватных модулей

### Организация пакетов
- Имя пакета = имя директории (конвенция)
- `internal/` — видимость ограничена parent-модулем
- `_test` suffix для test-only пакетов (black-box testing)
- Circular imports запрещены — влияет на архитектуру

---

## Обработка ошибок

### Идиоматичный паттерн
```go
result, err := doSomething()
if err != nil {
    return fmt.Errorf("context: %w", err)
}
```

### error — это интерфейс
```go
type error interface {
    Error() string
}
```

### Wrapping и Unwrapping (Go 1.13+)
- `fmt.Errorf("...: %w", err)` — оборачивает ошибку
- `errors.Is(err, target)` — проверка по цепочке обёрток
- `errors.As(err, &target)` — extraction конкретного типа
- `errors.Join(errs...)` (Go 1.20+) — мульти-ошибки → [[go error handling]]

### panic/recover
- `panic` — для truly unrecoverable situations (не для control flow!)
- `recover` работает только внутри `defer`
- Стандартная библиотека паникует при programming errors (index out of range, nil pointer deref) → [[go defer panic recover]]

---

## Concurrency-примитивы (обзор)

> "Do not communicate by sharing memory; instead, share memory by communicating." — Effective Go

### Goroutines
- Не потоки ОС — мультиплексируются через GMP scheduler → [[go scheduler]]
- Стоимость создания ~2-8 KB (stack), переключение контекста дёшево
- Нет goroutine ID (by design — чтобы не привязывать состояние к goroutine)
- Утечки goroutine — частая проблема, нет built-in join → [[go goroutine]]

### Channels
- `ch := make(chan T)` — unbuffered (синхронный рандеву)
- `ch := make(chan T, N)` — buffered
- Отправка в closed channel → panic
- Получение из closed channel → zero value + `ok=false`
- `select` — мультиплексирование каналов, `default` для non-blocking → [[go Channel]]

### Context
- `context.Background()` — корневой, `context.TODO()` — placeholder
- `context.WithCancel`, `WithTimeout`, `WithDeadline` — отмена и дедлайны
- `context.WithValue` — передача request-scoped данных (не для бизнес-логики!)
- Первый аргумент функции, по конвенции: `func DoWork(ctx context.Context, ...)` → [[go context]]

---

## init() и порядок инициализации

- Каждый файл может иметь несколько `init()` — все вызываются
- Порядок: зависимые пакеты → текущий пакет, внутри пакета — по алфавиту файлов, внутри файла — порядок объявления
- Переменные уровня пакета инициализируются до `init()`
- `init()` нельзя вызвать явно
- Злоупотребление `init()` → скрытые side effects, затрудняет тестирование

---

## defer, закрытие ресурсов

- LIFO — последний defer выполняется первым
- Аргументы `defer` вычисляются в момент объявления, не выполнения
- Deferred func может менять named return values
- Идиома: `defer f.Close()` сразу после успешного `Open`
- В циклах: `defer` привязан к функции, не к итерации — потенциальная утечка → [[go defer panic recover]]

---

## Тестирование

- `_test.go` файлы, функции `TestXxx(t *testing.T)`
- `t.Run("subtest", ...)` — subtests для table-driven tests
- `t.Parallel()` — параллельный запуск
- Benchmarks: `BenchmarkXxx(b *testing.B)`, запуск `go test -bench=.`
- Fuzzing (Go 1.18+): `FuzzXxx(f *testing.F)`
- `testdata/` — специальная директория, игнорируется компилятором
- Нет assertions в stdlib — `if got != want { t.Errorf(...) }` → [[go testing]]

---

## Инструментарий

- `go build` — компиляция, статическая линковка по умолчанию (один бинарь) → [[go compiler]]
- `go vet` — статический анализ (находит частые баги)
- `go test -race` — race detector (основан на ThreadSanitizer) → [[go memory model]]
- `go tool pprof` — CPU/memory профилирование
- `go tool trace` — трассировка scheduler, GC, syscalls → [[go scheduler]]
- `go generate` — кодогенерация по `//go:generate` комментариям
- `go doc` / `godoc` — документация из комментариев

---

## Build-директивы и теги

- `//go:build linux && amd64` — conditional compilation
- `//go:noescape`, `//go:nosplit`, `//go:linkname` — compiler directives (используются в runtime) → [[go compiler]]
- `//go:embed` (Go 1.16+) — встраивание файлов в бинарь
- `CGO_ENABLED=0` — отключение cgo для полностью статического бинаря

---

## Ссылки для углубления

### Типы и память
- [[go slices]] — внутренняя структура slice, append, copy, ловушки
- [[go map]] — hash-таблица, эвакуация бакетов, concurrent-доступ
- [[go Interfaces]] — itab, nil interface, embedding
- [[go generics]] — type parameters, constraints, ограничения
- [[go escape analysis]] — как компилятор решает stack vs heap
- [[go gc]] — tri-color mark-and-sweep, паузы, GOGC/GOMEMLIMIT
- [[go memory model]] — happens-before, гарантии синхронизации, race detector

### Concurrency
- [[go goroutine]] — жизненный цикл, утечки, best practices
- [[go scheduler]] — GMP, work stealing, preemption
- [[go Channel]] — паттерны, направленные каналы, select
- [[go context]] — отмена, дедлайны, propagation
- [[sync package]] — Mutex, RWMutex, Once, Cond
- [[sync atomic (атомики)]] — атомарные операции, memory ordering
- [[sync.Map]] — concurrent map, когда использовать
- [[sync.Pool]] — переиспользование объектов, GC interaction
- [[go WaitGroup]] — ожидание группы goroutine
- [[go Mutex]] — mutual exclusion, deadlock
- [[go concurrency patterns]] — fan-out/in, pipeline, worker pool
- [[go netpoller]] — неблокирующий I/O, epoll/kqueue интеграция

### Управление потоком
- [[go defer panic recover]] — порядок выполнения, именованные возвраты, recover
- [[go error handling]] — wrapping, Is/As, sentinel errors, кастомные типы
- [[go closures]] — захват переменных, ловушки в циклах

### Компилятор и инструменты
- [[go compiler]] — фазы компиляции, SSA, оптимизации

### Дизайн и архитектура
- [[go SOLID]] — SOLID-принципы применительно к Go
- [[go Interfaces]] — интерфейсы как инструмент дизайна

#interview-prep #concurrency #runtime #go-internals
