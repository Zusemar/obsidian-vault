#networking #tcp #transport-layer

TCP (Transmission Control Protocol) — надёжный, упорядоченный, байтовый потоковый протокол. Источники: **RFC 9293** (2022, объединяет RFC 793 и поправки), **RFC 7323** (TCP Extensions).

---

## Структура сегмента

```
 0                   1                   2                   3
 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
├─────────────────────────┬─────────────────────────────────────────┤
│      Source Port        │      Destination Port                   │
├─────────────────────────────────────────────────────────────────┤
│                     Sequence Number                               │
├─────────────────────────────────────────────────────────────────┤
│                  Acknowledgment Number                            │
├─────────┬───────────────┬─┬─┬─┬─┬─┬─┬─┬─┬───────────────────────┤
│ Data Off│   Reserved    │C│E│U│A│P│R│S│F│      Window Size       │
│         │               │W│C│R│C│S│S│Y│I│                       │
│         │               │R│E│G│K│H│T│N│N│                       │
├─────────────────────────┴───────────────┴───────────────────────┤
│         Checksum                │       Urgent Pointer           │
├─────────────────────────────────────────────────────────────────┤
│                    Options (0-40 bytes)                           │
├─────────────────────────────────────────────────────────────────┤
│                             Data                                  │
└─────────────────────────────────────────────────────────────────┘
```

**Ключевые поля:**
- **Sequence Number** — номер первого байта данных в этом сегменте
- **Acknowledgment Number** — следующий ожидаемый байт (кумулятивный ACK)
- **Window Size** — размер окна приёма (flow control)
- **Flags:** SYN, ACK, FIN, RST, PSH, URG

---

## Three-Way Handshake (установка соединения)

```
Client                          Server
  │                               │
  │──── SYN (seq=x) ──────────────▶│  LISTEN
  │                               │
  │◄─── SYN-ACK (seq=y, ack=x+1) ─│  SYN_RECEIVED
  │                               │
  │──── ACK (ack=y+1) ────────────▶│  ESTABLISHED
  │                               │
ESTABLISHED                  ESTABLISHED
```

- **SYN** — клиент выбирает ISN (Initial Sequence Number), рандомный для защиты от атак
- **SYN-ACK** — сервер подтверждает ISN клиента и сообщает свой ISN
- **ACK** — клиент подтверждает ISN сервера

ISN рандомизируется по RFC 6528 для защиты от TCP sequence prediction attacks.

---

## Four-Way Teardown (закрытие соединения)

```
Active Close                   Passive Close
  │                               │
  │──── FIN (seq=u) ──────────────▶│  CLOSE_WAIT
  │                               │
  │◄─── ACK (ack=u+1) ────────────│
  │                               │
FIN_WAIT_2                        │  (passive close сторона продолжает слать данные)
  │                               │
  │◄─── FIN (seq=v) ──────────────│  LAST_ACK
  │                               │
  │──── ACK (ack=v+1) ────────────▶│
  │                               │
TIME_WAIT (2*MSL)              CLOSED
```

**TIME_WAIT** — ожидание 2×MSL (Maximum Segment Lifetime, обычно 2×60=120s):
1. Гарантирует доставку последнего ACK
2. Позволяет устареть блуждающим сегментам в сети

**Проблема TIME_WAIT на сервере:**
- Сервер с огромным количеством TIME_WAIT соединений исчерпывает порты
- Решения: `SO_REUSEADDR`, `tcp_tw_reuse` (Linux), keepalive

---

## Управление потоком (Flow Control)

Предотвращает переполнение буфера получателя.

### Sliding Window

```
Sender window = min(cwnd, rwnd)

Sent & Acknowledged | Sent, Not Acked | Can Send | Cannot Send
◀──────────────────▶◀────────────────▶◀─────────▶◀───────────▶
          ▲               ▲                ▲
        SND.UNA        SND.NXT          SND.UNA + Window
```

- **rwnd** (receive window) — сколько байт готов принять получатель (поле Window в TCP-заголовке)
- **Zero Window** — rwnd=0: отправитель останавливается; получатель шлёт Window Update когда буфер освободился

### RFC 7323: Window Scale Option

Стандартный Window Size — 16 бит (max 65535 байт). Для высокопроизводительных сетей — Window Scale Factor (WSF) расширяет до 1 ГБ:

