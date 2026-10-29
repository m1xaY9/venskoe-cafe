package delivery

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DBStore читает настройки доставки из PostgreSQL.
type DBStore struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *DBStore {
	return &DBStore{pool: pool}
}

const querySettings = `
SELECT delivery_enabled, pickup_enabled, pickup_address,
       pickup_min_order_kop, last_order_before_close_min
FROM cafe_settings
WHERE id = 1`

const queryWorkHours = `
SELECT weekday, is_open, open_at, close_at
FROM work_hours
ORDER BY weekday`

const queryZones = `
SELECT id, name, polygon, delivery_price_kop, min_order_kop, free_from_kop, sort
FROM delivery_zones
WHERE is_active
ORDER BY sort, id`

func (s *DBStore) Settings(ctx context.Context) (Settings, error) {
	rows, err := s.pool.Query(ctx, querySettings)
	if err != nil {
		return Settings{}, fmt.Errorf("delivery: настройки: %w", err)
	}
	settings, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Settings])
	if err != nil {
		return Settings{}, fmt.Errorf("delivery: настройки: %w", err)
	}
	return settings, nil
}

func (s *DBStore) WorkHours(ctx context.Context) ([]DayHours, error) {
	rows, err := s.pool.Query(ctx, queryWorkHours)
	if err != nil {
		return nil, fmt.Errorf("delivery: часы работы: %w", err)
	}
	hours, err := pgx.CollectRows(rows, pgx.RowToStructByName[DayHours])
	if err != nil {
		return nil, fmt.Errorf("delivery: часы работы: %w", err)
	}
	return hours, nil
}

func (s *DBStore) Zones(ctx context.Context) ([]Zone, error) {
	rows, err := s.pool.Query(ctx, queryZones)
	if err != nil {
		return nil, fmt.Errorf("delivery: зоны: %w", err)
	}
	defer rows.Close()

	var zones []Zone
	for rows.Next() {
		var (
			z   Zone
			raw []byte
		)
		if err := rows.Scan(&z.ID, &z.Name, &raw, &z.DeliveryPriceKop, &z.MinOrderKop, &z.FreeFromKop, &z.Sort); err != nil {
			return nil, fmt.Errorf("delivery: зоны: %w", err)
		}
		poly, err := parsePolygon(raw)
		if err != nil {
			return nil, fmt.Errorf("delivery: зона %q: %w", z.Name, err)
		}
		z.Polygon = poly
		zones = append(zones, z)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("delivery: зоны: %w", err)
	}
	return zones, nil
}
