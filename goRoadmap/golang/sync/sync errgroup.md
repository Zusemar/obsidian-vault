---
tags: [golang, sync, internals]
go: go1.27.3
verified: 2026-10-21
---

# sync errgroup — проверено по Go 1.27.3

Обновлено по официальному архиву Go 1.27.3. Полный маршрут: [[Лекция — sync Go 1.27 — 90 минут]]. Внутреннее устройство относится к этому выпуску; контракт отмечен отдельно.

## Приложение B. errgroup — композиция примитивов, отдельная версия

Для воспроизводимости читаем [golang.org/x/sync v0.17.0, errgroup.go](https://github.com/golang/sync/blob/v0.17.0/errgroup/errgroup.go). Это фиксированная версия для дополнительной заметки, без утверждения, что она последняя на дату лекции.

В `Group` роли распределены явно: WaitGroup ждёт; Once выбирает единственную записывающую ошибку; `sem chan token` ограничивает допуск; cancel отменяет связанный контекст. В `Go` отправка токена происходит **до** `wg.Add(1)` и запуска G. Поэтому `len(sem)` включает зарезервированные слоты, а не обязано в каждый момент равняться числу уже исполняющихся G. Читать [Group.Go](https://github.com/golang/sync/blob/v0.17.0/errgroup/errgroup.go#L63-L102) и [done](https://github.com/golang/sync/blob/v0.17.0/errgroup/errgroup.go#L36-L41).

Первая ошибка — та, чей обработчик первым выиграл Once, а не обязательно «главная причина» в предметной области. После её записи вызывается cancel, но другие задачи остановятся лишь если сами соблюдают контекст. `Wait` дожидается всех зарегистрированных задач и отменяет derived context также при успехе. Основание: [Go](https://github.com/golang/sync/blob/v0.17.0/errgroup/errgroup.go#L63-L102), [Wait](https://github.com/golang/sync/blob/v0.17.0/errgroup/errgroup.go#L53-L61), [WithContext](https://github.com/golang/sync/blob/v0.17.0/errgroup/errgroup.go#L43-L51).

`SetLimit(0)` закрывает допуск новым задачам; отрицательное значение снимает лимит. Лимит нельзя менять при активных задачах: нельзя полагаться на диагностическую панику как на полную защиту от всех нарушений этого правила, особенно в ветке `n < 0`. `Go` при полном лимите блокирует вызывающего, `TryGo` возвращает false. Источники: [SetLimit](https://github.com/golang/sync/blob/v0.17.0/errgroup/errgroup.go#L134-L151) и [TryGo](https://github.com/golang/sync/blob/v0.17.0/errgroup/errgroup.go#L104-L132).

**Deadlock для домашнего разбора:** лимит 1; внешняя задача уже заняла слот и внутри вызывает `g.Go(child)`. Внутренний Go ждёт слот, внешний не возвращается и слот не освобождает. Это не исправляется наличием WaitGroup. Следует перестроить дерево работы или допуск. Доказательство следует из порядка [Go](https://github.com/golang/sync/blob/v0.17.0/errgroup/errgroup.go#L63-L102) и [done](https://github.com/golang/sync/blob/v0.17.0/errgroup/errgroup.go#L36-L41).

В этой версии необработанный panic задачи не переносится как error в Wait: комментарий в [Go](https://github.com/golang/sync/blob/v0.17.0/errgroup/errgroup.go#L63-L102) объясняет, почему откладывание паники до Wait скрывает исходный стек и может скрыть саму панику за deadlock. Не путайте это с особыми деталями stdlib [WaitGroup.Go go1.27.3](https://github.com/golang/go/blob/go1.27.3/src/sync/waitgroup.go#L220-L260).


## Связанные заметки

[[sync Mutex]], [[sync RWMutex]], [[sync WaitGroup]], [[sync.Once]], [[sync.Cond]], [[sync.Map]], [[sync.Pool]], [[sync atomic (атомики)]]
