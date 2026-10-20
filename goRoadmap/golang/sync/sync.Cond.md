---
tags: [golang, sync, internals]
go: go1.27.3
verified: 2026-10-21
---

# sync.Cond — проверено по Go 1.27.3

Обновлено по официальному архиву Go 1.27.3. Полный маршрут: [[Лекция — sync Go 1.27 — 90 минут]]. Внутреннее устройство относится к этому выпуску; контракт отмечен отдельно.

## Cond: билет до Unlock, предикат после Lock

### Пять шагов Wait

Откройте [Cond.Wait](https://github.com/golang/go/blob/go1.27.3/src/sync/cond.go#L52-L73): проверка копирования; получение билета; Unlock L; ожидание билета; Lock L. Потом [notifyListAdd](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L573-L582) и [notifyListWait](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L584-L626) в runtime. Порядок **взять билет → отпустить L** принципиален: сигнальщик не должен обновить защищённый предикат и отправить уведомление в промежутке до регистрации ожидания.

Это не означает, что Unlock и физическая парковка — одна машинная инструкция. Атомарность здесь — свойство протокола относительно уведомлений: если Signal случится после выдачи билета, но до парковки, notify уже пройдёт билет, и `notifyListWait` вернётся без сна. Это видно в [проверке less(t, l.notify)](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L584-L626).

### Почему цикл нужен без spurious wakeup

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

### Tickets и отсутствие гарантии FIFO исполнения

`notifyList` использует номера билетов и очередь `sudog`. В [notifyListNotifyOne](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L665-L722) notify увеличивается, а runtime ищет соответствующий билет. Если обладатель билета ещё не припарковался, будущая проверка увидит уведомление. Очерёдность билетов не гарантирует порядок повторного захвата L или работы CPU: это отдельно оговорено в [Signal](https://github.com/golang/go/blob/go1.27.3/src/sync/cond.go#L75-L85).

[notifyListNotifyAll](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L628-L663) забирает связный список под внутренним lock, а делает waiters runnable после его освобождения. Это сокращает участок работы под внутренним lock; обход ожидающих всё равно требует O(N). Не путайте внутренний `notifyList.lock` и пользовательский L: если приложение держит L во время Broadcast, этот L остаётся у приложения до его Unlock.

### noCopy и copyChecker

[copyChecker.check](https://github.com/golang/go/blob/go1.27.3/src/sync/cond.go#L101-L111) хранит собственный адрес и обнаруживает некоторые копии уже использованного Cond. Быстрый путь — сравнение, а не обязательный CAS. Если скопировать совершенно новый Cond до первого использования, его checker ещё нулевой: обещать немедленную панику в таком примере нельзя. После первого использования копирование запрещено независимо от того, сработала ли диагностика. Сверьте [трёхступенчатую проверку](https://github.com/golang/go/blob/go1.27.3/src/sync/cond.go#L101-L111) и [запрет копирования](https://github.com/golang/go/blob/go1.27.3/src/sync/cond.go#L12-L46).

**Вопрос:** Signal до появления любых waiter сохраняется для будущего Wait? **Ответ:** нет; при равенстве wait и notify NotifyOne возвращается. Источник: [быстрый return](https://github.com/golang/go/blob/go1.27.3/src/runtime/sema.go#L665-L722).


## Связанные заметки

[[sync Mutex]], [[sync RWMutex]], [[sync WaitGroup]], [[sync.Once]], [[sync.Map]], [[sync.Pool]], [[sync atomic (атомики)]], [[sync errgroup]]
