package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"venskoe/backend/internal/delivery"
)

func (d Deps) registerAddress(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/address/suggest", d.addressSuggest)
}

func (d Deps) addressSuggest(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	q := strings.TrimSpace(r.URL.Query().Get("q"))
	// Меньше 3 символов не ищем — это ещё не адрес, просто отдаём пустой список.
	if len([]rune(q)) < 3 {
		writeJSON(w, http.StatusOK, map[string]any{"suggestions": []delivery.Suggestion{}})
		return
	}
	suggestions, err := d.Address.Suggest(ctx, q)
	if err != nil {
		d.Log.Error("api/address/suggest: подсказки недоступны", "err", err)
		writeError(w, http.StatusServiceUnavailable, "address_unavailable", "Подсказки адреса временно недоступны")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"suggestions": suggestions})
}
