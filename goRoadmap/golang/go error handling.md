#golang #language #errors #собес #middle

Обработка ошибок в Go — через явные возвращаемые значения, а не исключения. Источник: `src/errors/`, `src/fmt/errors.go`, **Go blog: «Working with Errors in Go 1.13»**.

---

## Интерфейс error

```go
// Встроенный интерфейс
type error interface {
    Error() string
}

// nil означает «нет ошибки»
func divide(a, b float64) (float64, error) {
    if b == 0 {
        return 0, errors.New("division by zero")
    }
    return a / b, nil
}

result, err := divide(10, 0)
if err != nil {
    log.Fatal(err)
}
```

---

## Создание ошибок

```go
// errors.New — простая ошибка со строкой
var ErrNotFound = errors.New("not found")

// fmt.Errorf — с форматированием
err := fmt.Errorf("user %d: %s", id, "not found")

//Wraapping (Go 1.13+): сохраняет исходную ошибку в цепочку
err := fmt.Errorf("get user %d: %w", id, ErrNotFound)

// errors.Join (Go 1.20+): объединяет несколько ошибок
err := errors.Join(err1, err2, err3)
```

---

## Sentinel errors (именованные ошибки)

```go
// Экспортируемые переменные-ошибки для сравнения через errors.Is
var (
    ErrNotFound   = errors.New("not found")
    ErrPermission = errors.New("permission denied")
    ErrTimeout    = errors.New("timeout")
)

// Использование в пакете
func (r *UserRepo) FindByID(id int) (*User, error) {
    u, ok := r.users[id]
    if !ok {
        return nil, fmt.Errorf("FindByID %d: %w", id, ErrNotFound)
    }
    return u, nil
}

// Проверка на вызывающей стороне
err := repo.FindByID(42)
if errors.Is(err, ErrNotFound) {
    // обрабатываем "не найдено"
}
```

---

## Custom error types

```go
// Структура с дополнительным контекстом
type ValidationError struct {
    Field   string
    Message string
}

func (e *ValidationError) Error() string {
    return fmt.Sprintf("validation error: field %q: %s", e.Field, e.Message)
}

// Использование
func validate(u *User) error {
    if u.Email == "" {
        return &ValidationError{Field: "email", Message: "required"}
    }
    return nil
}

// Извлечение типа через errors.As
var ve *ValidationError
if errors.As(err, &ve) {
    fmt.Println("invalid field:", ve.Field)
}
```

---

## errors.Is — проверка цепочки

`errors.Is` обходит цепочку `Unwrap` и проверяет равенство:

```go
// errors.Is(err, target) = true если:
// 1. err == target
// 2. err.Unwrap() == target (рекурсивно)
// 3. err реализует Is(error) bool и возвращает true

base := errors.New("not found")
wrapped := fmt.Errorf("get user: %w", base)
wrapped2 := fmt.Errorf("service: %w", wrapped)

errors.Is(wrapped2, base)    // true  ← обходит всю цепочку
errors.Is(wrapped2, wrapped) // true
errors.Is(wrapped, base)     // true
```

Кастомный `Is` для семантического сравнения:
```go
type HTTPError struct{ Code int }
func (e *HTTPError) Error() string { return fmt.Sprintf("HTTP %d", e.Code) }
func (e *HTTPError) Is(target error) bool {
    t, ok := target.(*HTTPError)
    return ok && e.Code == t.Code
}

err := fmt.Errorf("request failed: %w", &HTTPError{Code: 404})
errors.Is(err, &HTTPError{Code: 404}) // true
```

---

## errors.As — извлечение типа из цепочки

`errors.As` обходит цепочку и ищет ошибку нужного типа:

```go
// errors.As(err, &target) устанавливает target и возвращает true если нашёл

var ve *ValidationError
if errors.As(err, &ve) {
    // ve содержит найденную ошибку нужного типа
    fmt.Println(ve.Field)
}

// Цепочка:
wrapped := fmt.Errorf("outer: %w", &ValidationError{Field: "email", Message: "invalid"})
errors.As(wrapped, &ve)  // true — находит в цепочке
```

---

## errors.Unwrap

```go
// Ошибка реализует Unwrap если обёртывает другую:
type WrappedError struct {
    msg string
    err error
}
func (e *WrappedError) Error() string { return e.msg + ": " + e.err.Error() }
func (e *WrappedError) Unwrap() error { return e.err }

// errors.Join реализует Unwrap() []error (множественное развёртывание):
joined := errors.Join(err1, err2)
// errors.Is / errors.As обходят оба
```

---

## Паттерны оборачивания ошибок

```go
// Правило: добавляй контекст на каждом уровне стека
func (s *UserService) GetUser(ctx context.Context, id int) (*User, error) {
    u, err := s.repo.FindByID(id)
    if err != nil {
        // %w = wrapping, %s = только строка (не wrap, не обходит errors.Is)
        return nil, fmt.Errorf("UserService.GetUser id=%d: %w", id, err)
    }
    return u, nil
}

// На вызывающей стороне:
u, err := svc.GetUser(ctx, 42)
if errors.Is(err, ErrNotFound) {
    // обработка независимо от глубины оборачивания
}
```

**Не оборачивай если:**
- Уже достаточно контекста в исходной ошибке
- Это приведёт к дублированию информации

---

## Множественные ошибки (Go 1.20+)

```go
// Собираем ошибки из параллельных операций
var errs []error
for _, item := range items {
    if err := process(item); err != nil {
        errs = append(errs, err)
    }
}
if err := errors.Join(errs...); err != nil {
    return err
}

// Или через errgroup:
g := errgroup.Group{}
g.Go(func() error { return validate(user) })
g.Go(func() error { return saveToCache(user) })
if err := g.Wait(); err != nil {
    // первая ошибка (остальные отменены через context)
}
```

---

## Антипаттерны

```go
// ПЛОХО: игнорировать ошибку
result, _ := json.Marshal(v)

// ПЛОХО: возвращать "true/false" вместо error
func (r *Repo) Exists(id int) bool { ... }

// ПЛОХО: panic вместо error для ожидаемых ситуаций
func getUser(id int) *User {
    u, err := repo.FindByID(id)
    if err != nil { panic(err) }  // нельзя — это ожидаемая ситуация
    return u
}

// ПЛОХО: fmt.Errorf без %w (теряет цепочку)
return fmt.Errorf("error: %s", err.Error())  // используй %w

// ПЛОХО: проверка строки ошибки
if err.Error() == "not found" { ... }  // используй errors.Is
```

---

## panic vs error

| | panic | error |
|---|---|---|
| Когда | баг в программе, невозможное состояние | ожидаемые сбои (нет данных, сеть упала) |
| Примеры | nil pointer deref, index out of range | db not found, timeout, permission |
| Обработка | recover() в defer | if err != nil |
| Propagation | unwinds stack | явный возврат |

```go
// panic допустим для:
// - инициализации (невалидный конфиг при старте)
// - внутренних инвариантов (должно быть невозможно)
func mustParseURL(s string) *url.URL {
    u, err := url.Parse(s)
    if err != nil { panic(err) }  // compile-time константа → OK
    return u
}
```

---

## Связанные темы

- [[go defer panic recover]] — recover() перехватывает panic; defer для cleanup при ошибках
- [[go Interfaces]] — error — это интерфейс; nil interface ловушка применима к error
- [[go context]] — context.DeadlineExceeded, context.Canceled — sentinel errors
- [[clean architecture]] — ошибки доменного уровня vs инфраструктурные ошибки
- [[gRPC]] — status.Errorf, codes.NotFound — gRPC ошибки маппятся на sentinel errors
