package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"venskoe/backend/internal/delivery"
	"venskoe/backend/internal/menu"
	"venskoe/backend/internal/orders"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type fakeDB struct{ err error }

func (f fakeDB) Ping(context.Context) error { return f.err }

// fakeMenu — заглушка домена: отдаёт заранее заданные категории или ошибку.
type fakeMenu struct {
	categories []menu.Category
	err        error
}

func (f fakeMenu) Menu(context.Context) ([]menu.Category, error) {
	return f.categories, f.err
}

// fakeDelivery — заглушка логики доставки.
type fakeDelivery struct {
	info  delivery.Info
	quote delivery.Quote
	err   error
}

func (f fakeDelivery) Info(context.Context, time.Time) (delivery.Info, error) {
	return f.info, f.err
}

func (f fakeDelivery) Quote(context.Context, time.Time, delivery.QuoteInput) (delivery.Quote, error) {
	return f.quote, f.err
}

// fakeAddress — заглушка подсказок адреса.
type fakeAddress struct {
	suggestions []delivery.Suggestion
	err         error
}

func (f fakeAddress) Suggest(context.Context, string) ([]delivery.Suggestion, error) {
	return f.suggestions, f.err
}

// testDeps собирает зависимости обработчиков для тестов с нужными заглушками.
func testDeps(db Pinger, m MenuProvider, d DeliveryService, a AddressSuggester) Deps {
	return Deps{Log: discardLogger(), Version: "test", DB: db, Menu: m, Delivery: d, Address: a}
}

func TestHealthz(t *testing.T) {
	h := NewHandler(testDeps(fakeDB{}, fakeMenu{}, fakeDelivery{}, fakeAddress{}))
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("код ответа %d, ждали 200", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("ответ не JSON: %v", err)
	}
	if body["status"] != "ok" || body["version"] != "test" {
		t.Errorf("неожиданный ответ: %v", body)
	}
}

func TestHealthzWrongMethod(t *testing.T) {
	h := NewHandler(testDeps(fakeDB{}, fakeMenu{}, fakeDelivery{}, fakeAddress{}))

	req := httptest.NewRequest(http.MethodPost, "/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("код ответа %d, ждали 405", rec.Code)
	}
}

func TestRecoverPanic(t *testing.T) {
	boom := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("бум") })
	h := recoverPanic(discardLogger(), boom)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("код ответа %d, ждали 500", rec.Code)
	}
}

func TestReadyz(t *testing.T) {
	cases := []struct {
		name string
		db   fakeDB
		want int
	}{
		{name: "база отвечает", db: fakeDB{}, want: http.StatusOK},
		{name: "база упала", db: fakeDB{err: errors.New("connection refused")}, want: http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHandler(testDeps(tc.db, fakeMenu{}, fakeDelivery{}, fakeAddress{}))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			if rec.Code != tc.want {
				t.Fatalf("код ответа %d, ждали %d", rec.Code, tc.want)
			}
		})
	}
}

