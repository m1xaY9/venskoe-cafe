package payments

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"venskoe/backend/internal/db"
	"venskoe/backend/internal/orders"
	"venskoe/backend/internal/sber"
	"venskoe/backend/migrations"

	"github.com/jackc/pgx/v5/pgxpool"
)

// fakeSber — заглушка клиента Сбера с заранее заданными ответами.
type fakeSber struct {
	mu            sync.Mutex
	registerResp  sber.RegisterResponse
	registerErr   error
	statusResp    sber.StatusResponse
	statusErr     error
	registerCalls int
	statusCalls   int
}

func (f *fakeSber) Register(context.Context, sber.RegisterRequest) (sber.RegisterResponse, error) {
	f.mu.Lock()
	f.registerCalls++
	f.mu.Unlock()
	return f.registerResp, f.registerErr
}

func (f *fakeSber) GetOrderStatusExtended(context.Context, sber.StatusRequest) (sber.StatusResponse, error) {
	f.mu.Lock()
	f.statusCalls++
	f.mu.Unlock()
	return f.statusResp, f.statusErr
}

func (f *fakeSber) BuildOrderBundle([]sber.BundleItem, int64) *sber.OrderBundle {
	return &sber.OrderBundle{}
}

func intp(i int) *int { return &i }

func setup(t *testing.T) *pgxpool.Pool {
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
		TRUNCATE payments, order_events, order_items, orders, dishes, categories RESTART IDENTITY CASCADE;
		INSERT INTO categories (slug, title) VALUES ('osn', 'Основные');
		INSERT INTO dishes (category_id, title, price_kop) VALUES (1, 'Сырники', 39000);`)
	if err != nil {
		t.Fatalf("подготовка данных: %v", err)
	}
	return pool
}

// createOrder заводит онлайн-заказ в заданном статусе и возвращает id, номер и токен.
func createOrder(t *testing.T, pool *pgxpool.Pool, status string, createdAgo time.Duration) (int64, int64, string) {
	t.Helper()
	ctx := context.Background()
	key := "idem-" + status + "-" + time.Now().Format("150405.000000000")
	token := "tok-" + key

	var (
		id     int64
		number int64
	)
	err := pool.QueryRow(ctx, `
		INSERT INTO orders (public_token, status, fulfillment, payment_method, customer_name, phone, email,
			subtotal_kop, delivery_kop, total_kop, idempotency_key, created_at)
		VALUES ($1, $2, 'pickup', 'online', 'Иван', '+79991234567', 'g@example.com',
			39000, 0, 39000, $3, now() - make_interval(secs => $4))
		RETURNING id, number`,
		token, status, key, createdAgo.Seconds()).Scan(&id, &number)
	if err != nil {
		t.Fatalf("создание заказа: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO order_items (order_id, dish_id, title, price_kop, qty, line_total_kop)
		VALUES ($1, 1, 'Сырники', 39000, 1, 39000)`, id); err != nil {
		t.Fatalf("позиция заказа: %v", err)
	}
	// Первое событие создания заказа.
	if _, err := pool.Exec(ctx, `
		INSERT INTO order_events (order_id, to_status, actor) VALUES ($1, $2, 'guest')`, id, status); err != nil {
		t.Fatalf("событие: %v", err)
	}
	return id, number, token
}

// createPayment заводит платёж в статусе registered для заказа. createdAgo — насколько
// давно создана попытка (для проверки сроков автоотмены).
func createPayment(t *testing.T, pool *pgxpool.Pool, orderID, number int64, createdAgo time.Duration) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(), `
		INSERT INTO payments (order_id, attempt, sber_order_number, sber_order_id, amount_kop, status, created_at)
		VALUES ($1, 1, $2, 'sber-abc', 39000, 'registered', now() - make_interval(secs => $3)) RETURNING id`,
		orderID, fmt.Sprintf("%d-1", number), createdAgo.Seconds()).Scan(&id)
	if err != nil {
		t.Fatalf("создание платежа: %v", err)
	}
	return id
}

