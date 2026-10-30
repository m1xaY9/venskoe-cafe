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

func TestBuildOrderBundle(t *testing.T) {
	c := &Client{fiscal: FiscalConfig{FFDVersion: "1.05", TaxSystem: "osn", INN: "123", VAT: 6}}
	bundle := c.BuildOrderBundle([]BundleItem{{Name: "Сырники", PriceKop: 39000, Qty: 2}}, 20000)

	if bundle.FFDVersion != "1.05" || bundle.ReceiptType != "sell" {
		t.Fatalf("реквизиты чека: %+v", bundle)
	}
	if len(bundle.CartItems.Items) != 2 { // блюдо + доставка
		t.Fatalf("позиций %d, ждали 2", len(bundle.CartItems.Items))
	}
	dish := bundle.CartItems.Items[0]
	if dish.ItemAmount != 78000 || dish.Tax.TaxType != 6 || dish.PaymentObject != "commodity" {
		t.Errorf("позиция блюда: %+v", dish)
	}
	delivery := bundle.CartItems.Items[1]
	if delivery.Name != "Доставка" || delivery.ItemAmount != 20000 || delivery.PaymentObject != "service" {
		t.Errorf("позиция доставки: %+v", delivery)
	}
}
