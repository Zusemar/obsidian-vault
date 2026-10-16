# sync.RWMutex

#go #concurrency #sync #internals

## Что это

Reader/Writer mutual exclusion lock. Позволяет **многим читателям** работать параллельно, но **только одному писателю** — и только когда нет читателей.

Zero value готов к использованию — `init()` не нужен.

---

## Структура

```go
type RWMutex struct {
    w           Mutex        // сериализует писателей между собой
    writerSem   uint32       // семафор: писатель ждёт уходящих читателей
    readerSem   uint32       // семафор: читатели ждут уходящего писателя
    readerCount atomic.Int32 // число активных читателей (может быть < 0 !)
    readerWait  atomic.Int32 // число читателей, которых ждёт писатель
}

const rwmutexMaxReaders = 1 << 30
```

### Ключевой трюк: `readerCount` как знаковое число

| Значение | Смысл |
|----------|-------|
| `>= 0` | Нет активного писателя; значение = кол-во читателей |
| `< 0` | Писатель анонсировал себя (вычел `1<<30`); новые `RLock` блокируются |

### Два семафора — два направления

```
Читатели → (RUnlock последний) → writerSem → будит → Писатель
Писатель → (Unlock)            → readerSem → будит → Читатели
```

---

## Writer preference

Писатель **не вытесняет** уже активных читателей — он ждёт пока они сами уйдут.
Но после анонса писателя **новые** `RLock()` блокируются.

```
Читатель A: ████████████  (уже держит RLock — продолжает)
Читатель B: ████████████  (уже держит RLock — продолжает)
Писатель:        └─ Lock() ──────────────────────────────┐
Читатель C:          RLock() → БЛОК                      │
Читатель D:          RLock() → БЛОК                      │
                                                          │
A и B делают RUnlock → писатель просыпается ─────────────┘
После Unlock() писателя: C и D получают RLock
```

Без writer preference поток новых читателей мог бы бесконечно обновляться и писатель никогда не дождался бы.

---

## RLock() — захват на чтение

```go
func (rw *RWMutex) RLock() {
    if rw.readerCount.Add(1) < 0 {
        // Писатель анонсировал себя — паркуемся
        runtime_SemacquireRWMutexR(&rw.readerSem, false, 0)
    }
}
```

- Атомарный `Add(+1)` — единственная операция в fast path → [[sync atomic (атомики)]]
- Результат `< 0` → писатель ждёт → `gopark` на `readerSem`
- Горутина снимается с M, M берёт другую горутину из run queue → [[go scheduler]]

## TryRLock() — неблокирующая попытка

```go
func (rw *RWMutex) TryRLock() bool {
    for {
        c := rw.readerCount.Load()
        if c < 0 {
            return false // писатель активен
        }
        if rw.readerCount.CompareAndSwap(c, c+1) {
            return true
        }
        // CAS не прошёл → retry
    }
}
```

CAS-loop вместо `Add` — нужно сначала проверить знак, потом инкрементировать. `Add` проверить-и-не-делать не позволяет.

## RUnlock() — освобождение чтения

```go
func (rw *RWMutex) RUnlock() {
    if r := rw.readerCount.Add(-1); r < 0 {
        rw.rUnlockSlow(r)
    }
}

func (rw *RWMutex) rUnlockSlow(r int32) {
    // r+1 == 0              → RUnlock без RLock
    // r+1 == -rwmutexMaxReaders → RUnlock при активном писателе без читателей
    if r+1 == 0 || r+1 == -rwmutexMaxReaders {
        fatal("sync: RUnlock of unlocked RWMutex")
    }
    if rw.readerWait.Add(-1) == 0 {
        // Последний уходящий читатель будит писателя
        runtime_Semrelease(&rw.writerSem, false, 1)
    }
}
```

Fast path инлайнится. Slow path — только когда писатель ждёт (`r < 0`).
Тот читатель, кто приводит `readerWait` к нулю — будит писателя.

---

## Lock() — захват на запись