func newSvc(pool *pgxpool.Pool, fake *fakeSber) *Service {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewService(pool, fake, log, "http://api/return", "http://api/return", "http://front", true)
}

func orderStatus(t *testing.T, pool *pgxpool.Pool, id int64) string {
	t.Helper()
	var s string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM orders WHERE id = $1`, id).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func paymentStatus(t *testing.T, pool *pgxpool.Pool, id int64) string {
	t.Helper()
	var s string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM payments WHERE id = $1`, id).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSyncPaymentStatuses(t *testing.T) {
	cases := []struct {
		sberStatus  int
		wantPayment string
		wantOrder   string
	}{
		{2, StatusPaid, orders.StatusPaid},
		{6, StatusDeclined, orders.StatusAwaitingPayment},
		{3, StatusReversed, orders.StatusAwaitingPayment},
		{4, StatusRefunded, orders.StatusAwaitingPayment},
		{0, StatusRegistered, orders.StatusAwaitingPayment},
	}
	for _, tc := range cases {
		t.Run(tc.wantPayment, func(t *testing.T) {
			pool := setup(t)
			orderID, number, _ := createOrder(t, pool, orders.StatusAwaitingPayment, 0)
			paymentID := createPayment(t, pool, orderID, number, 0)

			fake := &fakeSber{statusResp: sber.StatusResponse{ErrorCode: "0", OrderStatus: intp(tc.sberStatus), Amount: 39000}}
			svc := newSvc(pool, fake)

			if err := svc.SyncPayment(context.Background(), paymentID); err != nil {
				t.Fatalf("SyncPayment: %v", err)
			}
			if got := paymentStatus(t, pool, paymentID); got != tc.wantPayment {
				t.Errorf("статус платежа %q, ждали %q", got, tc.wantPayment)
			}
			if got := orderStatus(t, pool, orderID); got != tc.wantOrder {
				t.Errorf("статус заказа %q, ждали %q", got, tc.wantOrder)
			}
		})
	}
}

func TestSyncPaymentAmountMismatch(t *testing.T) {
	pool := setup(t)
	orderID, number, _ := createOrder(t, pool, orders.StatusAwaitingPayment, 0)
	paymentID := createPayment(t, pool, orderID, number, 0)

	// Сбер говорит «оплачено», но на другую сумму — в paid не переводим.
	fake := &fakeSber{statusResp: sber.StatusResponse{ErrorCode: "0", OrderStatus: intp(2), Amount: 10000}}
	svc := newSvc(pool, fake)

	if err := svc.SyncPayment(context.Background(), paymentID); err != nil {
		t.Fatalf("SyncPayment: %v", err)
	}
	if got := orderStatus(t, pool, orderID); got != orders.StatusAwaitingPayment {
		t.Errorf("заказ %q, ждали awaiting_payment (сумма не сошлась)", got)
	}
	if got := paymentStatus(t, pool, paymentID); got == StatusPaid {
		t.Errorf("платёж не должен стать paid при несовпадении суммы")
	}
}

