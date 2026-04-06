# sync.Cond — internals (Go 1.26)

**Источник:** `src/sync/cond.go` + `src/sync/runtime.go` + `src/runtime/sema.go`

---

## Что такое Cond и зачем он нужен

`sync.Cond` — классическая _condition variable_ из мира POSIX (аналог `pthread_cond_t`).  
Паттерн: одни горутины **ждут** наступления некоего условия, другие горутины **сигнализируют** о том, что условие изменилось.

> Сам документ-комментарий явно говорит: _«для большинства простых случаев каналы удобнее»_.  
> `Cond` нужен, когда:
> 
> - нужно разбудить **все** ожидающие горутины (аналог `close(ch)`, но переиспользуемый)
> - нужно разбудить **одну** из N ожидающих горутин (аналог `ch <- struct{}{}` с пулом воркеров, но без накопления в буфере)
> - условие проверяется по внешнему состоянию, а не по факту получения значения

---

## Структура Cond

```go
type Cond struct {
    noCopy   noCopy       // запрет копирования (go vet / -copylocks)
    L        Locker       // ассоциированный мьютекс (sync.Mutex или sync.RWMutex)
    notify   notifyList   // очередь ожидающих горутин (runtime)
    checker  copyChecker  // runtime-детектор копирования
}
```

### `notifyList` — сердце механизма

```go
// src/sync/runtime.go
type notifyList struct {
    wait   uint32         // счётчик: сколько горутин запросило ожидание
    notify uint32         // счётчик: сколько горутин уже было разбужено
    lock   uintptr        // runtime-спинлок (futex-based)
    head   unsafe.Pointer // начало связного списка sudog
    tail   unsafe.Pointer // конец списка
}
```

Это **ticket-based queue**: каждая горутина получает уникальный **номер билета** (`wait`) при входе, и пробуждается только когда счётчик `notify` дойдёт до её номера.

---

## API

|Метод|Действие|
|---|---|
|`NewCond(l Locker) *Cond`|Создаёт Cond, привязанный к локеру|
|`Wait()`|Атомарно отпускает `L` и паркует горутину|
|`Signal()`|Будит одну горутину (если есть)|
|`Broadcast()`|Будит все ожидающие горутины|

---

## `Wait()` — пошаговый разбор

```go
func (c *Cond) Wait() {
    c.checker.check()                        // 1. проверка копирования
    t := runtime_notifyListAdd(&c.notify)    // 2. взять билет
    c.L.Unlock()                             // 3. отпустить мьютекс
    runtime_notifyListWait(&c.notify, t)     // 4. парковка до пробуждения
    c.L.Lock()                               // 5. захватить мьютекс снова
}
```

### Шаг 2 — `runtime_notifyListAdd`

```go
// runtime/sema.go (go1.26 master)
func notifyListAdd(l *notifyList) uint32 {
    // атомарный инкремент — работает и под RWMutex read-lock,
    // и при конкурентных вызовах
    return l.wait.Add(1) - 1
}
```

Возвращает **номер билета** (0-based). Это происходит **до** освобождения мьютекса — ключевой момент для отсутствия гонки.

### Шаг 3 — `c.L.Unlock()`

Мьютекс отпускается уже **после** взятия билета. Если бы порядок был обратным, между `Unlock` и `Add` мог бы прийти `Signal()` и билет потерялся — горутина спала бы вечно.

### Шаг 4 — `runtime_notifyListWait`

```go
func notifyListWait(l *notifyList, t uint32) {
    lock(&l.lock)
    // Если notify уже обогнал наш билет — уходим немедленно
    if less(t, l.notify) {
        unlock(&l.lock)
        return
    }
    // Иначе добавляем текущий sudog в связный список и паркуемся
    s := acquireSudog()
    s.g = getg()
    s.ticket = t
    // ... добавление в хвост списка ...
    goparkunlock(&l.lock, waitReasonSyncCondWait, ...)
    // после пробуждения — releaseSudog(s)
}
```

`goparkunlock` — то же, что используется в каналах: переводит горутину в `_Gwaiting` и передаёт управление шедулеру.

---

## `Signal()` — будим одну горутину

```go
func (c *Cond) Signal() {
    c.checker.check()
    runtime_notifyListNotifyOne(&c.notify)
}
```

```go
func notifyListNotifyOne(l *notifyList) {
    // быстрый путь: нет ожидающих
    t := l.wait.Load()
    if t == atomic.Load(&l.notify) { return }

    lock(&l.lock)
    // инкрементируем notify — это и есть "выдача следующего пробуждения"
    t = l.notify
    atomic.Store(&l.notify, t+1)
    // ищем в списке goroutine с ticket == t и делаем goready()
    ...
    unlock(&l.lock)
}
```

Важно: **`Signal` не требует удерживать `L`** — это явно разрешено в комментарии к API. Но держать `L` во время `Signal` тоже можно (иногда это удобно для атомарности обновления состояния + уведомления).

---

## `Broadcast()` — будим всех

```go
func (c *Cond) Broadcast() {
    c.checker.check()
    runtime_notifyListNotifyAll(&c.notify)
}
```

```go
func notifyListNotifyAll(l *notifyList) {
    // если очередь пуста — выходим
    t := l.wait.Load()
    if t == atomic.Load(&l.notify) { return }

    lock(&l.lock)
    // переставляем notify сразу в конец очереди
    atomic.Store(&l.notify, t)
    // забираем весь список
    list := l.head
    l.head, l.tail = nil, nil
    unlock(&l.lock)

    // вне лока: goready() для каждого sudog в списке
    for s := list; s != nil; s = s.next {
        goready(s.g, ...)
    }
}
```

