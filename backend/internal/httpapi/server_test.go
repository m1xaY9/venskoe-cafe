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

func TestAddressSuggest(t *testing.T) {
	stub := fakeAddress{suggestions: []delivery.Suggestion{{Value: "г Чита, ул Ленина, д 1", Lat: 52.03, Lon: 113.5}}}
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
