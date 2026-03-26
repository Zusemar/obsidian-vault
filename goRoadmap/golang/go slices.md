#golang #language #slices #middle

Слайс — основная структура данных Go. Состоит из трёх полей: pointer + length + capacity. Источник: `src/runtime/slice.go`, `src/slices/`.

---

## Внутреннее устройство

```go
// runtime.SliceHeader (концептуально):
type slice struct {
    array unsafe.Pointer  // указатель на backing array
    len   int             // количество элементов
    cap   int             // ёмкость backing array от текущей позиции
}
```

```go
s := []int{1, 2, 3, 4, 5}
// array → [1, 2, 3, 4, 5]
// len=5, cap=5

sub := s[1:3]
// array → &s[1]  (тот же backing array!)
// len=2, cap=4   (cap считается до конца backing array)
// sub = [2, 3]
```

**Важно:** slicing **не копирует** данные — оба слайса используют один backing array.

---

## append и рост

```go
s := make([]int, 0, 3)  // len=0, cap=3

s = append(s, 1, 2, 3)  // len=3, cap=3  — в пределах capacity
s = append(s, 4)         // len=4, cap=6  — новый backing array (рост ~2x)
```

**Алгоритм роста (Go 1.18+):** не строго x2. Для маленьких слайсов (~256 элементов) рост ~2x, для больших — ближе к 1.25x (для снижения потребления памяти).

```go
// ВСЕГДА присваивать результат append обратно:
s = append(s, val)  // ← обязательно, т.к. может вернуть новый слайс

// Append нескольких элементов:
s = append(s, 1, 2, 3)
s = append(s, other...)  // раскрытие слайса
```

---

## Ловушка: разделяемый backing array

```go
a := []int{1, 2, 3, 4, 5}
b := a[:3]  // [1, 2, 3], cap=5, shared backing array!

b[0] = 99   // меняет и a[0]!
fmt.Println(a) // [99 2 3 4 5]

// append в пределах capacity тоже меняет оригинал:
b = append(b, 999)  // записывает в a[3]!
fmt.Println(a) // [99 2 3 999 5]
```

**Исправление:** `copy` или `slices.Clone`:
```go
b := slices.Clone(a[:3])   // Go 1.21+
// или:
b = make([]int, 3)
copy(b, a[:3])
```

---

## copy

```go
dst := make([]int, len(src))
n := copy(dst, src)  // копирует min(len(dst), len(src)) элементов
// n = количество скопированных элементов
```

---

## nil vs пустой слайс

```go
var s []int         // nil slice: s == nil, len=0, cap=0
s := []int{}        // empty slice: s != nil, len=0, cap=0
s := make([]int, 0) // empty slice: s != nil, len=0, cap=0

// Оба ведут себя одинаково при append, range, len
// Разница:
json.Marshal(nil)   // → "null"
json.Marshal([]int{}) // → "[]"

// Правило: возвращай nil slice при "нет данных",
// пустой slice когда нужен пустой JSON массив
```

---

## Операции (Go 1.21+ пакет slices)

```go
import "slices"

s := []int{3, 1, 4, 1, 5, 9, 2, 6}

slices.Sort(s)                       // сортировка in-place
slices.SortFunc(s, cmp.Compare)      // с функцией сравнения
slices.SortStableFunc(s, f)          // стабильная

slices.Contains(s, 5)                // true
slices.Index(s, 4)                   // индекс или -1
slices.Equal(a, b)                   // поэлементное сравнение

slices.Reverse(s)                    // разворот in-place
slices.Max(s) / slices.Min(s)        // максимум/минимум

// Удаление элемента по индексу (не сохраняет порядок — быстро):
s[i] = s[len(s)-1]
s = s[:len(s)-1]

// Удаление с сохранением порядка (O(n)):
s = slices.Delete(s, i, i+1)  // Go 1.21+

// Фильтрация in-place (Go 1.21+):
s = slices.DeleteFunc(s, func(v int) bool { return v < 0 })

// Бинарный поиск (требует отсортированный slice):
i, found := slices.BinarySearch(s, 5)

// Дедупликация (требует отсортированный):
slices.Sort(s)
s = slices.Compact(s)  // удаляет соседние дубликаты
```

---

## Эффективное использование

```go
// Pre-allocate когда известен размер:
result := make([]int, 0, len(input))
for _, v := range input {
    result = append(result, transform(v))
}

// Reuse slice для избежания аллокаций:
var buf []byte
buf = buf[:0]  // сбросить длину, сохранить capacity
buf = append(buf, data...)

// Или через sync.Pool:
var pool = sync.Pool{
    New: func() any { return make([]byte, 0, 4096) },
}
buf := pool.Get().([]byte)
buf = buf[:0]
// ...используем buf...
pool.Put(buf)
```

---

## Двумерные слайсы

```go
// Вариант 1: один большой буфер (cache-friendly, одна аллокация)
rows, cols := 100, 100
buf := make([]int, rows*cols)
matrix := make([][]int, rows)
for i := range matrix {
    matrix[i] = buf[i*cols : (i+1)*cols]
}

// Вариант 2: слайс слайсов (отдельные аллокации, менее эффективно)
matrix := make([][]int, rows)
for i := range matrix {
    matrix[i] = make([]int, cols)
}
```

---

## Связанные темы

- [[go generics]] — пакет `slices` generic: `slices.Sort[S ~[]E, E cmp.Ordered](x S)`
- [[go escape analysis]] — слайс может escape если его адрес уходит наружу
- [[go memory model]] — backing array аллоцируется через mspan/mcache
- [[go sync.Pool]] — переиспользование []byte буферов через Pool
- [[go error handling]] — `slices.DeleteFunc` = Go 1.21+; не сортируй что уже отсортировано
