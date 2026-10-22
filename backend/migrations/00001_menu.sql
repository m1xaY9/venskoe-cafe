-- +goose Up
CREATE TABLE categories (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    -- slug — короткое имя для адреса: /menu#deserty
    slug       text        NOT NULL UNIQUE,
    title      text        NOT NULL,
    -- sort задаёт порядок категорий в меню: меньше — выше.
    sort       integer     NOT NULL DEFAULT 0,
    is_active  boolean     NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE dishes (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    category_id  bigint      NOT NULL REFERENCES categories (id),
    title        text        NOT NULL,
    description  text        NOT NULL DEFAULT '',
    -- Порция как её показываем гостю: «250 г», «400 мл».
    portion      text        NOT NULL DEFAULT '',
    -- Деньги храним в копейках целым числом, никогда не во float:
    -- 0.1 + 0.2 во float не равно 0.3, а в копейках ошибок округления нет.
    -- Сбер тоже принимает суммы в копейках.
    price_kop    bigint      NOT NULL CHECK (price_kop > 0),
    image_url    text        NOT NULL DEFAULT '',
    -- false — блюдо в стоп-листе: видно в меню, но заказать нельзя.
    is_available boolean     NOT NULL DEFAULT true,
    sort         integer     NOT NULL DEFAULT 0,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

-- Индекс ускоряет самый частый запрос: «все блюда категории».
CREATE INDEX dishes_category_id_idx ON dishes (category_id);

-- +goose Down
-- Down откатывает Up. Порядок обратный: сначала зависимая таблица.
DROP TABLE dishes;
DROP TABLE categories;
