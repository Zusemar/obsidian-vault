#golang #language #defer #panic #runtime

`defer`, `panic`, `recover` — механизм обработки исключительных ситуаций в Go. Источник: `src/runtime/panic.go`, спецификация Go.

---

## defer — семантика

`defer` откладывает выполнение функции до **возврата из окружающей функции** (не блока!):

```go
func processFile(path string) error {
    f, err := os.Open(path)
    if err != nil { return err }
    defer f.Close()  // выполнится при любом выходе из функции

    // работаем с файлом...
    return nil
}
```

### LIFO порядок

Несколько `defer` выполняются в **обратном порядке** (Last In, First Out):

```go
func main() {
    defer fmt.Println("1")
    defer fmt.Println("2")
    defer fmt.Println("3")
}
// Вывод: 3, 2, 1
```

### Аргументы вычисляются сразу

Аргументы defer-функции вычисляются **в момент вызова defer**, не в момент выполнения:

```go
x := 1
defer fmt.Println(x)  // захватывает x=1 прямо сейчас
x = 2
// Выведет: 1  (не 2!)
```

Но если передаётся указатель или используется замыкание — получим актуальное значение:
```go
x := 1
defer func() { fmt.Println(x) }()  // замыкание — захват по ссылке
x = 2
// Выведет: 2
```

---

## defer и именованные возвращаемые значения

Мощный паттерн: defer-функция **видит и может изменить** именованные возвращаемые значения:

```go
// Обогащение ошибки контекстом
func openDB(dsn string) (db *sql.DB, err error) {
    defer func() {
        if err != nil {
            err = fmt.Errorf("openDB %q: %w", dsn, err)
        }
    }()

    db, err = sql.Open("postgres", dsn)
    if err != nil { return }

    err = db.Ping()
    return
}
```

```go
// Транзакция с автоматическим rollback/commit
func (r *Repo) Transfer(ctx context.Context, from, to int, amount float64) (err error) {
    tx, err := r.db.BeginTx(ctx, nil)
    if err != nil { return }

    defer func() {
        if err != nil {
            tx.Rollback()  // err != nil → откат
        } else {
            err = tx.Commit()  // err == nil → коммит (может вернуть ошибку)
        }
    }()

    _, err = tx.ExecContext(ctx, "UPDATE accounts SET balance=balance-$1 WHERE id=$2", amount, from)
    if err != nil { return }
    _, err = tx.ExecContext(ctx, "UPDATE accounts SET balance=balance+$1 WHERE id=$2", amount, to)
    return
}
```

---

## Ловушка: defer в цикле

```go
// ПЛОХО: defer откладывается до выхода из функции, не из итерации
func processFiles(files []string) error {
    for _, f := range files {
        fd, err := os.Open(f)
        if err != nil { return err }
        defer fd.Close()  // все Close вызовутся только при выходе из функции!
        process(fd)       // если много файлов → утечка файловых дескрипторов
    }
    return nil
}

// ХОРОШО: выносим в отдельную функцию или явно закрываем
func processFiles(files []string) error {
    for _, f := range files {
        if err := processOne(f); err != nil {
            return err
        }
    }
    return nil
}

func processOne(path string) error {
    fd, err := os.Open(path)
    if err != nil { return err }
    defer fd.Close()  // теперь закрывается при выходе из processOne
    return process(fd)
}
```

---

## defer и производительность

До Go 1.14: defer имел overhead ~50нс (heap allocation). Начиная с Go 1.14 — **Open-coded defers** (компилятор встраивает defer inline), overhead ~1-3нс. Бояться defer в hot path больше не нужно.

---

## panic — раскрутка стека

`panic` немедленно останавливает выполнение текущей функции и **раскручивает стек**, выполняя все defer:

```go
func riskyOperation() {
    defer fmt.Println("cleanup")  // выполнится несмотря на panic
    panic("something went wrong")
    fmt.Println("unreachable")
}
// Вывод: "cleanup", затем panic message
```

**Что вызывает панику:**
```go
// Рантайм паники (нельзя предотвратить кодом):
var p *int; *p = 1        // nil pointer dereference
s := []int{1}; _ = s[5]  // index out of range
var m map[int]int; m[1]=1 // assignment to nil map
i := interface{}("str")
_ = i.(int)               // type assertion (без comma-ok)

// Явные паники:
panic("explicit message")
panic(errors.New("structured error"))
panic(42)  // можно паниковать любым типом
```

---

## recover — перехват паники

`recover()` останавливает раскрутку стека. Работает **только в defer-функции**:

```go
func safeDiv(a, b int) (result int, err error) {
    defer func() {
        if r := recover(); r != nil {
            err = fmt.Errorf("recovered panic: %v", r)
        }
    }()
    return a / b, nil  // panic при b=0
}

result, err := safeDiv(10, 0)
// err = "recovered panic: runtime error: integer divide by zero"
```

**recover() вернёт nil если:**
- Паники нет
- Вызвана вне defer

### HTTP middleware с recover

```go
func recoveryMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        defer func() {
            if rec := recover(); rec != nil {
                // Логируем с stack trace
                buf := make([]byte, 1<<16)
                n := runtime.Stack(buf, false)
                log.Printf("PANIC: %v\n%s", rec, buf[:n])
                http.Error(w, "Internal Server Error", 500)
            }
        }()
        next.ServeHTTP(w, r)
    })
}
```

---

## runtime.Stack — получить stack trace

```go
import "runtime"

defer func() {
    if r := recover(); r != nil {
        buf := make([]byte, 4096)
        n := runtime.Stack(buf, false)  // false = только текущая горутина
        log.Printf("panic: %v\n%s", r, buf[:n])
    }
}()
```

Или через `debug.Stack()` из `runtime/debug`:
```go
import "runtime/debug"
stack := debug.Stack()  // []byte
```

---

## Паттерн: Must-функции

```go
// Для инициализации на старте программы (panic допустим)
func MustCompile(pattern string) *regexp.Regexp {
    re, err := regexp.Compile(pattern)
    if err != nil { panic(err) }
    return re
}

var emailRe = MustCompile(`^[^@]+@[^@]+$`)  // паника при невалидном паттерне
```

---

## Что нельзя перехватить recover

```go
// Эти "паники" не перехватываются recover():
runtime.Goexit()        // завершение горутины (не panic)
os.Exit(1)              // немедленный выход без defers

// Гонка данных обнаруженная race detector → os.Exit(2)
// stack overflow → неперехватываемая fatal
```

---

## Связанные темы

- [[go error handling]] — error vs panic: когда что использовать
- [[go goroutine]] — panic раскручивает стек только текущей горутины; panic в горутине = crash всей программы
- [[go closures]] — defer с замыканием захватывает переменные по ссылке
- [[go scheduler]] — runtime.Goexit() завершает горутину через механизм планировщика
