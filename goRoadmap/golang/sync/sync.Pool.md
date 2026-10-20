---
tags: [golang, sync, internals]
go: go1.27.3
verified: 2026-10-21
---

# sync.Pool — проверено по Go 1.27.3

Обновлено по официальному архиву Go 1.27.3. Полный маршрут: [[Лекция — sync Go 1.27 — 90 минут]]. Внутреннее устройство относится к этому выпуску; контракт отмечен отдельно.

## Pool: локальность P и право забыть объект

### Путь Get

У Pool нет гарантии хранения: Get может считать пул пустым, а элементы могут быть удалены без уведомления. Публикация `Put(x)` упорядочена с Get, вернувшим **тот же x**, но не означает, что следующий Get обязан его вернуть. Начните с [контракта Pool](https://github.com/golang/go/blob/go1.27.3/src/sync/pool.go#L15-L67).

Трасса [Get](https://github.com/golang/go/blob/go1.27.3/src/sync/pool.go#L124-L159): pin → private своего P → popHead локальной shared → [getSlow](https://github.com/golang/go/blob/go1.27.3/src/sync/pool.go#L161-L198), который пробует shared других P и victim → unpin → New, если ничего не найдено. **New вызывается уже после unpin**. В вашей заметке текстовые пункты давали обратный порядок, хотя вставленный код был правильным.

Локальные структуры принадлежат P планировщика, а не обязательно каждому физическому CPU. Массив создаётся по `GOMAXPROCS`; [pin](https://github.com/golang/go/blob/go1.27.3/src/sync/pool.go#L200-L222) запрещает вытеснение на короткий участок. Привязка не является OS thread affinity и не обещает, что G всегда вернётся на прежний P. Реализация: [pinSlow](https://github.com/golang/go/blob/go1.27.3/src/sync/pool.go#L224-L246) и [procPin](https://github.com/golang/go/blob/go1.27.3/src/runtime/proc.go#L7924-L7942).

### Почему нельзя назвать весь Pool lock-free

Private доступен только своему P во время pin; shared использует single-producer/multi-consumer очередь: владелец работает с head, конкуренты забирают tail. Конкретный `poolDequeue` описан как lock-free, но `pinSlow` использует `allPoolsMu`. Следовательно, переносить свойство одной очереди на весь Get/Put некорректно. Ссылки: [poolDequeue](https://github.com/golang/go/blob/go1.27.3/src/sync/poolqueue.go#L12-L64) и [allPoolsMu.Lock](https://github.com/golang/go/blob/go1.27.3/src/sync/pool.go#L224-L246).

Пара head/tail упакована в `atomic.Uint64`. Обратите внимание на владение слотом и атомарное очищение его type-части: потребитель ещё владеет слотом после продвижения индекса до завершения очистки. Это защищает переиспользование и уменьшает удержание объектов GC. Объяснение «индексы никогда не переполняются, поэтому ABA невозможна» неверно: индексы 32-битные, а ограничения на размер очереди специально учитывают wraparound. Читать [комментарии headTail, vals и dequeueLimit](https://github.com/golang/go/blob/go1.27.3/src/sync/poolqueue.go#L12-L64).

### Очистка и практический выбор

[poolCleanup](https://github.com/golang/go/blob/go1.27.3/src/sync/pool.go#L248-L282) вызывается при остановленном мире в начале GC: старые victim очищаются, primary становятся victim. Вызов из GC прослеживается через [clearpools](https://github.com/golang/go/blob/go1.27.3/src/runtime/mgc.go#L2175-L2211). Из этой схемы нельзя получить контракт «Put гарантированно живёт хотя бы один GC»: API разрешает забыть его, а под `-race` [Put](https://github.com/golang/go/blob/go1.27.3/src/sync/pool.go#L99-L122) даже случайно отбрасывает часть значений.

Восстановление объекта после Get и ограничение максимального размера буфера — обязанности приложения. Из реализации следует риск удержания больших буферов в private/shared/victim, но конкретную экономию allocation и retained heap надо измерить. Пул не заменяет пул соединений с обязательным Close: у него отсутствует контракт сохранности. Источник границы применения — [документация Pool](https://github.com/golang/go/blob/go1.27.3/src/sync/pool.go#L15-L67).


## Связанные заметки

[[sync Mutex]], [[sync RWMutex]], [[sync WaitGroup]], [[sync.Once]], [[sync.Cond]], [[sync.Map]], [[sync atomic (атомики)]], [[sync errgroup]]
