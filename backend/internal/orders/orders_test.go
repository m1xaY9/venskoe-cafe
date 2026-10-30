package orders

import (
	"context"
	"errors"
	"net/url"
	"os"
	"testing"
	"time"

	"venskoe/backend/internal/db"
	"venskoe/backend/internal/delivery"
	"venskoe/backend/migrations"

	"github.com/jackc/pgx/v5/pgxpool"
)

// setupOrders поднимает пул, применяет миграции и наполняет базу тестовыми данными:
// одна категория, доступное блюдо (id 1) и блюдо из стоп-листа (id 2), настройки,
// часы (всегда открыто) и одна зона доставки вокруг точки (113.5, 52.0).
func setupOrders(t *testing.T) *pgxpool.Pool {
	t.Helper()
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
	t.Cleanup(pool.Close)
	if err := db.Migrate(ctx, pool, migrations.FS); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	_, err = pool.Exec(ctx, `
		TRUNCATE order_events, order_items, orders, dishes, categories, delivery_zones, work_hours, cafe_settings
			RESTART IDENTITY CASCADE;

		INSERT INTO categories (slug, title, is_active) VALUES ('osn', 'Основные', true);
		INSERT INTO dishes (category_id, title, price_kop, is_available) VALUES
			(1, 'Сырники', 39000, true),
			(1, 'Гуляш', 65000, false);

		INSERT INTO cafe_settings (id, delivery_enabled, pickup_enabled, pickup_address, pickup_min_order_kop, last_order_before_close_min)
			VALUES (1, true, true, 'Чита, тест', 0, 0);
		INSERT INTO work_hours (weekday, is_open, open_at, close_at)
			SELECT g, true, '00:00', '23:59' FROM generate_series(0, 6) g;

		INSERT INTO delivery_zones (name, polygon, delivery_price_kop, min_order_kop, free_from_kop, sort) VALUES
			('Город', '{"type":"Polygon","coordinates":[[[113.449,51.984],[113.549,51.984],[113.549,52.084],[113.449,52.084],[113.449,51.984]]]}', 35000, 50000, NULL, 10);`)
	if err != nil {
		t.Fatalf("подготовка данных: %v", err)
	}
	return pool
}

func newTestService(pool *pgxpool.Pool) *Service {
	svc := NewService(pool, delivery.NewService(delivery.NewStore(pool), time.UTC))
	svc.now = func() time.Time { return time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC) }
	return svc
}

func deliveryOrderInput(key string) CreateInput {
	lat, lon := 52.0, 113.5
	return CreateInput{
		IdempotencyKey: key,
		Fulfillment:    FulfillmentDelivery,
		PaymentMethod:  PaymentOnDelivery,
		Name:           "Иван",
		Phone:          "8 999 123-45-67",
		Lat:            &lat,
		Lon:            &lon,
		Address:        "ул. Тестовая, 1",
		WhenAsap:       true,
		Items:          []ItemInput{{DishID: 1, Qty: 2}},
	}
}

