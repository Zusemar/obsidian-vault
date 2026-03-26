#api #grpc #rpc #http2 #protobuf

gRPC — высокопроизводительный RPC-фреймворк от Google. Транспорт — **HTTP/2**, сериализация по умолчанию — **Protocol Buffers**. Источники: **gRPC Core Concepts** (grpc.io), **gRPC over HTTP2** spec.

---

## 4 типа streaming

```protobuf
service OrderService {
    // Unary: один запрос → один ответ
    rpc GetOrder(GetOrderRequest) returns (Order);

    // Server streaming: один запрос → поток ответов
    rpc ListOrders(ListOrdersRequest) returns (stream Order);

    // Client streaming: поток запросов → один ответ
    rpc CreateBulkOrders(stream CreateOrderRequest) returns (BulkResult);

    // Bidirectional streaming: поток запросов ↔ поток ответов
    rpc TrackOrders(stream OrderEvent) returns (stream OrderStatus);
}
```

### Unary RPC — наиболее используемый

```go
// Сервер
func (s *server) GetOrder(ctx context.Context, req *pb.GetOrderRequest) (*pb.Order, error) {
    order, err := s.repo.FindByID(ctx, req.OrderId)
    if err != nil {
        return nil, status.Errorf(codes.NotFound, "order %s not found", req.OrderId)
    }
    return order, nil
}

// Клиент
resp, err := client.GetOrder(ctx, &pb.GetOrderRequest{OrderId: "42"})
```

### Server Streaming

```go
func (s *server) ListOrders(req *pb.ListOrdersRequest, stream pb.OrderService_ListOrdersServer) error {
    for _, order := range s.orders {
        if err := stream.Send(order); err != nil {
            return err  // клиент отключился
        }
        if err := stream.Context().Err(); err != nil {
            return err  // контекст отменён
        }
    }
    return nil
}

// Клиент
stream, err := client.ListOrders(ctx, req)
for {
    order, err := stream.Recv()
    if err == io.EOF { break }
    if err != nil { log.Fatal(err) }
    process(order)
}
```

---

## Маппинг на HTTP/2

Каждый gRPC вызов = один HTTP/2 stream:

```
HTTP/2 Frame Headers:
:method = POST
:path = /order.v1.OrderService/GetOrder
:scheme = https
content-type = application/grpc
grpc-timeout = 5S

DATA Frame:
[5-byte gRPC length-prefix][protobuf message]

Trailer (после последнего DATA):
grpc-status = 0
grpc-message = ""
```

**gRPC framing поверх HTTP/2:**
```
Byte 0:     Compressed flag (0 = нет, 1 = gzip/etc.)
Bytes 1-4:  Message length (big-endian uint32)
Bytes 5+:   Protobuf payload
```

---

## Status Codes

gRPC имеет свои статусы (не HTTP):

| Code | Value | Описание |
|---|---|---|
| OK | 0 | Успех |
| CANCELLED | 1 | Клиент отменил запрос |
| UNKNOWN | 2 | Неизвестная ошибка |
| INVALID_ARGUMENT | 3 | Невалидные аргументы |
| DEADLINE_EXCEEDED | 4 | Дедлайн истёк |
| NOT_FOUND | 5 | Ресурс не найден |
| ALREADY_EXISTS | 6 | Ресурс уже существует |
| PERMISSION_DENIED | 7 | Нет прав |
| RESOURCE_EXHAUSTED | 8 | Rate limit, квота |
| FAILED_PRECONDITION | 9 | Неверное состояние системы |
| UNAVAILABLE | 14 | Сервер недоступен (retry) |
| UNAUTHENTICATED | 16 | Не аутентифицирован |

```go
import "google.golang.org/grpc/status"
import "google.golang.org/grpc/codes"

// Возврат ошибки
return nil, status.Errorf(codes.NotFound, "user %d not found", id)

// Обогащённые ошибки (google.rpc.Status)
st := status.New(codes.InvalidArgument, "invalid request")
st, _ = st.WithDetails(&errdetails.BadRequest{
    FieldViolations: []*errdetails.BadRequest_FieldViolation{
        {Field: "email", Description: "invalid email format"},
    },
})
return nil, st.Err()

// Клиент: разбор ошибки
if st, ok := status.FromError(err); ok {
    switch st.Code() {
    case codes.NotFound:
        // ...
    case codes.Unavailable:
        // retry
    }
}
```

