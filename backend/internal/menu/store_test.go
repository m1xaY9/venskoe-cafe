package menu

import (
	"context"
	"net/url"
	"os"
	"testing"

	"venskoe/backend/internal/db"
	"venskoe/backend/migrations"
)

func TestStoreMenu(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL не задан, пропускаем тест с базой")
	}
	requireLocalDB(t, dbURL)
	ctx := context.Background()

	pool, err := db.Open(ctx, dbURL)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool, migrations.FS); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	_, err = pool.Exec(ctx, `
		TRUNCATE dishes, categories RESTART IDENTITY CASCADE;
		INSERT INTO categories (slug, title, sort, is_active) VALUES
			('deserty', 'Десерты', 20, true),
			('zavtraki', 'Завтраки', 10, true),
			('sezon', 'Сезонное', 30, false);
		INSERT INTO dishes (category_id, title, price_kop, is_available, sort) VALUES
			(1, 'Захер-торт', 36000, true, 0),
			(2, 'Омлет', 42000, false, 2),
			(2, 'Сырники', 39000, true, 1),
			(3, 'Глинтвейн', 30000, true, 0);`)
	if err != nil {
		t.Fatalf("подготовка данных: %v", err)
	}

	got, err := NewStore(pool).Menu(ctx)
	if err != nil {
		t.Fatalf("Menu: %v", err)
	}

	if len(got) != 2 || got[0].Slug != "zavtraki" || got[1].Slug != "deserty" {
		t.Fatalf("категории: %+v", got)
	}
	b := got[0].Dishes
	if len(b) != 2 || b[0].Title != "Сырники" || b[1].Title != "Омлет" {
		t.Fatalf("завтраки: %+v", b)
	}
	if b[1].Available || b[1].PriceKop != 42000 {
		t.Errorf("омлет: %+v", b[1])
	}
}

// requireLocalDB не даёт прогнать тест против удалённой базы: тест делает TRUNCATE,
// а значит должен работать только с локальной базой для разработки.
func requireLocalDB(t *testing.T, dbURL string) {
	t.Helper()
	u, err := url.Parse(dbURL)
	if err != nil {
		t.Fatalf("DATABASE_URL не разобрать: %v", err)
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
	default:
		t.Fatalf("тест с TRUNCATE можно запускать только на локальной базе, а не на %q", u.Hostname())
	}
}
