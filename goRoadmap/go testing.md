#golang #testing #middle #собес

Тестирование в Go — встроено в язык через пакет `testing`. Источник: `src/testing/`, **Go blog: «Testable Examples in Go»**, **«Table Driven Tests»**.

---

## Базовая структура теста

```go
// Файл: order_test.go (суффикс _test.go обязателен)
package order_test  // или package order (white-box тест)

import (
    "testing"
    "github.com/example/order"
)

func TestCalcTotal(t *testing.T) {
    o := order.New()
    o.AddItem(order.Item{Price: 100, Qty: 2})
    o.AddItem(order.Item{Price: 50,  Qty: 1})

    got := o.CalcTotal()
    want := 250

    if got != want {
        t.Errorf("CalcTotal() = %d, want %d", got, want)
    }
}
```

---

## Table-Driven Tests (идиома Go)

```go
func TestDivide(t *testing.T) {
    tests := []struct {
        name    string
        a, b    float64
        want    float64
        wantErr bool
    }{
        {"positive", 10, 2, 5, false},
        {"negative divisor", -10, 2, -5, false},
        {"division by zero", 10, 0, 0, true},
        {"float", 7, 2, 3.5, false},
    }

    for _, tc := range tests {
        t.Run(tc.name, func(t *testing.T) {
            got, err := Divide(tc.a, tc.b)

            if (err != nil) != tc.wantErr {
                t.Errorf("Divide(%v, %v) error = %v, wantErr %v", tc.a, tc.b, err, tc.wantErr)
                return
            }
            if !tc.wantErr && got != tc.want {
                t.Errorf("Divide(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
            }
        })
    }
}
```

`t.Run` создаёт подтест — можно запустить один: `go test -run TestDivide/division_by_zero`.

---

## Ошибки теста: t.Error vs t.Fatal

```go
t.Error("msg")    // отмечает тест как failed, продолжает выполнение
t.Errorf("msg %v", v)

t.Fatal("msg")    // отмечает failed + немедленно завершает текущую горутину теста
t.Fatalf("msg %v", v)

t.Log("debug")    // выводится только при go test -v
t.Helper()        // помечает как helper — в трейсбэке ошибки будет строка вызывающего
```

---

## Полезные флаги go test

```bash
go test ./...                    # все тесты в проекте
go test -v ./...                 # verbose: все t.Log выводятся
go test -run TestFoo             # только тесты matching regexp
go test -run TestFoo/subtest     # конкретный подтест
go test -count=3                 # запустить каждый тест 3 раза
go test -timeout 30s             # таймаут (default 10m)
go test -race ./...              # race detector (ОБЯЗАТЕЛЬНО в CI)
go test -cover ./...             # coverage %
go test -coverprofile=cov.out && go tool cover -html=cov.out  # html report
go test -short                   # пропустить долгие тесты (t.Short())
```

---

## Benchmarks

```go
func BenchmarkSort(b *testing.B) {
    data := generateData(1000)

    b.ResetTimer()  // не учитывать время генерации данных
    for b.N {       // b.N автоматически подбирается
        slices.Sort(slices.Clone(data))
    }
}

// С аллокациями:
func BenchmarkJSON(b *testing.B) {
    b.ReportAllocs()  // или go test -benchmem
    for b.N {
        json.Marshal(struct{ Name string }{"Alice"})
    }
}
```

```bash
go test -bench=. -benchmem ./...
# BenchmarkSort-8    500000    2345 ns/op    0 B/op    0 allocs/op
# BenchmarkJSON-8    200000    7890 ns/op  128 B/op    2 allocs/op
```

```bash
# Сравнение двух версий:
go test -bench=. -count=5 . > old.txt  # ветка v1
go test -bench=. -count=5 . > new.txt  # ветка v2
benchstat old.txt new.txt              # golang.org/x/perf/benchstat
```

---

## TestMain — setup/teardown для всего пакета