func TestSyncPaymentFromCreated(t *testing.T) {
	pool := setup(t)
	// register прошёл, markRegistered упал: заказ остался в created, платёж registered.
	orderID, number, _ := createOrder(t, pool, orders.StatusCreated, 0)
	paymentID := createPayment(t, pool, orderID, number, 0)

	fake := &fakeSber{statusResp: sber.StatusResponse{ErrorCode: "0", OrderStatus: intp(2), Amount: 39000}}
	svc := newSvc(pool, fake)

	if err := svc.SyncPayment(context.Background(), paymentID); err != nil {
		t.Fatalf("SyncPayment: %v", err)
	}
	if got := orderStatus(t, pool, orderID); got != orders.StatusPaid {
		t.Fatalf("заказ %q, ждали paid (created → awaiting_payment → paid)", got)
	}
	var events int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM order_events WHERE order_id = $1 AND to_status IN ('awaiting_payment','paid')`, orderID).Scan(&events)
	if events != 2 {
		t.Errorf("переходов awaiting_payment+paid %d, ждали 2", events)
	}
}

func TestSyncPaymentAfterCancelNeedsRefund(t *testing.T) {
	pool := setup(t)
	orderID, number, _ := createOrder(t, pool, orders.StatusCancelled, 0)
	paymentID := createPayment(t, pool, orderID, number, 0)

	fake := &fakeSber{statusResp: sber.StatusResponse{ErrorCode: "0", OrderStatus: intp(2), Amount: 39000}}
	svc := newSvc(pool, fake)

	if err := svc.SyncPayment(context.Background(), paymentID); err != nil {
		t.Fatalf("SyncPayment: %v", err)
	}
	if got := orderStatus(t, pool, orderID); got != orders.StatusCancelled {
		t.Errorf("заказ %q, ждали cancelled (не меняем)", got)
	}
	if got := paymentStatus(t, pool, paymentID); got != StatusPaid {
		t.Errorf("платёж %q, ждали paid", got)
	}
	var needsRefund bool
	_ = pool.QueryRow(context.Background(), `SELECT needs_refund FROM payments WHERE id = $1`, paymentID).Scan(&needsRefund)
	if !needsRefund {
		t.Errorf("ждали needs_refund=true")
	}
}

func TestSyncPaymentRepeatCallback(t *testing.T) {
	pool := setup(t)
	orderID, number, _ := createOrder(t, pool, orders.StatusAwaitingPayment, 0)
	paymentID := createPayment(t, pool, orderID, number, 0)

	fake := &fakeSber{statusResp: sber.StatusResponse{ErrorCode: "0", OrderStatus: intp(2), Amount: 39000}}
	svc := newSvc(pool, fake)

	// Повтор callback: два вызова SyncPayment не должны сломать заказ.
	for range 2 {
		if err := svc.SyncPayment(context.Background(), paymentID); err != nil {
			t.Fatalf("SyncPayment: %v", err)
		}
	}
	if got := orderStatus(t, pool, orderID); got != orders.StatusPaid {
		t.Fatalf("статус заказа %q, ждали paid", got)
	}
	// Перехода в paid должно быть ровно одно событие.
	var paidEvents int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM order_events WHERE order_id = $1 AND to_status = 'paid'`, orderID).Scan(&paidEvents)
	if paidEvents != 1 {
		t.Errorf("событий paid %d, ждали 1 (идемпотентность)", paidEvents)
	}
}

