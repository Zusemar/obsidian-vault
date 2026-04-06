#golang #concurrency #sync #internals #interview

## Структура

```go
type Mutex struct {
    state int32  // битовое поле: locked + woken + starving + waiter count
    sema  uint32 // адрес-ключ для очереди спящих горутин в рантайме
}
```

Всего 8 байт. Нулевое значение = свободен, готов к использованию без инициализации.

---

## Поле state — битовая карта

```
биты 31..3  →  waiter count (29 бит, счётчик ожидающих)
бит 2       →  mutexStarving (режим голодания)
бит 1       →  mutexWoken    (горутина уже крутится, не буди других)
бит 0       →  mutexLocked   (0 = свободен, 1 = захвачен)
```

Константы:
```go
mutexLocked      = 1 << 0  // = 1
mutexWoken       = 1 << 1  // = 2
mutexStarving    = 1 << 2  // = 4
mutexWaiterShift = 3       // waiter count начинается с бита 3
```

Примеры значений `state`:

| Состояние | Значение |
|---|---|
| свободен | 0 |
| захвачен | 1 |
| захвачен + 2 ждут | 17 (= 16 + 1) |
| голодание + 1 ждёт + захвачен | 13 (= 8 + 4 + 1) |

Счётчик ожидающих:
```go
state += 1 << mutexWaiterShift  // добавить waiter (+8)
state -= 1 << mutexWaiterShift  // убрать waiter (-8)
state >> mutexWaiterShift        // прочитать count
```

---

## Базовые операции на уровне железа

| Операция | Инструкция x86 | Где используется |
|---|---|---|
| CAS | `LOCK CMPXCHG` | весь `lockSlow` |
| атомарное сложение | `LOCK XADD` | `Unlock` fast path |
| семафор | syscall через рантайм | сон/пробуждение горутин |

Почему именно `int32`:
- CAS и `LOCK XADD` требуют naturally aligned операнд
- `int32` выровнен по 4 байтам автоматически
- 29 бит на waiter count = до ~536 млн ожидающих
- все состояния меняются одним атомарным CAS — отдельные поля потребовали бы несколько операций или вложенный лок

---

## Lock — три сценария

```
1. мьютекс свободен:
   CAS(state, 0, 1) → успех → вернулись
   одна инструкция, без syscall

2. мьютекс занят ненадолго:
   спин (до 4 итераций, PAUSE × 30)
   → держатель отпустил → CAS → вернулись
   всё в userspace

3. мьютекс занят долго:
   CAS: записываемся в waiter count
   → runtime_SemacquireMutex → засыпаем
   → Unlock разбудит через Semrelease
```

## lockSlow — детали

Три фазы в цикле:

**Фаза 1: спин** — только если `locked && !starving && runtime_canSpin(iter)`.
Внутри спина выставляем `mutexWoken` — сигнал Unlock не будить никого из очереди.

**Фаза 2: вычисление нового state** — оптимистично считаем желаемое состояние:
```go
new := old
if !starving        { new |= mutexLocked }          // хотим захватить
if locked||starving { new += 1<<mutexWaiterShift }  // записываемся в очередь
if starving && locked { new |= mutexStarving }      // переводим в starvation mode
if awoke            { new &^= mutexWoken }           // снимаем флаг
```

**Фаза 3: CAS или сон**:
```go
if CAS(state, old, new) {
    if мьютекс был свободен → break  // захватили
    // иначе идём спать
    queueLifo := waitStartTime != 0   // уже ждали → в голову очереди
    runtime_SemacquireMutex(&m.sema, queueLifo, 2)
    // после пробуждения...
    starving = ждали > 1ms
}
```

После пробуждения в starvation mode — владение передано напрямую, просто фиксируем state и выходим.

---

## Unlock

```go
// fast path
new := atomic.AddInt32(&m.state, -mutexLocked)
if new == 0 { return }  // никого нет

// slow path
unlockSlow(new)
```

`unlockSlow`:
- нормальный режим → CAS: уменьшаем waiter count, выставляем `mutexWoken` → `runtime_Semrelease`
- starvation режим → `runtime_Semrelease(handoff=true)` — прямая передача первому в очереди

---

## Два режима работы

| | Нормальный | Голодание (starvation) |
|---|---|---|
| Включается | всегда по умолчанию | если горутина ждёт > 1ms |
| Новые горутины | конкурируют с пробуждёнными | встают в хвост очереди |
| Передача мьютекса | через Semrelease (конкуренция) | handoff напрямую |
| Приоритет | throughput | latency / fairness |
| Выключается | — | очередь пуста или последний ждал < 1ms |

---

## Семафор — как работает

`sema uint32` — не хранит горутины. Это **адрес-ключ** для поиска в глобальной таблице рантайма.

```
semtable — массив из 251 корзины (простое число)

индекс = (uintptr(&m.sema) >> 3) % 251

каждая корзина — semaRoot:
    lock  mutex        — локальный лок корзины
    treap *sudog       — BST по адресу семафора
    nwait atomic.Uint32

treap нужен потому что в одной корзине могут быть
горутины от разных мьютексов — различаем по адресу
```

Каждая корзина выровнена по 64 байтам (`cpu.CacheLinePadSize`) — защита от false sharing между ядрами.

```
Lock → runtime_SemacquireMutex(&m.sema):
    найти корзину по адресу
    → добавить goroutine в treap как sudog
    → gopark() — горутина засыпает, M отдаётся планировщику

Unlock → runtime_Semrelease(&m.sema):
    найти корзину
    → вытащить первый sudog
    → goready(g) — горутина возвращается в run queue
```

Тот же `semtable` используется в [[go WaitGroup]] (`runtime_SemacquireWaitGroup`) и [[go Channel]] — механика парковки единая для всего пакета `sync`.

---

## Исходники (Go 1.25+)

- `src/internal/sync/mutex.go` — реализация
- `src/sync/mutex.go` — публичный враппер
- `src/runtime/sema.go` — semtable, semaRoot, sudog

---

## Связанные темы

- [[sync package]] — `RWMutex`, `Once`, `Cond` строятся поверх тех же примитивов
- [[go WaitGroup]] — использует тот же `semtable`; `handoff=true` vs `handoff=false`
- [[go Channel]] — `sudog` и `gopark`/`goready` — общая механика с мьютексом
- [[go scheduler]] — `gopark` снимает горутину с M; `goready` возвращает в run queue
- [[go goroutine]] — статусы горутины: `_Grunning` → `_Gwaiting` → `_Grunnable`
- [[sync atomic (атомики)]] — CAS и `LOCK XADD` под капотом `state`
- [[go memory model]] — happens-before: `Unlock` happens-before следующего `Lock`
