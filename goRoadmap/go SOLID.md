#golang #architecture #design #solid

SOLID — пять принципов объектно-ориентированного дизайна (Robert C. Martin, «Clean Architecture»). В Go реализуются через **интерфейсы, embedding и пакеты** вместо классов.

---

## S — Single Responsibility Principle

> Модуль должен иметь **одну причину для изменения** (одного акционера).

```go
// ПЛОХО: Order знает о БД, нотификациях и логике заказа
type Order struct{}
func (o *Order) Save() error       { /* SQL */ }
func (o *Order) Notify() error     { /* email */ }
func (o *Order) CalcTotal() Money  { /* бизнес-логика */ }

// ХОРОШО: разделяем ответственности
type Order struct { /* только данные доменной сущности */ }
func (o *Order) CalcTotal() Money  { /* только бизнес-логика */ }

type OrderRepository interface {
    Save(ctx context.Context, o *Order) error
}

type OrderNotifier interface {
    NotifyCreated(ctx context.Context, o *Order) error
}
```

В Go SRP реализуется через **маленькие пакеты с чётко очерченной зоной ответственности**.

---

## O — Open/Closed Principle

> Модуль должен быть **открыт для расширения, закрыт для модификации**.

```go
// ПЛОХО: добавление нового типа экспорта требует изменения функции
func Export(format string, data []Row) error {
    switch format {
    case "csv":  return exportCSV(data)
    case "json": return exportJSON(data)
    // добавляем "xlsx" → меняем эту функцию
    }
}

// ХОРОШО: новые форматы добавляются без изменения существующего кода
type Exporter interface {
    Export(ctx context.Context, data []Row) error
}

type CSVExporter  struct{}
type JSONExporter struct{}
type XLSXExporter struct{}  // новый формат — только новый тип

func RunExport(ctx context.Context, e Exporter, data []Row) error {
    return e.Export(ctx, data)
}
```

---

## L — Liskov Substitution Principle

> Подтип должен быть **подставляем** вместо базового типа без нарушения корректности.

В Go — контракт интерфейса должен соблюдаться полностью:

```go
type Reader interface {
    Read(p []byte) (n int, err error)
}

// ПЛОХО: LimitedReader нарушает контракт — паникует вместо возврата ошибки
type BrokenReader struct{}
func (r *BrokenReader) Read(p []byte) (int, error) {
    panic("not implemented")  // нарушает LSP: клиенты io.Reader не ожидают паники
}

// ХОРОШО: любая реализация Reader должна:
// - возвращать (0, io.EOF) при конце данных
// - не паниковать на пустом p
// - быть консистентной между вызовами
```

LSP в Go нарушается, когда реализация интерфейса:
- Паникует там, где клиент ожидает ошибку
- Игнорирует входные данные, которые должна обрабатывать
- Меняет побочные эффекты, на которые полагается клиент

---

## I — Interface Segregation Principle

> Клиент не должен зависеть от методов, которые он не использует.

В Go это достигается **естественно** — интерфейсы определяются на стороне потребителя, а не поставщика:

```go
// ПЛОХО: fat interface, большинство реализаций заглушат половину методов
type Storage interface {
    Get(key string) ([]byte, error)
    Set(key string, val []byte) error
    Delete(key string) error
    List(prefix string) ([]string, error)
    Stats() StorageStats
    Backup(dst io.Writer) error
    Restore(src io.Reader) error
}

// ХОРОШО: маленькие интерфейсы, каждый потребитель берёт только нужное
type Getter interface {
    Get(key string) ([]byte, error)
}

type Setter interface {
    Set(key string, val []byte) error
}

type ReadWriter interface {
    Getter
    Setter
}

// Функция кэша нуждается только в Get
func withCache(next Getter) Getter { ... }

// Функция репликации нуждается только в ReadWriter
func replicate(src Getter, dst Setter) error { ... }
```

Идиома Go: `io.Reader`, `io.Writer`, `io.Closer` — маленькие интерфейсы, которые комбинируются (`io.ReadWriteCloser`).

---

## D — Dependency Inversion Principle

> Модули высокого уровня не должны зависеть от модулей низкого уровня. Оба должны зависеть от **абстракций**.

```go
// ПЛОХО: usecase зависит от конкретной реализации PostgreSQL
package usecase

import "myapp/postgres"  // зависимость от конкретики

type OrderService struct {
    db *postgres.DB  // конкретный тип
}
```

```go
// ХОРОШО: usecase определяет интерфейс, инфраструктура его реализует
package usecase

// Интерфейс определяется здесь — рядом с потребителем
type OrderRepository interface {
    FindByID(ctx context.Context, id OrderID) (*Order, error)
    Save(ctx context.Context, o *Order) error
}

type OrderService struct {
    repo OrderRepository  // зависимость от абстракции
}

// ---
package postgres  // пакет низкого уровня реализует интерфейс из usecase

type OrderRepo struct { db *sql.DB }

func (r *OrderRepo) FindByID(ctx context.Context, id usecase.OrderID) (*usecase.Order, error) {
    // SQL запрос
}
```

```
Граф зависимостей:
  usecase.OrderService → usecase.OrderRepository (абстракция)
  postgres.OrderRepo   → usecase.OrderRepository (реализует)

  Стрелки зависимостей направлены к центру (usecase), а не от него.
```

### Dependency Injection в Go

```go
// Сборка в main (или wire/dig)
func main() {
    db, _ := sql.Open("postgres", dsn)
    repo := &postgres.OrderRepo{DB: db}         // инфраструктура
    notifier := &smtp.Notifier{...}              // инфраструктура
    svc := usecase.NewOrderService(repo, notifier)  // бизнес-логика
    handler := http.NewOrderHandler(svc)         // transport
}
```

---

## Сводка для Go

| Принцип | Инструмент Go |
|---|---|
| SRP | маленькие пакеты, функции с одной ответственностью |
| OCP | интерфейсы + новые типы вместо switch |
| LSP | соблюдение контракта интерфейса |
| ISP | интерфейсы на стороне потребителя, минимально необходимые |
| DIP | интерфейсы в пакете usecase, реализации в инфраструктурном пакете |

---

## Связанные темы

- [[go Interfaces]] — основной инструмент реализации SOLID в Go
- [[clean architecture]] — структура проекта, воплощающая DIP и SRP на уровне слоёв
- [[interfaces (архитектура)]] — Ports & Adapters как развитие DIP
- [[design patterns]] — конкретные паттерны реализации OCP (Strategy, Decorator)
- [[go context]] — DIP в действии: `context.Context` — интерфейс, не конкретный тип
