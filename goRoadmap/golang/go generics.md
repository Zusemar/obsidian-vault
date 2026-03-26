#golang #language #generics #типы #middle

Generics (обобщённые типы) — добавлены в **Go 1.18**. Позволяют писать код, работающий с разными типами без дублирования или `interface{}`. Источник: `src/cmd/compile/internal/types2/`, **Go specification: Type Parameters**.

---

## Базовый синтаксис

```go
// Обобщённая функция: [T constraint]
func Min[T constraints.Ordered](a, b T) T {
    if a < b { return a }
    return b
}

Min(3, 5)           // T=int (type inference)
Min(3.14, 2.71)     // T=float64
Min[int](3, 5)      // явное указание типа

// Обобщённый тип
type Stack[T any] struct {
    items []T
}

func (s *Stack[T]) Push(v T)       { s.items = append(s.items, v) }
func (s *Stack[T]) Pop() (T, bool) {
    if len(s.items) == 0 {
        var zero T
        return zero, false
    }
    v := s.items[len(s.items)-1]
    s.items = s.items[:len(s.items)-1]
    return v, true
}

s := Stack[int]{}
s.Push(1)
s.Push(2)
v, _ := s.Pop()  // v=2
```

---

## Ограничения (Constraints)

Constraint — интерфейс, ограничивающий допустимые типы:

```go
// Встроенные constraints (пакет golang.org/x/exp/constraints или встроенные):
any          // = interface{} — любой тип
comparable   // типы, которые можно сравнивать через == и !=

// Из пакета constraints:
constraints.Ordered  // int, float, string и их именованные типы
constraints.Integer  // все целые числа
constraints.Float    // float32, float64
```

### Определение собственного constraint

```go
// Constraint через интерфейс с методами:
type Stringer interface {
    String() string
}

func PrintAll[T Stringer](items []T) {
    for _, item := range items {
        fmt.Println(item.String())
    }
}

// Constraint через union типов (type set):
type Number interface {
    int | int8 | int16 | int32 | int64 |
    uint | uint8 | uint16 | uint32 | uint64 |
    float32 | float64
}

func Sum[T Number](nums []T) T {
    var total T
    for _, n := range nums { total += n }
    return total
}

Sum([]int{1, 2, 3})        // 6
Sum([]float64{1.1, 2.2})   // 3.3
```

### ~ (тильда) — underlying type

```go
type Celsius float64
type Fahrenheit float64

// Без ~: Celsius не удовлетворяет Float
// С ~: любой тип с underlying type float64
type FloatLike interface {
    ~float32 | ~float64
}

func Abs[T FloatLike](x T) T {
    if x < 0 { return -x }
    return x
}

Abs(Celsius(-5))     // работает: Celsius имеет underlying type float64
Abs(Fahrenheit(98))  // работает
```

---

## Type Inference (вывод типов)

Go выводит типовые параметры из аргументов:

```go
func Map[T, U any](slice []T, fn func(T) U) []U {
    result := make([]U, len(slice))
    for i, v := range slice { result[i] = fn(v) }
    return result
}

// Вывод типов: T=int, U=string
strs := Map([]int{1, 2, 3}, strconv.Itoa)
// Явно: Map[int, string](...)
```

Вывод типов работает из аргументов, но **не из возвращаемых значений**:
```go
func Zero[T any]() T { var z T; return z }
// Zero()       // ошибка: не может вывести T
Zero[int]()     // OK
```

---

## Встроенные обобщённые функции (Go 1.21+)

```go
// min / max для comparable ordered types
m := min(3, 5, 1, 4)   // 1
m = max(3, 5, 1, 4)    // 5

// Работают для слайсов с любым числом аргументов:
min("apple", "banana")   // "apple"

// clear — удалить все элементы map или обнулить slice
clear(m)     // удаляет все ключи из map
clear(s)     // s[:] = zero values (длина не меняется)
```

---

