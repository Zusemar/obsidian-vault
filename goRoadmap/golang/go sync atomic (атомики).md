#golang #concurrency #собес #middle

---

## Определение

**Атомарная операция** — операция, которая выполняется как единое неделимое действие с точки зрения всех потоков. Никакой другой поток не видит промежуточного состояния.

`sync/atomic` — тонкая обёртка над инструкциями процессора (`LOCK XADD`, `LOCK CMPXCHG` на x86), которая даёт потокобезопасный доступ к **одной переменной** без мьютекса.

---
## Архитектура пакета (слои)

```
Твой код
    ↓
sync/atomic — типизированный API (type.go, value.go) — Go 1.19+
    ↓
sync/atomic — функциональный API (doc.go) — compiler intrinsics
    ↓
runtime/internal/atomic — asm-файлы на каждую архитектуру
    ↓
CPU: LOCK CMPXCHG (x86) / CASAL (ARM64) / LRD+SCD (RISC-V)
```

---

## Типы (Go 1.19+) — всегда использовать их

|Тип|Методы|Применение|
|---|---|---|
|`atomic.Bool`|Load, Store, Swap, CAS|флаги состояния|
|`atomic.Int32 / Int64`|Load, Store, Swap, CAS, Add, And, Or|счётчики|
|`atomic.Uint32 / Uint64`|Load, Store, Swap, CAS, Add, And, Or|счётчики беззнаковые|
|`atomic.Uintptr`|Load, Store, Swap, CAS, Add, And, Or|указатели как числа|
|`atomic.Pointer[T]`|Load, Store, Swap, CAS|горячая замена конфига, RCU|
|`atomic.Value`|Load, Store, Swap, CAS|любой тип (any)|

> `And` / `Or` появились в **Go 1.23**

---

## Как это работает на уровне железа

### Add (x86-64)

```asm
TEXT ·Xadd(SB), NOSPLIT, $0-20
    MOVQ ptr+0(FP), BX
    MOVL delta+8(FP), AX
    LOCK
    XADDL AX, 0(BX)   ; атомарный exchange-and-add
    RET
```

### CAS (x86-64)

```asm
TEXT ·Cas64(SB), NOSPLIT, $0-25
    MOVQ ptr+0(FP), BX
    MOVQ old+8(FP), AX
    MOVQ new+16(FP), CX
    LOCK CMPXCHGQ CX, 0(BX)   ; compare and exchange
    SETEQ ret+24(FP)
    RET
```

`LOCK` — это префикс, не инструкция. Блокирует шину памяти на время следующей инструкции.

---

## Внутреннее устройство типов

### Bool хранит uint32, не bool

```go
type Bool struct {
    _ noCopy
    v uint32  // аппаратный CAS работает с 32-битными словами
}

func (x *Bool) Load() bool { return LoadUint32(&x.v) != 0 }
func (x *Bool) Store(val bool) { StoreUint32(&x.v, b32(val)) }
```

### Pointer[T] — типобезопасный указатель

```go
type Pointer[T any] struct {
    _ [0]*T        // запрещает конверсию Pointer[A] → Pointer[B]
    _ noCopy
    v unsafe.Pointer
}

func (x *Pointer[T]) Load() *T { return (*T)(LoadPointer(&x.v)) }
```

### noCopy — защита от случайного копирования

```go
type noCopy struct{}
func (*noCopy) Lock()   {}  // go vet / copylocks анализатор
func (*noCopy) Unlock() {}
```

### align64 — выравнивание на 32-битных платформах

Добавляется в `Int64`, `Uint64` — без 8-байтного выравнивания атомарные операции падают на 32-битных ОС.

---

## atomic.Value — самый сложный тип

Хранит `any`. Под капотом — трюк с `efaceWords`:

```go
type efaceWords struct {
    typ  unsafe.Pointer  // *rtype
    data unsafe.Pointer
}

var firstStoreInProgress byte  // sentinel
```

### Протокол первой записи

1. Пробуем CAS: `nil → &firstStoreInProgress` (занимаем слот)
2. Если проиграли — spin, ждём
3. Записываем `data`, потом `typ`
4. `runtime_procUnpin()` — разрешаем preemption

```go
func (v *Value) Store(val any) {
    vp := (*efaceWords)(unsafe.Pointer(v))
    vlp := (*efaceWords)(unsafe.Pointer(&val))
    for {
        typ := LoadPointer(&vp.typ)
        if typ == nil {
            runtime_procPin()  // запрещаем вытеснение
            if !CompareAndSwapPointer(&vp.typ, nil,
                unsafe.Pointer(&firstStoreInProgress)) {
                runtime_procUnpin()
                continue
            }
            StorePointer(&vp.data, vlp.data)
            StorePointer(&vp.typ, vlp.typ)
            runtime_procUnpin()
            return
        }
        if typ == unsafe.Pointer(&firstStoreInProgress) {
            continue  // spin-wait
        }
        if typ != vlp.typ {
            panic("inconsistently typed value")
        }
        StorePointer(&vp.data, vlp.data)
        return
    }
}
```

