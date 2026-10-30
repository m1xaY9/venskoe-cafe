package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"venskoe/backend/internal/orders"
)

// maxOrdersPerMinute — сколько заказов в минуту разрешаем с одного IP.
const maxOrdersPerMinute = 10

func (d Deps) registerOrders(mux *http.ServeMux) {
	limiter := newRateLimiter(maxOrdersPerMinute, time.Minute)
	mux.HandleFunc("POST /api/orders", func(w http.ResponseWriter, r *http.Request) {
		d.createOrder(w, r, limiter)
	})
	mux.HandleFunc("GET /api/orders/{token}", d.getOrder)
}

func (d Deps) createOrder(w http.ResponseWriter, r *http.Request, limiter *rateLimiter) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		writeError(w, http.StatusBadRequest, "idempotency_key_required", "Нужен заголовок Idempotency-Key")
		return
	}
	if !limiter.allow(clientIP(r), time.Now()) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "Слишком много заказов, попробуйте через минуту")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var body struct {
		Fulfillment    string     `json:"fulfillment"`
		PaymentMethod  string     `json:"payment_method"`
		Name           string     `json:"name"`
		Phone          string     `json:"phone"`
		Email          string     `json:"email"`
		Address        string     `json:"address"`
		Entrance       string     `json:"entrance"`
		Floor          string     `json:"floor"`
		Flat           string     `json:"flat"`
		CourierComment string     `json:"courier_comment"`
		Lat            *float64   `json:"lat"`
		Lon            *float64   `json:"lon"`
		Comment        string     `json:"comment"`
		WhenAsap       bool       `json:"when_asap"`
		WhenAt         *time.Time `json:"when_at"`
		Items          []struct {
			DishID int64 `json:"dish_id"`
			Qty    int   `json:"qty"`
		} `json:"items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Неверное тело запроса")
		return
	}

	in := orders.CreateInput{
		IdempotencyKey: key,
		Fulfillment:    body.Fulfillment,
		PaymentMethod:  body.PaymentMethod,
		Name:           body.Name,
		Phone:          body.Phone,
		Email:          body.Email,
		Address:        body.Address,
		Entrance:       body.Entrance,
		Floor:          body.Floor,
		Flat:           body.Flat,
		CourierComment: body.CourierComment,
		Lat:            body.Lat,
		Lon:            body.Lon,
		Comment:        body.Comment,
		WhenAsap:       body.WhenAsap,
		WhenAt:         body.WhenAt,
	}
	for _, it := range body.Items {
		in.Items = append(in.Items, orders.ItemInput{DishID: it.DishID, Qty: it.Qty})
	}

	created, err := d.Orders.Create(ctx, in)
	if err != nil {
		var verrs orders.ValidationErrors
		if errors.As(err, &verrs) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
				"error":  map[string]string{"code": "validation_failed", "message": "Проверьте данные заказа"},
				"errors": verrs,
			})
			return
		}
		d.Log.Error("api/orders: не смогли создать заказ", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "Не удалось создать заказ")
		return
	}

	writeJSON(w, http.StatusCreated, created)
}

func (d Deps) getOrder(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	order, err := d.Orders.ByToken(ctx, r.PathValue("token"))
	if errors.Is(err, orders.ErrOrderNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Заказ не найден")
		return
	}
	if err != nil {
		d.Log.Error("api/orders: не смогли прочитать заказ", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "Не удалось загрузить заказ")
		return
	}
	writeJSON(w, http.StatusOK, order)
}

// clientIP достаёт IP гостя из адреса соединения. За обратным прокси (Caddy) позже можно
// будет доверять X-Forwarded-For; пока берём адрес напрямую.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
