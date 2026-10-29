package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

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

// testDeps собирает зависимости обработчиков для тестов с нужными заглушками.
func testDeps(db Pinger, m MenuProvider) Deps {
	return Deps{Log: discardLogger(), Version: "test", DB: db, Menu: m}
}

func TestHealthz(t *testing.T) {
	h := NewHandler(testDeps(fakeDB{}, fakeMenu{}))
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
	h := NewHandler(testDeps(fakeDB{}, fakeMenu{}))

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
			h := NewHandler(testDeps(tc.db, fakeMenu{}))
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
	h := NewHandler(testDeps(fakeDB{}, stub))

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
	h := NewHandler(testDeps(fakeDB{}, fakeMenu{err: errors.New("база недоступна")}))

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
