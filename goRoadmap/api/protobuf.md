#api #protobuf #serialization #encoding

Protocol Buffers (protobuf) — бинарный формат сериализации от Google. Источник: **proto3 Language Specification** и **Encoding Guide** (protobuf.dev).

---

## .proto файл (proto3)

```protobuf
syntax = "proto3";

package order.v1;
option go_package = "github.com/example/order/v1;orderv1";

import "google/protobuf/timestamp.proto";

message Order {
    string  order_id   = 1;
    int64   user_id    = 2;
    Status  status     = 3;
    repeated Item items = 4;
    google.protobuf.Timestamp created_at = 5;
    oneof payment {
        CardPayment  card  = 6;
        CashPayment  cash  = 7;
    }
}

message Item {
    string product_id = 1;
    int32  quantity   = 2;
    int64  price_rub  = 3;  // в копейках
}

enum Status {
    STATUS_UNSPECIFIED = 0;  // нулевое значение обязательно в proto3
    STATUS_PENDING     = 1;
    STATUS_PAID        = 2;
    STATUS_CANCELLED   = 3;
}
```

---

## Wire Types (типы кодирования)

| Wire Type | ID | Типы | Encoding |
|---|---|---|---|
| Varint | 0 | int32, int64, uint32, uint64, sint32, sint64, bool, enum | переменная длина |
| 64-bit | 1 | fixed64, sfixed64, double | 8 байт little-endian |
| Length-delimited | 2 | string, bytes, embedded messages, repeated | длина + данные |
| Start group | 3 | (устарело) | — |
| End group | 4 | (устарело) | — |
| 32-bit | 5 | fixed32, sfixed32, float | 4 байта little-endian |

Каждое поле кодируется как `(field_number << 3) | wire_type`.

---

## Varint кодирование

Переменная длина: маленькие числа занимают меньше байт.

```
Алгоритм: берём 7 бит числа, если есть ещё биты — MSB=1, иначе MSB=0

Число 1:
  0000 0001 → 0x01  (1 байт)

Число 300 (= 0b100101100):
  300 = 0b1_0010_1100
  Первые 7 бит: 010 1100 → с MSB=1: 1010 1100 = 0xAC
  Остаток: 0000 010  → с MSB=0: 0000 0010 = 0x02
  Итого: 0xAC 0x02  (2 байта)

Число 2^28 (268 млн):
  4 байта вместо 4-байтного int32
```

**Проблема отрицательных чисел:** `int32` с `-1` → 10 байт varint (дополнение до 2)!

### ZigZag encoding (sint32, sint64)

Для отрицательных чисел — zigzag кодирование:
```
n ≥ 0: 2n         (0 → 0, 1 → 2, 2 → 4)
n < 0: 2|n| - 1   (-1 → 1, -2 → 3, -3 → 5)

sint32(-1) = 1  → 1 байт varint
int32(-1)  = 0xFFFFFFFF → 10 байт varint  ← используй sint если нужны отрицательные
```

---

## Length-delimited кодирование

Строки, bytes, embedded messages, repeated fields:

```
field_tag (varint) | length (varint) | bytes

Пример: string order_id = 1 со значением "abc"
  Field tag: (1 << 3) | 2 = 0x0A
  Length: 3 = 0x03
  Data: 0x61 0x62 0x63
  → 0x0A 0x03 0x61 0x62 0x63  (5 байт)
```

---

## Пример бинарного кодирования

```protobuf
message Example {
    int32  a = 1;
    string b = 2;
}
```

```
{ a: 150, b: "test" }

a: field=1, type=varint(0): tag = (1<<3)|0 = 0x08
   value 150 = varint: 0x96 0x01
b: field=2, type=length-del(2): tag = (2<<3)|2 = 0x12
   length = 4: 0x04
   "test" = 0x74 0x65 0x73 0x74

Wire bytes: 08 96 01 12 04 74 65 73 74  (9 байт)
```

Для сравнения JSON: `{"a":150,"b":"test"}` = 20 байт.

---

## proto3 vs proto2

| | proto2 | proto3 |
|---|---|---|
| Required fields | ✅ | ❌ (removed) |
| Optional fields | explicit `optional` | все поля optional |
| Default values | задаются явно | zero values |
| Extensions | ✅ | ❌ (Any вместо) |
| Unknown fields | опционально | сохраняются (proto3.5+) |

**proto3 default values:**
- `int32`, `int64`, `float`, etc. → `0`
- `string` → `""`
- `bool` → `false`
- `enum` → первое значение (должно быть 0)
- `message` → nil (не установлено)

---

## Backward / Forward совместимость

```protobuf
// Версия 1
message User {
    string name  = 1;
    string email = 2;
}

// Версия 2: добавляем поля — СОВМЕСТИМО
message User {
    string name    = 1;
    string email   = 2;
    int32  age     = 3;  // новое поле: старые клиенты игнорируют
}

// НЕЛЬЗЯ: изменить тип поля или переиспользовать field number
// НЕЛЬЗЯ: удалить поле и переиспользовать его номер → используй reserved
message User {
    string name  = 1;
    // string email = 2;  удалено
    reserved 2;          // номер 2 зарезервирован навсегда
    reserved "email";    // имя тоже зарезервировано
    int32  age   = 3;
}
```

**Правила совместимости:**
1. Никогда не меняй field number существующего поля
2. Никогда не меняй wire type поля
3. Удалённые поля — резервируй через `reserved`
4. Новые поля — только добавляй

---

## Well-Known Types (google/protobuf/)

| Тип | Назначение |
|---|---|
| `Timestamp` | время (seconds + nanos от Unix epoch) |
| `Duration` | временной интервал |
| `Any` | произвольное сообщение с type URL |
| `Struct` | произвольный JSON-like объект |
| `Wrapper types` | `StringValue`, `Int32Value` — nullable примитивы |
| `FieldMask` | partial updates (какие поля обновить) |

---

## Генерация кода (Go)

```bash
# Установка
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# Генерация
protoc --go_out=. --go_opt=paths=source_relative \
       --go-grpc_out=. --go-grpc_opt=paths=source_relative \
       api/order.proto
```

---

## Связанные темы

- [[gRPC]] — protobuf как формат сериализации для gRPC
- [[REST]] — JSON vs protobuf: бинарный, типизированный, но не human-readable
- [[HTTP]] — gRPC (и protobuf) работают поверх HTTP/2
