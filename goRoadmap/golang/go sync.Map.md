```go
type Map struct {
    _ noCopy
    m isync.HashTrieMap[any, any] // Делегирование всей логики
}
```

```go
// HashTrieMap is an implementation of a concurrent hash-trie. The implementation
// is designed around frequent loads, but offers decent performance for stores
// and deletes as well, especially if the map is larger. Its primary use-case is
// the unique package, but can be used elsewhere as well.
//
// The zero HashTrieMap is empty and ready to use.
// It must not be copied after first use.
type HashTrieMap[K comparable, V any] struct {
	inited   atomic.Uint32
	initMu   Mutex
	root     atomic.Pointer[indirect[K, V]]
	keyHash  hashFunc
	valEqual equalFunc
	seed     uintptr
}
```





Внутреннее устройство `sync.Map` включает следующие ключевые поля:
- **mu Mutex**: Мьютекс, который защищает доступ к «грязной» карте (`dirty`) и координирует процессы обновления данных.
- **read atomic.Pointer[readOnly]**: Указатель на структуру `readOnly`. Это «быстрый путь» (fast path) для чтений, так как доступ к ней осуществляется атомарно без захвата мьютекса.
- **dirty map[any]*entry**: Обычная карта Go, содержащая новые ключи или измененные записи. Доступ к ней требует захвата мьютекса `mu`.
- **misses int**: Счетчик «промахов» при чтении из `read`. Когда количество промахов становится равным количеству записей в `dirty`, «грязная» карта копируется (продвигается) в `read`

---

## API — полный список методов

```go
var m sync.Map

// Store — записать
m.Store("key", "value")

// Load — прочитать
v, ok := m.Load("key")  // ok=false если нет
if ok {
    s := v.(string)  // нужно type assertion — Map не типизирована
}

// LoadOrStore — прочитать или записать если нет
v, loaded := m.LoadOrStore("key", "default")
// loaded=true: вернули существующее; false: записали новое

// LoadAndDelete — прочитать и удалить атомарно
v, loaded := m.LoadAndDelete("key")

// Delete — удалить
m.Delete("key")

// Swap (Go 1.20+) — заменить и вернуть старое
old, loaded := m.Swap("key", "new_value")

// CompareAndSwap (Go 1.20+) — заменить если текущее == old
ok := m.CompareAndSwap("key", "old_value", "new_value")

// CompareAndDelete (Go 1.20+) — удалить если текущее == old
ok = m.CompareAndDelete("key", "value_to_delete")

// Range — итерация (не гарантирует порядок)
m.Range(func(key, value any) bool {
    fmt.Println(key, value)
    return true  // false = прекратить итерацию
})
```

## Когда sync.Map быстрее sync.Mutex + map

```go
// sync.Map выигрывает при:
// 1. Ключи записываются один раз, читаются много раз (cache, registry)
// 2. Разные горутины работают с непересекающимися ключами

// Пример: service registry
var registry sync.Map

func Register(name string, handler http.Handler) {
    registry.Store(name, handler)
}

func Lookup(name string) (http.Handler, bool) {
    v, ok := registry.Load(name)
    if !ok { return nil, false }
    return v.(http.Handler), true  // lock-free!
}

// sync.Map ХУЖЕ sync.Mutex + map при:
// - частых записях
// - большом числе уникальных новых ключей (dirty map растёт)
// - нужна типизация → пиши свою обёртку
```

## Типизированная обёртка (идиома)

```go
// sync.Map не generic — используй обёртку
type TypedMap[K comparable, V any] struct {
    m sync.Map
}

func (t *TypedMap[K, V]) Store(k K, v V)          { t.m.Store(k, v) }
func (t *TypedMap[K, V]) Load(k K) (V, bool) {
    v, ok := t.m.Load(k)
    if !ok { var zero V; return zero, false }
    return v.(V), true
}
func (t *TypedMap[K, V]) Delete(k K)              { t.m.Delete(k) }
func (t *TypedMap[K, V]) LoadOrStore(k K, v V) (V, bool) {
    actual, loaded := t.m.LoadOrStore(k, v)
    return actual.(V), loaded
}
```

---

#golang #concurrency #datastructure

## Связанные темы

- [[go sync atomic (атомики)]] — `read atomic.Pointer[readOnly]`, `inited atomic.Uint32` — быстрый путь без мьютекса
- [[go sync package]] — `mu Mutex` защищает dirty-карту при записи и продвижении
- [[go map]] — обычная Go map под капотом dirty; Swiss Table в read-path нового HashTrieMap
- [[go goroutine]] — оптимизирована для паттерна «много читателей, редкие записи»