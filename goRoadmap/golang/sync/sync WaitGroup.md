---
tags: [golang, sync, internals]
go: go1.27.3
verified: 2026-10-21
---

# sync WaitGroup — проверено по Go 1.27.3

Обновлено по официальному архиву Go 1.27.3. Полный маршрут: [[Лекция — sync Go 1.27 — 90 минут]]. Внутреннее устройство относится к этому выпуску; контракт отмечен отдельно.

## WaitGroup: ноль как событие завершения поколения

### От API к packed state

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

### Гонка Wait с последним Done

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

### Повторное использование и паники в Go

Повторное поколение разрешено только после возврата **всех** прежних Wait. После пробуждения Wait проверяет, что state не начал новое поколение; иначе паникует. Это не универсальный барьер с автоматической сменой поколения: [правило reuse](https://github.com/golang/go/blob/go1.27.3/src/sync/waitgroup.go#L62-L144) и [проверка isReset](https://github.com/golang/go/blob/go1.27.3/src/sync/waitgroup.go#L159-L218).

Особая деталь проверенной **Go 1.27.3**: `WaitGroup.Go` нельзя описывать как буквально `Add(1); go { defer Done(); f() }`. При необработанной панике f deferred-обработчик делает повторный panic **без Done**. Комментарий объясняет мотив: разблокированный Wait мог бы позволить основной горутине завершить процесс раньше полного сообщения о fatal panic. При нормальном возврате и `Goexit` Done выполняется. Это поведение реализации не меняет контракт **f must not panic**. Прочитайте [Go полностью, включая deferred-функцию](https://github.com/golang/go/blob/go1.27.3/src/sync/waitgroup.go#L220-L260).

### Контрольная задача

Две задачи пишут в разные заранее созданные элементы `[]Result`, после Wait читаем весь срез. Это допустимо. Две задачи делают `append` в один и тот же общий срез — нет, Wait не защищает конкурентную запись slice header. В обоих случаях Wait завершает ожидание; только первый протокол обеспечивает независимые записи. Happens-before окончания не устраняет гонку, которая уже была между задачами. Опора: [контракт Done](https://github.com/golang/go/blob/go1.27.3/src/sync/waitgroup.go#L146-L157) и [определение data race](https://go.dev/ref/mem#model).


## Связанные заметки

[[sync Mutex]], [[sync RWMutex]], [[sync.Once]], [[sync.Cond]], [[sync.Map]], [[sync.Pool]], [[sync atomic (атомики)]], [[sync errgroup]]
