---
tags: [golang, sync, internals]
go: go1.27.3
verified: 2026-10-21
---

# sync Mutex — проверено по Go 1.27.3

Обновлено по официальному архиву Go 1.27.3. Полный маршрут: [[Лекция — sync Go 1.27 — 90 минут]]. Внутреннее устройство относится к этому выпуску; контракт отмечен отдельно.

## Mutex: биты, конкуренция и starvation

### Публичный тип и внутреннее состояние

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

### Fast path: что доказывает CAS

[Lock](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L58-L71) сначала пытается `CompareAndSwapInt32(&m.state, 0, mutexLocked)`. Успех означает, что горутина захватила полностью свободное состояние. Сбой означает только, что состояние не было нулём в момент CAS; дальше вызывается вынесенный `lockSlow`. Комментарий связывает разделение с возможностью инлайнинга быстрого пути.

Почему нельзя заменить проверку на `if m.state == 0 { m.state = 1 }`? Две горутины могут обе прочитать ноль до любой записи. Почему не CAS из любого состояния со снятым locked-битом? В starvation состоянии снятый locked-бит не означает доступность для нового претендента. Это прямо видно в [запрете захвата starving mutex](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L95-L182).

### Спин зависит от планировщика

В [начале lockSlow](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L95-L182) спин разрешён только при locked без starving и положительном `runtime_canSpin(iter)`. Перейдите в [internal_sync_runtime_canSpin](https://github.com/golang/go/blob/go1.27.3/src/runtime/proc.go#L7985-L8002): важны лимит попыток, многопроцессорность, работа других P и пустота локальной run queue. При starvation новые претенденты не спинятся: владение должно идти очереди.

Почему проверяют локальную очередь? Из кода и комментария следует мотив: не тратить CPU на ожидание, когда на этом P есть другая готовая работа. Спин может быть полезен, если владелец параллельно завершает короткую секцию; это объяснение эвристики, а не обещание, что такая попытка быстрее парковки на любой нагрузке. Ссылки: [политика spin](https://github.com/golang/go/blob/go1.27.3/src/runtime/proc.go#L7985-L8002) и [doSpin](https://github.com/golang/go/blob/go1.27.3/src/runtime/proc.go#L8004-L8008).

Не переносим `PAUSE × N` в универсальное объяснение Go: конкретная машинная последовательность зависит от архитектуры и компиляции. В этой лекции достаточно увидеть вызов `procyield(active_spin_cnt)` и условия, при которых до него дойдут. Для amd64 нижний слой атомиков отдельно лежит в [internal/runtime/atomic/atomic_amd64.s](https://github.com/golang/go/blob/go1.27.3/src/internal/runtime/atomic/atomic_amd64.s#L1-L303).

### Регистрация и повторная конкуренция

Читаем один проход [lockSlow](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L95-L182): загрузили `old`; вычислили `new`; если locked/starving, увеличили число waiter; согласовали флаги; CAS зафиксировал переход. После успешного CAS либо блокировка уже получена, либо вызывается `runtime_SemacquireMutex`. Не путайте логический waiter count в `state` и физическую очередь `sudog`: между их изменениями тоже исполняется код.

При первом ожидании `queueLifo=false`. Если уже ждали, снова проснулись и проиграли конкурентам, `queueLifo=true` ставит ожидающего ближе к началу очереди. В исходной сводной заметке было обратное утверждение о повторной постановке в хвост. Найдите `queueLifo := waitStartTime != 0` и комментарий перед ним в [lockSlow](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L95-L182).

В нормальном режиме [Unlock](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L184-L200) снимает locked-бит. Если осталось нетривиальное состояние, [unlockSlow](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L202-L234) проверяет необходимость пробуждения; CAS уменьшает waiter count и выставляет woken, затем release будит претендента. Пробуждённый ещё не владелец: новая уже исполняющаяся горутина может раньше захватить mutex. Это объясняет, как хороший throughput сочетается с большой задержкой у отдельного waiter.

**Упражнение, 2 минуты.** Начало `state=9`: владелец A и waiter B. A делает Unlock. Без других изменений `unlockSlow` переведёт `8` в `2`, то есть заберёт право на одно пробуждение. Затем новая C может установить locked-бит и войти раньше B. B проснётся и снова попадёт на медленный путь. Найдите в [unlockSlow](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L202-L234) и [lockSlow](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L95-L182) строки для каждого перехода. Не выдавайте эту конкретную последовательность за единственный возможный schedule.

### Starvation: смена политики, не таймерная гарантия

В [комментарии о двух режимах](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L17-L60) порог задан `starvationThresholdNs = 1e6`. `lockSlow` после ожидания вычисляет, ждала ли горутина дольше этого порога, и при соответствующем занятом состоянии выставляет starvation. Это не таймер, который через миллисекунду гарантированно даёт CPU и lock. Измерение и переключение происходят при выполнении конкретных ветвей [lockSlow](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L95-L182).

Starvation меняет протокол: новые горутины не захватывают mutex и идут в очередь, а `unlockSlow` вызывает release с `handoff=true`. У разбуженного locked-бит ещё не выставлен, но starvation удерживает остальных от захвата; получатель корректирует `state`, убирая себя из waiter count и устанавливая locked. Отсюда важный ответ: **`locked == 0` не равнозначно «lock доступен»**. Опора: [ветка starvation](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L202-L234) и [коррекция state после handoff](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L95-L182).

В runtime handoff пытается закрепить semaphore-токен за конкретным `sudog` через `ticket`, вызывает ready и при допустимых условиях уступает исполнение через `goyield`. На этом уровне видно, почему слово «передача» означает больше, чем пробуждение произвольного конкурента. Читать [ticket, readyWithTime и goyield](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L207-L289).

Выход из starvation происходит, если получатель оказался последним ожидающим или его собственное ожидание было коротким. В комментариях отмечена неэффективность постоянного starvation и риск работы двух горутин в lock-step. Это обоснование компромисса авторами: нормальный режим повышает throughput, starvation сдерживает патологическую tail latency. Ссылки: [обоснование политики](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L17-L60) и [снятие mutexStarving](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L95-L182). Публичного обещания FIFO или ограниченного времени ожидания из этого не выводим.

### Три проверки понимания

1. **Почему woken не равен locked?** Разбудить претендента и предоставить владение — разные события нормального режима; сверить [условия пробуждения](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L202-L234).
2. **Можно ли после `TryLock()==false` читать защищённое поле?** Нет: провал не создаёт синхронизации и не даёт права на доступ. Основание — [контракт TryLock](https://github.com/golang/go/blob/go1.27.3/src/sync/mutex.go#L1-L66).
3. **Можно ли Lock в одной G, Unlock в другой?** Да, если приложение корректно организовало передачу; mutex не привязан к горутине. `Unlock` незахваченного mutex — runtime fatal в этой реализации, не обычная восстановимая паника. Проверить [комментарий Unlock](https://github.com/golang/go/blob/go1.27.3/src/sync/mutex.go#L1-L66) и [fatal](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/mutex.go#L202-L234).

**Мини-вывод для дальнейшего чтения:** очередь, woken, starvation и scheduler handoff решают разные задачи. Нельзя объяснять все четыре словом «семафор».


## Runtime semaphore: почему пробуждение не теряется

### Переходим через границу пакетов

Откройте [runtime API пакета sync](https://github.com/golang/go/blob/go1.27.3/src/sync/runtime.go#L1-L59), затем [связку sync.runtime_SemacquireWaitGroup](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L108-L115). Имена `runtime_Semacquire…` в `sync` связаны с реализацией через `go:linkname`; это не вызов системного семафора по имени. Mutex и WaitGroup используют механизм `semacquire1`/`semrelease1`, Cond — отдельный `notifyList` в том же runtime-файле.

`sema uint32` имеет две роли: его адрес идентифицирует ожидание, а значение участвует в протоколе доступных semaphore-токенов. Это не просто «неиспользуемое число ради адреса». В [semrelease1](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L207-L289) сначала увеличивается `*addr`, а [cansemacquire](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L291-L301) пробует уменьшить положительное значение с CAS.

Адрес хешируется в `semtable`; внутри `semaRoot` находятся lock, дерево `sudog` по адресам и счётчик ожидающих. В проверенной версии таблица содержит 251 корзину, но это не контракт и не число, которое должен знать пользователь `Mutex`. Читать: [semaRoot, semTable и rootFor](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L30-L68). Каналы тоже используют `sudog`, однако их очереди находятся в `hchan`, а не в `semtable`: [hchan.recvq и sendq](https://github.com/golang/go/blob/go1.27.3/src/runtime/chan.go#L34-L56).

### Главная трассировка: release между проверкой и сном

Откройте [semacquire1](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L146-L201) и [semrelease1](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L207-L289) рядом. У acquire есть быстрый `cansemacquire`, а затем медленный путь: взять root lock, увеличить `nwait`, ещё раз попытаться забрать токен, поставить `sudog` в очередь и вызвать `goparkunlock`. У release увеличение токена стоит **до** проверки `nwait`.

| Момент release | Почему acquire не уснёт навсегда |
|---|---|
| До первой попытки acquire | Первая попытка может забрать токен |
| После первой неудачи, до регистрации waiter | Повторная проверка под root lock увидит токен |
| После регистрации и повторной неудачи | Release видит `nwait`, берёт тот же lock и найдёт очередь |
| После парковки | Release извлекает waiter и делает его runnable |

Почему недостаточно один раз проверить токен и затем просто лечь спать? Между этими действиями release мог бы решить, что ждать некому. Порядок `nwait` и повторной проверки закрывает это окно. Это наш разбор конкретного кода; аналогичное объяснение оставлено авторами рядом с повторным `cansemacquire`. Ссылки: [регистрация waiter](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L146-L201) и [порядок Xadd и проверки nwait](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L207-L289).

### Runnable не означает running

`gopark` передаёт управление через runtime, а `park_m` переводит G в ожидание и запускает планирование. Поэтому ожидающая `sync.Mutex` горутина не обязана блокировать OS-поток на системном вызове; M может выполнять другую G. Это видно в [gopark](https://github.com/golang/go/blob/go1.27.3/src/runtime/proc.go#L429-L475), [park_m](https://github.com/golang/go/blob/go1.27.3/src/runtime/proc.go#L4267-L4320). Если работы нет, runtime может парковать уже поток, но это следующий уровень, а не обязательный syscall каждого `Lock`.

Обратный путь: `readyWithTime` → `goready` → `ready` → `runqput`. В этой версии `goready` передаёт `next=true` независимо от аргумента semaphore `handoff`. Значит, запись из старой заметки «WaitGroup: handoff=false, поэтому обычная run queue и никакого runnext» неверна. `handoff` влияет на выдачу токена и условный `goyield`, а не просто выбирает очередь. Даже `runnext` не является обещанием немедленного запуска API. Читать: [readyWithTime](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L127-L132), [goready](https://github.com/golang/go/blob/go1.27.3/src/runtime/proc.go#L483-L497), [ready](https://github.com/golang/go/blob/go1.27.3/src/runtime/proc.go#L1132-L1153), [handoff](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L207-L289).

**Пауза на 30 секунд:** показать место повторной проверки и попросить объяснить, какое чередование сломается при её удалении.


## Связанные заметки

[[sync RWMutex]], [[sync WaitGroup]], [[sync.Once]], [[sync.Cond]], [[sync.Map]], [[sync.Pool]], [[sync atomic (атомики)]], [[sync errgroup]]
