#golang #language #functions #compiler

**Замыкание (closure)** — функция, захватывающая переменные из окружающего лексического контекста. В Go замыкание — это пара: **указатель на код функции** (`funcval`) + **указатель на захваченный контекст**.

---

## Внутреннее устройство

Компилятор Go представляет каждое замыкание как анонимную структуру:

```go
// Псевдокод того, что генерирует компилятор
// для: func() { count++ }
type closure_makeCounter struct {
    fn    uintptr  // указатель на код функции
    count *int     // указатель на захваченную переменную
}
```

Вызов замыкания — это косвенный вызов через `funcval`, регистр `DX` содержит указатель на структуру контекста (ABI Go).

```bash
# Посмотреть что генерирует компилятор:
go build -gcflags="-m -m" ./...
```

---

## Захват переменных — всегда по ссылке

Замыкание захватывает **указатель** на переменную, а не её значение. Если переменная захвачена — она уходит в heap через escape analysis.

```go
func makeCounter() func() int {
    count := 0          // → heap: захвачена замыканием
    return func() int {
        count++
        return count
    }
}

c1 := makeCounter()
c2 := makeCounter()

c1() // 1  — у c1 свой count
c1() // 2
c2() // 1  — у c2 независимый count
```

```bash
# Escape analysis покажет:
# ./main.go: moved to heap: count
go build -gcflags="-m" ./...
```

---

## Изменение семантики переменной цикла в Go 1.22

До Go 1.22 все итерации цикла `for` **разделяли одну переменную**:

```go
// Go < 1.22: классическая ловушка
funcs := make([]func(), 3)
for i := 0; i < 3; i++ {
    funcs[i] = func() { fmt.Println(i) }
}
// i == 3 к моменту вызова
funcs[0]() // 3  ← все три напечатают 3
funcs[1]() // 3
funcs[2]() // 3
```

**Начиная с Go 1.22** — итерационная переменная создаётся **заново на каждой итерации**:

```go
// Go 1.22+: ожидаемое поведение
for i := range 3 {
    funcs[i] = func() { fmt.Println(i) }
}
funcs[0]() // 0 ✓
funcs[1]() // 1 ✓
funcs[2]() // 2 ✓
```

Фикс для старых версий — явное переопределение:
```go
for i := 0; i < 3; i++ {
    i := i  // новая переменная на каждой итерации
    go func() { fmt.Println(i) }()
}
```

---

## Горутины и замыкания — ловушка

```go
// ПЛОХО: горутина захватывает v из внешней области
for _, v := range items {
    go func() {
        process(v)  // v может изменится до запуска горутины
    }()
}

// ХОРОШО: передаём значение через параметр
for _, v := range items {
    go func(val Item) {
        process(val)
    }(v)
}
// Либо используй Go 1.22+ где это фиксится автоматически
```

---

## Замыкания и память

Замыкание удерживает **всю захваченную переменную** от GC, пока само замыкание живо:

```go
// ПЛОХО: захвачен весь большой slice
func makeReader(data []byte) func() byte {
    idx := 0
    return func() byte {
        b := data[idx]  // data (мегабайты) не освобождается
        idx++
        return b
    }
}

// ХОРОШО: захватываем только нужное
func makeReader(data []byte) func() byte {
    r := bytes.NewReader(data)  // data можно отдать GC после
    return func() byte {
        b, _ := r.ReadByte()
        return b
    }
}
```

---

## Паттерны использования

### Middleware / декоратор

```go
func withLogging(h http.HandlerFunc) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        log.Printf("%s %s", r.Method, r.URL.Path)
        h(w, r)
    }
}
```

### Functional options

```go
type Option func(*Server)

func WithTimeout(d time.Duration) Option {
    return func(s *Server) { s.timeout = d }
}

func WithMaxConns(n int) Option {
    return func(s *Server) { s.maxConns = n }
}

s := NewServer(WithTimeout(5*time.Second), WithMaxConns(100))
```

### Ленивое вычисление (memoize)

```go
func memoize(fn func(int) int) func(int) int {
    cache := map[int]int{}
    return func(n int) int {
        if v, ok := cache[n]; ok {
            return v
        }
        v := fn(n)
        cache[n] = v
        return v
    }
}
```

### Callback с состоянием

```go
func newBatcher(flush func([]Event)) func(Event) {
    var buf []Event
    return func(e Event) {
        buf = append(buf, e)
        if len(buf) >= 100 {
            flush(buf)
            buf = buf[:0]
        }
    }
}
```

---

## Связанные темы

- [[go escape analysis]] — захваченные переменные всегда уходят в heap
- [[go goroutine]] — горутины создаются через замыкания: `go func() {...}()`; ловушка захвата loop var
- [[go gc]] — замыкание удерживает захваченные переменные от GC
- [[go Interfaces]] — functional options как идиома вместо конфигурационных структур
- [[go scheduler]] — `runtime.newproc` получает `funcval` (указатель на замыкание) при `go func()`
