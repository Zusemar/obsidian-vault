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