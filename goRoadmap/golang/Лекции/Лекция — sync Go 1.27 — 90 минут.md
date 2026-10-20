---
title: "sync без магии — чтение исходников Go 1.27.3"
tags: [golang, sync, internals, lecture]
go: go1.27.3
duration: 90 минут
verified: 2026-10-21
---

# sync без магии: 90 минут внутри Go 1.27.3

Это сценарий лекции с совместным чтением исходников. За 90 минут мы проследим путь от публичного контракта до атомарной операции, регистрации ожидания и планировщика. Слушатель уже умеет пользоваться `go`, каналами, `Mutex` и `WaitGroup`; на занятии мы объясняем, почему реализация сохраняет инварианты при неприятных чередованиях горутин.

Основа — десять ваших заметок из `obsVault/goRoadmap/golang/sync`. Проверенная версия — **Go 1.27.3**, а не `master` и не произвольная ветка 1.27. Официальный архив: [go1.27.3.src.tar.gz](https://go.dev/dl/go1.27.3.src.tar.gz). Его SHA-256: `6e7749035cf360f06b2575d162ea79df9fda8ef671889e5c375f8e50777f26bb`. Для офлайн-чтения используйте этот официальный архив; все ссылки далее закреплены за его тегом. `x/sync/errgroup` вынесен в приложение и имеет свою версию `v0.17.0`: номер Go не задаёт версию внешнего модуля.

**Как вести:** не читать вслух весь текст. На экране держать исходник, объяснять выделенный переход, затем давать аудитории предсказать следующий шаг. В тайминг включены вопросы и ручные трассировки. Приложения и домашняя практика идут сверх 90 минут.

| Время | Раздел | Что делаем на экране |
|---|---|---|
| 00–07 | Контракт, happens-before, atomic | Строим цепочку публикации |
| 07–15 | Runtime semaphore | Доказываем отсутствие потерянного wakeup |
| 15–35 | Mutex | Читаем fast path, spin, очередь, starvation |
| 35–45 | RWMutex | Трассируем двух читателей и писателя |
| 45–55 | WaitGroup | Разбираем регистрацию waiter и переход к нулю |
| 55–63 | Once | Ломаем реализацию на одном CAS |
| 63–72 | Cond | Разбираем билет до Unlock и цикл по предикату |
| 72–82 | Map | Читаем hash-trie и публикацию нового поддерева |
| 82–87 | Pool | Прослеживаем Get, pin и очистку GC |
| 87–90 | Контрольный разбор | Восстанавливаем три инварианта без подсказки |

## 1. 00–07. Начинаем с контракта и атомарной публикации

### 00–02. Что именно мы должны доказать

Попросите назвать две разные задачи синхронизации: исключить одновременное нарушение инварианта и сделать результаты одной горутины видимыми другой. Для `Mutex` контракт говорит: n-й `Unlock` synchronizes-before m-го `Lock` при `n < m`. Успешный `TryLock` равен `Lock` по этому отношению, проваленный не даёт отношения синхронизации. Откройте [комментарии публичного Mutex](https://github.com/golang/go/blob/go1.27.3/src/sync/mutex.go#L1-L66) и [раздел Locks модели памяти](https://go.dev/ref/mem#Locks).

```go
// Одна и та же mu в обеих горутинах.
mu.Lock()
x = 42
mu.Unlock()

// Другая горутина, захватившая mu после этой записи:
mu.Lock()
fmt.Println(x)
mu.Unlock()
```

Цепочка доказательства: запись `x` → порядок внутри первой горутины → `Unlock` → synchronizes-before → успешный `Lock` → чтение `x`. Именно цепочка, а не прошедшее время, объясняет видимость. Data race — конфликтующие обычные доступы, не упорядоченные happens-before; в Go нельзя автоматически переносить на них модель «любое поведение допустимо» из C++. Точные ограничения приведены в [Memory model: data races](https://go.dev/ref/mem#restrictions).

### 02–05. Атомики — строительные блоки, а не готовая транзакция

Прочитайте [контракт sync/atomic](https://github.com/golang/go/blob/go1.27.3/src/sync/atomic/doc.go#L1-L222): операции выглядят выполненными в едином последовательно согласованном порядке; если B наблюдает эффект A, A synchronizes-before B. В API нет пользовательского выбора `relaxed`/`acquire`/`release`. Из этого не следует, что две отдельные атомарные записи образуют один неделимый снимок.

```go
type Config struct {
    Endpoint string
    Limit    int
}
var current atomic.Pointer[Config]

func publish() {
    c := &Config{Endpoint: "internal", Limit: 8}
    current.Store(c)
}
func read() *Config { return current.Load() }
```

Почему читатель, наблюдающий опубликованный указатель, видит инициализированные поля? Инициализация предшествует `Store`, наблюдающий её `Load` связывает горутины, чтение полей идёт после `Load`. Условия примера: читатель обрабатывает `nil`, если публикации ещё не было, а опубликованный объект после `Store` не мутируется. Атомарность указателя не распространяется на конкурентные записи в `Endpoint` или `Limit`. Опора: [Pointer[T]](https://github.com/golang/go/blob/go1.27.3/src/sync/atomic/type.go#L44-L70) и [правило наблюдения эффекта](https://github.com/golang/go/blob/go1.27.3/src/sync/atomic/doc.go#L1-L222).

### 05–07. Как читать следующие файлы

На каждом шаге задаём четыре вопроса: что защищено, кто может менять состояние, где фиксируется переход и почему ожидание не потеряет пробуждение. Отдельно помечаем **контракт**, **реализацию go1.27.3** и **наш вывод из реализации**. Например, поле `state` — реализация; запрет копирования использованного mutex — контракт. `noCopy` помогает статическому анализу, но ничего не блокирует во время выполнения. Сверьте [noCopy](https://github.com/golang/go/blob/go1.27.3/src/sync/cond.go#L113-L124) и [публичный тип](https://github.com/golang/go/blob/go1.27.3/src/sync/mutex.go#L1-L66).

Внутренний CAS не следует трактовать как часть публичного happens-before протокола. Особенно хорошо это видно у `RWMutex`: обработка атомарной синхронизации для race detector временно отключается, чтобы внутренний общий счётчик не скрывал гонки между читателями. Вернёмся к [комментарию перед RLock](https://github.com/golang/go/blob/go1.27.3/src/sync/rwmutex.go#L16-L61) в разделе про RWMutex.

**Вопрос аудитории:** если сделать `Load` двух разных atomic-полей подряд, получается ли согласованный снимок пары? **Ответ:** нет, между ними писатель может обновить одно поле; нужен протокол версии, неизменяемый объект за одним указателем или общий lock.

## 2. 07–15. Runtime semaphore: почему пробуждение не теряется

### 07–09. Переходим через границу пакетов

Откройте [runtime API пакета sync](https://github.com/golang/go/blob/go1.27.3/src/sync/runtime.go#L1-L59), затем [связку sync.runtime_SemacquireWaitGroup](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L108-L115). Имена `runtime_Semacquire…` в `sync` связаны с реализацией через `go:linkname`; это не вызов системного семафора по имени. Mutex и WaitGroup используют механизм `semacquire1`/`semrelease1`, Cond — отдельный `notifyList` в том же runtime-файле.

`sema uint32` имеет две роли: его адрес идентифицирует ожидание, а значение участвует в протоколе доступных semaphore-токенов. Это не просто «неиспользуемое число ради адреса». В [semrelease1](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L207-L289) сначала увеличивается `*addr`, а [cansemacquire](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L291-L301) пробует уменьшить положительное значение с CAS.

Адрес хешируется в `semtable`; внутри `semaRoot` находятся lock, дерево `sudog` по адресам и счётчик ожидающих. В проверенной версии таблица содержит 251 корзину, но это не контракт и не число, которое должен знать пользователь `Mutex`. Читать: [semaRoot, semTable и rootFor](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L30-L68). Каналы тоже используют `sudog`, однако их очереди находятся в `hchan`, а не в `semtable`: [hchan.recvq и sendq](https://github.com/golang/go/blob/go1.27.3/src/runtime/chan.go#L34-L56).

### 09–13. Главная трассировка: release между проверкой и сном

Откройте [semacquire1](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L146-L201) и [semrelease1](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L207-L289) рядом. У acquire есть быстрый `cansemacquire`, а затем медленный путь: взять root lock, увеличить `nwait`, ещё раз попытаться забрать токен, поставить `sudog` в очередь и вызвать `goparkunlock`. У release увеличение токена стоит **до** проверки `nwait`.

| Момент release | Почему acquire не уснёт навсегда |
|---|---|
| До первой попытки acquire | Первая попытка может забрать токен |
| После первой неудачи, до регистрации waiter | Повторная проверка под root lock увидит токен |
| После регистрации и повторной неудачи | Release видит `nwait`, берёт тот же lock и найдёт очередь |
| После парковки | Release извлекает waiter и делает его runnable |

Почему недостаточно один раз проверить токен и затем просто лечь спать? Между этими действиями release мог бы решить, что ждать некому. Порядок `nwait` и повторной проверки закрывает это окно. Это наш разбор конкретного кода; аналогичное объяснение оставлено авторами рядом с повторным `cansemacquire`. Ссылки: [регистрация waiter](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L146-L201) и [порядок Xadd и проверки nwait](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L207-L289).

### 13–15. Runnable не означает running

`gopark` передаёт управление через runtime, а `park_m` переводит G в ожидание и запускает планирование. Поэтому ожидающая `sync.Mutex` горутина не обязана блокировать OS-поток на системном вызове; M может выполнять другую G. Это видно в [gopark](https://github.com/golang/go/blob/go1.27.3/src/runtime/proc.go#L429-L475), [park_m](https://github.com/golang/go/blob/go1.27.3/src/runtime/proc.go#L4267-L4320). Если работы нет, runtime может парковать уже поток, но это следующий уровень, а не обязательный syscall каждого `Lock`.

Обратный путь: `readyWithTime` → `goready` → `ready` → `runqput`. В этой версии `goready` передаёт `next=true` независимо от аргумента semaphore `handoff`. Значит, запись из старой заметки «WaitGroup: handoff=false, поэтому обычная run queue и никакого runnext» неверна. `handoff` влияет на выдачу токена и условный `goyield`, а не просто выбирает очередь. Даже `runnext` не является обещанием немедленного запуска API. Читать: [readyWithTime](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L127-L132), [goready](https://github.com/golang/go/blob/go1.27.3/src/runtime/proc.go#L483-L497), [ready](https://github.com/golang/go/blob/go1.27.3/src/runtime/proc.go#L1132-L1153), [handoff](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L207-L289).

**Пауза на 30 секунд:** показать место повторной проверки и попросить объяснить, какое чередование сломается при её удалении.

## 3. 15–35. Mutex: биты, конкуренция и starvation

### 15–18. Публичный тип и внутреннее состояние

В `sync/mutex.go` сначала найдите поле внутреннего mutex: публичный тип оборачивает `internal/sync.Mutex`. Затем перейдите к [внутренней структуре и константам](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L17-L60). У внутренней структуры два поля: `state int32` и `sema uint32`. Говорить «любой sync.Mutex всегда ровно 8 байт» как об API нельзя; в лекции нас интересует проверенная раскладка, а не гарантия размера на будущее.

```text
state, младшие биты:
0       mutexLocked
1       mutexWoken
2       mutexStarving
3..31   waiter count
```

Несколько логических фактов упакованы в одно слово, чтобы CAS мог проверить исходную комбинацию и заменить её целиком. Иначе между проверками отдельных полей могли бы вмешаться другие горутины. Это вывод из переходов в [lockSlow](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L95-L182); сама упаковка видна в [константах](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L17-L60).

Для ручной трассировки нормального режима: `0` — свободен; `1` — locked; `9 = 1 + 8` — locked и один учтённый waiter; `17 = 1 + 2*8` — locked и два waiter. Флаг woken не обозначает владельца: он координирует пробуждение, чтобы не будить лишних претендентов. В спине его тоже может выставить активная горутина. Читать [выставление mutexWoken](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L95-L182).

### 18–20. Fast path: что доказывает CAS

[Lock](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L58-L71) сначала пытается `CompareAndSwapInt32(&m.state, 0, mutexLocked)`. Успех означает, что горутина захватила полностью свободное состояние. Сбой означает только, что состояние не было нулём в момент CAS; дальше вызывается вынесенный `lockSlow`. Комментарий связывает разделение с возможностью инлайнинга быстрого пути.

Почему нельзя заменить проверку на `if m.state == 0 { m.state = 1 }`? Две горутины могут обе прочитать ноль до любой записи. Почему не CAS из любого состояния со снятым locked-битом? В starvation состоянии снятый locked-бит не означает доступность для нового претендента. Это прямо видно в [запрете захвата starving mutex](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L95-L182).

### 20–23. Спин зависит от планировщика

В [начале lockSlow](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L95-L182) спин разрешён только при locked без starving и положительном `runtime_canSpin(iter)`. Перейдите в [internal_sync_runtime_canSpin](https://github.com/golang/go/blob/go1.27.3/src/runtime/proc.go#L7985-L8002): важны лимит попыток, многопроцессорность, работа других P и пустота локальной run queue. При starvation новые претенденты не спинятся: владение должно идти очереди.

Почему проверяют локальную очередь? Из кода и комментария следует мотив: не тратить CPU на ожидание, когда на этом P есть другая готовая работа. Спин может быть полезен, если владелец параллельно завершает короткую секцию; это объяснение эвристики, а не обещание, что такая попытка быстрее парковки на любой нагрузке. Ссылки: [политика spin](https://github.com/golang/go/blob/go1.27.3/src/runtime/proc.go#L7985-L8002) и [doSpin](https://github.com/golang/go/blob/go1.27.3/src/runtime/proc.go#L8004-L8008).

Не переносим `PAUSE × N` в универсальное объяснение Go: конкретная машинная последовательность зависит от архитектуры и компиляции. В этой лекции достаточно увидеть вызов `procyield(active_spin_cnt)` и условия, при которых до него дойдут. Для amd64 нижний слой атомиков отдельно лежит в [internal/runtime/atomic/atomic_amd64.s](https://github.com/golang/go/blob/go1.27.3/src/internal/runtime/atomic/atomic_amd64.s#L1-L303).

### 23–27. Регистрация и повторная конкуренция

Читаем один проход [lockSlow](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L95-L182): загрузили `old`; вычислили `new`; если locked/starving, увеличили число waiter; согласовали флаги; CAS зафиксировал переход. После успешного CAS либо блокировка уже получена, либо вызывается `runtime_SemacquireMutex`. Не путайте логический waiter count в `state` и физическую очередь `sudog`: между их изменениями тоже исполняется код.

При первом ожидании `queueLifo=false`. Если уже ждали, снова проснулись и проиграли конкурентам, `queueLifo=true` ставит ожидающего ближе к началу очереди. В исходной сводной заметке было обратное утверждение о повторной постановке в хвост. Найдите `queueLifo := waitStartTime != 0` и комментарий перед ним в [lockSlow](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L95-L182).

В нормальном режиме [Unlock](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L184-L200) снимает locked-бит. Если осталось нетривиальное состояние, [unlockSlow](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L202-L234) проверяет необходимость пробуждения; CAS уменьшает waiter count и выставляет woken, затем release будит претендента. Пробуждённый ещё не владелец: новая уже исполняющаяся горутина может раньше захватить mutex. Это объясняет, как хороший throughput сочетается с большой задержкой у отдельного waiter.

**Упражнение, 2 минуты.** Начало `state=9`: владелец A и waiter B. A делает Unlock. Без других изменений `unlockSlow` переведёт `8` в `2`, то есть заберёт право на одно пробуждение. Затем новая C может установить locked-бит и войти раньше B. B проснётся и снова попадёт на медленный путь. Найдите в [unlockSlow](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L202-L234) и [lockSlow](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L95-L182) строки для каждого перехода. Не выдавайте эту конкретную последовательность за единственный возможный schedule.

### 27–32. Starvation: смена политики, не таймерная гарантия

В [комментарии о двух режимах](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L17-L60) порог задан `starvationThresholdNs = 1e6`. `lockSlow` после ожидания вычисляет, ждала ли горутина дольше этого порога, и при соответствующем занятом состоянии выставляет starvation. Это не таймер, который через миллисекунду гарантированно даёт CPU и lock. Измерение и переключение происходят при выполнении конкретных ветвей [lockSlow](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L95-L182).

Starvation меняет протокол: новые горутины не захватывают mutex и идут в очередь, а `unlockSlow` вызывает release с `handoff=true`. У разбуженного locked-бит ещё не выставлен, но starvation удерживает остальных от захвата; получатель корректирует `state`, убирая себя из waiter count и устанавливая locked. Отсюда важный ответ: **`locked == 0` не равнозначно «lock доступен»**. Опора: [ветка starvation](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L202-L234) и [коррекция state после handoff](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L95-L182).

В runtime handoff пытается закрепить semaphore-токен за конкретным `sudog` через `ticket`, вызывает ready и при допустимых условиях уступает исполнение через `goyield`. На этом уровне видно, почему слово «передача» означает больше, чем пробуждение произвольного конкурента. Читать [ticket, readyWithTime и goyield](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L207-L289).

Выход из starvation происходит, если получатель оказался последним ожидающим или его собственное ожидание было коротким. В комментариях отмечена неэффективность постоянного starvation и риск работы двух горутин в lock-step. Это обоснование компромисса авторами: нормальный режим повышает throughput, starvation сдерживает патологическую tail latency. Ссылки: [обоснование политики](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L17-L60) и [снятие mutexStarving](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L95-L182). Публичного обещания FIFO или ограниченного времени ожидания из этого не выводим.

### 32–35. Три проверки понимания

1. **Почему woken не равен locked?** Разбудить претендента и предоставить владение — разные события нормального режима; сверить [условия пробуждения](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L202-L234).
2. **Можно ли после `TryLock()==false` читать защищённое поле?** Нет: провал не создаёт синхронизации и не даёт права на доступ. Основание — [контракт TryLock](https://github.com/golang/go/blob/go1.27.3/src/sync/mutex.go#L1-L66).
3. **Можно ли Lock в одной G, Unlock в другой?** Да, если приложение корректно организовало передачу; mutex не привязан к горутине. `Unlock` незахваченного mutex — runtime fatal в этой реализации, не обычная восстановимая паника. Проверить [комментарий Unlock](https://github.com/golang/go/blob/go1.27.3/src/sync/mutex.go#L1-L66) и [fatal](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L202-L234).

**Мини-вывод для дальнейшего чтения:** очередь, woken, starvation и scheduler handoff решают разные задачи. Нельзя объяснять все четыре словом «семафор».

## 4. 35–45. RWMutex: закрыть вход новым читателям

### 35–38. Поля и смысл отрицательного счётчика

Открываем [RWMutex](https://github.com/golang/go/blob/go1.27.3/src/sync/rwmutex.go#L16-L61). `w` сериализует писателей, `readerSem` удерживает новых читателей, `writerSem` ждёт завершения прежних. `readerCount` и `readerWait` имеют разные обязанности. В [Lock](https://github.com/golang/go/blob/go1.27.3/src/sync/rwmutex.go#L141-L162) писатель сначала берёт `w`, затем вычитает `rwmutexMaxReaders = 1<<30` из `readerCount`.

После вычитания новые [RLock](https://github.com/golang/go/blob/go1.27.3/src/sync/rwmutex.go#L62-L80) делают `Add(1)`, видят отрицательный результат и ждут `readerSem`. Старые читатели продолжают и в [rUnlockSlow](https://github.com/golang/go/blob/go1.27.3/src/sync/rwmutex.go#L129-L139) уменьшают `readerWait`; последний будит писателя. Отрицательное значение означает объявленного писателя, который может ещё ждать старых читателей. Поэтому формулировка «писатель уже исполняется» слишком сильна.

### 38–42. Трассировка, включая неприятное окно

Обозначим `B = 1<<30`. Два активных читателя дают `readerCount=2`. W захватывает `w`, делает `Add(-B)` и запоминает `r=2`. Новый читатель C увеличивает счётчик до `-B+3` и паркуется. Два старых читателя уйдут; после писательского `Unlock` восстановленное значение будет учитывать ожидающего C, которого нужно выпустить. Проверяем каждую операцию по [Lock](https://github.com/golang/go/blob/go1.27.3/src/sync/rwmutex.go#L141-L162), [RUnlock](https://github.com/golang/go/blob/go1.27.3/src/sync/rwmutex.go#L110-L127), [Unlock](https://github.com/golang/go/blob/go1.27.3/src/sync/rwmutex.go#L195-L223).

Теперь остановите W **между** вычитанием B и `readerWait.Add(r)`. Если оба старых читателя уже сделали `RUnlock`, `readerWait` успеет стать `-2`. Прибавление `r=2` вернёт ноль: W не должен парковаться. Если ушёл один, получится положительный остаток, второй разбудит W. Отрицательный `readerWait` в этом промежутке — часть протокола, а не обязательно повреждение. Читать [проверку результата Add(r)](https://github.com/golang/go/blob/go1.27.3/src/sync/rwmutex.go#L141-L162) и [decrement уходящих читателей](https://github.com/golang/go/blob/go1.27.3/src/sync/rwmutex.go#L129-L139).

Почему нужна вторая переменная? `readerCount` после объявления писателя уже включает новых заблокированных читателей, а писателю нужно дождаться только прежней группы. `readerWait` учитывает именно её завершение. Это вывод из только что прочитанного протокола, не публичная гарантия раскладки.

### 42–45. Почему читатели не защищают друг друга

Два `RLock` допускают одновременную работу, значит запись под `RLock` не становится безопасной от того, что второй читатель тоже держит `RLock`. Комментарий о race detector в [rwmutex.go](https://github.com/golang/go/blob/go1.27.3/src/sync/rwmutex.go#L16-L61) прямо запрещает считать внутренний atomic Add синхронизацией читателей. Контракт ordering описывает переходы writer ↔ reader, а не взаимное исключение reader ↔ reader.

Рекурсивный RLock может зависнуть: A держит чтение; W закрывает вход; A пытается ещё раз взять чтение; W ждёт первого `RUnlock` A. Повышение RLock до Lock тоже не поддерживается. Оба ограничения явно документированы в [публичном контракте RWMutex](https://github.com/golang/go/blob/go1.27.3/src/sync/rwmutex.go#L16-L61).

Производительность — отдельный вопрос. В fast path каждого `RLock` есть общий atomic Add; отсюда возможна конкуренция за cache line. Это причина **измерить** `Mutex` против `RWMutex` при коротких чтениях, а не доказательство, что один всегда быстрее. Факт общей операции виден в [RLock](https://github.com/golang/go/blob/go1.27.3/src/sync/rwmutex.go#L62-L80); измерения доступны в исходных [rwmutex_test.go](https://github.com/golang/go/blob/go1.27.3/src/sync/rwmutex_test.go#L209-L229).

**Вопрос:** зачем `TryRLock` использует CAS-loop, а не просто Add? **Ответ:** нужно согласованно проверить отсутствие объявления писателя и увеличить счётчик; при провале нельзя оставить инкремент. Смотреть [TryRLock](https://github.com/golang/go/blob/go1.27.3/src/sync/rwmutex.go#L82-L108).

## 5. 45–55. WaitGroup: ноль как событие завершения поколения

### 45–48. От API к packed state

Начните с [WaitGroup](https://github.com/golang/go/blob/go1.27.3/src/sync/waitgroup.go#L14-L61) и [Go](https://github.com/golang/go/blob/go1.27.3/src/sync/waitgroup.go#L220-L260). WaitGroup считает зарегистрированные задачи, а не автоматически «все созданные горутины». `Go` регистрирует задачу перед запуском новой G. Если группа пуста, этот вызов должен предшествовать `Wait`; если непуста, работающая задача может добавить новую. Для ручного API остаётся правило `Add` до события, которое мы собираемся ждать: [контракт Add](https://github.com/golang/go/blob/go1.27.3/src/sync/waitgroup.go#L62-L144).

```go
var wg sync.WaitGroup
results := make([]int, 4)
for i := range results {
    wg.Go(func() {
        results[i] = i * i
    })
}
wg.Wait()
fmt.Println(results)
```

Почему пример корректен: переменная `i` в таком цикле имеет отдельное значение для итерации при современной семантике Go; задачи пишут в разные элементы уже выделенного среза; `Wait` упорядочивает чтение результатов после завершения. Здесь нет конкурентного `append` и изменения slice header. Синхронизация завершения описана у [Go](https://github.com/golang/go/blob/go1.27.3/src/sync/waitgroup.go#L220-L260); семантика цикла — в [спецификации range](https://go.dev/ref/spec#For_range).

В `state atomic.Uint64` старшие 32 бита — signed task counter, бит 31 младшей половины — `synctest` bubble flag, младшие 31 бит — waiter count. Формулу сверяйте с `v := int32(state >> 32)` и `w := uint32(state & 0x7fffffff)` в [Add](https://github.com/golang/go/blob/go1.27.3/src/sync/waitgroup.go#L62-L144). Старое «нижние 32 бита целиком waiters» для этой версии неверно.

### 48–51. Гонка Wait с последним Done

[Wait](https://github.com/golang/go/blob/go1.27.3/src/sync/waitgroup.go#L159-L218) сначала загружает всё состояние. Если counter равен нулю, возвращается; иначе CAS увеличивает waiter count. [Done](https://github.com/golang/go/blob/go1.27.3/src/sync/waitgroup.go#L146-L157) вызывает Add(-1); тот, кто переводит counter в ноль при наличии waiters, проверяет state, сбрасывает его и вызывает release нужное число раз. Один packed CAS связывает проверку «задачи ещё есть» с регистрацией ожидающего.

Разберите оба порядка. Если последний Done успел первым, CAS Wait на старом state провалится; повторный Load увидит ноль. Если CAS Wait успел первым, Done увидит увеличенное число waiters и выпустит semaphore-токен. Токен не теряется, даже если фактическая парковка ещё не началась: это механизм предыдущего раздела. Ссылки: [регистрация Wait](https://github.com/golang/go/blob/go1.27.3/src/sync/waitgroup.go#L159-L218) и [переход к нулю](https://github.com/golang/go/blob/go1.27.3/src/sync/waitgroup.go#L62-L144).

А вот такой порядок библиотека исправить не может:

```go
go func() {
    wg.Add(1) // ошибка протокола: Wait может уже вернуть управление
    defer wg.Done()
    work()
}()
wg.Wait()
```

Дождаться незарегистрированной работы невозможно. Диагностики misuse обнаруживают некоторые опасные чередования, но не гарантируют паники на каждом неверном использовании. Если `Wait` увидел ноль и уже вернулся, поздний `Add` может не вызвать никакого сообщения. Основание: [ранний return при v==0](https://github.com/golang/go/blob/go1.27.3/src/sync/waitgroup.go#L159-L218) и [условия panic](https://github.com/golang/go/blob/go1.27.3/src/sync/waitgroup.go#L62-L144).

### 51–53. Повторное использование и паники в Go

Повторное поколение разрешено только после возврата **всех** прежних Wait. После пробуждения Wait проверяет, что state не начал новое поколение; иначе паникует. Это не универсальный барьер с автоматической сменой поколения: [правило reuse](https://github.com/golang/go/blob/go1.27.3/src/sync/waitgroup.go#L62-L144) и [проверка isReset](https://github.com/golang/go/blob/go1.27.3/src/sync/waitgroup.go#L159-L218).

Особая деталь проверенной **Go 1.27.3**: `WaitGroup.Go` нельзя описывать как буквально `Add(1); go { defer Done(); f() }`. При необработанной панике f deferred-обработчик делает повторный panic **без Done**. Комментарий объясняет мотив: разблокированный Wait мог бы позволить основной горутине завершить процесс раньше полного сообщения о fatal panic. При нормальном возврате и `Goexit` Done выполняется. Это поведение реализации не меняет контракт **f must not panic**. Прочитайте [Go полностью, включая deferred-функцию](https://github.com/golang/go/blob/go1.27.3/src/sync/waitgroup.go#L220-L260).

### 53–55. Контрольная задача

Две задачи пишут в разные заранее созданные элементы `[]Result`, после Wait читаем весь срез. Это допустимо. Две задачи делают `append` в один и тот же общий срез — нет, Wait не защищает конкурентную запись slice header. В обоих случаях Wait завершает ожидание; только первый протокол обеспечивает независимые записи. Happens-before окончания не устраняет гонку, которая уже была между задачами. Опора: [контракт Done](https://github.com/golang/go/blob/go1.27.3/src/sync/waitgroup.go#L146-L157) и [определение data race](https://go.dev/ref/mem#model).

## 6. 55–63. Once: «один начал» не означает «все дождались»

### 55–58. Ломаем наивный CAS

В [Once.Do](https://github.com/golang/go/blob/go1.27.3/src/sync/once.go#L32-L71) авторы сами показывают неправильную идею: победитель ставит done через CAS, затем выполняет f. Горутина B, увидев done, немедленно вернётся, хотя A ещё инициализирует состояние. Значит, «ровно один исполнитель» не выполняет полного контракта Once — остальные должны дождаться завершения и получить видимость результата.

Правильная реализация: быстрый `done.Load`; при false — [doSlow](https://github.com/golang/go/blob/go1.27.3/src/sync/once.go#L73-L80), mutex, повторная проверка done, вызов f, отложенная публикация done. Повторная проверка обязательна: B могла пройти первый Load до завершения A, а mutex получить уже после него. Поле done стоит первым для компактности горячего пути на некоторых архитектурах; этот мотив прямо записан в [комментарии к структуре](https://github.com/golang/go/blob/go1.27.3/src/sync/once.go#L11-L31).

### 58–61. Порядок defer объясняет panic

В `doSlow` сначала зарегистрирован `defer m.Unlock()`, затем `defer done.Store(true)`. Defer выполняются в обратном порядке: сначала публикация завершения, затем Unlock. При panic раскрутка тоже выполнит Store. Поэтому будущий `Do` **не повторяет f после паники**. Это исправление противоречия в вашей заметке `sync.Once.md`; см. [doSlow](https://github.com/golang/go/blob/go1.27.3/src/sync/once.go#L73-L80) и [документированную семантику panic](https://github.com/golang/go/blob/go1.27.3/src/sync/once.go#L32-L71).

Рекурсивный `Do` на том же Once зависнет: внешний вызов удерживает mutex до окончания f, а внутренний пытается получить его. Это не рекурсивный lock и не механизм повторных попыток инициализации. Ограничение описано в [комментарии Do](https://github.com/golang/go/blob/go1.27.3/src/sync/once.go#L32-L71).

`OnceValue` и `OnceValues` добавляют результат и кэширование ошибки как результата, если f возвращает error; успешное завершение не означает, что error nil. В отличие от прямого `Once.Do`, фабрики при панике воспроизводят panic с тем же значением **на каждом вызове**. Посмотрите поля `valid`, `p`, `result` и проверки после `once.Do` в [OnceValue](https://github.com/golang/go/blob/go1.27.3/src/sync/oncefunc.go#L42-L74) и [OnceValues](https://github.com/golang/go/blob/go1.27.3/src/sync/oncefunc.go#L76-L109).

### 61–63. Мини-эксперимент

В примере Once из приложения C1 каждый вызов обёрнут в recover только для наблюдения. Ожидаем: для прямого Once функция запускается один раз, первый вызов паникует, второй нормально возвращается; для OnceValue функция также запускается один раз, оба обращения паникуют. Recover в демонстрации нужен, чтобы процесс дошёл до второй попытки. Сравниваем результат с [Store в defer](https://github.com/golang/go/blob/go1.27.3/src/sync/once.go#L73-L80) и [повторным panic](https://github.com/golang/go/blob/go1.27.3/src/sync/oncefunc.go#L42-L74).

**Вопрос:** возвращённый `*Config` из OnceValue можно безусловно мутировать конкурентно? **Ответ:** нет, Once синхронизирует инициализацию; последующие мутации требуют собственного протокола. Контракт ограничен завершением f: [Once](https://github.com/golang/go/blob/go1.27.3/src/sync/once.go#L11-L31).

## 7. 63–72. Cond: билет до Unlock, предикат после Lock

### 63–66. Пять шагов Wait

Откройте [Cond.Wait](https://github.com/golang/go/blob/go1.27.3/src/sync/cond.go#L52-L73): проверка копирования; получение билета; Unlock L; ожидание билета; Lock L. Потом [notifyListAdd](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L573-L582) и [notifyListWait](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L584-L626) в runtime. Порядок **взять билет → отпустить L** принципиален: сигнальщик не должен обновить защищённый предикат и отправить уведомление в промежутке до регистрации ожидания.

Это не означает, что Unlock и физическая парковка — одна машинная инструкция. Атомарность здесь — свойство протокола относительно уведомлений: если Signal случится после выдачи билета, но до парковки, notify уже пройдёт билет, и `notifyListWait` вернётся без сна. Это видно в [проверке less(t, l.notify)](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L584-L626).

### 66–69. Почему цикл нужен без spurious wakeup

В Go Wait не возвращается без Signal/Broadcast — это прямо гарантировано в [документации Wait](https://github.com/golang/go/blob/go1.27.3/src/sync/cond.go#L52-L73). Поэтому объяснение «for нужен из-за spurious wakeups как в pthreads» для Go неверно. Но уведомление не резервирует ресурс за разбуженным: пока тот заново захватывает L, другой потребитель мог уже забрать элемент.

```go
mu.Lock()
for len(queue) == 0 && !closed {
    cond.Wait()
}
if len(queue) == 0 { // closed && очередь пуста
    mu.Unlock()
    return
}
v := queue[0]
queue = queue[1:]
mu.Unlock()
consume(v)
```

Для этого примера добавление в очередь и смена `closed` тоже выполняются под mu. Завершение: под mu поставить closed и вызвать Broadcast, чтобы ожидающие смогли перепроверить предикат и выйти. Уведомление о закрытии не равно закрытому каналу: будущий Wait не получает постоянный «закрыт» сигнал, поэтому состояние `closed` хранит приложение. Связь locker и условия описана в [Cond](https://github.com/golang/go/blob/go1.27.3/src/sync/cond.go#L12-L46), отсутствие накопления видно в [NotifyOne](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L665-L722).

Signal допустим без удержания L; это не разрешение читать и менять предикат без блокировки. Надёжный учебный порядок: взять mu, изменить состояние, Signal/Broadcast, отпустить mu. Вариант «изменить под mu, Unlock, потом Signal» тоже допустим. Нельзя путать этот вариант с изменением состояния вообще без mu. Основание: [контракт Signal](https://github.com/golang/go/blob/go1.27.3/src/sync/cond.go#L75-L85) и [условия использования L](https://github.com/golang/go/blob/go1.27.3/src/sync/cond.go#L12-L46).

### 69–71. Tickets и отсутствие гарантии FIFO исполнения

`notifyList` использует номера билетов и очередь `sudog`. В [notifyListNotifyOne](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L665-L722) notify увеличивается, а runtime ищет соответствующий билет. Если обладатель билета ещё не припарковался, будущая проверка увидит уведомление. Очерёдность билетов не гарантирует порядок повторного захвата L или работы CPU: это отдельно оговорено в [Signal](https://github.com/golang/go/blob/go1.27.3/src/sync/cond.go#L75-L85).

[notifyListNotifyAll](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L628-L663) забирает связный список под внутренним lock, а делает waiters runnable после его освобождения. Это сокращает участок работы под внутренним lock; обход ожидающих всё равно требует O(N). Не путайте внутренний `notifyList.lock` и пользовательский L: если приложение держит L во время Broadcast, этот L остаётся у приложения до его Unlock.

### 71–72. noCopy и copyChecker

[copyChecker.check](https://github.com/golang/go/blob/go1.27.3/src/sync/cond.go#L101-L111) хранит собственный адрес и обнаруживает некоторые копии уже использованного Cond. Быстрый путь — сравнение, а не обязательный CAS. Если скопировать совершенно новый Cond до первого использования, его checker ещё нулевой: обещать немедленную панику в таком примере нельзя. После первого использования копирование запрещено независимо от того, сработала ли диагностика. Сверьте [трёхступенчатую проверку](https://github.com/golang/go/blob/go1.27.3/src/sync/cond.go#L101-L111) и [запрет копирования](https://github.com/golang/go/blob/go1.27.3/src/sync/cond.go#L12-L46).

**Вопрос:** Signal до появления любых waiter сохраняется для будущего Wait? **Ответ:** нет; при равенстве wait и notify NotifyOne возвращается. Источник: [быстрый return](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L665-L722).

## 8. 72–82. Map: hash-trie вместо read/dirty

### 72–74. Удаляем смешение поколений реализации

Откройте [sync.Map](https://github.com/golang/go/blob/go1.27.3/src/sync/map.go#L11-L43): внутри `isync.HashTrieMap[any, any]`. Затем [HashTrieMap](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/hashtriemap.go#L14-L29). Для этой версии нет устройства `read`, `dirty`, `misses` и продвижения dirty → read. Его нельзя использовать для объяснения стоимости актуального Store. В вашей заметке старое описание шло сразу после новой структуры; здесь историческую модель отделяем от текущей.

Публичный тип остаётся `any`/`any`, хотя внутренняя структура generic. Это не Swiss Table обычной Go map: поиск выполняется по фрагментам хеша через trie. В [nChildrenLog2](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/hashtriemap.go#L530-L540) значение 4 задаёт 16 ветвей; [Load](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/hashtriemap.go#L61-L83) извлекает следующий фрагмент хеша для перехода.

### 74–77. Load и публикация

[Load](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/hashtriemap.go#L61-L83) начинает с `ht.init()`. После инициализации он читает root и child pointers атомарно, находит entry либо nil. Но самый первый Load способен попасть в [initSlow](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/hashtriemap.go#L36-L56) и взять initMu. Поэтому «любой Load всегда без mutex, включая первый» — чрезмерное утверждение. Корректное описание: в установившемся пути поиска нет захвата mutex узлов.

В [LoadOrStore](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/hashtriemap.go#L85-L163) запись сначала оптимистично ищет точку вставки, затем захватывает mutex найденного indirect-узла и повторно проверяет slot и dead. Пока шёл поиск, другой писатель мог расширить или удалить узел. Повторная проверка защищает от применения решения к уже устаревшей структуре; при изменении поиск начинается заново.

При коллизии фрагментов хеша [expand](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/hashtriemap.go#L165-L196) строит поддерево. В LoadOrStore новый указатель публикуется **последним**, чтобы читатели не увидели исчезновение старой записи во время расширения. Если совпал весь хеш, используется overflow-цепочка. Это мотив, явно описанный комментарием перед `slot.Store(ht.expand(...))` в [LoadOrStore](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/hashtriemap.go#L85-L163).

### 77–80. Что конкурентная Map не делает за нас

`Load` + вычисление + `Store` — не атомарный increment. Два потока могут загрузить 0 и оба записать 1. В примере Map из приложения C1 каналы специально заставляют обе G прочитать до любой записи; итог будет 1 при отсутствии data race самой Map. Это логическая потеря обновления, не свидетельство неисправности concurrent map. Для одного ключа нужен CompareAndSwap-loop с корректной обработкой отсутствия ключа либо отдельный atomic-счётчик; для составного инварианта — общий lock. Границы операций описаны в [перечне read/write операций](https://github.com/golang/go/blob/go1.27.3/src/sync/map.go#L11-L43) и [CompareAndSwap](https://github.com/golang/go/blob/go1.27.3/src/sync/map.go#L86-L91).

`LoadOrStore(k, expensive())` не делает вычисление ленивым: аргумент вычисляется до вызова. Если нужно не более одной инициализации на ключ, можно хранить фабрику `OnceValue` и вызывать фактически выбранную фабрику после LoadOrStore; несколько фабрик могут быть созданы, но работа должна запускаться только через сохранённую. Это композиция двух контрактов, а не дополнительное обещание Map. Основание: [LoadOrStore](https://github.com/golang/go/blob/go1.27.3/src/sync/map.go#L61-L66), [OnceValue](https://github.com/golang/go/blob/go1.27.3/src/sync/oncefunc.go#L42-L74) и [порядок вычисления вызова](https://go.dev/ref/spec#Calls).

[Range](https://github.com/golang/go/blob/go1.27.3/src/sync/map.go#L102-L115) не является согласованным snapshot всей карты, может наблюдать значения из разных моментов и допускает callback с обращениями к самой Map. Даже если callback быстро вернул false, сложность вызова может быть O(N). Это публичная документация, не вывод о старых read/dirty структурах.

### 80–82. Почему «частые записи — всегда плохо» устарело

Документация [Map](https://github.com/golang/go/blob/go1.27.3/src/sync/map.go#L11-L43) описывает два оптимизированных паттерна: write-once/read-many и работа горутин с непересекающимися наборами ключей. Во втором случае записи вполне могут быть частыми. Внутренние locks у indirect-узлов дают причину ожидать меньше общей конкуренции на разнесённых ветвях, но распределение ключей и workload определяют фактическую стоимость. Сверьте [indirect.mu](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/hashtriemap.go#L540-L548). Это основание для бенчмарка, а не универсальный рейтинг коллекций.

**Упражнение:** показать в LoadOrStore место, где старый найденный узел перепроверяется после Lock, и объяснить, почему само слово concurrent не даёт транзакцию между двумя ключами. Ответы — [повторная проверка](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/hashtriemap.go#L85-L163) и [контракт отдельных операций](https://github.com/golang/go/blob/go1.27.3/src/sync/map.go#L11-L43).

## 9. 82–87. Pool: локальность P и право забыть объект

### 82–84. Путь Get

У Pool нет гарантии хранения: Get может считать пул пустым, а элементы могут быть удалены без уведомления. Публикация `Put(x)` упорядочена с Get, вернувшим **тот же x**, но не означает, что следующий Get обязан его вернуть. Начните с [контракта Pool](https://github.com/golang/go/blob/go1.27.3/src/sync/pool.go#L15-L67).

Трасса [Get](https://github.com/golang/go/blob/go1.27.3/src/sync/pool.go#L124-L159): pin → private своего P → popHead локальной shared → [getSlow](https://github.com/golang/go/blob/go1.27.3/src/sync/pool.go#L161-L198), который пробует shared других P и victim → unpin → New, если ничего не найдено. **New вызывается уже после unpin**. В вашей заметке текстовые пункты давали обратный порядок, хотя вставленный код был правильным.

Локальные структуры принадлежат P планировщика, а не обязательно каждому физическому CPU. Массив создаётся по `GOMAXPROCS`; [pin](https://github.com/golang/go/blob/go1.27.3/src/sync/pool.go#L200-L222) запрещает вытеснение на короткий участок. Привязка не является OS thread affinity и не обещает, что G всегда вернётся на прежний P. Реализация: [pinSlow](https://github.com/golang/go/blob/go1.27.3/src/sync/pool.go#L224-L246) и [procPin](https://github.com/golang/go/blob/go1.27.3/src/runtime/proc.go#L7924-L7942).

### 84–86. Почему нельзя назвать весь Pool lock-free

Private доступен только своему P во время pin; shared использует single-producer/multi-consumer очередь: владелец работает с head, конкуренты забирают tail. Конкретный `poolDequeue` описан как lock-free, но `pinSlow` использует `allPoolsMu`. Следовательно, переносить свойство одной очереди на весь Get/Put некорректно. Ссылки: [poolDequeue](https://github.com/golang/go/blob/go1.27.3/src/sync/poolqueue.go#L12-L64) и [allPoolsMu.Lock](https://github.com/golang/go/blob/go1.27.3/src/sync/pool.go#L224-L246).

Пара head/tail упакована в `atomic.Uint64`. Обратите внимание на владение слотом и атомарное очищение его type-части: потребитель ещё владеет слотом после продвижения индекса до завершения очистки. Это защищает переиспользование и уменьшает удержание объектов GC. Объяснение «индексы никогда не переполняются, поэтому ABA невозможна» неверно: индексы 32-битные, а ограничения на размер очереди специально учитывают wraparound. Читать [комментарии headTail, vals и dequeueLimit](https://github.com/golang/go/blob/go1.27.3/src/sync/poolqueue.go#L12-L64).

### 86–87. Очистка и практический выбор

[poolCleanup](https://github.com/golang/go/blob/go1.27.3/src/sync/pool.go#L248-L282) вызывается при остановленном мире в начале GC: старые victim очищаются, primary становятся victim. Вызов из GC прослеживается через [clearpools](https://github.com/golang/go/blob/go1.27.3/src/runtime/mgc.go#L2175-L2211). Из этой схемы нельзя получить контракт «Put гарантированно живёт хотя бы один GC»: API разрешает забыть его, а под `-race` [Put](https://github.com/golang/go/blob/go1.27.3/src/sync/pool.go#L99-L122) даже случайно отбрасывает часть значений.

Восстановление объекта после Get и ограничение максимального размера буфера — обязанности приложения. Из реализации следует риск удержания больших буферов в private/shared/victim, но конкретную экономию allocation и retained heap надо измерить. Пул не заменяет пул соединений с обязательным Close: у него отсутствует контракт сохранности. Источник границы применения — [документация Pool](https://github.com/golang/go/blob/go1.27.3/src/sync/pool.go#L15-L67).

## 10. 87–90. Проверка маршрута чтения

За последнюю минуту обсуждения каждой задачи слушатель должен назвать и инвариант, и исходную функцию.

1. **Почему release перед park не теряется?** В semaphore сохраняется токен; после регистрации waiter есть повторная проверка под root lock. В Cond сначала выдаётся ticket, затем отпускается L; notified ticket не паркуется. Источники: [semacquire1](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L146-L201) и [notifyListWait](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L584-L626).
2. **Почему CAS(done=false→true) недостаточно для Once?** Второй вызов может вернуться до завершения f; нужна сериализация незавершённой инициализации и публикация результата после f. Источник: [авторский контрпример](https://github.com/golang/go/blob/go1.27.3/src/sync/once.go#L32-L71).
3. **Почему «всё на атомиках» не доказывает корректность?** Атомарность отдельных операций не задаёт транзакцию над несколькими состояниями. На практическом уровне это потеря обновления у Map и гонка допуска в примере shutdown из исходной заметки. Контракт атомиков: [sequential consistency](https://github.com/golang/go/blob/go1.27.3/src/sync/atomic/doc.go#L1-L222).

**Критерий результата занятия:** слушатель может открыть незнакомый sync-примитив, найти контракт, состояние, точку перехода, регистрацию ожидания, пробуждение и объяснить хотя бы одно опасное чередование. Назвать число корзин semtable без этого недостаточно.

## Приложение A. Атомики глубже — самостоятельное чтение

### A1. Типизированные атомики и машинный слой

В [type.go](https://github.com/golang/go/blob/go1.27.3/src/sync/atomic/type.go#L1-L254) проследите `Bool` и `Int64`: методы ведут к пакетным операциям, `noCopy` помогает анализатору, `align64` обеспечивает выравнивание 64-битного типа. Для низкоуровневых функций на перечисленных 32-битных архитектурах обязанность выравнивания отдельно оговорена в [doc.go](https://github.com/golang/go/blob/go1.27.3/src/sync/atomic/doc.go#L1-L222). Не все операции обязаны в точности проходить по одной цепочке вызовов: компилятор может раскрыть их как intrinsics.

Для явного нижнего слоя найдите переходы в [sync/atomic/asm.s](https://github.com/golang/go/blob/go1.27.3/src/sync/atomic/asm.s#L1-L115), затем `Cas64` и `Xadd64` в [internal/runtime/atomic/atomic_amd64.s](https://github.com/golang/go/blob/go1.27.3/src/internal/runtime/atomic/atomic_amd64.s#L1-L303). Это чтение конкретной amd64-реализации. Утверждение из заметки «LOCK всегда блокирует шину» не выводится из Go-исходника и не нужно для объяснения контракта; детали когерентности конкретного CPU здесь не подменяем универсальным обещанием языка.

### A2. atomic.Value: первая публикация типа и значения

[Value.Store](https://github.com/golang/go/blob/go1.27.3/src/sync/atomic/value.go#L44-L83) поддерживает один конкретный тип после первой записи и запрещает nil. Первая запись использует sentinel `firstStoreInProgress`: претендент pin-ится, CAS занимает type-поле, затем публикует data и настоящий type, после чего unpin. Другие писатели при sentinel повторяют попытку; [Value.Load](https://github.com/golang/go/blob/go1.27.3/src/sync/atomic/value.go#L26-L40) при nil или sentinel может вернуть nil, не дожидаясь окончания первой записи.

Почему тип нельзя менять? После инициализации последующие записи обновляют data при фиксированном type; проверка совместимости сохраняет интерпретацию интерфейсного значения. Это объяснение проверок в [Store](https://github.com/golang/go/blob/go1.27.3/src/sync/atomic/value.go#L44-L83), а не разрешение приложению самостоятельно менять слова интерфейса через unsafe.

### A3. Исправляем shutdown из исходной заметки

```text
handler: Load(shuttingDown) == false
shutdown: Store(shuttingDown, true)
shutdown: Load(active) == 0 → решил, что всё завершилось
handler: active.Add(1) → запрос начался слишком поздно
```

Каждая операция атомарна, но общего перехода «проверить допуск и зарегистрировать активность» нет. Под `-race` такой код может молчать, потому что ошибка логическая. Решение: сериализовать закрытие допуска и регистрацию работы одним mutex или создать один согласованный CAS-протокол состояния. Требование завершения нужно сформулировать до выбора примитива. Основание ограничения: [атомарность отдельных операций](https://github.com/golang/go/blob/go1.27.3/src/sync/atomic/doc.go#L1-L222); аналогичная проблема регистрации есть у [WaitGroup.Add](https://github.com/golang/go/blob/go1.27.3/src/sync/waitgroup.go#L62-L144).

### A4. ABA

GC может сохранять адресуемый объект живым, но не запрещает логический переход A→B→A в структуре приложения. Один CAS сравнивает текущее значение, не всю историю. Версия помогает только тогда, когда pointer и version действительно проверяются/меняются согласованно; две отдельные atomic-переменные не создают автоматически двойной CAS. Сравните определение [CompareAndSwap](https://github.com/golang/go/blob/go1.27.3/src/sync/atomic/doc.go#L1-L222) с packed state у [WaitGroup](https://github.com/golang/go/blob/go1.27.3/src/sync/waitgroup.go#L14-L61) и [poolDequeue](https://github.com/golang/go/blob/go1.27.3/src/sync/poolqueue.go#L12-L64).

## Приложение B. errgroup — композиция примитивов, отдельная версия

Для воспроизводимости читаем [golang.org/x/sync v0.17.0, errgroup.go](https://github.com/golang/sync/blob/v0.17.0/errgroup/errgroup.go). Это фиксированная версия для дополнительной заметки, без утверждения, что она последняя на дату лекции.

В `Group` роли распределены явно: WaitGroup ждёт; Once выбирает единственную записывающую ошибку; `sem chan token` ограничивает допуск; cancel отменяет связанный контекст. В `Go` отправка токена происходит **до** `wg.Add(1)` и запуска G. Поэтому `len(sem)` включает зарезервированные слоты, а не обязано в каждый момент равняться числу уже исполняющихся G. Читать [Group.Go](https://github.com/golang/sync/blob/v0.17.0/errgroup/errgroup.go#L63-L102) и [done](https://github.com/golang/sync/blob/v0.17.0/errgroup/errgroup.go#L36-L41).

Первая ошибка — та, чей обработчик первым выиграл Once, а не обязательно «главная причина» в предметной области. После её записи вызывается cancel, но другие задачи остановятся лишь если сами соблюдают контекст. `Wait` дожидается всех зарегистрированных задач и отменяет derived context также при успехе. Основание: [Go](https://github.com/golang/sync/blob/v0.17.0/errgroup/errgroup.go#L63-L102), [Wait](https://github.com/golang/sync/blob/v0.17.0/errgroup/errgroup.go#L53-L61), [WithContext](https://github.com/golang/sync/blob/v0.17.0/errgroup/errgroup.go#L43-L51).

`SetLimit(0)` закрывает допуск новым задачам; отрицательное значение снимает лимит. Лимит нельзя менять при активных задачах: нельзя полагаться на диагностическую панику как на полную защиту от всех нарушений этого правила, особенно в ветке `n < 0`. `Go` при полном лимите блокирует вызывающего, `TryGo` возвращает false. Источники: [SetLimit](https://github.com/golang/sync/blob/v0.17.0/errgroup/errgroup.go#L134-L151) и [TryGo](https://github.com/golang/sync/blob/v0.17.0/errgroup/errgroup.go#L104-L132).

**Deadlock для домашнего разбора:** лимит 1; внешняя задача уже заняла слот и внутри вызывает `g.Go(child)`. Внутренний Go ждёт слот, внешний не возвращается и слот не освобождает. Это не исправляется наличием WaitGroup. Следует перестроить дерево работы или допуск. Доказательство следует из порядка [Go](https://github.com/golang/sync/blob/v0.17.0/errgroup/errgroup.go#L63-L102) и [done](https://github.com/golang/sync/blob/v0.17.0/errgroup/errgroup.go#L36-L41).

В этой версии необработанный panic задачи не переносится как error в Wait: комментарий в [Go](https://github.com/golang/sync/blob/v0.17.0/errgroup/errgroup.go#L63-L102) объясняет, почему откладывание паники до Wait скрывает исходный стек и может скрыть саму панику за deadlock. Не путайте это с особыми деталями stdlib [WaitGroup.Go go1.27.3](https://github.com/golang/go/blob/go1.27.3/src/sync/waitgroup.go#L220-L260).

## Приложение C. Практика после занятия и ответы

### C1. Три самостоятельных примера

Сохраните каждый пример в отдельный файл `main.go` и запускайте на Go 1.27.x командой `go run -race main.go`. Все три примера проверены на Go 1.27.3 с race detector; `go vet` прошёл. Устанавливать внешние зависимости не нужно. Ожидаемый вывод приведён в комментариях.

#### Once: паника и повторное обращение

```go
package main

import (
	"fmt"
	"sync"
)

func observe(label string, f func()) {
	defer func() {
		if p := recover(); p != nil {
			fmt.Println(label, "panic:", p)
		}
	}()
	f()
	fmt.Println(label, "returned")
}

func main() {
	var once sync.Once
	n := 0
	f := func() {
		n++
		panic("boom")
	}
	observe("Do #1", func() { once.Do(f) })
	observe("Do #2", func() { once.Do(f) })
	fmt.Println("Do executions:", n)

	n = 0
	value := sync.OnceValue(func() int {
		n++
		panic("boom")
	})
	observe("OnceValue #1", func() { _ = value() })
	observe("OnceValue #2", func() { _ = value() })
	fmt.Println("OnceValue executions:", n)
}

// Expected:
// Do #1 panic: boom
// Do #2 returned
// Do executions: 1
// OnceValue #1 panic: boom
// OnceValue #2 panic: boom
// OnceValue executions: 1
```

#### Map: потеря обновления без data race

```go
package main

import (
	"fmt"
	"sync"
)

func main() {
	var m sync.Map
	m.Store("counter", 0)
	var wg sync.WaitGroup
	ready := make(chan struct{}, 2)
	proceed := make(chan struct{})
	for range 2 {
		wg.Go(func() {
			old, _ := m.Load("counter")
			ready <- struct{}{}
			<-proceed
			m.Store("counter", old.(int)+1)
		})
	}
	<-ready
	<-ready
	close(proceed)
	wg.Wait()
	value, _ := m.Load("counter")
	fmt.Println(value)
}

// Expected: 1. Both loads finish before either store.
// There is a lost update, but no data race on the map.
```

#### WaitGroup: видимость результатов после Wait

```go
package main

import (
	"fmt"
	"sync"
)

func main() {
	var wg sync.WaitGroup
	results := make([]int, 4)
	for i := range results {
		wg.Go(func() {
			results[i] = i * i
		})
	}
	wg.Wait()
	fmt.Println(results)
}

// Expected: [0 1 4 9].
```

### C2. Профилирование вместо догадок

В своём проекте сравните Mutex/RWMutex при разных длительностях чтения и долях записи; Map — на общем горячем ключе и на разнесённых ключах; Pool — по аллокациям и удержанной памяти. Для начала возьмите [бенчмарки RWMutex](https://github.com/golang/go/blob/go1.27.3/src/sync/rwmutex_test.go#L209-L229), [бенчмарки Map](https://github.com/golang/go/blob/go1.27.3/src/sync/map_bench_test.go#L21-L39) и [бенчмарки Pool](https://github.com/golang/go/blob/go1.27.3/src/sync/pool_test.go#L284-L292) из той же версии. Эти программы позволяют увидеть метод измерения, но не предсказывают автоматически результат вашего сервиса.

```sh
go test -race ./...
go test -run '^$' -bench . -benchmem -cpu 1,2,4 -count 5 ./...
go test -run '^$' -bench . -mutexprofile mutex.out -blockprofile block.out ./...
go tool pprof mutex.out
```

Не используйте результаты с `-race` для выводов о скорости без инструмента: например, код Pool при включённом race detector намеренно отбрасывает часть объектов. Это видно в [Put](https://github.com/golang/go/blob/go1.27.3/src/sync/pool.go#L99-L122). Для объяснения профилей ожидания прочитайте [semaBlockProfile](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L146-L201) и [mutexevent](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L207-L289): они учитывают разные стороны блокировки.

### C3. Ответы на вопросы для самостоятельной проверки

- Отсутствие сообщения `-race` не доказывает корректность алгоритма: atomic-протокол допуска может быть логически неверен без data race. Пример — приложение A3; контракт [atomic](https://github.com/golang/go/blob/go1.27.3/src/sync/atomic/doc.go#L1-L222).
- Копирование использованного mutex копирует его состояние, а не «создаёт новый нулевой mutex». Новый объект получает другой адрес semaphore key и расходится с очередью старого. Именно поэтому запрет — про идентичность синхронизации. Опора: [поля Mutex](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L17-L60), [ключ по адресу](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L30-L68) и [запрет копирования](https://github.com/golang/go/blob/go1.27.3/src/sync/mutex.go#L1-L66).
- У `Cond` нет требуемого встроенного типа lock: `L` имеет интерфейс `Locker`. Но менять и проверять предикат надо под тем же протоколом блокировки. Это определено в [Cond.L](https://github.com/golang/go/blob/go1.27.3/src/sync/cond.go#L12-L46) и [Locker](https://github.com/golang/go/blob/go1.27.3/src/sync/mutex.go#L1-L66).
- `RWMutex` не обещает, что произвольный ожидающий писатель немедленно закроет вход: сначала он должен получить `w`. Точная граница объявления — вычитание B в [Lock](https://github.com/golang/go/blob/go1.27.3/src/sync/rwmutex.go#L141-L162).
- «Два семафора у WaitGroup» полезно лишь как образное сравнение: фактические поля — packed state и один runtime semaphore. Не ищите два независимых runtime-объекта в [WaitGroup](https://github.com/golang/go/blob/go1.27.3/src/sync/waitgroup.go#L14-L61).

## Приложение D. Карта исходников

Все ссылки ниже закреплены за `go1.27.3` и ведут на проверенные диапазоны строк. Версии и границы методов получены из распакованного официального архива, а не угаданы по master.

| Узел маршрута | Исходник |
|---|---|
| `mutex.public` | [src/sync/mutex.go:1](https://github.com/golang/go/blob/go1.27.3/src/sync/mutex.go#L1-L66) |
| `mutex.state` | [src/internal/sync/mutex.go:17](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L17-L60) |
| `mutex.lock` | [src/internal/sync/mutex.go:58](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L58-L71) |
| `mutex.slow` | [src/internal/sync/mutex.go:95](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L95-L182) |
| `mutex.unlock` | [src/internal/sync/mutex.go:184](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L184-L200) |
| `mutex.unslow` | [src/internal/sync/mutex.go:202](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L202-L234) |
| `sync.runtime` | [src/sync/runtime.go:1](https://github.com/golang/go/blob/go1.27.3/src/sync/runtime.go#L1-L59) |
| `sema.table` | [src/runtime/sema.go:30](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L30-L68) |
| `sema.acquire` | [src/runtime/sema.go:146](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L146-L201) |
| `sema.release` | [src/runtime/sema.go:207](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L207-L289) |
| `sema.can` | [src/runtime/sema.go:291](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L291-L301) |
| `sema.ready` | [src/runtime/sema.go:127](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L127-L132) |
| `sema.wg` | [src/runtime/sema.go:108](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L108-L115) |
| `notify.add` | [src/runtime/sema.go:573](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L573-L582) |
| `notify.wait` | [src/runtime/sema.go:584](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L584-L626) |
| `notify.one` | [src/runtime/sema.go:665](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L665-L722) |
| `notify.all` | [src/runtime/sema.go:628](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L628-L663) |
| `proc.park` | [src/runtime/proc.go:429](https://github.com/golang/go/blob/go1.27.3/src/runtime/proc.go#L429-L475) |
| `proc.parkm` | [src/runtime/proc.go:4267](https://github.com/golang/go/blob/go1.27.3/src/runtime/proc.go#L4267-L4320) |
| `proc.goready` | [src/runtime/proc.go:483](https://github.com/golang/go/blob/go1.27.3/src/runtime/proc.go#L483-L497) |
| `proc.ready` | [src/runtime/proc.go:1132](https://github.com/golang/go/blob/go1.27.3/src/runtime/proc.go#L1132-L1153) |
| `proc.pin` | [src/runtime/proc.go:7924](https://github.com/golang/go/blob/go1.27.3/src/runtime/proc.go#L7924-L7942) |
| `proc.spin` | [src/runtime/proc.go:7985](https://github.com/golang/go/blob/go1.27.3/src/runtime/proc.go#L7985-L8002) |
| `proc.dospin` | [src/runtime/proc.go:8004](https://github.com/golang/go/blob/go1.27.3/src/runtime/proc.go#L8004-L8008) |
| `chan.type` | [src/runtime/chan.go:34](https://github.com/golang/go/blob/go1.27.3/src/runtime/chan.go#L34-L56) |
| `rw.type` | [src/sync/rwmutex.go:16](https://github.com/golang/go/blob/go1.27.3/src/sync/rwmutex.go#L16-L61) |
| `rw.lock` | [src/sync/rwmutex.go:141](https://github.com/golang/go/blob/go1.27.3/src/sync/rwmutex.go#L141-L162) |
| `rw.rlock` | [src/sync/rwmutex.go:62](https://github.com/golang/go/blob/go1.27.3/src/sync/rwmutex.go#L62-L80) |
| `rw.runlock` | [src/sync/rwmutex.go:110](https://github.com/golang/go/blob/go1.27.3/src/sync/rwmutex.go#L110-L127) |
| `rw.rslow` | [src/sync/rwmutex.go:129](https://github.com/golang/go/blob/go1.27.3/src/sync/rwmutex.go#L129-L139) |
| `rw.unlock` | [src/sync/rwmutex.go:195](https://github.com/golang/go/blob/go1.27.3/src/sync/rwmutex.go#L195-L223) |
| `rw.tryr` | [src/sync/rwmutex.go:82](https://github.com/golang/go/blob/go1.27.3/src/sync/rwmutex.go#L82-L108) |
| `rw.bench` | [src/sync/rwmutex_test.go:209](https://github.com/golang/go/blob/go1.27.3/src/sync/rwmutex_test.go#L209-L229) |
| `wg.type` | [src/sync/waitgroup.go:14](https://github.com/golang/go/blob/go1.27.3/src/sync/waitgroup.go#L14-L61) |
| `wg.add` | [src/sync/waitgroup.go:62](https://github.com/golang/go/blob/go1.27.3/src/sync/waitgroup.go#L62-L144) |
| `wg.done` | [src/sync/waitgroup.go:146](https://github.com/golang/go/blob/go1.27.3/src/sync/waitgroup.go#L146-L157) |
| `wg.wait` | [src/sync/waitgroup.go:159](https://github.com/golang/go/blob/go1.27.3/src/sync/waitgroup.go#L159-L218) |
| `wg.go` | [src/sync/waitgroup.go:220](https://github.com/golang/go/blob/go1.27.3/src/sync/waitgroup.go#L220-L260) |
| `once.type` | [src/sync/once.go:11](https://github.com/golang/go/blob/go1.27.3/src/sync/once.go#L11-L31) |
| `once.do` | [src/sync/once.go:32](https://github.com/golang/go/blob/go1.27.3/src/sync/once.go#L32-L71) |
| `once.slow` | [src/sync/once.go:73](https://github.com/golang/go/blob/go1.27.3/src/sync/once.go#L73-L80) |
| `once.value` | [src/sync/oncefunc.go:42](https://github.com/golang/go/blob/go1.27.3/src/sync/oncefunc.go#L42-L74) |
| `once.values` | [src/sync/oncefunc.go:76](https://github.com/golang/go/blob/go1.27.3/src/sync/oncefunc.go#L76-L109) |
| `cond.type` | [src/sync/cond.go:12](https://github.com/golang/go/blob/go1.27.3/src/sync/cond.go#L12-L46) |
| `cond.wait` | [src/sync/cond.go:52](https://github.com/golang/go/blob/go1.27.3/src/sync/cond.go#L52-L73) |
| `cond.signal` | [src/sync/cond.go:75](https://github.com/golang/go/blob/go1.27.3/src/sync/cond.go#L75-L85) |
| `cond.check` | [src/sync/cond.go:101](https://github.com/golang/go/blob/go1.27.3/src/sync/cond.go#L101-L111) |
| `cond.nocopy` | [src/sync/cond.go:113](https://github.com/golang/go/blob/go1.27.3/src/sync/cond.go#L113-L124) |
| `map.type` | [src/sync/map.go:11](https://github.com/golang/go/blob/go1.27.3/src/sync/map.go#L11-L43) |
| `map.range` | [src/sync/map.go:102](https://github.com/golang/go/blob/go1.27.3/src/sync/map.go#L102-L115) |
| `map.cas` | [src/sync/map.go:86](https://github.com/golang/go/blob/go1.27.3/src/sync/map.go#L86-L91) |
| `map.los` | [src/sync/map.go:61](https://github.com/golang/go/blob/go1.27.3/src/sync/map.go#L61-L66) |
| `map.bench` | [src/sync/map_bench_test.go:21](https://github.com/golang/go/blob/go1.27.3/src/sync/map_bench_test.go#L21-L39) |
| `trie.type` | [src/internal/sync/hashtriemap.go:14](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/hashtriemap.go#L14-L29) |
| `trie.init` | [src/internal/sync/hashtriemap.go:36](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/hashtriemap.go#L36-L56) |
| `trie.load` | [src/internal/sync/hashtriemap.go:61](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/hashtriemap.go#L61-L83) |
| `trie.los` | [src/internal/sync/hashtriemap.go:85](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/hashtriemap.go#L85-L163) |
| `trie.expand` | [src/internal/sync/hashtriemap.go:165](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/hashtriemap.go#L165-L196) |
| `trie.const` | [src/internal/sync/hashtriemap.go:530](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/hashtriemap.go#L530-L540) |
| `trie.indirect` | [src/internal/sync/hashtriemap.go:540](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/hashtriemap.go#L540-L548) |
| `pool.type` | [src/sync/pool.go:15](https://github.com/golang/go/blob/go1.27.3/src/sync/pool.go#L15-L67) |
| `pool.get` | [src/sync/pool.go:124](https://github.com/golang/go/blob/go1.27.3/src/sync/pool.go#L124-L159) |
| `pool.slow` | [src/sync/pool.go:161](https://github.com/golang/go/blob/go1.27.3/src/sync/pool.go#L161-L198) |
| `pool.put` | [src/sync/pool.go:99](https://github.com/golang/go/blob/go1.27.3/src/sync/pool.go#L99-L122) |
| `pool.pin` | [src/sync/pool.go:200](https://github.com/golang/go/blob/go1.27.3/src/sync/pool.go#L200-L222) |
| `pool.pinslow` | [src/sync/pool.go:224](https://github.com/golang/go/blob/go1.27.3/src/sync/pool.go#L224-L246) |
| `pool.cleanup` | [src/sync/pool.go:248](https://github.com/golang/go/blob/go1.27.3/src/sync/pool.go#L248-L282) |
| `pool.queue` | [src/sync/poolqueue.go:12](https://github.com/golang/go/blob/go1.27.3/src/sync/poolqueue.go#L12-L64) |
| `pool.bench` | [src/sync/pool_test.go:284](https://github.com/golang/go/blob/go1.27.3/src/sync/pool_test.go#L284-L292) |
| `gc.clear` | [src/runtime/mgc.go:2175](https://github.com/golang/go/blob/go1.27.3/src/runtime/mgc.go#L2175-L2211) |
| `atomic.doc` | [src/sync/atomic/doc.go:1](https://github.com/golang/go/blob/go1.27.3/src/sync/atomic/doc.go#L1-L222) |
| `atomic.type` | [src/sync/atomic/type.go:1](https://github.com/golang/go/blob/go1.27.3/src/sync/atomic/type.go#L1-L254) |
| `atomic.pointer` | [src/sync/atomic/type.go:44](https://github.com/golang/go/blob/go1.27.3/src/sync/atomic/type.go#L44-L70) |
| `atomic.value` | [src/sync/atomic/value.go:44](https://github.com/golang/go/blob/go1.27.3/src/sync/atomic/value.go#L44-L83) |
| `atomic.vload` | [src/sync/atomic/value.go:26](https://github.com/golang/go/blob/go1.27.3/src/sync/atomic/value.go#L26-L40) |
| `atomic.asm` | [src/sync/atomic/asm.s:1](https://github.com/golang/go/blob/go1.27.3/src/sync/atomic/asm.s#L1-L115) |
| `atomic.amd64` | [src/internal/runtime/atomic/atomic_amd64.s:1](https://github.com/golang/go/blob/go1.27.3/src/internal/runtime/atomic/atomic_amd64.s#L1-L303) |
| `err.go` | [reference/errgroup-v0.17.0.go:63](https://github.com/golang/sync/blob/v0.17.0/errgroup/errgroup.go#L63-L102) |
| `err.done` | [reference/errgroup-v0.17.0.go:36](https://github.com/golang/sync/blob/v0.17.0/errgroup/errgroup.go#L36-L41) |
| `err.wait` | [reference/errgroup-v0.17.0.go:53](https://github.com/golang/sync/blob/v0.17.0/errgroup/errgroup.go#L53-L61) |
| `err.ctx` | [reference/errgroup-v0.17.0.go:43](https://github.com/golang/sync/blob/v0.17.0/errgroup/errgroup.go#L43-L51) |
| `err.limit` | [reference/errgroup-v0.17.0.go:134](https://github.com/golang/sync/blob/v0.17.0/errgroup/errgroup.go#L134-L151) |
| `err.try` | [reference/errgroup-v0.17.0.go:104](https://github.com/golang/sync/blob/v0.17.0/errgroup/errgroup.go#L104-L132) |

