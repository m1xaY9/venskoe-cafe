-- +goose Up
-- Настройки кафе: одна строка. id = 1 гарантирует, что строка ровно одна.
CREATE TABLE cafe_settings (
    id                          integer PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    delivery_enabled            boolean NOT NULL DEFAULT true,
    pickup_enabled              boolean NOT NULL DEFAULT true,
    -- Адрес, куда гость приходит за самовывозом.
    pickup_address              text    NOT NULL DEFAULT '',
    -- Минимальная сумма заказа на самовывоз, в копейках.
    pickup_min_order_kop        bigint  NOT NULL DEFAULT 0 CHECK (pickup_min_order_kop >= 0),
    -- За сколько минут до закрытия перестаём принимать заказы.
    last_order_before_close_min integer NOT NULL DEFAULT 0 CHECK (last_order_before_close_min >= 0),
    updated_at                  timestamptz NOT NULL DEFAULT now()
);

-- Часы работы по дням недели. weekday как в Go time.Weekday: 0 — воскресенье ... 6 — суббота.
-- Время храним строкой "ЧЧ:ММ" — это проще читать в seed и в коде, чем тип time.
CREATE TABLE work_hours (
    weekday  smallint PRIMARY KEY CHECK (weekday BETWEEN 0 AND 6),
    is_open  boolean  NOT NULL DEFAULT true,
    open_at  text     NOT NULL DEFAULT '10:00',
    close_at text     NOT NULL DEFAULT '21:00'
);

-- Зоны доставки. polygon — GeoJSON-полигон в jsonb, координаты в порядке [lon, lat].
CREATE TABLE delivery_zones (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name               text    NOT NULL,
    polygon            jsonb   NOT NULL,
    delivery_price_kop bigint  NOT NULL CHECK (delivery_price_kop >= 0),
    min_order_kop      bigint  NOT NULL CHECK (min_order_kop >= 0),
    -- С какой суммы доставка бесплатна. NULL — бесплатной доставки в этой зоне нет.
    free_from_kop      bigint  CHECK (free_from_kop IS NULL OR free_from_kop >= 0),
    is_active          boolean NOT NULL DEFAULT true,
    -- При пересечении зон берём зону с меньшим sort.
    sort               integer NOT NULL DEFAULT 0
);

-- +goose Down
DROP TABLE delivery_zones;
DROP TABLE work_hours;
DROP TABLE cafe_settings;
