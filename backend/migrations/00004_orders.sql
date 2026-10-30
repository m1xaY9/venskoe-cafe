-- +goose Up
-- Короткий человекочитаемый номер заказа для кухни. Последовательность, стартуем с 1001,
-- чтобы номера сразу выглядели «настоящими». Пропуски в номерах допустимы.
CREATE SEQUENCE order_number_seq START 1001;

CREATE TABLE orders (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    -- Случайный токен (128 бит, base64url): по нему гость открывает свой заказ.
    public_token    text   NOT NULL UNIQUE,
    number          bigint NOT NULL DEFAULT nextval('order_number_seq') UNIQUE,
    status          text   NOT NULL,
    fulfillment     text   NOT NULL,
    payment_method  text   NOT NULL,
    customer_name   text   NOT NULL,
    -- Телефон нормализован к виду +7XXXXXXXXXX.
    phone           text   NOT NULL,
    -- Email обязателен для онлайн-оплаты (его требует Сбер), иначе может быть пустым.
    email           text   NOT NULL DEFAULT '',
    address         text   NOT NULL DEFAULT '',
    entrance        text   NOT NULL DEFAULT '',
    floor           text   NOT NULL DEFAULT '',
    flat            text   NOT NULL DEFAULT '',
    courier_comment text   NOT NULL DEFAULT '',
    lat             double precision,
    lon             double precision,
    delivery_zone_id bigint REFERENCES delivery_zones (id),
    comment         text   NOT NULL DEFAULT '',
    -- Когда нужен заказ: asap (как можно скорее) или на конкретное время в when_at.
    when_asap       boolean NOT NULL DEFAULT true,
    when_at         timestamptz,
    subtotal_kop    bigint NOT NULL CHECK (subtotal_kop >= 0),
    delivery_kop    bigint NOT NULL DEFAULT 0 CHECK (delivery_kop >= 0),
    total_kop       bigint NOT NULL CHECK (total_kop >= 0),
    -- Ключ идемпотентности: повтор запроса с тем же ключом не создаёт второй заказ.
    idempotency_key text   NOT NULL UNIQUE,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT orders_fulfillment_chk    CHECK (fulfillment IN ('delivery', 'pickup')),
    CONSTRAINT orders_payment_method_chk CHECK (payment_method IN ('online', 'on_delivery')),
    CONSTRAINT orders_status_chk CHECK (status IN (
        'created', 'awaiting_payment', 'paid', 'accepted', 'cooking',
        'ready', 'delivering', 'delivered', 'picked_up', 'cancelled')),
    CONSTRAINT orders_total_chk CHECK (total_kop = subtotal_kop + delivery_kop)
);

CREATE TABLE order_items (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    order_id       bigint  NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    dish_id        bigint  NOT NULL REFERENCES dishes (id),
    -- Снимок названия и цены на момент заказа: смена цены в меню не трогает старые заказы.
    title          text    NOT NULL,
    price_kop      bigint  NOT NULL CHECK (price_kop > 0),
    qty            integer NOT NULL CHECK (qty BETWEEN 1 AND 50),
    line_total_kop bigint  NOT NULL CHECK (line_total_kop > 0),
    CONSTRAINT order_items_line_total_chk CHECK (line_total_kop = price_kop * qty)
);

CREATE INDEX order_items_order_id_idx ON order_items (order_id);

CREATE TABLE order_events (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    order_id    bigint NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    -- from_status пустой (NULL) только у самого первого события — создания заказа.
    from_status text,
    to_status   text   NOT NULL,
    actor       text   NOT NULL,
    actor_id    bigint,
    reason      text   NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT order_events_actor_chk CHECK (actor IN ('guest', 'staff', 'bot', 'system'))
);

CREATE INDEX order_events_order_id_idx ON order_events (order_id);

-- +goose Down
DROP TABLE order_events;
DROP TABLE order_items;
DROP TABLE orders;
DROP SEQUENCE order_number_seq;