## Пакеты slices и maps (Go 1.21+)

```go
import (
    "slices"
    "maps"
)

// slices
slices.Sort([]int{3, 1, 2})                    // [1 2 3]
slices.Contains([]string{"a", "b"}, "a")       // true
slices.Index([]string{"a", "b"}, "b")          // 1
slices.Equal([]int{1,2}, []int{1,2})           // true
slices.Reverse(s)                              // in place
slices.Max([]float64{1.5, 2.3, 0.1})          // 2.3
filtered := slices.DeleteFunc(s, func(v int) bool { return v < 0 })

// maps
maps.Keys(m)                                   // []K (нет гарантии порядка)
maps.Values(m)                                 // []V
maps.Clone(m)                                  // shallow copy
maps.Equal(m1, m2)                             // true если одинаковые key-value
maps.DeleteFunc(m, func(k, v int) bool { ... })
```

---

## Практические паттерны

### Generic filter/map/reduce

```go
func Filter[T any](s []T, fn func(T) bool) []T {
    var result []T
    for _, v := range s {
        if fn(v) { result = append(result, v) }
    }
    return result
}

func Reduce[T, U any](s []T, init U, fn func(U, T) U) U {
    acc := init
    for _, v := range s { acc = fn(acc, v) }
    return acc
}

evens := Filter([]int{1,2,3,4,5}, func(n int) bool { return n%2 == 0 })
sum   := Reduce([]int{1,2,3}, 0, func(acc, v int) int { return acc + v })
```

### Generic Set

```go
type Set[K comparable] map[K]struct{}

func NewSet[K comparable](items ...K) Set[K] {
    s := make(Set[K])
    for _, k := range items { s[k] = struct{}{} }
    return s
}

func (s Set[K]) Add(k K)      { s[k] = struct{}{} }
func (s Set[K]) Has(k K) bool { _, ok := s[k]; return ok }
func (s Set[K]) Delete(k K)   { delete(s, k) }
```

### Pointer constraint — для обнуляемых типов

```go
// Иногда нужно создать нулевое значение указателя на T
func Ptr[T any](v T) *T { return &v }

x := Ptr(42)  // *int, указывает на 42
```

---

## Ограничения generics в Go

```go
// Нельзя: тип-параметр в switch
func typeSwitch[T any](v T) {
    switch v.(type) { ... }  // ошибка компиляции
}

// Нельзя: методы с дополнительными type параметрами
type Foo[T any] struct{}
func (f Foo[T]) Bar[U any](v U) {}  // ошибка компиляции

// Нельзя: приводить T к другому типу без constraint
func cast[T any](v T) int {
    return int(v)  // ошибка: нельзя, T может быть строкой
}

// МОЖНО: через comparable + union в constraint
type Integer interface{ ~int | ~int64 }
func toInt[T Integer](v T) int { return int(v) }
```

---

## Когда использовать generics vs interfaces

| | Generics | Interfaces |
|---|---|---|
| Type safety | compile-time | runtime |
| Performance | лучше (нет indirect call) | хуже (itab lookup) |
| Динамическое поведение | нет | да |
| Гетерогенные коллекции | нет | да |
| Code generation | встроено | не нужно |

**Generics подходят для:**
- Контейнерных типов (Set, Stack, Queue, Cache)
- Алгоритмов (sort, filter, map, reduce)
- Функций с одинаковой логикой для разных числовых типов

**Interfaces подходят для:**
- Полиморфного поведения (разная логика для разных типов)
- Dependency injection
- Расширяемых API

---

## Связанные темы

- [[go Interfaces]] — generics constraint = интерфейс; comparable; any = interface{}
- [[go map]] — встроенные map уже generic: map[K]V; pакет `maps` (Go 1.21+)
- [[go error handling]] — `errors.Join`, `slices.Collect` используют generics внутри
- [[go sync.Map]] — sync.Map не generic, для типизированного варианта пиши свой с Mutex
