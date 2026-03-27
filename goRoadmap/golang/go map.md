Terminology:
// - Slot: A storage location of a single key/element pair.
// - Group: A group of abi.MapGroupSlots (8) slots, plus a control word.
// - Control word: An 8-byte word which denotes whether each slot is empty,
//   deleted, or used. If a slot is used, its control byte also contains the
//   lower 7 bits of the hash (H2).
// - H1: Upper 57 bits of a hash.
// - H2: Lower 7 bits of a hash.
// - Table: A complete "Swiss Table" hash table. A table consists of one or
//   more groups for storage plus metadata to handle operation and determining
//   when to grow.
// - Map: The top-level Map type consists of zero or more tables for storage.
//   The upper bits of the hash select which table a key belongs to.
// - Directory: Array of the tables used by the map.

//At its core, the table design is similar to a traditional open-addressed
//hash table. Storage consists of an array of groups, which effectively means
// an array of key/elem slots with some control words interspersed. Lookup uses
// the hash to determine an initial group to check. If, due to collisions, this
// group contains no match, the probe sequence selects the next group to check

//A swiss table utilizes the extra control word to check all 8 slots in parallel.

---

## Практическое использование

### Создание и операции

```go
// Создание
m := make(map[string]int)          // пустая map
m := make(map[string]int, 100)     // hint для initial capacity (не максимум!)
m := map[string]int{"a": 1, "b": 2} // литерал

// Запись / обновление
m["key"] = 42

// Чтение с comma-ok (обязательно для проверки существования)
v, ok := m["key"]  // v=42, ok=true
v, ok := m["nope"] // v=0,  ok=false

// Удаление
delete(m, "key")  // безопасно даже если ключ не существует

// Длина
n := len(m)
```

### nil map — читать можно, писать нельзя

```go
var m map[string]int  // nil map

v := m["key"]         // ok: возвращает 0 (zero value)
v, ok := m["key"]     // ok: v=0, ok=false
n := len(m)           // ok: 0

m["key"] = 1          // PANIC: assignment to entry in nil map
delete(m, "key")      // ok: no-op
```

### Итерация — порядок случаен намеренно

```go
// Порядок итерации не определён и меняется между запусками (rand seed с Go 1.0)
for k, v := range m {
    fmt.Println(k, v)
}

// Только ключи
for k := range m { ... }

// Только значения
for _, v := range m { ... }

// Удаление во время range — безопасно:
for k := range m {
    if shouldDelete(k) {
        delete(m, k)  // ok
    }
}
```

Для детерминированного порядка — сортировать ключи:
```go
keys := make([]string, 0, len(m))
for k := range m { keys = append(keys, k) }
sort.Strings(keys)
for _, k := range keys { fmt.Println(k, m[k]) }
```

### Конкурентный доступ → panic

```go
// map НЕ потокобезопасна!
// Одновременная запись из двух горутин → fatal: concurrent map writes

// ПЛОХО
var m = make(map[string]int)
go func() { m["a"] = 1 }()   // panic!
go func() { m["b"] = 2 }()

// ХОРОШО: sync.Mutex
var (
    mu sync.RWMutex
    m  = make(map[string]int)
)
// write:
mu.Lock(); m["a"] = 1; mu.Unlock()
// read:
mu.RLock(); v := m["a"]; mu.RUnlock()

// ИЛИ sync.Map для read-heavy сценариев (см. [[go sync.Map]])
```

Обнаружение: `go test -race ./...`

### Map как множество (set)

```go
// map[K]struct{} — нулевой размер value, не аллоцирует память
seen := make(map[string]struct{})
seen["hello"] = struct{}{}
_, exists := seen["hello"]  // true

// Или через встроенный тип (Go 1.18+ generics):
type Set[K comparable] map[K]struct{}
func (s Set[K]) Add(k K)      { s[k] = struct{}{} }
func (s Set[K]) Has(k K) bool { _, ok := s[k]; return ok }
```

### Ключи с указателями → давление на GC

```go
// ПЛОХО для больших map: GC должен сканировать все указатели в map
cache := make(map[string]*Value, 1_000_000)

// ЛУЧШЕ: если возможно, избегать указателей в value
cache := make(map[string]Value, 1_000_000)

// ЛУЧШЕ ещё: map[uint64]uint64 — GC не сканирует (нет указателей)
// используй hashing или int ID вместо строк если возможно
```

### clear (Go 1.21+)

```go
// Удалить все элементы без пересоздания (сохраняет аллоцированную память)
clear(m)
len(m) // 0
```

---

#golang #datastructure #runtime

## Связанные темы

- [[go sync.Map]] — конкурентная map; новый HashTrieMap тоже использует идеи Swiss Table
- [[go compiler]] — хэш-функция и seed выбираются компилятором/рантаймом при инициализации
- [[go memory model]] — map аллоцируется в heap через аллокатор (mspan/mcache)
- [[go generics]] — `maps.Keys`, `maps.Clone`, `maps.Equal` (Go 1.21+)
- [[go sync package]] — mutex + map = безопасная конкурентная map в большинстве случаев
- [[go gc]] — map с указателями-значениями → GC сканирует все; map[K]uint64 быстрее