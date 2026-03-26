#api #rest #architecture #http

REST (Representational State Transfer) — архитектурный стиль распределённых систем. Введён Roy Fielding в диссертации **«Architectural Styles and the Design of Network-based Software Architectures»** (2000, UC Irvine). REST — не стандарт и не протокол, а набор ограничений.

---

## 6 ограничений REST (Fielding, 2000)

### 1. Client-Server

Разделение ответственностей: клиент занимается UI, сервер — хранением данных. Позволяет им развиваться независимо.

### 2. Stateless

**Каждый запрос содержит всю информацию**, необходимую серверу для его обработки. Сервер не хранит состояние сессии клиента между запросами.

```
✅ Stateless: Authorization: Bearer <token>  (каждый запрос)
❌ Stateful: SET SESSION user_id=42; затем GET /profile
```

Следствие: горизонтальное масштабирование — любой сервер может обработать любой запрос.

### 3. Cache

Ответы должны быть явно помечены как кэшируемые или нет (`Cache-Control`). Кэширование снижает нагрузку и латентность.

### 4. Uniform Interface

Единый интерфейс — ключевое ограничение, отличающее REST от RPC:
- **Resource identification** — ресурсы идентифицируются URI
- **Manipulation through representations** — клиент меняет ресурс через его представление (JSON/XML)
- **Self-descriptive messages** — каждое сообщение содержит достаточно информации для обработки
- **HATEOAS** — Hypermedia As The Engine Of Application State

### 5. Layered System

Клиент не знает, общается ли он с конечным сервером или промежуточным (load balancer, CDN, API gateway).

### 6. Code on Demand (опционально)

Сервер может передавать исполняемый код клиенту (JavaScript). Единственное необязательное ограничение.

---

## Richardson Maturity Model

Практическая шкала «RESTfulness» (Leonard Richardson, 2008):

| Уровень | Описание | Пример |
|---|---|---|
| 0 | HTTP как транспорт RPC | `POST /api` с action в теле |
| 1 | Ресурсы | `POST /users`, `POST /orders` |
| 2 | HTTP методы | `GET /users`, `DELETE /users/42` |
| 3 | HATEOAS | ответ содержит ссылки на связанные действия |

> Fielding: уровень 3 — единственный «настоящий» REST. На практике большинство «REST API» — это уровень 2.

---

## HTTP методы и их семантика (RFC 9110)

| Метод | Семантика | Idempotent | Safe | Cacheable |
|---|---|---|---|---|
| GET | Получить ресурс | ✅ | ✅ | ✅ |
| HEAD | Только заголовки | ✅ | ✅ | ✅ |
| POST | Создать / действие | ❌ | ❌ | ⚠️ |
| PUT | Заменить ресурс целиком | ✅ | ❌ | ❌ |
| PATCH | Частично обновить | ❌ | ❌ | ❌ |
| DELETE | Удалить | ✅ | ❌ | ❌ |
| OPTIONS | Возможности ресурса | ✅ | ✅ | ❌ |

**Safe** — метод не изменяет состояние сервера.
**Idempotent** — повторный вызов с теми же параметрами даёт тот же результат. Важно для ретраев.

---

## Дизайн URI

```
# Ресурсы — существительные, не глаголы
✅ GET    /users
✅ POST   /users
✅ GET    /users/42
✅ PUT    /users/42
✅ DELETE /users/42
✅ GET    /users/42/orders
✅ GET    /users/42/orders/7

# Действия — через POST или sub-resource
✅ POST /orders/7/cancel
✅ POST /payments/capture

❌ GET  /getUser
❌ POST /deleteUser?id=42
❌ GET  /users/42/getOrders
```

**Правила именования:**
- Строчные буквы, дефис вместо подчёркивания: `/order-items`
- Существительные во множественном числе: `/users`, не `/user`
- Иерархия отражает отношения: `/users/{id}/orders/{orderId}`
- Не кодировать действия в URL для CRUD операций

---

## Коды статусов для REST

```
POST /users        → 201 Created, Location: /users/42
GET  /users/42     → 200 OK
PUT  /users/42     → 200 OK или 204 No Content
DELETE /users/42   → 204 No Content
GET  /users/999    → 404 Not Found

POST /users (невалидные данные)  → 400 Bad Request + { errors: [...] }
POST /users (нет токена)         → 401 Unauthorized
POST /users (нет прав)           → 403 Forbidden
POST /users (дубликат email)     → 409 Conflict
PUT  /users/42 (устаревший ETag) → 412 Precondition Failed
POST /orders (rate limit)        → 429 Too Many Requests
```

---

## Версионирование API

| Метод | Пример | Плюсы | Минусы |
|---|---|---|---|
| URL path | `/v1/users` | Явно, просто | URL "загрязняется" |
| Query param | `/users?version=1` | Не ломает кэш | Неочевидно |
| Header | `API-Version: 1` | Чистые URL | Менее заметно |
| Content-Type | `application/vnd.api+json;version=1` | Стандартизировано | Сложно |

Практика: **URL versioning** (`/v1/`) наиболее распространён.

---

## HATEOAS (опционально, уровень 3)

```json
{
  "id": 42,
  "status": "pending",
  "_links": {
    "self":   { "href": "/orders/42" },
    "cancel": { "href": "/orders/42/cancel", "method": "POST" },
    "pay":    { "href": "/orders/42/payments", "method": "POST" }
  }
}
```

Клиент не знает URL заранее — он их «обнаруживает» через гиперссылки в ответах.

---

## Pagination

```
# Cursor-based (рекомендуется для больших наборов)
GET /users?cursor=eyJpZCI6NDJ9&limit=20
Response: { "data": [...], "next_cursor": "eyJpZCI6NjJ9" }

# Offset-based (простой, но проблемы при удалении)
GET /users?offset=40&limit=20
Response: { "data": [...], "total": 1000 }

# Link header (RFC 5988)
Link: </users?page=3>; rel="next", </users?page=1>; rel="prev"
```

---

## Связанные темы

- [[HTTP]] — транспорт REST: методы, статусы, кэширование
- [[gRPC]] — альтернатива REST для внутренних сервисов; бинарный, типизированный
- [[protobuf]] — сериализация в gRPC; альтернатива JSON
- [[resilience patterns]] — retry должен учитывать idempotent vs non-idempotent методы
- [[OSI]] — REST на L7
