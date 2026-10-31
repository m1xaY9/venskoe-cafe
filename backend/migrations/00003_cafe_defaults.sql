-- +goose Up
-- Настройки и часы работы нужны и в боевой базе, куда seed не попадает; строки по
-- умолчанию создаёт миграция. ON CONFLICT DO NOTHING — для идемпотентности.

-- Формат времени "ЧЧ:ММ" проверяем в базе, чтобы не попало "25:00" или "9:5".
ALTER TABLE work_hours
    ADD CONSTRAINT work_hours_open_at_format CHECK (open_at ~ '^([01][0-9]|2[0-3]):[0-5][0-9]$'),
    ADD CONSTRAINT work_hours_close_at_format CHECK (close_at ~ '^([01][0-9]|2[0-3]):[0-5][0-9]$');

INSERT INTO cafe_settings (id) VALUES (1)
ON CONFLICT (id) DO NOTHING;

INSERT INTO work_hours (weekday) VALUES (0), (1), (2), (3), (4), (5), (6)
ON CONFLICT (weekday) DO NOTHING;

-- +goose Down
DELETE FROM work_hours WHERE weekday BETWEEN 0 AND 6;
DELETE FROM cafe_settings WHERE id = 1;
ALTER TABLE work_hours
    DROP CONSTRAINT work_hours_open_at_format,
    DROP CONSTRAINT work_hours_close_at_format;
