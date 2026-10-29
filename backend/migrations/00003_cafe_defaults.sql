-- +goose Up
-- Настройки кафе и часы работы нужны и в боевой базе (seed туда не попадает),
-- поэтому строки по умолчанию создаёт миграция. ON CONFLICT DO NOTHING — чтобы
-- повторный прогон или уже существующие строки ничего не ломали.

-- Формат времени "ЧЧ:ММ" (00:00–23:59). Проверяем на уровне базы, чтобы в work_hours
-- не попало что-то вроде "25:00" или "9:5".
ALTER TABLE work_hours
    ADD CONSTRAINT work_hours_open_at_format CHECK (open_at ~ '^([01][0-9]|2[0-3]):[0-5][0-9]$'),
    ADD CONSTRAINT work_hours_close_at_format CHECK (close_at ~ '^([01][0-9]|2[0-3]):[0-5][0-9]$');

-- Одна строка настроек со значениями по умолчанию (из DEFAULT столбцов).
INSERT INTO cafe_settings (id) VALUES (1)
ON CONFLICT (id) DO NOTHING;

-- Семь дней недели с часами по умолчанию (is_open=true, 10:00–21:00 из DEFAULT).
INSERT INTO work_hours (weekday) VALUES (0), (1), (2), (3), (4), (5), (6)
ON CONFLICT (weekday) DO NOTHING;

-- +goose Down
DELETE FROM work_hours WHERE weekday BETWEEN 0 AND 6;
DELETE FROM cafe_settings WHERE id = 1;
ALTER TABLE work_hours
    DROP CONSTRAINT work_hours_open_at_format,
    DROP CONSTRAINT work_hours_close_at_format;
