package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"venskoe/backend/internal/orders"
	"venskoe/backend/internal/payments"
)

// callbackPerMinute — лимит callback-уведомлений Сбера с одного IP в минуту.
const callbackPerMinute = 60

func (d Deps) registerPayments(mux *http.ServeMux) {
	cbLimiter := newRateLimiter(callbackPerMinute, time.Minute)
	mux.HandleFunc("POST /api/orders/{token}/pay", d.payOrder)
	mux.HandleFunc("POST /api/payments/sber/callback", func(w http.ResponseWriter, r *http.Request) {
		d.sberCallback(w, r, cbLimiter)
	})
	mux.HandleFunc("GET /api/payments/sber/return", d.sberReturn)
}

func (d Deps) payOrder(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	res, err := d.Payments.Pay(ctx, r.PathValue("token"))
	switch {
	case errors.Is(err, payments.ErrDisabled):
		writeError(w, http.StatusServiceUnavailable, "payment_disabled", "Онлайн-оплата сейчас недоступна")
		return
	case errors.Is(err, orders.ErrOrderNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Заказ не найден")
		return
	case errors.Is(err, payments.ErrNotOnline):
		writeError(w, http.StatusConflict, "not_online", "Этот заказ оплачивается не онлайн")
		return
	case errors.Is(err, payments.ErrNotPayable):
		writeError(w, http.StatusConflict, "not_payable", "Этот заказ нельзя оплатить")
		return
	case errors.Is(err, payments.ErrInProgress):
		writeError(w, http.StatusConflict, "payment_in_progress", "Оплата уже создаётся, подождите")
		return
	case err != nil:
		d.Log.Error("api/pay: не смогли создать оплату", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "Не удалось создать оплату")
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// sberCallback принимает уведомление Сбера, сразу отвечает 200 и запускает перепроверку
// статуса в фоне. Самому callback не верим — всё решает getOrderStatusExtended.
func (d Deps) sberCallback(w http.ResponseWriter, r *http.Request, limiter *rateLimiter) {
	if !limiter.allow(clientIP(r, d.TrustedProxies), time.Now()) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "Слишком много уведомлений")
		return
	}
	// Тело callback маленькое — ограничиваем 16 КБ.
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)

	mdOrder, orderNumber := parseCallbackIDs(r)

	// Отвечаем быстро, чтобы Сбер не копил повторы; работу делаем после ответа.
	w.WriteHeader(http.StatusOK)

	id, found, err := d.Payments.FindPaymentByCallback(r.Context(), mdOrder, orderNumber)
	if err != nil {
		d.Log.Error("api/callback: поиск платежа", "err", err)
		return
	}
	if !found {
		d.Log.Warn("api/callback: платёж не найден", "mdOrder", mdOrder, "orderNumber", orderNumber)
		return
	}

	// Фоновая перепроверка со своим контекстом — запрос уже завершён.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := d.Payments.SyncPayment(ctx, id); err != nil {
			d.Log.Error("api/callback: перепроверка статуса", "payment_id", id, "err", err)
		}
	}()
}

// sberReturn — гость вернулся со страницы оплаты. Перепроверяем статус и ведём на страницу
// заказа во фронтенде.
func (d Deps) sberReturn(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := d.Payments.SyncByOrderToken(ctx, token); err != nil {
		// Не удалось синхронизировать — всё равно ведём гостя на страницу заказа.
		d.Log.Error("api/return: перепроверка статуса", "err", err)
	}

	http.Redirect(w, r, d.PublicBaseURL+"/order/"+token, http.StatusSeeOther)
}

// parseCallbackIDs достаёт mdOrder и orderNumber из callback: из query/form или из JSON.
func parseCallbackIDs(r *http.Request) (mdOrder, orderNumber string) {
	_ = r.ParseForm()
	mdOrder = r.FormValue("mdOrder")
	orderNumber = r.FormValue("orderNumber")
	if mdOrder != "" || orderNumber != "" {
		return mdOrder, orderNumber
	}

	var body struct {
		MdOrder     string `json:"mdOrder"`
		OrderNumber string `json:"orderNumber"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
		return body.MdOrder, body.OrderNumber
	}
	return "", ""
}