func TestGetMenu(t *testing.T) {
	stub := fakeMenu{categories: []menu.Category{
		{ID: 1, Slug: "zavtraki", Title: "Завтраки", Dishes: []menu.Dish{
			{ID: 10, Title: "Сырники", PriceKop: 39000, Available: true},
		}},
	}}
	h := NewHandler(testDeps(fakeDB{}, stub, fakeDelivery{}, fakeAddress{}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/menu", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("код ответа %d, ждали 200", rec.Code)
	}
	var body struct {
		Categories []menu.Category `json:"categories"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("ответ не JSON: %v", err)
	}
	if len(body.Categories) != 1 || body.Categories[0].Slug != "zavtraki" {
		t.Fatalf("категории: %+v", body.Categories)
	}
	if len(body.Categories[0].Dishes) != 1 || body.Categories[0].Dishes[0].Title != "Сырники" {
		t.Fatalf("блюда: %+v", body.Categories[0].Dishes)
	}
}

func TestGetMenuError(t *testing.T) {
	h := NewHandler(testDeps(fakeDB{}, fakeMenu{err: errors.New("база недоступна")}, fakeDelivery{}, fakeAddress{}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/menu", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("код ответа %d, ждали 500", rec.Code)
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("ответ не JSON: %v", err)
	}
	if body.Error.Code != "internal" || body.Error.Message == "" {
		t.Errorf("ждали единый формат ошибки, получили: %+v", body.Error)
	}
	// Внутренний текст ошибки не должен утечь в ответ.
	if body.Error.Message == "база недоступна" {
		t.Errorf("внутренний текст ошибки утёк в ответ: %q", body.Error.Message)
	}
}

func TestDeliveryInfo(t *testing.T) {
	stub := fakeDelivery{info: delivery.Info{DeliveryEnabled: true, OpenNow: true, PickupAddress: "Чита, ул. Ленина, 1"}}
	h := NewHandler(testDeps(fakeDB{}, fakeMenu{}, stub, fakeAddress{}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/delivery/info", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("код ответа %d, ждали 200", rec.Code)
	}
	var info delivery.Info
	if err := json.NewDecoder(rec.Body).Decode(&info); err != nil {
		t.Fatalf("ответ не JSON: %v", err)
	}
	if !info.OpenNow || info.PickupAddress != "Чита, ул. Ленина, 1" {
		t.Errorf("неожиданный ответ: %+v", info)
	}
}

func TestDeliveryInfoError(t *testing.T) {
	stub := fakeDelivery{err: errors.New("база недоступна")}
	h := NewHandler(testDeps(fakeDB{}, fakeMenu{}, stub, fakeAddress{}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/delivery/info", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("код ответа %d, ждали 500", rec.Code)
	}
}

func TestDeliveryQuote(t *testing.T) {
	stub := fakeDelivery{quote: delivery.Quote{InZone: true, Zone: "Центр", DeliveryPriceKop: 20000, CanOrder: true}}
	h := NewHandler(testDeps(fakeDB{}, fakeMenu{}, stub, fakeAddress{}))

	body := strings.NewReader(`{"lat":52.034,"lon":113.499,"subtotal_kop":100000}`)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/delivery/quote", body))

	if rec.Code != http.StatusOK {
		t.Fatalf("код ответа %d, ждали 200", rec.Code)
	}
	var q delivery.Quote
	if err := json.NewDecoder(rec.Body).Decode(&q); err != nil {
		t.Fatalf("ответ не JSON: %v", err)
	}
	if !q.CanOrder || q.Zone != "Центр" {
		t.Errorf("неожиданный ответ: %+v", q)
	}
}

func TestDeliveryQuoteBadBody(t *testing.T) {
	h := NewHandler(testDeps(fakeDB{}, fakeMenu{}, fakeDelivery{}, fakeAddress{}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/delivery/quote", strings.NewReader(`не json`)))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("код ответа %d, ждали 400", rec.Code)
	}
}

func TestDeliveryQuoteInvalidCoordinates(t *testing.T) {
	h := NewHandler(testDeps(fakeDB{}, fakeMenu{}, fakeDelivery{}, fakeAddress{}))

	cases := []struct {
		name string
		body string
	}{
		{"координат нет", `{"subtotal_kop":100000}`},
		{"нет широты", `{"lon":113.5,"subtotal_kop":100000}`},
		{"широта за пределами", `{"lat":100,"lon":113.5,"subtotal_kop":100000}`},
		{"долгота за пределами", `{"lat":52,"lon":200,"subtotal_kop":100000}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/delivery/quote", strings.NewReader(tc.body)))

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("код ответа %d, ждали 422", rec.Code)
			}
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatalf("ответ не JSON: %v", err)
			}
			if body.Error.Code != "invalid_coordinates" {
				t.Errorf("код ошибки %q, ждали invalid_coordinates", body.Error.Code)
			}
		})
	}
}

