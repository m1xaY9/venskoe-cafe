package delivery

import (
	"context"
	"net/url"
	"os"
	"testing"

	"venskoe/backend/internal/db"
	"venskoe/backend/migrations"
)

func TestStore(t *testing.T) {
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
		TRUNCATE delivery_zones RESTART IDENTITY CASCADE;
		TRUNCATE work_hours;
		TRUNCATE cafe_settings;
		INSERT INTO cafe_settings (id, delivery_enabled, pickup_enabled, pickup_address, pickup_min_order_kop, last_order_before_close_min)
			VALUES (1, true, false, 'Чита, тест', 50000, 30);
		INSERT INTO work_hours (weekday, is_open, open_at, close_at) VALUES
			(1, true, '10:00', '22:00'),
			(0, false, '00:00', '00:00');
		INSERT INTO delivery_zones (name, polygon, delivery_price_kop, min_order_kop, free_from_kop, sort) VALUES
			('Город', '{"type":"Polygon","coordinates":[[[113.449,51.984],[113.549,51.984],[113.549,52.084],[113.449,52.084],[113.449,51.984]]]}', 35000, 100000, NULL, 20),
			('Центр', '{"type":"Polygon","coordinates":[[[113.479,52.014],[113.519,52.014],[113.519,52.054],[113.479,52.054],[113.479,52.014]]]}', 20000, 80000, 150000, 10);`)
	if err != nil {
		t.Fatalf("подготовка данных: %v", err)
	}

	store := NewStore(pool)

	settings, err := store.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if !settings.DeliveryEnabled || settings.PickupEnabled || settings.PickupMinOrderKop != 50000 || settings.LastOrderBeforeCloseMin != 30 {
		t.Fatalf("настройки: %+v", settings)
	}

	hours, err := store.WorkHours(ctx)
	if err != nil {
		t.Fatalf("WorkHours: %v", err)
	}
	if len(hours) != 2 || hours[0].Weekday != 0 || hours[1].Weekday != 1 {
		t.Fatalf("часы (ждали сортировку по weekday): %+v", hours)
	}

	zones, err := store.Zones(ctx)
	if err != nil {
		t.Fatalf("Zones: %v", err)
	}
	// Сортировка по sort: сначала «Центр» (10), потом «Город» (20).
	if len(zones) != 2 || zones[0].Name != "Центр" || zones[1].Name != "Город" {
		t.Fatalf("зоны (ждали сортировку по sort): %+v", zones)
	}
	if zones[0].FreeFromKop == nil || *zones[0].FreeFromKop != 150000 {
		t.Errorf("free_from_kop Центра: %+v", zones[0].FreeFromKop)
	}
	if zones[1].FreeFromKop != nil {
		t.Errorf("у Города не должно быть free_from_kop: %+v", zones[1].FreeFromKop)
	}
	// Полигон должен разобраться и содержать центр Читы.
	if !zones[0].Polygon.Contains(Point{Lon: 113.499, Lat: 52.034}) {
		t.Error("полигон Центра не содержит центр Читы")
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