---

## Гарантии модели памяти

**Sequential consistency** — все атомарные операции ведут себя как выполненные в едином глобальном порядке.

> Если эффект операции A наблюдается операцией B → A **synchronizes-before** B

Эквивалентно `sequentially_consistent` атомикам в C++ и `volatile` в Java.

---

## CAS-loop — основной паттерн

```go
for {
    old := counter.Load()          // 1. читаем
    new := calculateNew(old)       // 2. вычисляем
    if counter.CompareAndSwap(old, new) {
        break                      // 3. победили
    }
    // проиграли — кто-то изменил между Load и CAS, повторяем
}
```

---

## Проблема ABA

### Что происходит

```
Стек: A → B → C

Горутина 1: читает A, планирует CAS(A → B), засыпает
Горутина 2: вытаскивает A, вытаскивает B, кладёт A обратно
Стек теперь: A → C

Горутина 1: просыпается, CAS(A → B) — ПРОХОДИТ!
Стек: B → ??? (B уже не существует, указатель в никуда)
```

### Почему CAS не замечает

CAS проверяет только **значение указателя**. Адрес `A` совпал — CAS успешен. То что структура за это время изменилась — не видно.

### Решение — версионный счётчик

```go
type Versioned struct {
    ptr     unsafe.Pointer
    version uint64  // монотонно растёт, CAS проверяет оба поля
}
```

### Как Go частично защищает

GC не освобождает память пока есть хоть одна ссылка. Пока горутина 1 держит указатель на `A` — GC не трогает эту память. Устраняет **use-after-free**, но не логическую ABA.

---

## Почему sync.Pool не имеет ABA

`sync.Pool` использует `poolDequeue` — кольцевой буфер с **парой индексов** в одном `uint64`:

```go
type poolDequeue struct {
    headTail uint64  // head и tail упакованы вместе
    vals     []eface
}
```

CAS делается над `headTail` целиком:

```go
ptrs2 := d.pack(head, tail+1)
if d.headTail.CompareAndSwap(ptrs, ptrs2) { ... }
```

- Индексы **монотонно растут** — не возвращаются к старому значению
- CAS проверяет сразу head и tail — подмена одного не проходит
- Слот после изъятия обнуляется: `slot.val = nil`

Поэтому ABA физически не успевает возникнуть.

---

## Атомики vs Мьютекс

|Критерий|Атомики|Mutex|
|---|---|---|
|Одна переменная|✅ идеально|избыточно|
|Несколько переменных|❌ нельзя|✅|
|Сложная логика внутри|❌|✅|
|Низкий contention|✅ быстрее|чуть медленнее|
|Высокий contention|⚠️ CAS-storm|✅ усыпляет горутину|
|Легко писать правильно|❌|✅|

---

## Продакшен-сценарии

### Счётчики и метрики

```go
type Server struct {
    requests atomic.Int64
    errors   atomic.Int64
}
func (s *Server) Handle() {
    s.requests.Add(1)
}
```

### Горячая замена конфига (RCU-паттерн)

```go
var cfg atomic.Pointer[Config]

// читатели — без блокировок
c := cfg.Load()

// один писатель
cfg.Store(&Config{...})
```

### Graceful shutdown

```go
type Server struct {
    shuttingDown atomic.Bool
    activeReqs   atomic.Int64
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    if s.shuttingDown.Load() {
        http.Error(w, "shutting down", 503)
        return
    }
    s.activeReqs.Add(1)
    defer s.activeReqs.Add(-1)
}
```

### Побитовые флаги (Go 1.23+)

```go
var flags atomic.Uint32
flags.Or(1 << 3)           // установить флаг
flags.And(^uint32(1 << 3)) // снять флаг
```

---

## Антипаттерны — красные флаги

### Два атомика в связке → мьютекс

```go
// ПЛОХО — между двумя Store влезет другая горутина
atomic.StoreInt64(&balance, newBalance)
atomic.StorePointer(&lastTx, newTx)

// ХОРОШО — одна структура, один указатель
type State struct { Balance int64; LastTx *Tx }
var state atomic.Pointer[State]
state.Store(&State{Balance: newBalance, LastTx: newTx})
```

### Смешивание атомик + обычный доступ

```go
// ПЛОХО — data race
atomic.AddInt64(&x, 1)
fmt.Println(x)  // читаем без атомики!

// ХОРОШО
fmt.Println(atomic.LoadInt64(&x))
```

