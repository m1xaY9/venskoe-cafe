-- +goose Up
CREATE TABLE payments (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    order_id         bigint  NOT NULL REFERENCES orders (id),
    -- Номер попытки оплаты заказа (1, 2, ...). Гость может платить повторно после отказа.
    attempt          integer NOT NULL CHECK (attempt >= 1),
    -- orderNumber в Сбере: "<order.number>-<attempt>". Уникален навсегда.
    sber_order_number text   NOT NULL UNIQUE,
    -- orderId в Сбере (приходит в ответе register.do). Пусто, пока не зарегистрирован.
    sber_order_id    text    NOT NULL DEFAULT '',
    form_url         text    NOT NULL DEFAULT '',
    amount_kop       bigint  NOT NULL CHECK (amount_kop > 0),
    status           text    NOT NULL,
    -- Последний orderStatus из Сбера (0..6). NULL, пока не проверяли.
    last_sber_status integer,
    -- Ответ последней проверки getOrderStatusExtended, без секретов. Для разбора спорных.
    raw              jsonb,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT payments_status_chk CHECK (status IN (
        'created', 'registered', 'paid', 'declined', 'expired', 'reversed', 'refunded')),
    CONSTRAINT payments_order_attempt_uniq UNIQUE (order_id, attempt)
);

CREATE INDEX payments_order_id_idx ON payments (order_id);
-- Для фоновой сверки: быстро находим платежи, ждущие оплаты.
CREATE INDEX payments_status_idx ON payments (status);

-- +goose Down
DROP TABLE payments;
