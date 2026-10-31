-- +goose Up
CREATE TABLE categories (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    slug       text        NOT NULL UNIQUE,
    title      text        NOT NULL,
    sort       integer     NOT NULL DEFAULT 0,
    is_active  boolean     NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE dishes (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    category_id  bigint      NOT NULL REFERENCES categories (id),
    title        text        NOT NULL,
    description  text        NOT NULL DEFAULT '',
    portion      text        NOT NULL DEFAULT '',
    -- Деньги в копейках: целое, без ошибок округления float.
    price_kop    bigint      NOT NULL CHECK (price_kop > 0),
    image_url    text        NOT NULL DEFAULT '',
    -- false — блюдо в стоп-листе: видно в меню, но заказать нельзя.
    is_available boolean     NOT NULL DEFAULT true,
    sort         integer     NOT NULL DEFAULT 0,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX dishes_category_id_idx ON dishes (category_id);

-- +goose Down
DROP TABLE dishes;
DROP TABLE categories;
