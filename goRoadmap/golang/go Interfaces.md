Интерфейсный тип в Go — это своего рода _определение_. Он определяет и описывает конкретные методы, которые должны быть у _какого-то другого типа_.

__Что-то__ _удовлетворяет этому интерфейсу_ (или _реализует этот интерфейс_), если у этого «что-то» есть метод с конкретным сигнатурным строковым значением

Неважно, каким типом __что-то__ является или что он делает. Важно лишь, что у него есть такой же метод, который возвращает тот же тип, что и интерфейс.

##### Пустой интефейс

Пустой интерфейсный тип _не описывает методы_. У него нет правил. И поэтому любой объект удовлетворяет пустому интерфейсу.

Создадим мапу у которой ключи - стринги а значения - любой тип, сделать это можно как раз благодаря пустым интерфейсам

```go
package main
import "log"

func main() {    
person := make(map[string]interface{}, 0)
person["name"] = "Alice"
person["age"] = 21
person["height"] = 167.64

person["age"] = person["age"] + 1

fmt.Printf("%+v", person)}
```

Так когда же следует использовать пустой интерфейсный тип?  
  
Пожалуй, _не слишком часто_. Если вы к этому пришли, то остановитесь и подумайте, правильно ли сейчас использовать `interface{}`. В качестве общего совета могу сказать, что будет понятнее, безопаснее и производительнее использовать конкретные типы, то есть не пустые интерфейсные типы.

##### Полезные интерфейсные типы

Вот короткий список самых востребованных и полезных интерфейсных типов из стандартной библиотеки. Если вы с ними ещё не знакомы, то рекомендую почитать соответствующую документацию.  
  

- [builtin.Error](https://golang.org/pkg/builtin/#error)
- [fmt.Stringer](https://golang.org/pkg/fmt/#Stringer)
- [io.Reader](https://golang.org/pkg/io/#Reader)
- [io.Writer](https://golang.org/pkg/io/#Writer)
- [io.ReadWriteCloser](https://golang.org/pkg/io/#ReadWriteCloser)
- [http.ResponseWriter](https://golang.org/pkg/net/http/#ResponseWriter)
- [http.Handler](https://golang.org/pkg/net/http/#Handler)

---

## Внутреннее устройство: iface и eface

Источник: `src/runtime/iface.go`, `src/runtime/type.go`

```go
// Непустой интерфейс (iface) — хранит тип + указатель на данные
type iface struct {
    tab  *itab          // таблица методов + тип
    data unsafe.Pointer // указатель на значение (или само значение если ≤ указателю)
}

// Пустой интерфейс (eface / any) — только тип + данные
type eface struct {
    _type *_type        // тип
    data  unsafe.Pointer
}

// itab кэшируется в глобальной хэш-таблице
type itab struct {
    inter *interfacetype // тип интерфейса
    _type *_type         // конкретный тип
    hash  uint32         // копия _type.hash (для type switch)
    _     [4]byte
    fun   [1]uintptr     // таблица методов (variable size)
}
```

**Первый вызов метода через интерфейс:** поиск/создание `itab` (~200нс). Последующие — O(1) lookup в кэше.

```go
// Накладные расходы интерфейсного вызова vs прямого:
// Прямой: 1 инструкция CALL
// Интерфейсный: load itab.fun[n] → indirect CALL  (~2-3нс overhead)
```

---

## Type Assertion

```go
var i interface{} = "hello"

// Паникующая форма
s := i.(string)        // ok
n := i.(int)           // panic: interface conversion

// Comma-ok форма (безопасная — всегда использовать)
s, ok := i.(string)    // s="hello", ok=true
n, ok := i.(int)       // n=0, ok=false
```

---

## Type Switch

```go
func describe(i any) string {
    switch v := i.(type) {
    case int:
        return fmt.Sprintf("int: %d", v)
    case string:
        return fmt.Sprintf("string: %q", v)
    case []byte:
        return fmt.Sprintf("bytes: %d", len(v))
    case nil:
        return "nil"
    default:
        return fmt.Sprintf("unknown: %T", v)
    }
}
```

В каждом `case` переменная `v` имеет конкретный тип, а не `any`.

---

## Nil Interface — классическая ловушка

```go
// ЛОВУШКА: (*MyError)(nil) ≠ nil интерфейс
func mayFail() error {
    var err *MyError = nil
    if someFailed {
        err = &MyError{"fail"}
    }
    return err  // ПЛОХО: возвращает iface{type=*MyError, data=nil}
                //        это НЕ nil error!
}

if err := mayFail(); err != nil {
    // этот код выполнится даже если err.(*MyError) == nil!
}

// ХОРОШО: возвращать нетипизированный nil
func mayFail() error {
    var err *MyError = nil
    if someFailed {
        err = &MyError{"fail"}
    }
    if err != nil {
        return err  // возвращаем только если реально есть ошибка
    }
    return nil      // нетипизированный nil → iface{nil, nil} → == nil ✓
}
```

```
iface{type=nil,  data=nil}  → == nil  (true)
iface{type=*T,   data=nil}  → == nil  (false!) ← ловушка
iface{type=*T,   data=ptr}  → == nil  (false)
```

---

## Embedding интерфейсов

```go
// Composable interfaces (идиома стандартной библиотеки)
type Reader interface { Read(p []byte) (n int, err error) }
type Writer interface { Write(p []byte) (n int, err error) }
type Closer interface { Close() error }

type ReadWriter interface {
    Reader
    Writer
}

type ReadWriteCloser interface {
    Reader
    Writer
    Closer
}
```

Embedded интерфейс расширяет набор требуемых методов.

---

## Implicit Implementation (идиома Go)

В Go нет ключевого слова `implements`. Тип реализует интерфейс автоматически:

```go
type Stringer interface{ String() string }

type User struct{ Name string }
func (u User) String() string { return u.Name }
// User реализует Stringer без объявления

// Compile-time проверка совместимости:
var _ Stringer = User{}         // если User не реализует — ошибка компиляции
var _ Stringer = (*User)(nil)   // проверка через указатель
```

---

## any (Go 1.18+)

`any` — алиас для `interface{}`:
```go
// Эквивалентно:
var x interface{} = 42
var x any = 42

// В generics:
func Print[T any](v T) { fmt.Println(v) }
```

---

## comparable

```go
// comparable — constraint: типы, которые можно сравнивать через == и !=
// Используется как ограничение в generics и как ключи map
func Contains[T comparable](slice []T, val T) bool {
    for _, v := range slice {
        if v == val { return true }
    }
    return false
}

// Интерфейсы сравнимы если их динамический тип сравним:
var a, b any = 1, 1
fmt.Println(a == b) // true

var c, d any = []int{1}, []int{1}
// panic: runtime error: comparing uncomparable type []int
fmt.Println(c == d)
```

---

#golang #types #design #internals

## Связанные темы

- [[go goroutine]] — `error` интерфейс используется для обработки паник и отложенных вызовов
- [[go netpoller]] — `io.Reader` / `io.Writer` — интерфейсы над которыми работает сетевой I/O
- [[go Channel]] — паттерны передачи данных через каналы дополняют паттерны Reader/Writer
- [[go map]] — `interface{}` / `any` используется как тип значения в map[string]interface{}
- [[go generics]] — `any` = `interface{}`; `comparable` constraint для generic map/set
- [[go error handling]] — `error` — самый важный интерфейс Go; nil interface ловушка
- [[go defer panic recover]] — type assertion паникует без comma-ok формы