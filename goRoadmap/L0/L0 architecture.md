entities - Чистые структуры и методы предметной области, которые никак не зависят от бд и прочего.
order, payment, items, delivery

use cases - бизнесс логика, по сути то, что может делать приложение. ТУТ ТОЛЬКО ИНТЕРФЕЙСЫ
get by id (вывести по id заказа всю инфу)
newOrder (добавить новый заказ)

adapters - конкретные реализации интерфейсов
postgresql как база данных

---

#golang #project #architecture #clean-architecture

## Связанные темы

- [[L0 database]] — схема БД PostgreSQL: orders, payment, delivery, order_items
- [[example.json]] — пример JSON-объекта заказа (тестовые данные)