```go
func (rw *RWMutex) Lock() {
    rw.w.Lock()                                                      // (1)
    r := rw.readerCount.Add(-rwmutexMaxReaders) + rwmutexMaxReaders // (2)
    if r != 0 && rw.readerWait.Add(r) != 0 {                        // (3)
        runtime_SemacquireRWMutex(&rw.writerSem, false, 0)          // (4)
    }
}
```

**(1)** `w.Lock()` — сериализует писателей. Только один пройдёт дальше. → [[go Mutex]]

**(2)** Вычитаем `1<<30` из `readerCount` — анонс писателя. Новые `RLock` заблокируются. Восстанавливаем реальное число уже активных читателей:
```
readerCount.Add(-1<<30) вернул, например, -1073741821
+ rwmutexMaxReaders (1073741824) = 3 активных читателя
```

**(3)** Если читатели есть — записываем их число в `readerWait`. Если `Add(r) != 0` — они ещё не ушли, паркуемся.

> **Тонкость:** между (2) и (3) читатели могут успеть уйти и декрементировать `readerWait`. Если все ушли до нашего `Add(r)` — результат `Add` будет `0` и мы не паркуемся.

**(4)** `gopark` на `writerSem`. Последний читатель нас разбудит. → [[go goroutine]]

## TryLock() — неблокирующая попытка записи

```go
func (rw *RWMutex) TryLock() bool {
    if !rw.w.TryLock() {
        return false // другой писатель
    }
    if !rw.readerCount.CompareAndSwap(0, -rwmutexMaxReaders) {
        rw.w.Unlock()
        return false // есть активные читатели
    }
    return true
}
```

Два условия: нет другого писателя + нет активных читателей (`readerCount == 0`).
Если CAS провалился — откатываем `w.Unlock()`. Порядок важен: сначала `wLock`, потом CAS.

## Unlock() — освобождение записи

```go
func (rw *RWMutex) Unlock() {
    r := rw.readerCount.Add(rwmutexMaxReaders) // (1)
    if r >= rwmutexMaxReaders {
        fatal("sync: Unlock of unlocked RWMutex")
    }
    for i := 0; i < int(r); i++ {             // (2)
        runtime_Semrelease(&rw.readerSem, false, 0)
    }
    rw.w.Unlock()                             // (3)
}
```

**(1)** Возвращаем `1<<30` в `readerCount` — анонс снят. `r` = число читателей, стоящих в очереди.

**(2)** Будим ровно `r` горутин-читателей, по одному `Semrelease` на каждого.

**(3)** `w.Unlock()` — следующий писатель может войти. Порядок важен: сначала читатели, потом писатели. Иначе новый писатель мог бы анонсировать себя раньше, чем текущие читатели успеют проснуться.

---

## Полная карта взаимодействий

```
RLock()                            Lock()
  │                                  │
  ├─ readerCount.Add(+1)             ├─ w.Lock()
  │                                  │
  ├─ если < 0:                       ├─ readerCount.Add(-1<<30)
  │   gopark → readerSem             │   новые RLock встают в очередь
  │                                  │
  │                                  ├─ если активных readers > 0:
  │                                  │   readerWait.Add(r)
  │                                  │   gopark → writerSem
  ▼                                  ▼
RUnlock()                          Unlock()
  │                                  │
  ├─ readerCount.Add(-1)             ├─ readerCount.Add(+1<<30)
  │                                  │   анонс снят
  └─ если < 0 (писатель ждёт):       │
      readerWait.Add(-1)             ├─ Semrelease(readerSem) × r
      если == 0:                     │   будим всех ждавших читателей
        Semrelease(writerSem)        │
        будим писателя              └─ w.Unlock()
                                        пускаем следующего писателя
```

---

## Когда использовать

| Ситуация | Что использовать |
|----------|-----------------|
| Данные только читаются несколькими горутинами | `RLock / RUnlock` |
| Данные изменяются | `Lock / Unlock` |
| Читают часто, пишут редко | `RWMutex` выгоднее `Mutex` |
| Пишут так же часто как читают | Обычный `Mutex` проще и не медленнее |

