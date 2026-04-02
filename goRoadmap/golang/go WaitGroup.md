#golang #concurrency #sync #internals

## Что такое WaitGroup

`WaitGroup` — счётчик с барьером. Позволяет одной горутине дождаться завершения группы других.

```go
var wg sync.WaitGroup

wg.Go(task1)   // Go 1.25+
wg.Go(task2)
wg.Wait()      // блокируется пока обе задачи не завершатся
```

Старый паттерн (до Go 1.25):

```go
var wg sync.WaitGroup

wg.Add(1)
go func() {
    defer wg.Done()
    task1()
}()

wg.Add(1)
go func() {
    defer wg.Done()
    task2()
}()

wg.Wait()
```

---

## API

| Метод            | Что делает                                        |
| ---------------- | ------------------------------------------------- |
| `Add(delta int)` | увеличивает/уменьшает счётчик задач               |
| `Done()`         | `Add(-1)` — задача завершена                      |
| `Wait()`         | блокируется пока счётчик не станет 0              |
| `Go(f func())`   | `Add(1)` + горутина + `defer Done()` *(Go 1.25+)* |

---

## Структура

```go
type WaitGroup struct {
    noCopy noCopy        // защита от копирования (go vet)
    state  atomic.Uint64 // упакованы counter + waiters
    sema   uint32        // семафор рантайма для парковки горутин
}
```

### Упаковка state

```
bits [63:32]  →  counter  (int32)   — кол-во незавершённых задач
bit  [31]     →  bubble flag        — для synctest (игнорировать)
bits [30:0]   →  waiters  (uint32)  — кол-во горутин в Wait()
```

Всё в одном `uint64` чтобы **атомарно** читать оба значения одновременно — иначе между чтением counter и waiters мог бы проскочить `Add`.

Максимум задач на одну WaitGroup: **2,147,483,647** (int32 max). На практике недостижимо.

---

## Два семафора

WaitGroup использует два семафора на разных уровнях:

### 1. `state.counter` — логический (инвертированный)

Реализует семантику WaitGroup. Инвертированный — ждёт не **появления** ресурса, а его **исчерпания**.

| Классический семафор | WaitGroup |
|---|---|
| P: жди пока число > 0 | `Wait`: жди пока counter **== 0** |
| V: увеличь число | `Done`: уменьши counter |

### 2. `sema` — транспортный

Просто `uint32` — адрес как ключ в хэш-таблице рантайма. Паркует/будит горутины физически. Ничего не знает про логику WaitGroup.

```
state  →  логика (кто и сколько ждёт)
sema   →  механика (физическая парковка горутины в рантайме)
```

---

## Как работает Add(delta)

```go
state := wg.state.Add(uint64(delta) << 32)  // delta в верхние 32 бита
v := int32(state >> 32)                      // достаём counter
w := uint32(state & 0x7fffffff)              // достаём waiters
```

**Паники:**
- `v < 0` → счётчик отрицательный
- `w != 0 && delta > 0 && v == int32(delta)` → Add с нуля при живых waiters (misuse)

**Если `v == 0 && w > 0`** — последний Done, есть ожидающие:

```go
wg.state.Store(0)                          // сбрасываем всё состояние
for ; w != 0; w-- {
    runtime_Semrelease(&wg.sema, false, 0) // будим каждого waiter-а
}
```

---

## Как работает Wait()

CAS retry loop:

```go
for {
    state := wg.state.Load()
    v := int32(state >> 32)
    if v == 0 { return }  // counter уже 0 — выходим сразу

    // атомарно инкрементируем waiters
    if wg.state.CompareAndSwap(state, state+1) {
        runtime_SemacquireWaitGroup(&wg.sema, ...)  // паркуемся
        return  // проснулись — Add сбросил state в 0
    }
    // CAS не прошёл — кто-то изменил state, retry
}
```

`state+1` инкрементирует нижние 32 бита (waiters) — без сдвига.

---

## Семафоры рантайма

`runtime_Semacquire` / `runtime_Semrelease` — Go-линковочные stubs. Реализация в `runtime/sema.go`.

Адрес `&wg.sema` — ключ в глобальной хэш-таблице `semtable`:

```
&wg.sema → semtable[hash(&wg.sema)] → [sudog(g1), sudog(g2), ...]
```

### sudog

Структура "горутина ждёт чего-то". Та же используется в [[go Channel]] операциях.

```go
type sudog struct {
    g    *g             // сама горутина
    next *sudog
    prev *sudog
    elem unsafe.Pointer // на что ждём
}
```

### Acquire (засыпание)

1. Fast path: декрементирует `*s` если `*s > 0` — выходит сразу
2. Slow path: создаёт `sudog`, кладёт в очередь `semtable[hash(s)]`
3. `gopark` — горутина снимается с процессора

### Release (пробуждение)

1. Инкрементирует `*s`
2. Достаёт `sudog` из очереди
3. `goready` — горутина становится runnable

---

## GMP: куда уходит горутина после goready

```
goready(gp)
  → gp.status = _Grunnable
  → runqput(p, gp, next=false)  // в local run queue своего P
```

Горутина **не запускается мгновенно** — она становится runnable и ждёт своей очереди на P.

### runnext

`runqput(p, gp, next=true)` — кладёт горутину в `p.runnext` (один слот, запустится **следующей**, минуя всю очередь).

В WaitGroup используется `handoff=false` — пробуждённые waiters идут в обычную очередь без приоритета. Это нормально — нет причины давать им приоритет.

`handoff=true` используется в `sync.Mutex` в режиме честной передачи — разбуженная горутина получает мьютекс и `runnext` чтобы сразу продолжить.

---

## Инварианты и ограничения

| Ситуация | Правило |
|---|---|
| `Add` когда counter > 0 | можно в любое время |
| `Add` когда counter == 0 | должен быть **до** `Wait` (happens-before) |
| Реиспользование WaitGroup | новый `Add` только после того как все `Wait` вернулись |
| Копирование | запрещено после первого использования (`noCopy` + `go vet`) |

---

## Связанные темы

- [[go goroutine]] — горутины которые WaitGroup ждёт; `gopark` / `goready` меняют их статус
- [[go scheduler]] — GMP модель; `runqput` и `runnext` после `goready`
- [[go Channel]] — `sudog` используется и тут, и там; похожая механика парковки
- [[go sync package]] — `sync.Mutex` использует те же семафоры рантайма с `handoff=true`
- [[go sync atomic (атомики)]] — `atomic.Uint64` под капотом `state`; CAS в `Wait`
- [[go memory model]] — happens-before гарантии: `Done` happens-before возврата из `Wait`