```go
func TestMain(m *testing.M) {
    // setup
    db := setupTestDB()

    code := m.Run()  // запускает все тесты пакета

    // teardown
    db.Close()

    os.Exit(code)
}
```

---

## t.Cleanup — cleanup в тесте (Go 1.14+)

```go
func TestWithDB(t *testing.T) {
    db := openTestDB(t)
    t.Cleanup(func() { db.Close() })  // вызовется при завершении теста (в т.ч. при t.Fatal)

    // тест...
}

// Паттерн: helper создаёт ресурс + регистрирует cleanup
func openTestDB(t *testing.T) *sql.DB {
    t.Helper()
    db, err := sql.Open("sqlite", ":memory:")
    require.NoError(t, err)
    t.Cleanup(func() { db.Close() })
    return db
}
```

---

## t.Parallel — параллельные тесты

```go
func TestFoo(t *testing.T) {
    t.Parallel()  // этот тест может идти параллельно с другими Parallel тестами
    // ...
}

// Внутри table-driven:
for _, tc := range tests {
    tc := tc  // Go < 1.22: захват переменной!
    t.Run(tc.name, func(t *testing.T) {
        t.Parallel()
        // ...
    })
}
```

---

## Testify (популярная библиотека)

```go
import (
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    "github.com/stretchr/testify/mock"
)

func TestUser(t *testing.T) {
    u, err := NewUser("Alice", "alice@example.com")

    require.NoError(t, err)      // Fatal при ошибке
    assert.Equal(t, "Alice", u.Name)
    assert.NotNil(t, u)
    assert.True(t, u.IsActive())
    assert.ErrorIs(t, someErr, ErrNotFound)
    assert.ElementsMatch(t, []int{1,2,3}, []int{3,1,2})  // без учёта порядка
}
```

**require vs assert:** `require` = t.Fatal (стоп при провале), `assert` = t.Error (продолжить).

---

## Mocks с testify/mock

```go
// 1. Описываем mock
type MockOrderRepo struct{ mock.Mock }

func (m *MockOrderRepo) FindByID(ctx context.Context, id int) (*Order, error) {
    args := m.Called(ctx, id)
    return args.Get(0).(*Order), args.Error(1)
}

// 2. Используем в тесте
func TestGetOrder(t *testing.T) {
    repo := &MockOrderRepo{}
    repo.On("FindByID", mock.Anything, 42).Return(&Order{ID: 42}, nil)
    repo.On("FindByID", mock.Anything, 99).Return((*Order)(nil), ErrNotFound)

    svc := NewOrderService(repo)

    order, err := svc.GetOrder(ctx, 42)
    require.NoError(t, err)
    assert.Equal(t, 42, order.ID)

    _, err = svc.GetOrder(ctx, 99)
    assert.ErrorIs(t, err, ErrNotFound)

    repo.AssertExpectations(t)  // все ожидаемые вызовы случились
}
```

---

## httptest — тестирование HTTP хендлеров

```go
func TestOrderHandler(t *testing.T) {
    repo := &MockOrderRepo{}
    repo.On("FindByID", mock.Anything, 42).Return(&Order{ID: 42}, nil)

    handler := NewOrderHandler(NewOrderService(repo))

    req := httptest.NewRequest("GET", "/orders/42", nil)
    w   := httptest.NewRecorder()
    handler.ServeHTTP(w, req)

    resp := w.Result()
    assert.Equal(t, http.StatusOK, resp.StatusCode)

    var body Order
    json.NewDecoder(resp.Body).Decode(&body)
    assert.Equal(t, 42, body.ID)
}
```

---

## Связанные темы

- [[go error handling]] — `require.NoError`, `assert.ErrorIs` для проверки ошибок
- [[go generics]] — generic тест-хелперы: `assert.Equal[T]` внутри
- [[go concurrency patterns]] — тестирование конкурентного кода: `-race`, `goleak`
- [[go defer panic recover]] — `t.Cleanup` реализован через defer; `recover` в тестах
- [[clean architecture]] — моки реализуют интерфейсы репозиториев; тестируем use cases изолированно
