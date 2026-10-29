package menu

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

const queryCategories = `
SELECT id, slug, title
FROM categories
WHERE is_active
ORDER BY sort, id`

const queryDishes = `
SELECT d.id, d.category_id, d.title, d.description, d.portion,
       d.price_kop, d.image_url, d.is_available
FROM dishes d
JOIN categories c ON c.id = d.category_id
WHERE c.is_active
ORDER BY d.sort, d.id`

func (s *Store) Menu(ctx context.Context) ([]Category, error) {
	categories, err := queryAll[Category](ctx, s.pool, queryCategories)
	if err != nil {
		return nil, fmt.Errorf("menu: категории: %w", err)
	}
	dishes, err := queryAll[Dish](ctx, s.pool, queryDishes)
	if err != nil {
		return nil, fmt.Errorf("menu: блюда: %w", err)
	}
	return Build(categories, dishes), nil
}

func queryAll[T any](ctx context.Context, pool *pgxpool.Pool, sql string) ([]T, error) {
	rows, err := pool.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByName[T])
}
