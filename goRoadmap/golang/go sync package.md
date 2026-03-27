#golang #concurrency #sync #собес #middle

`sync` — пакет примитивов синхронизации. Источник: `src/sync/`. Все типы **нельзя копировать** после первого использования (`noCopy`).

---

## sync.Mutex

### Внутреннее устройство

```go
type Mutex struct {
    state int32   // битовое поле: locked | starvation | waiter_count
    sema  uint32  // семафор для парковки горутин
}

// Биты поля state:
// bit 0 (mutexLocked):    1 = заблокирован
// bit 1 (mutexWoken):     горутина из sleep-очереди уже разбужена
// bit 2 (mutexStarvation): режим голодания
// bits 3+: количество горутин в очереди ожидания
```

### Режимы работы (Go 1.9+)

**Normal mode:** пробуждённая горутина соревнуется за блокировку с новыми горутинами (которые уже на CPU). Новые **побеждают** — они уже в планировщике. Пробуждённая горутина встаёт в хвост очереди.

**Starvation mode:** включается если горутина ждёт > 1ms. В этом режиме `Unlock` **напрямую передаёт** мьютекс горутине из головы очереди. Новые горутины в очередь не прыгают — честная очередь FIFO.

```go
var mu sync.Mutex

mu.Lock()
defer mu.Unlock()
// критическая секция
```

### TryLock (Go 1.18+)

```go
if mu.TryLock() {
    defer mu.Unlock()
    // без блокировки
} else {
    // мьютекс занят, делаем что-то другое
}
```

### Антипаттерны

```go
// ПЛОХО: копирование мьютекса
type Cache struct{ mu sync.Mutex }
c2 := c1  // go vet: assignment copies lock value

// ПЛОХО: Lock без Unlock (забыли defer)
func (c *Cache) Get(k string) string {
    c.mu.Lock()
    v := c.data[k]
    // если return раньше Unlock → дедлок
    c.mu.Unlock()
    return v
}

// ХОРОШО: defer Unlock сразу после Lock
func (c *Cache) Get(k string) string {
    c.mu.Lock()
    defer c.mu.Unlock()
    return c.data[k]
}

// ПЛОХО: Lock в горутине, Unlock в другой (не детектируется race detector)
go func() { mu.Lock() }()
go func() { mu.Unlock() }()
```

---

## sync.RWMutex

Множественные читатели **одновременно**, но **один** писатель. Оптимален когда чтений >> записей.

```go
type RWMutex struct {
    w           Mutex        // мьютекс для писателей
    writerSem   uint32       // семафор для очереди писателей
    readerSem   uint32       // семафор для очереди читателей
    readerCount atomic.Int32 // число активных читателей (отрицательное = писатель ждёт)
    readerWait  atomic.Int32 // число читателей которых ждёт писатель
}
```

```go
var rw sync.RWMutex

// Читатель:
rw.RLock()
defer rw.RUnlock()
v := cache[key]

// Писатель:
rw.Lock()
defer rw.Unlock()
cache[key] = value
```

**Ловушка:** если писатель ждёт — новые читатели блокируются (чтобы предотвратить starvation писателей).

```go
// TryRLock / TryLock (Go 1.18+)
if rw.TryRLock() {
    defer rw.RUnlock()
    return cache[key]
}
```

---

## sync.Once

Гарантирует однократное выполнение функции, потокобезопасно:

```go
type Once struct {
    done atomic.Uint32  // 0 = не выполнялось, 1 = выполнено
    m    Mutex
}
```

```go
var (
    instance *DB
    once     sync.Once
)

func GetDB() *DB {
    once.Do(func() {
        instance = openDB()
    })
    return instance
}
```

**Важно:** если `f()` паникует — `Once` считается выполненным. Повторные вызовы `Do` не запустят `f` снова.

**OnceFunc / OnceValue / OnceValues (Go 1.21+):**
```go
// Более эргономичные обёртки
getDB := sync.OnceValue(func() *DB { return openDB() })
db := getDB()  // безопасно вызывать несколько раз
```

---

## sync.WaitGroup

Ожидание завершения группы горутин:

```go
type WaitGroup struct {
    noCopy noCopy
    state  atomic.Uint64  // counter (высокие 32 бита) + waiter count (низкие 32 бита)
    sema   uint32
}
```

```go
var wg sync.WaitGroup

for _, item := range items {
    wg.Add(1)
    go func(item Item) {
        defer wg.Done()  // = Add(-1)
        process(item)
    }(item)
}

wg.Wait()  // блокируется пока counter != 0
```

**Правило:** `Add` должен вызываться **до** запуска горутины (иначе race с Wait):

```go
// ПЛОХО: Add внутри горутины — race с Wait
go func() {
    wg.Add(1)  // может выполниться после Wait
    defer wg.Done()
    process()
}()

// ХОРОШО: Add перед go
wg.Add(1)
go func() {
    defer wg.Done()
    process()
}()
```

---

## sync.Cond

Условная переменная — горутина ждёт наступления условия:

```go
type Cond struct {
    L Locker  // должен быть *Mutex или *RWMutex
    // ...
}

cond := sync.NewCond(&mu)

// Горутина-потребитель (ждёт данных):
mu.Lock()
for len(queue) == 0 {      // всегда в цикле (spurious wakeups)
    cond.Wait()             // атомарно: unlock(mu) + park + lock(mu)
}
item := queue[0]
queue = queue[1:]
mu.Unlock()

// Горутина-производитель (сигнализирует):
mu.Lock()
queue = append(queue, item)
cond.Signal()   // разбудить одну горутину
// cond.Broadcast()  // разбудить все
mu.Unlock()
```

**На практике** `sync.Cond` редко нужен — обычно заменяется каналами или `sync.WaitGroup`.

---

## sync.Map — когда использовать

| Паттерн | sync.Mutex + map | sync.Map |
|---|---|---|
| Много записей | ✅ | ❌ медленнее |
| Read-heavy, стабильные ключи | ❌ lock на каждое чтение | ✅ lock-free read |
| Много уникальных ключей | ✅ | ❌ (dirty map растёт) |
| Ключи добавляются раз, читаются много | ✅ | ✅ |

**Правило:** default — `sync.Mutex + map`. `sync.Map` — только для специфичных паттернов (cache, registry).

---

## Вопросы на собесе

**Чем отличается Mutex от RWMutex?**
Mutex — один обладатель; RWMutex — много читателей одновременно, но писатель эксклюзивен. Используй RWMutex когда чтений значительно больше записей.

**Что такое starvation mode в Mutex?**
Если горутина ждёт мьютекс > 1ms, включается режим голодания: мьютекс передаётся напрямую горутине из головы очереди (честный FIFO), новые горутины не прыгают вперёд.

**Можно ли копировать sync.Mutex?**
Нет. Копирование создаёт независимый мьютекс с нулевым состоянием, что ломает синхронизацию. `go vet` ловит через `noCopy`.

**Зачем WaitGroup.Add перед go?**
Если Add вызвать внутри горутины, Wait может завершиться до Add — race condition.

---

## Связанные темы

- [[go sync atomic (атомики)]] — атомики как альтернатива для одной переменной; CAS vs mutex
- [[go goroutine]] — `waitReasonSyncMutexLock` — причина парковки горутины
- [[go scheduler]] — mutex усыпляет горутину через sema (планировщик), CAS-loop нет
- [[go sync.Map]] — `mu Mutex` защищает dirty-карту; когда использовать sync.Map
- [[go Channel]] — каналы как альтернатива mutex для передачи данных
- [[go concurrency patterns]] — Worker Pool использует WaitGroup; Once для singleton
