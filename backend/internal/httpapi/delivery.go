package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"venskoe/backend/internal/delivery"
)

func (d Deps) registerDelivery(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/delivery/info", d.deliveryInfo)
	mux.HandleFunc("POST /api/delivery/quote", d.deliveryQuote)
}

func (d Deps) deliveryInfo(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	info, err := d.Delivery.Info(ctx, time.Now())
	if err != nil {
		d.Log.Error("api/delivery/info: не смогли собрать данные", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "Не удалось загрузить информацию о доставке")
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (d Deps) deliveryQuote(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	// Lat/Lon — указатели, чтобы отличить «0» от «не прислали».
	var body struct {
		Lat         *float64 `json:"lat"`
		Lon         *float64 `json:"lon"`
		SubtotalKop int64    `json:"subtotal_kop"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Неверное тело запроса")
		return
	}
	if !validCoordinates(body.Lat, body.Lon) {
		writeError(w, http.StatusUnprocessableEntity, "invalid_coordinates", "Неверные координаты адреса")
		return
	}
	if body.SubtotalKop < 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "Сумма заказа не может быть отрицательной")
		return
	}

	quote, err := d.Delivery.Quote(ctx, time.Now(), delivery.QuoteInput{
		Lat: *body.Lat, Lon: *body.Lon, SubtotalKop: body.SubtotalKop,
	})
	if err != nil {
		d.Log.Error("api/delivery/quote: не смогли рассчитать", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "Не удалось рассчитать доставку")
		return
	}
	writeJSON(w, http.StatusOK, quote)
}

// validCoordinates проверяет, что координаты присланы и лежат в допустимых пределах:
// широта −90..90, долгота −180..180.
func validCoordinates(lat, lon *float64) bool {
	if lat == nil || lon == nil {
		return false
	}
	return *lat >= -90 && *lat <= 90 && *lon >= -180 && *lon <= 180
}