func TestSyncPaymentCallbackReturnRace(t *testing.T) {
	pool := setup(t)
	orderID, number, _ := createOrder(t, pool, orders.StatusAwaitingPayment, 0)
	paymentID := createPayment(t, pool, orderID, number, 0)

	fake := &fakeSber{statusResp: sber.StatusResponse{ErrorCode: "0", OrderStatus: intp(2), Amount: 39000}}
	svc := newSvc(pool, fake)

	// Callback и возврат гостя пришли одновременно.
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = svc.SyncPayment(context.Background(), paymentID)
		}()
	}
	wg.Wait()

	if got := orderStatus(t, pool, orderID); got != orders.StatusPaid {
		t.Fatalf("статус заказа %q, ждали paid", got)
	}
	var paidEvents int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM order_events WHERE order_id = $1 AND to_status = 'paid'`, orderID).Scan(&paidEvents)
	if paidEvents != 1 {
		t.Errorf("событий paid %d, ждали 1 (без гонки)", paidEvents)
	}
}

func TestAutoCancel(t *testing.T) {
	pool := setup(t)
	now := time.Now()

	// Неоплаченный заказ старше 20 минут, в Сбере не оплачен (status 0) → отмена.
	staleID, staleNum, _ := createOrder(t, pool, orders.StatusAwaitingPayment, 25*time.Minute)
	createPayment(t, pool, staleID, staleNum, 30*time.Minute)

	// Свежий заказ (5 минут) не трогаем.
	freshID, _, _ := createOrder(t, pool, orders.StatusAwaitingPayment, 5*time.Minute)

	fake := &fakeSber{statusResp: sber.StatusResponse{ErrorCode: "0", OrderStatus: intp(0)}}
	svc := newSvc(pool, fake)

	if err := svc.AutoCancel(context.Background(), now); err != nil {
		t.Fatalf("AutoCancel: %v", err)
	}

	if got := orderStatus(t, pool, staleID); got != orders.StatusCancelled {
		t.Errorf("старый заказ %q, ждали cancelled", got)
	}
	if got := orderStatus(t, pool, freshID); got != orders.StatusAwaitingPayment {
		t.Errorf("свежий заказ %q, ждали awaiting_payment", got)
	}
}

func TestAutoCancelSkipsPaid(t *testing.T) {
	pool := setup(t)
	now := time.Now()

	// Старый заказ, но финальная проверка показывает оплату (status 2) → не отменяем.
	id, num, _ := createOrder(t, pool, orders.StatusAwaitingPayment, 25*time.Minute)
	createPayment(t, pool, id, num, 30*time.Minute)

	fake := &fakeSber{statusResp: sber.StatusResponse{ErrorCode: "0", OrderStatus: intp(2), Amount: 39000}}
	svc := newSvc(pool, fake)

	if err := svc.AutoCancel(context.Background(), now); err != nil {
		t.Fatalf("AutoCancel: %v", err)
	}
	if got := orderStatus(t, pool, id); got != orders.StatusPaid {
		t.Errorf("заказ %q, ждали paid (финальная проверка нашла оплату)", got)
	}
}

func TestPayReusesLivePayment(t *testing.T) {
	pool := setup(t)
	_, _, token := createOrder(t, pool, orders.StatusCreated, 0)

	fake := &fakeSber{registerResp: sber.RegisterResponse{ErrorCode: "0", OrderID: "sber-1", FormURL: "https://pay/1"}}
	svc := newSvc(pool, fake)

	first, err := svc.Pay(context.Background(), token)
	if err != nil {
		t.Fatalf("Pay: %v", err)
	}
	if first.FormURL != "https://pay/1" {
		t.Fatalf("form_url %q", first.FormURL)
	}

	// Второй вызов должен вернуть ту же форму и не регистрировать заказ заново.
	second, err := svc.Pay(context.Background(), token)
	if err != nil {
		t.Fatalf("повторный Pay: %v", err)
	}
	if second.FormURL != first.FormURL {
		t.Errorf("form_url отличается: %q vs %q", second.FormURL, first.FormURL)
	}
	fake.mu.Lock()
	calls := fake.registerCalls
	fake.mu.Unlock()
	if calls != 1 {
		t.Errorf("register вызван %d раз, ждали 1 (переиспользование живой попытки)", calls)
	}
}

func TestPayInProgress(t *testing.T) {
	pool := setup(t)
	orderID, number, token := createOrder(t, pool, orders.StatusCreated, 0)

	// Свежая попытка в created — значит оплата уже создаётся.
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO payments (order_id, attempt, sber_order_number, amount_kop, status)
		VALUES ($1, 1, $2, 39000, 'created')`, orderID, fmt.Sprintf("%d-1", number)); err != nil {
		t.Fatal(err)
	}

	svc := newSvc(pool, &fakeSber{registerResp: sber.RegisterResponse{ErrorCode: "0", OrderID: "s", FormURL: "u"}})
	if _, err := svc.Pay(context.Background(), token); err != ErrInProgress {
		t.Fatalf("ждали ErrInProgress, получили %v", err)
	}
}

func TestPayDisabled(t *testing.T) {
	pool := setup(t)
	_, _, token := createOrder(t, pool, orders.StatusCreated, 0)
	svc := NewService(pool, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), "r", "f", "front", false)

	if _, err := svc.Pay(context.Background(), token); err != ErrDisabled {
		t.Fatalf("ждали ErrDisabled, получили %v", err)
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
