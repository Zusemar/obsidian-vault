---
tags: [golang, sync, internals]
go: go1.27.3
verified: 2026-10-21
---

# sync.Map — проверено по Go 1.27.3

Обновлено по официальному архиву Go 1.27.3. Полный маршрут: [[Лекция — sync Go 1.27 — 90 минут]]. Внутреннее устройство относится к этому выпуску; контракт отмечен отдельно.

## Map: hash-trie вместо read/dirty

### Удаляем смешение поколений реализации

Откройте [sync.Map](https://github.com/golang/go/blob/go1.27.3/src/sync/map.go#L11-L43): внутри `isync.HashTrieMap[any, any]`. Затем [HashTrieMap](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/hashtriemap.go#L14-L29). Для этой версии нет устройства `read`, `dirty`, `misses` и продвижения dirty → read. Его нельзя использовать для объяснения стоимости актуального Store. В вашей заметке старое описание шло сразу после новой структуры; здесь историческую модель отделяем от текущей.

Публичный тип остаётся `any`/`any`, хотя внутренняя структура generic. Это не Swiss Table обычной Go map: поиск выполняется по фрагментам хеша через trie. В [nChildrenLog2](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/hashtriemap.go#L530-L540) значение 4 задаёт 16 ветвей; [Load](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/hashtriemap.go#L61-L83) извлекает следующий фрагмент хеша для перехода.

### Load и публикация

[Load](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/hashtriemap.go#L61-L83) начинает с `ht.init()`. После инициализации он читает root и child pointers атомарно, находит entry либо nil. Но самый первый Load способен попасть в [initSlow](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/hashtriemap.go#L36-L56) и взять initMu. Поэтому «любой Load всегда без mutex, включая первый» — чрезмерное утверждение. Корректное описание: в установившемся пути поиска нет захвата mutex узлов.

В [LoadOrStore](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/hashtriemap.go#L85-L163) запись сначала оптимистично ищет точку вставки, затем захватывает mutex найденного indirect-узла и повторно проверяет slot и dead. Пока шёл поиск, другой писатель мог расширить или удалить узел. Повторная проверка защищает от применения решения к уже устаревшей структуре; при изменении поиск начинается заново.

При коллизии фрагментов хеша [expand](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/hashtriemap.go#L165-L196) строит поддерево. В LoadOrStore новый указатель публикуется **последним**, чтобы читатели не увидели исчезновение старой записи во время расширения. Если совпал весь хеш, используется overflow-цепочка. Это мотив, явно описанный комментарием перед `slot.Store(ht.expand(...))` в [LoadOrStore](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/hashtriemap.go#L85-L163).

### Что конкурентная Map не делает за нас

`Load` + вычисление + `Store` — не атомарный increment. Два потока могут загрузить 0 и оба записать 1. В примере Map из приложения C1 отдельной лекции каналы специально заставляют обе G прочитать до любой записи; итог будет 1 при отсутствии data race самой Map. Это логическая потеря обновления, не свидетельство неисправности concurrent map. Для одного ключа нужен CompareAndSwap-loop с корректной обработкой отсутствия ключа либо отдельный atomic-счётчик; для составного инварианта — общий lock. Границы операций описаны в [перечне read/write операций](https://github.com/golang/go/blob/go1.27.3/src/sync/map.go#L11-L43) и [CompareAndSwap](https://github.com/golang/go/blob/go1.27.3/src/sync/map.go#L86-L91).

`LoadOrStore(k, expensive())` не делает вычисление ленивым: аргумент вычисляется до вызова. Если нужно не более одной инициализации на ключ, можно хранить фабрику `OnceValue` и вызывать фактически выбранную фабрику после LoadOrStore; несколько фабрик могут быть созданы, но работа должна запускаться только через сохранённую. Это композиция двух контрактов, а не дополнительное обещание Map. Основание: [LoadOrStore](https://github.com/golang/go/blob/go1.27.3/src/sync/map.go#L61-L66), [OnceValue](https://github.com/golang/go/blob/go1.27.3/src/sync/oncefunc.go#L42-L74) и [порядок вычисления вызова](https://go.dev/ref/spec#Calls).

[Range](https://github.com/golang/go/blob/go1.27.3/src/sync/map.go#L102-L115) не является согласованным snapshot всей карты, может наблюдать значения из разных моментов и допускает callback с обращениями к самой Map. Даже если callback быстро вернул false, сложность вызова может быть O(N). Это публичная документация, не вывод о старых read/dirty структурах.

### Почему «частые записи — всегда плохо» устарело

Документация [Map](https://github.com/golang/go/blob/go1.27.3/src/sync/map.go#L11-L43) описывает два оптимизированных паттерна: write-once/read-many и работа горутин с непересекающимися наборами ключей. Во втором случае записи вполне могут быть частыми. Внутренние locks у indirect-узлов дают причину ожидать меньше общей конкуренции на разнесённых ветвях, но распределение ключей и workload определяют фактическую стоимость. Сверьте [indirect.mu](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/hashtriemap.go#L540-L548). Это основание для бенчмарка, а не универсальный рейтинг коллекций.

**Упражнение:** показать в LoadOrStore место, где старый найденный узел перепроверяется после Lock, и объяснить, почему само слово concurrent не даёт транзакцию между двумя ключами. Ответы — [повторная проверка](https://github.com/golang/go/blob/go1.27.3/src/internal/sync/hashtriemap.go#L85-L163) и [контракт отдельных операций](https://github.com/golang/go/blob/go1.27.3/src/sync/map.go#L11-L43).


## Напоминание API

`Load`, `Store`, `Delete`, `Clear`, `LoadOrStore`, `LoadAndDelete`, `Swap`, `CompareAndSwap`, `CompareAndDelete`, `Range`. Публичный Map хранит any/any, не даёт транзакций нескольких ключей, не защищает мутации объектов за сохранёнными указателями и не даёт snapshot Range. Для CAS значения old должны быть сравнимы. Полный контракт и делегирование методов: [src/sync/map.go](https://github.com/golang/go/blob/go1.27.3/src/sync/map.go#L11-L43).

## Связанные заметки

[[sync Mutex]], [[sync RWMutex]], [[sync WaitGroup]], [[sync.Once]], [[sync.Cond]], [[sync.Pool]], [[sync atomic (атомики)]], [[sync errgroup]]
