package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"venskoe/backend/internal/payments"
)

type fakePayments struct {
	payResult    payments.PayResult
	payErr       error
	findID       int64
	found        bool
	findErr      error
	syncErr      error
	syncTokenErr error
}

func (f fakePayments) Pay(context.Context, string) (payments.PayResult, error) {
	return f.payResult, f.payErr
}
func (f fakePayments) SyncPayment(context.Context, int64) error { return f.syncErr }
func (f fakePayments) FindPaymentByCallback(context.Context, string, string) (int64, bool, error) {
	return f.findID, f.found, f.findErr
}
func (f fakePayments) SyncByOrderToken(context.Context, string) error { return f.syncTokenErr }

func paymentsDeps(p PaymentService) Deps {
	return Deps{
		Log: discardLogger(), Version: "test",
		DB: fakeDB{}, Menu: fakeMenu{}, Delivery: fakeDelivery{}, Address: fakeAddress{},
		Orders: fakeOrders{}, Payments: p, PublicBaseURL: "http://front",
	}
}

func TestPayOrder(t *testing.T) {
	h := NewHandler(paymentsDeps(fakePayments{payResult: payments.PayResult{FormURL: "https://pay/1"}}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/orders/tok1/pay", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("код ответа %d, ждали 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "https://pay/1") {
		t.Errorf("нет form_url в ответе: %s", rec.Body.String())
	}
}

func TestPayOrderDisabled(t *testing.T) {
	h := NewHandler(paymentsDeps(fakePayments{payErr: payments.ErrDisabled}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/orders/tok1/pay", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("код ответа %d, ждали 503", rec.Code)
	}
}

func TestPayOrderNotPayable(t *testing.T) {
	h := NewHandler(paymentsDeps(fakePayments{payErr: payments.ErrNotPayable}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/orders/tok1/pay", nil))

	if rec.Code != http.StatusConflict {
		t.Fatalf("код ответа %d, ждали 409", rec.Code)
	}
}

func TestPayOrderInProgress(t *testing.T) {
	h := NewHandler(paymentsDeps(fakePayments{payErr: payments.ErrInProgress}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/orders/tok1/pay", nil))

	if rec.Code != http.StatusConflict {
		t.Fatalf("код ответа %d, ждали 409", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "payment_in_progress") {
		t.Errorf("ждали код payment_in_progress: %s", rec.Body.String())
	}
}

func TestSberCallback(t *testing.T) {
	h := NewHandler(paymentsDeps(fakePayments{findID: 7, found: true}))

	body := strings.NewReader("mdOrder=sber-abc&orderNumber=1001-1&operation=deposited&status=1")
	req := httptest.NewRequest(http.MethodPost, "/api/payments/sber/callback", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// Callback всегда отвечает 200 быстро.
	if rec.Code != http.StatusOK {
		t.Fatalf("код ответа %d, ждали 200", rec.Code)
	}
}

func TestCallbackRateLimited(t *testing.T) {
	h := NewHandler(paymentsDeps(fakePayments{}))

	newCb := func() *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/api/payments/sber/callback",
			strings.NewReader("mdOrder=x&orderNumber=y"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = "203.0.113.10:5555"
		return req
	}

	for i := range callbackPerMinute {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, newCb())
		if rec.Code != http.StatusOK {
			t.Fatalf("callback %d: код %d, ждали 200", i+1, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newCb())
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("код %d, ждали 429 после лимита", rec.Code)
	}
}

func TestSberReturnRedirects(t *testing.T) {
	h := NewHandler(paymentsDeps(fakePayments{}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/payments/sber/return?token=tok1", nil))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("код ответа %d, ждали 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "http://front/order/tok1" {
		t.Errorf("редирект на %q, ждали http://front/order/tok1", loc)
	}
}
