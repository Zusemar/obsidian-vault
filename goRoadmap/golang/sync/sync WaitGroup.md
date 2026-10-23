---
handwriting-page-id: aec9ee06-96fc-47cd-a3c1-163e4cacfc82
---
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
```go
// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package sync

import (
	"internal/race"
	"internal/synctest"
	"sync/atomic"
	"unsafe"
)

// A WaitGroup is a counting semaphore typically used to wait
// for a group of goroutines or tasks to finish.
//
// Typically, a main goroutine will start tasks, each in a new
// goroutine, by calling [WaitGroup.Go] and then wait for all tasks to
// complete by calling [WaitGroup.Wait]. For example:
//
//	var wg sync.WaitGroup
//	wg.Go(task1)
//	wg.Go(task2)
//	wg.Wait()
//
// A WaitGroup may also be used for tracking tasks without using Go to
// start new goroutines by using [WaitGroup.Add] and [WaitGroup.Done].
//
// The previous example can be rewritten using explicitly created
// goroutines along with Add and Done:
//
//	var wg sync.WaitGroup
//	wg.Add(1)
//	go func() {
//		defer wg.Done()
//		task1()
//	}()
//	wg.Add(1)
//	go func() {
//		defer wg.Done()
//		task2()
//	}()
//	wg.Wait()
//
// This pattern is common in code that predates [WaitGroup.Go].
//
// A WaitGroup must not be copied after first use.
type WaitGroup struct {
	noCopy noCopy

	// Bits (high to low):
	//   bits[0:32]  counter
	//   bits[32]    flag: synctest bubble membership
	//   bits[33:64] wait count
	state atomic.Uint64
	sema  uint32
}

// waitGroupBubbleFlag indicates that a WaitGroup is associated with a synctest bubble.
const waitGroupBubbleFlag = 0x8000_0000

// Add adds delta, which may be negative, to the [WaitGroup] task counter.
// If the counter becomes zero, all goroutines blocked on [WaitGroup.Wait] are released.
// If the counter goes negative, Add panics.
//
// Callers should prefer [WaitGroup.Go].
//
// Note that calls with a positive delta that occur when the counter is zero
// must happen before a Wait. Calls with a negative delta, or calls with a
// positive delta that start when the counter is greater than zero, may happen
// at any time.
// Typically this means the calls to Add should execute before the statement
// creating the goroutine or other event to be waited for.
// If a WaitGroup is reused to wait for several independent sets of events,
// new Add calls must happen after all previous Wait calls have returned.
// See the WaitGroup example.
func (wg *WaitGroup) Add(delta int) {
	if race.Enabled {
		if delta < 0 {
			// Synchronize decrements with Wait.
			race.ReleaseMerge(unsafe.Pointer(wg))
		}
		race.Disable()
		defer race.Enable()
	}
	bubbled := false
	if synctest.IsInBubble() {
		// If Add is called from within a bubble, then all Add calls must be made
		// from the same bubble.
		switch synctest.Associate(wg) {
		case synctest.Unbubbled:
		case synctest.OtherBubble:
			// wg is already associated with a different bubble.
			fatal("sync: WaitGroup.Add called from multiple synctest bubbles")
		case synctest.CurrentBubble:
			bubbled = true
			state := wg.state.Or(waitGroupBubbleFlag)
			if state != 0 && state&waitGroupBubbleFlag == 0 {
				// Add has been called from outside this bubble.
				fatal("sync: WaitGroup.Add called from inside and outside synctest bubble")
			}
		}
	}
	state := wg.state.Add(uint64(delta) << 32)
	if state&waitGroupBubbleFlag != 0 && !bubbled {
		// Add has been called from within a synctest bubble (and we aren't in one).
		fatal("sync: WaitGroup.Add called from inside and outside synctest bubble")
	}
	v := int32(state >> 32)
	w := uint32(state & 0x7fffffff)
	if race.Enabled && delta > 0 && v == int32(delta) {
		// The first increment must be synchronized with Wait.
		// Need to model this as a read, because there can be
		// several concurrent wg.counter transitions from 0.
		race.Read(unsafe.Pointer(&wg.sema))
	}
	if v < 0 {
		panic("sync: negative WaitGroup counter")
	}
	if w != 0 && delta > 0 && v == int32(delta) {
		panic("sync: WaitGroup misuse: Add called concurrently with Wait")
	}
	if v > 0 || w == 0 {
		return
	}
	// This goroutine has set counter to 0 when waiters > 0.
	// Now there can't be concurrent mutations of state:
	// - Adds must not happen concurrently with Wait,
	// - Wait does not increment waiters if it sees counter == 0.
	// Still do a cheap sanity check to detect WaitGroup misuse.
	if wg.state.Load() != state {
		panic("sync: WaitGroup misuse: Add called concurrently with Wait")
	}
	// Reset waiters count to 0.
	wg.state.Store(0)
	if bubbled {
		// Adds must not happen concurrently with wait when counter is 0,
		// so we can safely disassociate wg from its current bubble.
		synctest.Disassociate(wg)
	}
	for ; w != 0; w-- {
		runtime_Semrelease(&wg.sema, false, 0)
	}
}

// Done decrements the [WaitGroup] task counter by one.
// It is equivalent to Add(-1).
//
// Callers should prefer [WaitGroup.Go].
//
// In the terminology of [the Go memory model], a call to Done
// "synchronizes before" the return of any Wait call that it unblocks.
//
// [the Go memory model]: https://go.dev/ref/mem
func (wg *WaitGroup) Done() {
	wg.Add(-1)
}

// Wait blocks until the [WaitGroup] task counter is zero.
func (wg *WaitGroup) Wait() {
	if race.Enabled {
		race.Disable()
	}
	for {
		state := wg.state.Load()
		v := int32(state >> 32)
		w := uint32(state & 0x7fffffff)
		if v == 0 {
			// Counter is 0, no need to wait.
			if race.Enabled {
				race.Enable()
				race.Acquire(unsafe.Pointer(wg))
			}
			if w == 0 && state&waitGroupBubbleFlag != 0 && synctest.IsAssociated(wg) {
				// Adds must not happen concurrently with wait when counter is 0,
				// so we can disassociate wg from its current bubble.
				if wg.state.CompareAndSwap(state, 0) {
					synctest.Disassociate(wg)
				}
			}
			return
		}
		// Increment waiters count.
		if wg.state.CompareAndSwap(state, state+1) {
			if race.Enabled && w == 0 {
				// Wait must be synchronized with the first Add.
				// Need to model this is as a write to race with the read in Add.
				// As a consequence, can do the write only for the first waiter,
				// otherwise concurrent Waits will race with each other.
				race.Write(unsafe.Pointer(&wg.sema))
			}
			synctestDurable := false
			if state&waitGroupBubbleFlag != 0 && synctest.IsInBubble() {
				if race.Enabled {
					race.Enable()
				}
				if synctest.IsAssociated(wg) {
					// Add was called within the current bubble,
					// so this Wait is durably blocking.
					synctestDurable = true
				}
				if race.Enabled {
					race.Disable()
				}
			}
			runtime_SemacquireWaitGroup(&wg.sema, synctestDurable)
			isReset := wg.state.Load() != 0
			if race.Enabled {
				race.Enable()
				race.Acquire(unsafe.Pointer(wg))
			}
			if isReset {
				panic("sync: WaitGroup is reused before previous Wait has returned")
			}
			return
		}
	}
}

// Go calls f in a new goroutine and adds that task to the [WaitGroup].
// When f returns, the task is removed from the WaitGroup.
//
// The function f must not panic.
//
// If the WaitGroup is empty, Go must happen before a [WaitGroup.Wait].
// Typically, this simply means Go is called to start tasks before Wait is called.
// If the WaitGroup is not empty, Go may happen at any time.
// This means a goroutine started by Go may itself call Go.
// If a WaitGroup is reused to wait for several independent sets of tasks,
// new Go calls must happen after all previous Wait calls have returned.
//
// In the terminology of [the Go memory model], the return from f
// "synchronizes before" the return of any Wait call that it unblocks.
//
// [the Go memory model]: https://go.dev/ref/mem
func (wg *WaitGroup) Go(f func()) {
	wg.Add(1)
	go func() {
		defer func() {
			if x := recover(); x != nil {
				// f panicked, which will be fatal because
				// this is a new goroutine.
				//
				// Calling Done will unblock Wait in the main goroutine,
				// allowing it to race with the fatal panic and
				// possibly even exit the process (os.Exit(0))
				// before the panic completes.
				//
				// This is almost certainly undesirable,
				// so instead avoid calling Done and simply panic.
				panic(x)
			}

			// f completed normally, or abruptly using goexit.
			// Either way, decrement the semaphore.
			wg.Done()
		}()
		f()
	}()
}
```
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
- [[sync package]] — `sync.Mutex` использует те же семафоры рантайма с `handoff=true`
- [[sync atomic (атомики)]] — `atomic.Uint64` под капотом `state`; CAS в `Wait`
- [[go memory model]] — happens-before гарантии: `Done` happens-before возврата из `Wait`