---

## Metadata (заголовки)

Аналог HTTP заголовков. Два типа: **incoming** (запрос) и **outgoing** (ответ + trailer).

```go
// Клиент: отправка metadata
md := metadata.Pairs(
    "authorization", "Bearer " + token,
    "x-request-id",  requestID,
)
ctx = metadata.NewOutgoingContext(ctx, md)

// Сервер: получение metadata
md, ok := metadata.FromIncomingContext(ctx)
if !ok { return nil, status.Error(codes.Unauthenticated, "missing metadata") }
tokens := md.Get("authorization")

// Сервер: отправка metadata в ответе
grpc.SetHeader(ctx, metadata.Pairs("x-custom", "value"))
grpc.SetTrailer(ctx, metadata.Pairs("x-processing-time", "42ms"))
```

---

## Interceptors (middleware)

### Unary Interceptor

```go
// Server-side
func loggingInterceptor(
    ctx context.Context,
    req interface{},
    info *grpc.UnaryServerInfo,
    handler grpc.UnaryHandler,
) (interface{}, error) {
    start := time.Now()
    resp, err := handler(ctx, req)
    log.Printf("%s took %v, err=%v", info.FullMethod, time.Since(start), err)
    return resp, err
}

server := grpc.NewServer(
    grpc.ChainUnaryInterceptor(
        loggingInterceptor,
        authInterceptor,
        recoveryInterceptor,
    ),
)

// Client-side
conn, err := grpc.NewClient(addr,
    grpc.WithChainUnaryInterceptor(
        retryInterceptor,
        tracingInterceptor,
    ),
)
```

---

## Deadlines и Cancellation

gRPC **пропагирует дедлайны через цепочку сервисов**:

```go
// Клиент устанавливает дедлайн
ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
defer cancel()

resp, err := client.GetOrder(ctx, req)

// Дедлайн автоматически передаётся в gRPC-запрос через grpc-timeout заголовок
// Если сервис A вызывает сервис B — дедлайн оставшегося времени передаётся далее
```

На сервере всегда проверяй `ctx.Err()` в долгих операциях.

---

## Keepalive (ping/pong на HTTP/2 уровне)

```go
// Сервер
server := grpc.NewServer(
    grpc.KeepaliveParams(keepalive.ServerParameters{
        MaxConnectionIdle:     15 * time.Minute,
        MaxConnectionAge:      30 * time.Minute,
        MaxConnectionAgeGrace: 5 * time.Second,
        Time:                  5 * time.Second,   // PING интервал
        Timeout:               1 * time.Second,   // PING таймаут
    }),
    grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
        MinTime:             5 * time.Second,
        PermitWithoutStream: true,
    }),
)
```

---

## gRPC vs REST

| | gRPC | REST |
|---|---|---|
| Протокол | HTTP/2 | HTTP/1.1 / HTTP/2 |
| Формат | Protobuf (бинарный) | JSON (текстовый) |
| Типизация | строгая (схема) | слабая (JSON) |
| Streaming | 4 типа нативно | SSE, WebSocket отдельно |
| Browser support | нет (grpc-web нужен) | да |
| Отладка | сложнее | curl/browser |
| Кодогенерация | обязательна | опционально |
| Latency | ниже | выше |

**Когда gRPC:** внутренние микросервисы, high-throughput, нужен streaming.
**Когда REST:** публичный API, браузерные клиенты, простота.

---

## Reflection и grpcurl

```bash
# Если сервер включает reflection:
grpc.NewServer(grpc.ChainUnaryInterceptor(...))
// + reflection.Register(server)

# Вызов без клиентского кода:
grpcurl -plaintext localhost:50051 list
grpcurl -plaintext -d '{"order_id": "42"}' \
    localhost:50051 order.v1.OrderService/GetOrder
```

---

## Связанные темы

- [[protobuf]] — формат сериализации сообщений
- [[HTTP]] — gRPC использует HTTP/2 как транспорт
- [[REST]] — альтернатива gRPC для публичных API
- [[go context]] — дедлайны и отмена propagate через context в gRPC
- [[resilience patterns]] — retry с backoff, circuit breaker для gRPC клиентов