func TestAddressSuggest(t *testing.T) {
	lat, lon := 52.03, 113.5
	stub := fakeAddress{suggestions: []delivery.Suggestion{{Value: "г Чита, ул Ленина, д 1", Lat: &lat, Lon: &lon, Precise: true}}}
	h := NewHandler(testDeps(fakeDB{}, fakeMenu{}, fakeDelivery{}, stub))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/address/suggest?q=Чита+Ленина", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("код ответа %d, ждали 200", rec.Code)
	}
	var body struct {
		Suggestions []delivery.Suggestion `json:"suggestions"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("ответ не JSON: %v", err)
	}
	if len(body.Suggestions) != 1 || body.Suggestions[0].Value != "г Чита, ул Ленина, д 1" {
		t.Errorf("подсказки: %+v", body.Suggestions)
	}
}

func TestAddressSuggestShortQuery(t *testing.T) {
	// На коротком запросе DaData не дёргаем: если бы дёрнули, заглушка вернула бы 503.
	stub := fakeAddress{err: errors.New("не должно вызываться")}
	h := NewHandler(testDeps(fakeDB{}, fakeMenu{}, fakeDelivery{}, stub))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/address/suggest?q=чи", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("код ответа %d, ждали 200", rec.Code)
	}
}

func TestAddressSuggestUnavailable(t *testing.T) {
	stub := fakeAddress{err: delivery.ErrNoAPIKey}
	h := NewHandler(testDeps(fakeDB{}, fakeMenu{}, fakeDelivery{}, stub))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/address/suggest?q=Чита", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("код ответа %d, ждали 503", rec.Code)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("ответ не JSON: %v", err)
	}
	if body.Error.Code != "address_unavailable" {
		t.Errorf("код ошибки %q, ждали address_unavailable", body.Error.Code)
	}
}

// --- Заказы ---

type fakeOrders struct {
	created    orders.Created
	guest      orders.GuestOrder
	createErr  error
	byTokenErr error
}

func (f fakeOrders) Create(context.Context, orders.CreateInput) (orders.Created, error) {
	return f.created, f.createErr
}

func (f fakeOrders) ByToken(context.Context, string) (orders.GuestOrder, error) {
	return f.guest, f.byTokenErr
}

// ordersDeps собирает зависимости с нужной заглушкой заказов; остальные — пустые.
func ordersDeps(o OrderService) Deps {
	return Deps{
		Log: discardLogger(), Version: "test",
		DB: fakeDB{}, Menu: fakeMenu{}, Delivery: fakeDelivery{}, Address: fakeAddress{}, Orders: o,
	}
}

const validOrderBody = `{"fulfillment":"delivery","payment_method":"on_delivery","name":"Иван","phone":"89991234567","lat":52.0,"lon":113.5,"items":[{"dish_id":1,"qty":1}]}`

func orderRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/orders", strings.NewReader(body))
	req.Header.Set("Idempotency-Key", "11111111-1111-1111-1111-111111111111")
	return req
}

func TestCreateOrder(t *testing.T) {
	stub := fakeOrders{created: orders.Created{Token: "tok123", Number: 1001, Status: "accepted", TotalKop: 113000}}
	h := NewHandler(ordersDeps(stub))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, orderRequest(validOrderBody))

	if rec.Code != http.StatusCreated {
		t.Fatalf("код ответа %d, ждали 201", rec.Code)
	}
	var got orders.Created
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("ответ не JSON: %v", err)
	}
	if got.Token != "tok123" || got.Number != 1001 || got.TotalKop != 113000 {
		t.Errorf("неожиданный ответ: %+v", got)
	}
}

func TestCreateOrderValidation(t *testing.T) {
	dishID := int64(5)
	stub := fakeOrders{createErr: orders.ValidationErrors{
		{Code: "dish_unavailable", DishID: &dishID},
		{Code: "below_minimum"},
	}}
	h := NewHandler(ordersDeps(stub))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, orderRequest(validOrderBody))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("код ответа %d, ждали 422", rec.Code)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		Errors []struct {
			Code   string `json:"code"`
			DishID *int64 `json:"dish_id"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("ответ не JSON: %v", err)
	}
	if body.Error.Code != "validation_failed" || len(body.Errors) != 2 {
		t.Fatalf("неожиданный ответ: %+v", body)
	}
	if body.Errors[0].Code != "dish_unavailable" || body.Errors[0].DishID == nil || *body.Errors[0].DishID != 5 {
		t.Errorf("ждали dish_unavailable с id 5: %+v", body.Errors[0])
	}
}

func TestCreateOrderInternalError(t *testing.T) {
	// Обычная (не валидационная) ошибка из сервиса → 500.
	stub := fakeOrders{createErr: errors.New("база недоступна")}
	h := NewHandler(ordersDeps(stub))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, orderRequest(validOrderBody))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("код ответа %d, ждали 500", rec.Code)
	}
}

func TestCreateOrderMissingIdempotencyKey(t *testing.T) {
	h := NewHandler(ordersDeps(fakeOrders{}))

	rec := httptest.NewRecorder()
	// Без заголовка Idempotency-Key.
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/orders", strings.NewReader(validOrderBody)))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("код ответа %d, ждали 400", rec.Code)
	}
}

func TestCreateOrderRateLimited(t *testing.T) {
	stub := fakeOrders{created: orders.Created{Token: "tok", Number: 1}}
	h := NewHandler(ordersDeps(stub))

	// Первые maxOrdersPerMinute запросов проходят, следующий получает 429.
	for i := range maxOrdersPerMinute {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, orderRequest(validOrderBody))
		if rec.Code != http.StatusCreated {
			t.Fatalf("запрос %d: код %d, ждали 201", i+1, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, orderRequest(validOrderBody))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("код ответа %d, ждали 429", rec.Code)
	}
}

func TestGetOrder(t *testing.T) {
	stub := fakeOrders{guest: orders.GuestOrder{Token: "tok123", Number: 1001, Status: "accepted"}}
	h := NewHandler(ordersDeps(stub))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/orders/tok123", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("код ответа %d, ждали 200", rec.Code)
	}
	var got orders.GuestOrder
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("ответ не JSON: %v", err)
	}
	if got.Token != "tok123" || got.Number != 1001 {
		t.Errorf("неожиданный ответ: %+v", got)
	}
}

func TestGetOrderNotFound(t *testing.T) {
	stub := fakeOrders{byTokenErr: orders.ErrOrderNotFound}
	h := NewHandler(ordersDeps(stub))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/orders/nope", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("код ответа %d, ждали 404", rec.Code)
	}
}
