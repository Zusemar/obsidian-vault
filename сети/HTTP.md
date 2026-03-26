#networking #http #application-layer

HTTP (HyperText Transfer Protocol) — протокол прикладного уровня. Источники: **RFC 9110** (HTTP Semantics), **RFC 9112** (HTTP/1.1), **RFC 9113** (HTTP/2), **RFC 9114** (HTTP/3).

---

## HTTP/1.1 (RFC 9112)

### Формат сообщения

```
Request:
GET /api/users?page=2 HTTP/1.1\r\n
Host: example.com\r\n
Accept: application/json\r\n
Authorization: Bearer <token>\r\n
\r\n

Response:
HTTP/1.1 200 OK\r\n
Content-Type: application/json\r\n
Content-Length: 42\r\n
\r\n
{"users": [...]}
```

### Keep-Alive (persistent connections)

По умолчанию в HTTP/1.1 соединение **не закрывается** после запроса:

```
Connection: keep-alive  ← дефолт в HTTP/1.1
Connection: close       ← закрыть после ответа
```

**Head-of-Line Blocking (HOL Blocking):** запросы в одном соединении обрабатываются **строго последовательно**. Ответ на второй запрос ждёт полного ответа на первый.

### Pipelining

HTTP/1.1 поддерживает конвейеризацию (отправку нескольких запросов без ожидания ответов), но:
- Ответы должны приходить в **том же порядке**
- Большинство браузеров и прокси его **отключают** из-за сложности
- HTTP/2 решает это мультиплексированием

### Chunked Transfer Encoding

Для ответов неизвестной длины:
```
HTTP/1.1 200 OK
Transfer-Encoding: chunked\r\n
\r\n
1a\r\n          ← размер чанка в hex
abcdefghijklmnopqrstuvwxyz\r\n
5\r\n
hello\r\n
0\r\n           ← конец тела
\r\n
```

---

## HTTP/2 (RFC 9113)

### Ключевые улучшения

| Проблема HTTP/1.1 | Решение HTTP/2 |
|---|---|
| HOL blocking | Мультиплексирование потоков |
| Текстовый протокол | Бинарное фреймирование |
| Дублирование заголовков | HPACK компрессия |
| Нет приоритизации | Stream priorities (устарело в RFC 9113) |
| Только pull | Server Push |

### Бинарное фреймирование

Всё разбивается на **фреймы**:

```
+-----------------------------------------------+
|                 Length (24)                   |
+---------------+---------------+---------------+
|   Type (8)    |   Flags (8)   |
+-+-------------+---------------+
|R|                Stream ID (31)               |
+=+=============+===================================+
|                   Frame Payload               |
+-----------------------------------------------+
```

Типы фреймов: `DATA`, `HEADERS`, `SETTINGS`, `WINDOW_UPDATE`, `PING`, `GOAWAY`, `RST_STREAM`, `PUSH_PROMISE`.

### Мультиплексирование (Streams)

Одно TCP-соединение → множество **логических потоков** (streams):

```
TCP Connection
├── Stream 1: GET /api/users     ──▶ response
├── Stream 3: GET /api/products  ──▶ response  (параллельно!)
├── Stream 5: POST /api/order    ──▶ response
└── Stream 7: GET /static/app.js ──▶ response
```

- Чётные ID → сервер-инициированные (Server Push)
- Нечётные ID → клиент-инициированные

### HPACK (RFC 7541) — сжатие заголовков

1. **Static Table** (61 запись) — часто используемые заголовки закодированы индексом (1 байт)
2. **Dynamic Table** — заголовки предыдущих запросов; новые добавляются
3. **Huffman кодирование** строковых значений

```
GET / HTTP/1.1 + Accept-Encoding: gzip → 2 байта (индексы в static table)
```

### Connection Preface

Клиент начинает соединение с:
```
PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n
```
Затем `SETTINGS` фрейм. Это позволяет HTTP/1.1 серверам отвергнуть соединение.

### HOL Blocking на уровне TCP

HTTP/2 решает HOL blocking на уровне HTTP, но **не на уровне TCP**: потеря одного TCP-сегмента блокирует все HTTP/2 потоки до ретрансмиссии. Именно это решает HTTP/3.

---

## HTTP/3 (RFC 9114) + QUIC (RFC 9000)

### QUIC вместо TCP

HTTP/3 работает поверх **QUIC** — надёжного транспорта поверх **UDP**:

```
HTTP/3
  └── QUIC
        └── UDP
              └── IP
```

| Возможность | TCP | QUIC |
|---|---|---|
| Надёжность | ✅ | ✅ |
| Упорядоченность | глобальная | per-stream |
| HOL Blocking | ❌ | ✅ нет |
| Handshake | TCP 3-way + TLS 1.3 (2-RTT) | 1-RTT (0-RTT при повторном) |
| Шифрование | TLS отдельно | TLS 1.3 встроен |
| Смена сети | разрыв соединения | Connection Migration (новый IP, тот же conn) |

### 0-RTT Connection Resumption

При повторном подключении к знакомому серверу:
```
Client ──── Initial (0-RTT data) ──▶ Server
Client ◄─── Response ─────────────── Server
```

Данные отправляются **немедленно** (replay attack возможен — только для идемпотентных запросов).

### QUIC Streams

Не зависят друг от друга на уровне транспорта. Потеря пакета в stream 1 не блокирует stream 3.

---

## HTTPS и TLS

### TLS 1.3 Handshake (1-RTT)

```
Client                              Server
  │──── ClientHello (key_share) ────▶│
  │◄─── ServerHello + {Certificate,  │
  │     CertificateVerify, Finished} │
  │──── {Finished} ─────────────────▶│
  │◄═══ Application Data ════════════│  (зашифровано)
```

TLS 1.2 требовал 2-RTT. TLS 1.3 — 1-RTT (или 0-RTT при session resumption).

---

## Коды статусов (RFC 9110)

| Диапазон | Класс | Примеры |
|---|---|---|
| 1xx | Informational | 100 Continue, 101 Switching Protocols |
| 2xx | Success | 200 OK, 201 Created, 204 No Content |
| 3xx | Redirection | 301 Moved Permanently, 302 Found, 304 Not Modified |
| 4xx | Client Error | 400 Bad Request, 401 Unauthorized, 403 Forbidden, 404 Not Found, 429 Too Many Requests |
| 5xx | Server Error | 500 Internal Server Error, 502 Bad Gateway, 503 Service Unavailable |

**Разница 401 vs 403:**
- `401 Unauthorized` — не аутентифицирован (нет или невалидный токен)
- `403 Forbidden` — аутентифицирован, но нет прав

---

## HTTP Caching (RFC 9111)

```
Cache-Control: max-age=3600, public
Cache-Control: no-cache          ← всегда проверять у сервера (ETag/Last-Modified)
Cache-Control: no-store          ← не кэшировать вообще
Cache-Control: private           ← только браузер, не CDN
ETag: "abc123"                   ← идентификатор версии ресурса
Last-Modified: Thu, 27 Mar 2025 12:00:00 GMT
```

**Conditional requests:**
```
If-None-Match: "abc123"   → 304 Not Modified (если не изменилось)
If-Modified-Since: ...    → 304 Not Modified
```

---

## Связанные темы

- [[OSI]] — HTTP на L7 (Application Layer)
- [[TCP]] — HTTP/1.1 и HTTP/2 поверх TCP; HTTP/3 поверх QUIC/UDP
- [[REST]] — архитектурный стиль поверх HTTP
- [[gRPC]] — бинарный RPC поверх HTTP/2
- [[TLS / certificates]] — HTTPS: HTTP + TLS