### Копирование атомика

```go
// ПЛОХО
a := atomic.Int64{}
b := a  // независимая копия, go vet поймает

// ХОРОШО — всегда указатель
func inc(c *atomic.Int64) { c.Add(1) }
```

### atomic.Value со смешанными типами

```go
var v atomic.Value
v.Store("hello")
v.Store(42)  // panic! тип зафиксирован первым Store

// ХОРОШО — если тип известен, используй Pointer[T]
var v atomic.Pointer[MyStruct]
```

---

## Правила кода — чеклист

- [ ] Использую `atomic.Int64`, а не `AddInt64(&x, 1)`
- [ ] Атомики нигде не копирую по значению
- [ ] Все обращения к переменной — только через атомик, нигде напрямую
- [ ] `go test -race ./...` проходит
- [ ] Несколько атомиков не обновляются в логической связке
- [ ] CAS-loop сходится быстро при ожидаемой нагрузке

---

## Вопросы на собесе и ответы

### Базовые

**В чём разница между атомиком и мьютексом?** Атомик — одна операция, одна переменная, без блокировки потока. Мьютекс — блокирует целый участок кода, умеет защищать несколько переменных вместе.

**Когда атомик быстрее мьютекса, а когда медленнее?** Быстрее при низкой конкуренции. При высокой — CAS-loop крутится вхолостую и сжигает CPU, мьютекс усыпляет горутину через планировщик — это выгоднее.

**Что такое data race и как атомики его предотвращают?** Data race — когда два потока читают и пишут одну переменную без синхронизации, результат непредсказуем. Атомики делают операцию неделимой на уровне железа.

### Средние

**Что такое CAS и где применяется?** Compare-And-Swap — атомарно сравнивает значение с ожидаемым и заменяет если совпало. Основа lock-free алгоритмов. Паттерн: читаем → вычисляем → CAS в цикле пока не победим.

**Почему `atomic.Bool` хранит `uint32` внутри, а не `bool`?** Аппаратный CAS работает с 32-битными словами. Эффективного 8-битного атомарного CAS на x86 нет.

**Почему атомики нельзя копировать?** Копирование создаёт независимую переменную. Код думает что работает с одним объектом, а на самом деле с разными — гарантии ломаются. `go vet` ловит через `noCopy`.

**Чем `atomic.Pointer[T]` лучше `atomic.Value`?** `Pointer[T]` типобезопасен на уровне компилятора, не позволяет хранить разные типы. `Value` принимает `any` — ошибка типа обнаруживается только в рантайме через panic.

**Зачем `align64` в `Int64`?** На 32-битных платформах 64-битные поля могут оказаться невыровненными. Атомарные операции требуют 8-байтного выравнивания — иначе SIGBUS.

### Продвинутые

**Что такое ABA-проблема?** Поток читает значение A. Другой меняет A→B→A. Первый делает CAS, видит A, думает "ничего не изменилось" — но структура данных за это время полностью поменялась. CAS проходит успешно, но работает с устаревшими данными. Решение — версионный счётчик рядом с указателем.

**Какую гарантию модели памяти дают атомики в Go?** Sequential consistency — все атомарные операции программы выглядят как выполненные в едином глобальном порядке. Если операция A наблюдается операцией B, то A synchronizes-before B.

**Почему нельзя атомарно обновить две переменные?** Аппаратура гарантирует атомарность только для одного машинного слова. Два отдельных атомика — два отдельных действия с окном между ними.

**Как sync.Pool избегает ABA при краже из чужой очереди?** Использует кольцевой буфер с парой монотонно растущих индексов, упакованных в одно `uint64`. CAS делается над этой парой целиком. Индексы не возвращаются к старому значению в разумные сроки — ABA не успевает возникнуть.

**Что такое compiler intrinsics применительно к атомикам?** Функции `sync/atomic` не вызываются как обычные функции — компилятор заменяет их напрямую на соответствующие инструкции CPU при компиляции. Нет overhead вызова функции.

---

## Связанные темы

- [[go sync package]] — когда нужно больше одной переменной или сложная логика внутри критической секции
- [[go sync.Pool]] — использует CAS над `headTail` внутри; `poolDequeue` — пример lock-free структуры без ABA
- [[go memory model]] — sequential consistency атомиков; happens-before гарантии аллокатора
- [[go goroutine]] — почему mutex усыпляет горутину через планировщик, а CAS-loop нет
- [[go scheduler]] — `runtime_procPin/Unpin` запрещает preemption во время атомарных операций Value
- [[go sync.Map]] — `read atomic.Pointer[readOnly]` — применение атомиков для read-path без мьютекса

![[Pasted image 20260326222928.png]]