```
Effective window = Window × 2^WSF
```

Договаривается в SYN/SYN-ACK.

---

## Управление перегрузкой (Congestion Control)

Предотвращает перегрузку сети (не буфера получателя — это flow control).

### cwnd (Congestion Window)

```
cwnd — сколько байт отправитель может отправить не дожидаясь ACK
ssthresh — порог переключения алгоритма
```

### Slow Start

```
Начало: cwnd = 1 MSS
На каждый ACK: cwnd += 1 MSS  (экспоненциальный рост)
При cwnd >= ssthresh: переход в Congestion Avoidance
```

### Congestion Avoidance (AIMD)

```
На каждый RTT: cwnd += 1 MSS  (линейный рост — Additive Increase)
При потере (timeout): ssthresh = cwnd/2; cwnd = 1  (Multiplicative Decrease)
При 3 dup ACK (fast retransmit): ssthresh = cwnd/2; cwnd = ssthresh (TCP Reno)
```

### Современные алгоритмы

| Алгоритм | Метод обнаружения | Применение |
|---|---|---|
| CUBIC | потеря пакетов | Linux default (< 6.x) |
| BBR v3 | bandwidth + RTT | Google, высокопроизводительные сети |
| TCP Vegas | RTT | бесполосные сети |

---

## Надёжность и повторные передачи

### RTO (Retransmission Timeout)

Рассчитывается по алгоритму **Karn/Jacobson** (RFC 6298):

```
SRTT = (1 - α) × SRTT + α × RTT_sample   (α = 0.125)
RTTVAR = (1 - β) × RTTVAR + β × |SRTT - RTT_sample|  (β = 0.25)
RTO = SRTT + 4 × RTTVAR
```

Минимальный RTO = 1s (RFC 6298), максимальный = 60-120s.

### Fast Retransmit / Fast Recovery

- **3 dup ACK** → немедленная повторная передача потерянного сегмента без ожидания RTO
- **TCP SACK** (RFC 2018) — Selective ACK: получатель сообщает, какие блоки получены, отправитель повторяет только пропущенные

---

## TCP State Machine

```
                    ┌──────────────────────────────────────┐
                    │              CLOSED                  │
                    └──────────┬───────────────────────────┘
                   passive open │                │ active open
                               ▼                ▼
                           LISTEN           SYN_SENT
                               │                │
                    rcv SYN    │                │ rcv SYN+ACK
                    snd SYN+ACK│                │ snd ACK
                               ▼                ▼
                        SYN_RECEIVED        ESTABLISHED
                               │                │
                    rcv ACK    │                │
                               └───────┬────────┘
                                       │ ESTABLISHED
                                       │
              ┌────────────────────────┤
              │ close, snd FIN         │ rcv FIN, snd ACK
              ▼                        ▼
         FIN_WAIT_1              CLOSE_WAIT
              │                        │ close, snd FIN
              │ rcv ACK                ▼
              ▼                   LAST_ACK
         FIN_WAIT_2                    │ rcv ACK
              │                        ▼
              │ rcv FIN, snd ACK    CLOSED
              ▼
          TIME_WAIT
              │ 2*MSL timeout
              ▼
           CLOSED
```

---

## Важные TCP опции

| Опция | Код | Описание |
|---|---|---|
| MSS | 2 | Maximum Segment Size; договаривается в SYN |
| Window Scale | 3 | Масштабирование окна (RFC 7323) |
| SACK Permitted | 4 | Поддержка Selective ACK |
| SACK | 5 | Блоки полученных данных |
| Timestamps | 8 | RTT измерения, PAWS (RFC 7323) |
| TFO | 34 | TCP Fast Open: данные в SYN (RFC 7413) |

---

## TCP keepalive

Механизм обнаружения мёртвых соединений (не для приложений!):

```
tcp_keepalive_time    = 7200s  (когда начать пробы)
tcp_keepalive_intvl   = 75s    (интервал между пробами)
tcp_keepalive_probes  = 9      (число проб перед RST)
```

---

## Связанные темы

- [[OSI]] — TCP на L4 (Transport Layer)
- [[HTTP]] — HTTP/1.1 и HTTP/2 работают поверх TCP
- [[gRPC]] — поверх HTTP/2 поверх TCP
- [[databases]] — connection pooling скрывает стоимость TCP handshake