```go
var mu sync.RWMutex
var cache map[string]string

func Get(key string) string {
    mu.RLock()
    defer mu.RUnlock()
    return cache[key] // параллельно с другими Get — ок
}

func Set(key, value string) {
    mu.Lock()
    defer mu.Unlock()
    cache[key] = value // единственный владелец
}
```

Для concurrent map с read-heavy нагрузкой также стоит рассмотреть [[sync.Map]] — не требует явного лока.

---

## Подводные камни

**Рекурсивный RLock — дедлок:**
```go
mu.RLock()
// где-то внутри вызывается Lock() другой горутиной
mu.RLock() // дедлок — писатель ждёт первого RLock, второй RLock ждёт писателя
```

**RLock нельзя апгрейдить до Lock:**
```go
mu.RLock()
mu.Lock() // дедлок — Lock ждёт когда RLock уйдёт, но RLock держим мы сами
```

**RWMutex не привязан к горутине** — одна горутина может взять RLock, другая сделать RUnlock. Это легально, но обычно признак плохого дизайна.

---

## runtime.rwmutex — отличия от sync.RWMutex

> Внутренний вариант для использования самим рантаймом. Исходник: `src/runtime/rwmutex.go`

| Аспект | `sync.RWMutex` | `runtime.rwmutex` |
|--------|----------------|-------------------|
| Что блокируется | Горутина | M (OS-поток) |
| Механизм парковки | `gopark` / `goready` | `notesleep` / `notewakeup` |
| Планировщик | Задействован | **Не задействован** |
| Очередь читателей | Семафор `readerSem` | Intrusive linked list из M |
| Сериализация писателей | `w Mutex` | `wLock mutex` (отдельный) |
| Защита очереди читателей | Не нужна (семафор атомарен) | `rLock mutex` (отдельный) |
| `readerPass` | Отсутствует | Есть — решает TOCTOU гонку linked list |
| Lock ranking | Нет | Есть (`readRank`, `readRankInternal`, `writeRank`) |
| `init()` | Не нужен | **Обязателен** |
| `TryRLock` / `TryLock` | Есть | Нет |

### Почему рантайму нужен `notesleep`

`runtime.rwmutex` защищает структуры которые использует сам планировщик (например `allocmLock` — создание новых M). Если вызвать `gopark` в этом контексте — планировщик попытается найти другую горутину, что рекурсивно обратится к тем же структурам → дедлок или corruption. → [[go scheduler]]

### `readerPass` — артефакт linked list

В `sync.RWMutex` этого поля нет. Оно появляется из-за гонки специфичной для ручного списка:

```
Читатель: readerCount.Add(1) → видит < 0 → идёт парковаться
                                   ↕ (писатель успевает сделать Unlock)
Читатель: lock(&rLock) → список пуст, readerPass > 0 → берёт пропуск и не ждёт
```

В семафорном варианте `sync.RWMutex` этой гонки нет — `Semrelease/Semacquire` атомарно управляют очередью.

---

## Связанные темы

- [[go Mutex]] — используется внутри RWMutex для сериализации писателей; сравнение fast/slow path
- [[sync package]] — обзор всего пакета sync: Once, Cond, WaitGroup, Pool
- [[sync atomic (атомики)]] — атомики на которых строится readerCount / readerWait
- [[go memory model]] — happens-before гарантии RWMutex, видимость записей после Unlock
- [[go WaitGroup]] — другой примитив sync, ожидание группы горутин
- [[sync.Map]] — concurrent map; альтернатива `map + RWMutex` при read-heavy нагрузке
- [[sync.Pool]] — переиспользование объектов без блокировок
- [[go scheduler]] — GMP, gopark/goready, почему runtime.rwmutex не может использовать gopark
- [[go goroutine]] — жизненный цикл горутины, парковка, пробуждение
- [[go concurrency patterns]] — паттерны с RWMutex: read-through cache, copy-on-write
- [[go Channel]] — альтернативный подход к синхронизации через CSP
- [[go philosophy]] — философия Go: explicit sync vs channels, "share memory by communicating"