`Broadcast` за одну операцию «обрубает» весь хвост списка и отдаёт все `sudog` шедулеру. Это O(N) по числу ожидающих, но без удержания лока в процессе `goready`.

---

## `copyChecker` — runtime-детектор копирования

```go
type copyChecker uintptr  // хранит своё собственное &self

func (c *copyChecker) check() {
    if uintptr(*c) != uintptr(unsafe.Pointer(c)) &&
        !atomic.CompareAndSwapUintptr((*uintptr)(c), 0, uintptr(unsafe.Pointer(c))) &&
        uintptr(*c) != uintptr(unsafe.Pointer(c)) {
        panic("sync.Cond is copied")
    }
}
```

Трёхступенчатая проверка:

|Шаг|Условие|Смысл|
|---|---|---|
|1|`*c != &c`|fast-path: уже инициализирован и не скопирован → выходим|
|2|CAS(0 → &c) fails|попытка инициализировать; если CAS провалился — либо гонка инициализации, либо копия|
|3|`*c != &c` снова|после CAS: если адрес всё ещё чужой — точно копия → panic|

Идея проста: `copyChecker` хранит **указатель на себя**. После копирования объект переедет по новому адресу, а сохранённый указатель будет смотреть на старое место.

### `noCopy` vs `copyChecker`

||`noCopy`|`copyChecker`|
|---|---|---|
|Когда срабатывает|`go vet` / статический анализ|**runtime** при первом вызове любого метода|
|Стоимость|0|одна атомарная операция (fast-path)|
|Паника|нет|`panic("sync.Cond is copied")`|

`Cond` использует **оба** механизма.

---

## Канонический паттерн использования

```go
var mu sync.Mutex
var ready bool
cond := sync.NewCond(&mu)

// Ожидатель
go func() {
    mu.Lock()
    for !ready {          // ← обязательно for, не if
        cond.Wait()       // атомарно: Unlock → park → Lock
    }
    // используем ready
    mu.Unlock()
}()

// Сигнальщик
mu.Lock()
ready = true
cond.Signal()   // или Broadcast()
mu.Unlock()
```

**Почему `for`, а не `if`?**

- `Broadcast` будит **всех**, но только одна горутина может пройти дальше если ресурс один
- Spurious wakeup технически невозможен в Go (в отличие от pthreads), но `for` всё равно является идиомой и защищает от логических гонок при множественных ожидателях

---

## Взаимосвязь с другими примитивами

```
sync.Mutex ──────────────────────────┐
                                     ▼
sync.Cond ──► notifyList ──► runtime/sema.go ──► sudog ──► goparkunlock
                                                              │
                                     ┌────────────────────────┘
                                     ▼
                              GMP Scheduler (goready → runqueue)
```

- `notifyList` — это **не** `semaphore` из `sync.Mutex`. Это отдельная структура с тикет-очередью
- `sudog` — тот же объект, что используется в каналах; горутина паркуется аналогичным образом
- `goready` — симметрична `gopark`: переводит горутину из `_Gwaiting` → `_Grunnable` и кладёт в runqueue P

---

## Сравнение с каналами

||`sync.Cond`|channel|
|---|---|---|
|Broadcast (разбудить всех)|`Broadcast()`|`close(ch)` — одноразово|
|Signal (разбудить одного)|`Signal()`|`ch <- v`|
|Переиспользование|✅|❌ (закрытый канал нельзя переоткрыть)|
|Передача данных|❌|✅|
|Проверка условия в цикле|нужна явно|обычно не нужна|
|Порядок пробуждения|FIFO по ticket|FIFO по semaphore|

---

## Потенциальные ловушки

### Сигнал без лока — ок, но осторожно

```go
// Допустимо:
cond.Signal()  // без mu.Lock()

// Риск: если сигнальщик обновляет состояние и сигналит без лока,
// ожидатель может увидеть старое состояние до Unlock и снова уснуть
mu.Lock()
ready = true    // сначала меняем состояние
mu.Unlock()
cond.Signal()   // потом сигналим (уже без лока — ок)
```

### Копирование — panic

```go
c1 := sync.NewCond(&mu)
c2 := *c1  // скопировали значение
c2.Wait()  // panic: sync.Cond is copied
```

### Использование без `for`

```go
mu.Lock()
if !ready {       // ← баг: if вместо for
    cond.Wait()
}
// можем оказаться здесь при Broadcast, когда ready ещё false!
mu.Unlock()
```

---

## Ключевые инварианты

1. **Билет берётся до `Unlock`** — гарантирует, что `Signal` до парковки не потеряется
2. **`notify` только растёт** — монотонный счётчик, `less()` обрабатывает wraparound `uint32`
3. **`notifyListWait` проверяет `notify` под локом** — TOCTOU закрыт
4. **`Broadcast` изымает список под локом, но `goready` вызывает вне лока** — минимизирует время удержания `l.lock`
5. **`copyChecker` использует трёхшаговый CAS** — корректно при конкурентной инициализации

---

## Связанные темы

- [[sync.Mutex]] — `L` чаще всего является `*sync.Mutex`
- [[sync.RWMutex]] — `L` может быть `*sync.RWMutex`; `notifyListAdd` конкурентно-безопасен для read-lock
- [[Go Channels internals]] — `sudog`, `gopark`/`goready` — общий механизм
- [[GMP Scheduler]] — `goready` кладёт горутину в runqueue текущего P
- [[sync.WaitGroup]] — другой паттерн ожидания завершения (semaphore-based)