orders

| order id | track number | entry | locale | int_sig | cust_id | del_serv | shardkey | sm id | date created | oof shard |
| -------- | ------------ | ----- | ------ | ------- | ------- | -------- | -------- | ----- | ------------ | --------- |
|          |              |       |        |         |         |          |          |       |              |           |
|          |              |       |        |         |         |          |          |       |              |           |

payment

| order id | transaction | reqest | curr | provider | amount | payment_dt | bank | deliv cost | goods total | custom fee |
| -------- | ----------- | ------ | ---- | -------- | ------ | ---------- | ---- | ---------- | ----------- | ---------- |
|          |             |        |      |          |        |            |      |            |             |            |
|          |             |        |      |          |        |            |      |            |             |            |

delivery

| order id | name | phone | zip | city | address | region | email |
| -------- | ---- | ----- | --- | ---- | ------- | ------ | ----- |
|          |      |       |     |      |         |        |       |
order_items

| order id | chart it | track num | price | rid | name | sale | size | total price | nm id | brand | status |
| -------- | -------- | --------- | ----- | --- | ---- | ---- | ---- | ----------- | ----- | ----- | ------ |
|          |          |           |       |     |      |      |      |             |       |       |        |


code to create this type shi

```
CREATE TABLE orders (
    order_uid VARCHAR(50) PRIMARY KEY,
    track_number VARCHAR(50) NOT NULL,
    entry VARCHAR(10) NOT NULL,
    locale VARCHAR(5) NOT NULL,
    internal_signature VARCHAR(100),
    customer_id VARCHAR(50) NOT NULL,
    delivery_service VARCHAR(50) NOT NULL,
    shardkey VARCHAR(10) NOT NULL,
    sm_id INTEGER NOT NULL,
    date_created TIMESTAMP WITH TIME ZONE NOT NULL,
    oof_shard VARCHAR(10) NOT NULL
);

CREATE TABLE delivery (
    order_uid VARCHAR(50) PRIMARY KEY REFERENCES orders(order_uid) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL,
    phone VARCHAR(20) NOT NULL,
    zip VARCHAR(20) NOT NULL,
    city VARCHAR(50) NOT NULL,
    address VARCHAR(100) NOT NULL,
    region VARCHAR(50) NOT NULL,
    email VARCHAR(100) NOT NULL
);

CREATE TABLE payment (
    order_uid VARCHAR(50) PRIMARY KEY REFERENCES orders(order_uid) ON DELETE CASCADE,
    transaction VARCHAR(50) NOT NULL,
    request_id VARCHAR(50),
    currency VARCHAR(10) NOT NULL,
    provider VARCHAR(50) NOT NULL,
    amount INTEGER NOT NULL,
    payment_dt BIGINT NOT NULL,
    bank VARCHAR(50) NOT NULL,
    delivery_cost INTEGER NOT NULL,
    goods_total INTEGER NOT NULL,
    custom_fee INTEGER NOT NULL
);

CREATE TABLE order_items (
    item_id SERIAL PRIMARY KEY,
    order_uid VARCHAR(50) NOT NULL REFERENCES orders(order_uid) ON DELETE CASCADE,
    chrt_id BIGINT NOT NULL,
    price INTEGER NOT NULL,
    rid VARCHAR(50) NOT NULL,
    name VARCHAR(100) NOT NULL,
    sale INTEGER NOT NULL,
    size VARCHAR(10) NOT NULL,
    total_price INTEGER NOT NULL,
    nm_id BIGINT NOT NULL,
    brand VARCHAR(50) NOT NULL,
    status INTEGER NOT NULL
);
```

request to get all data 
```
SELECT 
    o.*,
    d.*,
    p.*,
    json_agg(i) AS items
FROM orders o
JOIN delivery d ON o.order_uid = d.order_uid
JOIN payment p ON o.order_uid = p.order_uid
JOIN (
    SELECT 
        order_uid,
        chrt_id,
        price,
        rid,
        name,
        sale,
        size,
        total_price,
        nm_id,
        brand,
        status
    FROM order_items
) i ON o.order_uid = i.order_uid
WHERE o.order_uid = 'b563feb7b2b84b6test'
GROUP BY
    o.order_uid,
    d.order_uid,
    p.order_uid;
```

---

#golang #project #database #postgresql

## Связанные темы

- [[L0 architecture]] — архитектура проекта: entities, use cases, adapters
- [[example.json]] — пример JSON-структуры заказа, которую хранит эта схема
