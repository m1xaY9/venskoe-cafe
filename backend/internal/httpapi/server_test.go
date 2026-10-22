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
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type fakeDB struct{ err error }

func (f fakeDB) Ping(context.Context) error { return f.err }

func TestHealthz(t *testing.T) {
	h := NewHandler(discardLogger(), "test", fakeDB{})
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
	h := NewHandler(discardLogger(), "test", fakeDB{})

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
			h := NewHandler(discardLogger(), "test", tc.db)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			if rec.Code != tc.want {
				t.Fatalf("код ответа %d, ждали %d", rec.Code, tc.want)
			}
		})
	}
}