func TestCreateOrder(t *testing.T) {
	pool := setupOrders(t)
	svc := newTestService(pool)
	ctx := context.Background()

	created, err := svc.Create(ctx, deliveryOrderInput("key-1"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Сумму и доставку считает сервер по ценам из базы.
	if created.SubtotalKop != 78000 || created.DeliveryKop != 35000 || created.TotalKop != 113000 {
		t.Fatalf("суммы неверны: %+v", created)
	}
	// Оплата при получении → заказ сразу accepted.
	if created.Status != StatusAccepted {
		t.Fatalf("статус %q, ждали accepted", created.Status)
	}
	if created.Token == "" || created.Number == 0 {
		t.Fatalf("нет токена или номера: %+v", created)
	}

	// Повтор с тем же ключом не создаёт второй заказ.
	again, err := svc.Create(ctx, deliveryOrderInput("key-1"))
	if err != nil {
		t.Fatalf("повторный Create: %v", err)
	}
	if again.Token != created.Token || again.Number != created.Number {
		t.Fatalf("повтор по ключу вернул другой заказ: %+v против %+v", again, created)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM orders`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("заказов в базе %d, ждали 1 (идемпотентность)", count)
	}

	// Гостевой вид: состав, история этапов, адрес.
	guest, err := svc.ByToken(ctx, created.Token)
	if err != nil {
		t.Fatalf("ByToken: %v", err)
	}
	if len(guest.Items) != 1 || guest.Items[0].Title != "Сырники" || guest.Items[0].LineTotalKop != 78000 {
		t.Fatalf("позиции гостя: %+v", guest.Items)
	}
	if guest.Address == nil || guest.Address.Address != "ул. Тестовая, 1" {
		t.Fatalf("адрес гостя: %+v", guest.Address)
	}
	// События: создание (created) и принятие (accepted).
	if len(guest.Events) != 2 || guest.Events[0].Status != StatusCreated || guest.Events[1].Status != StatusAccepted {
		t.Fatalf("история этапов: %+v", guest.Events)
	}
	// Телефон нормализован.
	if guest.Phone != "+79991234567" {
		t.Errorf("телефон %q, ждали +79991234567", guest.Phone)
	}
}

func TestCreateOrderScheduled(t *testing.T) {
	pool := setupOrders(t)
	svc := newTestService(pool)
	ctx := context.Background()
	// now зафиксирован на 2026-10-26 12:00 UTC; часы работы в seed — круглосуточно.
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

	// Заказ на 14:00 того же дня — в окне и в часах работы.
	okWhen := time.Date(2026, 10, 26, 14, 0, 0, 0, time.UTC)
	in := deliveryOrderInput("key-when-ok")
	in.WhenAsap = false
	in.WhenAt = &okWhen
	if _, err := svc.Create(ctx, in); err != nil {
		t.Fatalf("валидный when_at должен пройти: %v", err)
	}

	// Заказ через 10 минут — слишком рано → invalid_when.
	tooSoon := now.Add(10 * time.Minute)
	in2 := deliveryOrderInput("key-when-soon")
	in2.WhenAsap = false
	in2.WhenAt = &tooSoon
	_, err := svc.Create(ctx, in2)
	var verrs ValidationErrors
	if !errors.As(err, &verrs) || !hasCode(verrs, "invalid_when") {
		t.Fatalf("ждали invalid_when, получили %v", err)
	}

	// when_asap=false без when_at → invalid_when.
	in3 := deliveryOrderInput("key-when-nil")
	in3.WhenAsap = false
	in3.WhenAt = nil
	_, err = svc.Create(ctx, in3)
	if !errors.As(err, &verrs) || !hasCode(verrs, "invalid_when") {
		t.Fatalf("ждали invalid_when при отсутствии when_at, получили %v", err)
	}
}

func TestCreateOrderStopListDish(t *testing.T) {
	pool := setupOrders(t)
	svc := newTestService(pool)

	in := deliveryOrderInput("key-stop")
	in.Items = []ItemInput{{DishID: 2, Qty: 1}} // блюдо из стоп-листа

	_, err := svc.Create(context.Background(), in)
	var verrs ValidationErrors
	if !errors.As(err, &verrs) {
		t.Fatalf("ждали ValidationErrors, получили %v", err)
	}
	found := false
	for _, fe := range verrs {
		if fe.Code == "dish_unavailable" && fe.DishID != nil && *fe.DishID == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("ждали dish_unavailable для блюда 2, получили %+v", verrs)
	}
}

func TestCreateOrderOnlineStaysCreated(t *testing.T) {
	pool := setupOrders(t)
	svc := newTestService(pool)

	in := deliveryOrderInput("key-online")
	in.PaymentMethod = PaymentOnline
	in.Email = "guest@example.com"

	created, err := svc.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Онлайн-оплата: заказ остаётся в created (оплата будет в следующей ветке).
	if created.Status != StatusCreated {
		t.Fatalf("статус %q, ждали created", created.Status)
	}
}

func TestTransitionIntegration(t *testing.T) {
	pool := setupOrders(t)
	svc := newTestService(pool)
	ctx := context.Background()

	// Создаём онлайн-заказ — он в created.
	in := deliveryOrderInput("key-trans")
	in.PaymentMethod = PaymentOnline
	in.Email = "guest@example.com"
	created, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	var orderID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM orders WHERE public_token = $1`, created.Token).Scan(&orderID); err != nil {
		t.Fatal(err)
	}

	// Запрещённый переход created → delivered отклоняется.
	tx, _ := pool.Begin(ctx)
	err = Transition(ctx, tx, orderID, StatusDelivered, ActorStaff, "")
	_ = tx.Rollback(ctx)
	if !errors.Is(err, ErrForbiddenTransition) {
		t.Fatalf("ждали ErrForbiddenTransition, получили %v", err)
	}

	// Разрешённый переход created → cancelled проходит и пишет событие.
	tx, _ = pool.Begin(ctx)
	if err := Transition(ctx, tx, orderID, StatusCancelled, ActorStaff, "передумал"); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	var status string
	var events int
	_ = pool.QueryRow(ctx, `SELECT status FROM orders WHERE id = $1`, orderID).Scan(&status)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM order_events WHERE order_id = $1`, orderID).Scan(&events)
	if status != StatusCancelled {
		t.Errorf("статус %q, ждали cancelled", status)
	}
	// Событие создания + отмена.
	if events != 2 {
		t.Errorf("событий %d, ждали 2", events)
	}
}

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
