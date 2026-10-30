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
	if !isUUID(key) {
		writeError(w, http.StatusBadRequest, "invalid_idempotency_key", "Idempotency-Key должен быть UUID")
		return
	}
	if !limiter.allow(clientIP(r, d.TrustedProxies), time.Now()) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "Слишком много заказов, попробуйте через минуту")
		return
	}

	// Тело заказа небольшое; ограничиваем 64 КБ, чтобы не читать мусор в память.
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)

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

// clientIP определяет IP гостя. Если соединение пришло от доверенного прокси, берём
// самый правый адрес из X-Forwarded-For, не входящий в доверенные прокси (его труднее
// подделать). Иначе — адрес самого соединения.
func clientIP(r *http.Request, trusted []*net.IPNet) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}

	remote := net.ParseIP(host)
	if remote == nil || !ipInAny(remote, trusted) {
		return host
	}

	for _, raw := range splitForwardedFor(r.Header.Values("X-Forwarded-For")) {
		ip := net.ParseIP(raw)
		if ip == nil {
			continue
		}
		if !ipInAny(ip, trusted) {
			return ip.String()
		}
	}
	return host
}

// splitForwardedFor разбирает заголовки X-Forwarded-For (могут повторяться и содержать
// список через запятую) в срез адресов по порядку, слева направо.
func splitForwardedFor(headers []string) []string {
	var out []string
	for _, h := range headers {
		for _, part := range strings.Split(h, ",") {
			if p := strings.TrimSpace(part); p != "" {
				out = append(out, p)
			}
		}
	}
	// Идём справа налево: правее — ближе к нашему прокси, их труднее подделать.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func ipInAny(ip net.IP, nets []*net.IPNet) bool {
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// ParseCIDRs превращает список CIDR в сети. Ошибку возвращает на первом неверном значении.
// Нужна в main для разбора TRUSTED_PROXIES.
func ParseCIDRs(cidrs []string) ([]*net.IPNet, error) {
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			return nil, err
		}
		nets = append(nets, n)
	}
	return nets, nil
}

// isUUID проверяет, что строка — UUID вида 8-4-4-4-12 из hex-символов.
func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !isHexDigit(r) {
				return false
			}
		}
	}
	return true
}

func isHexDigit(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}
