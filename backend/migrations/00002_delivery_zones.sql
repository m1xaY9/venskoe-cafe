-- +goose Up
-- id = 1 с CHECK гарантирует ровно одну строку настроек.
CREATE TABLE cafe_settings (
    id                          integer PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    delivery_enabled            boolean NOT NULL DEFAULT true,
    pickup_enabled              boolean NOT NULL DEFAULT true,
    pickup_address              text    NOT NULL DEFAULT '',
    pickup_min_order_kop        bigint  NOT NULL DEFAULT 0 CHECK (pickup_min_order_kop >= 0),
    last_order_before_close_min integer NOT NULL DEFAULT 0 CHECK (last_order_before_close_min >= 0),
    updated_at                  timestamptz NOT NULL DEFAULT now()
);

-- weekday как в Go time.Weekday: 0 — воскресенье ... 6 — суббота. Время строкой "ЧЧ:ММ".
CREATE TABLE work_hours (
    weekday  smallint PRIMARY KEY CHECK (weekday BETWEEN 0 AND 6),
    is_open  boolean  NOT NULL DEFAULT true,
    open_at  text     NOT NULL DEFAULT '10:00',
    close_at text     NOT NULL DEFAULT '21:00'
);

CREATE TABLE delivery_zones (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name               text    NOT NULL,
    -- GeoJSON-полигон; координаты в порядке [lon, lat].
    polygon            jsonb   NOT NULL,
    delivery_price_kop bigint  NOT NULL CHECK (delivery_price_kop >= 0),
    min_order_kop      bigint  NOT NULL CHECK (min_order_kop >= 0),
    -- NULL — бесплатной доставки в зоне нет.
    free_from_kop      bigint  CHECK (free_from_kop IS NULL OR free_from_kop >= 0),
    is_active          boolean NOT NULL DEFAULT true,
    -- При пересечении зон выбирается зона с меньшим sort.
    sort               integer NOT NULL DEFAULT 0
);

-- +goose Down
DROP TABLE delivery_zones;
DROP TABLE work_hours;
DROP TABLE cafe_settings;
