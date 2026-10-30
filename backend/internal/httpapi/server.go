package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"venskoe/backend/internal/delivery"
	"venskoe/backend/internal/menu"
	"venskoe/backend/internal/orders"
)

type Pinger interface {
	Ping(ctx context.Context) error
}

// MenuProvider отдаёт собранное меню. За ним скрыт menu.Store, в тестах — заглушка.
type MenuProvider interface {
	Menu(ctx context.Context) ([]menu.Category, error)
}

// DeliveryService — логика доставки. За ней скрыт delivery.Service, в тестах — заглушка.
type DeliveryService interface {
	Info(ctx context.Context, now time.Time) (delivery.Info, error)
	Quote(ctx context.Context, now time.Time, in delivery.QuoteInput) (delivery.Quote, error)
}

// AddressSuggester отдаёт подсказки адреса. За ним скрыт delivery.DaData, в тестах — заглушка.
type AddressSuggester interface {
	Suggest(ctx context.Context, query string) ([]delivery.Suggestion, error)
}

// OrderService создаёт и выдаёт заказы. За ним скрыт orders.Service, в тестах — заглушка.
type OrderService interface {
	Create(ctx context.Context, in orders.CreateInput) (orders.Created, error)
	ByToken(ctx context.Context, token string) (orders.GuestOrder, error)
}

// Deps — зависимости обработчиков. Передаём их одной структурой, чтобы в тестах
// подставлять заглушки вместо настоящих базы и домена.
type Deps struct {
	Log      *slog.Logger
	Version  string
	DB       Pinger
	Menu     MenuProvider
	Delivery DeliveryService
	Address  AddressSuggester
	Orders   OrderService
}

func NewHandler(d Deps) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": d.Version})
	})

	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := d.DB.Ping(ctx); err != nil {
			d.Log.Error("readyz: база недоступна", "err", err)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "db unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})

	mux.HandleFunc("GET /api/menu", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		categories, err := d.Menu.Menu(ctx)
		if err != nil {
			d.Log.Error("api/menu: не смогли собрать меню", "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "Не удалось загрузить меню")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"categories": categories})
	})

	d.registerDelivery(mux)
	d.registerAddress(mux)
	d.registerOrders(mux)

	return recoverPanic(d.Log, logRequests(d.Log, mux))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError отдаёт ошибку в едином формате {"error":{"code","message"}}.
// code — латиницей для программы, message — по-русски для человека.
// Текст внутренней ошибки сюда не попадает, он только в логе.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func logRequests(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.Info(
			"http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", time.Since(start).Microseconds(),
		)
	})
}

func recoverPanic(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Error("panic", "err", err, "path", r.URL.Path, "stack", string(debug.Stack()))
				writeError(w, http.StatusInternalServerError, "internal", "Внутренняя ошибка сервера")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
