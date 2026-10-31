package sber

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testClient(t *testing.T, log *slog.Logger, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c, err := New(Config{BaseURL: srv.URL, Username: "testUser", Password: "testPass"}, log)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestRegisterSuccess(t *testing.T) {
	c := testClient(t, discardLogger(), func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/register.do") {
			t.Errorf("путь %q, ждали register.do", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"errorCode":"0","errorMessage":"ok","orderId":"abc-123","formUrl":"https://pay/abc"}`))
	})

	resp, err := c.Register(context.Background(), RegisterRequest{OrderNumber: "1001-1", Amount: 19900, ReturnURL: "https://r"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if resp.OrderID != "abc-123" || resp.FormURL != "https://pay/abc" {
		t.Errorf("ответ разобран неверно: %+v", resp)
	}
}

func TestRegisterAlreadyProcessed(t *testing.T) {
	c := testClient(t, discardLogger(), func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"errorCode":"1","errorMessage":"Заказ с таким номером уже обработан"}`))
	})

	_, err := c.Register(context.Background(), RegisterRequest{OrderNumber: "1001-1", Amount: 19900, ReturnURL: "https://r"})
	if !IsAlreadyProcessed(err) {
		t.Fatalf("ждали IsAlreadyProcessed, получили %v", err)
	}
}

func TestGetOrderStatusExtended(t *testing.T) {
	c := testClient(t, discardLogger(), func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"errorCode":"0","orderNumber":"1001-1","orderStatus":2,"amount":19900}`))
	})

	resp, err := c.GetOrderStatusExtended(context.Background(), StatusRequest{OrderID: "abc-123"})
	if err != nil {
		t.Fatalf("GetOrderStatusExtended: %v", err)
	}
	if resp.OrderStatus == nil || *resp.OrderStatus != 2 {
		t.Fatalf("orderStatus: %+v", resp.OrderStatus)
	}
}

func TestPasswordNotLogged(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	c := testClient(t, log, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"errorCode":"0","orderId":"abc","formUrl":"u"}`))
	})

	// Пароль клиента — "testPass"; он не должен попасть в лог ни в запросе, ни в ответе.
	_, err := c.Register(context.Background(), RegisterRequest{OrderNumber: "1001-1", Amount: 19900, ReturnURL: "https://r"})
	if err != nil {
		t.Fatal(err)
	}

	logged := buf.String()
	if logged == "" {
		t.Fatal("ожидали, что запрос/ответ логируются")
	}
	if strings.Contains(logged, "testPass") {
		t.Errorf("пароль попал в лог: %s", logged)
	}
	if strings.Contains(logged, "testUser") {
		t.Errorf("логин попал в лог: %s", logged)
	}
	if !strings.Contains(logged, "register.do") {
		t.Errorf("метод должен логироваться: %s", logged)
	}
}

func TestEmailPhoneMaskedInLog(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	c := testClient(t, log, func(w http.ResponseWriter, r *http.Request) {
		// Ответ тоже содержит email — он должен быть замаскирован в логе.
		_, _ = w.Write([]byte(`{"errorCode":"0","payerData":{"email":"payer@mail.ru"}}`))
	})

	_, err := c.GetOrderStatusExtended(context.Background(), StatusRequest{OrderID: "abc"})
	if err != nil {
		t.Fatal(err)
	}
	// Отдельно проверим маскировку в запросе register (email/phone передаём мы).
	_, _ = c.Register(context.Background(), RegisterRequest{
		OrderNumber: "1001-1", Amount: 19900, ReturnURL: "https://r",
		Email: "customer@testmail.ru", Phone: "+79991234567",
	})

	logged := buf.String()
	for _, secret := range []string{"customer@testmail.ru", "+79991234567", "payer@mail.ru"} {
		if strings.Contains(logged, secret) {
			t.Errorf("в лог попал незамаскированный контакт %q", secret)
		}
	}
	if !strings.Contains(logged, "cu") || !strings.Contains(logged, "ru") {
		t.Errorf("ждали замаскированный email (первые/последние символы): %s", logged)
	}
}

func TestMaskContact(t *testing.T) {
	cases := map[string]string{
		"customer@testmail.ru": "cu****************ru",
		"+79991234567":         "+7********67",
		"ab":                   "**",
		"abcd":                 "****",
	}
	for in, want := range cases {
		if got := maskContact(in); got != want {
			t.Errorf("maskContact(%q) = %q, ждали %q", in, got, want)
		}
	}
}

func TestBuildOrderBundle(t *testing.T) {
	cases := []struct {
		ffd             string
		wantDishObject  string
		wantDelivObject string
		wantMeasure     string
		wantMeasurement string // measurementUnit блюда
	}{
		{"1.05", "commodity", "service", "", "шт."},
		{"1.2", "1", "4", "0", ""},
	}
	for _, tc := range cases {
		t.Run(tc.ffd, func(t *testing.T) {
			c := &Client{
				log:         discardLogger(),
				fiscal:      FiscalConfig{FFDVersion: tc.ffd, TaxSystem: "osn", INN: "123", VAT: 6},
				fiscalReady: true,
			}
			const amount = 78000 + 20000
			bundle := c.BuildOrderBundle([]BundleItem{{ItemCode: "7", Name: "Сырники", PriceKop: 39000, Qty: 2}}, 20000)

			if bundle.FFDVersion != tc.ffd || bundle.ReceiptType != "sell" {
				t.Fatalf("реквизиты чека: %+v", bundle)
			}
			if len(bundle.CartItems.Items) != 2 {
				t.Fatalf("позиций %d, ждали 2", len(bundle.CartItems.Items))
			}

			dish := bundle.CartItems.Items[0]
			if dish.ItemCode != "7" || dish.ItemAmount != 78000 || dish.PaymentObject != tc.wantDishObject {
				t.Errorf("позиция блюда: %+v", dish)
			}
			if dish.Quantity.Measure != tc.wantMeasure || dish.MeasurementUnit != tc.wantMeasurement {
				t.Errorf("единицы блюда: measure=%q unit=%q", dish.Quantity.Measure, dish.MeasurementUnit)
			}

			deliv := bundle.CartItems.Items[1]
			if deliv.ItemCode != "delivery" || deliv.ItemAmount != 20000 || deliv.PaymentObject != tc.wantDelivObject {
				t.Errorf("позиция доставки: %+v", deliv)
			}

			// Сумма позиций должна равняться сумме заказа.
			var sum int64
			for _, it := range bundle.CartItems.Items {
				sum += it.ItemAmount
			}
			if sum != amount {
				t.Errorf("сумма позиций %d, ждали %d", sum, amount)
			}
		})
	}
}

func TestBuildOrderBundleNotConfigured(t *testing.T) {
	c := &Client{log: discardLogger(), fiscalReady: false}
	if bundle := c.BuildOrderBundle([]BundleItem{{Name: "x", PriceKop: 100, Qty: 1}}, 0); bundle != nil {
		t.Errorf("без реквизитов чека orderBundle должен быть nil, получили %+v", bundle)
	}
}
