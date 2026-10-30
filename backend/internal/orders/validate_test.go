package orders

import (
	"testing"
	"time"
)

func TestWhenTimingOK(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		when time.Time
		want bool
	}{
		{"за 29 минут — рано", now.Add(29 * time.Minute), false},
		{"через 31 минуту — ок", now.Add(31 * time.Minute), true},
		{"почти через 7 дней — ок", now.Add(7*24*time.Hour - time.Minute), true},
		{"позже 7 дней — нельзя", now.Add(7*24*time.Hour + time.Minute), false},
		{"в прошлом — нельзя", now.Add(-time.Hour), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := whenTimingOK(tc.when, now); got != tc.want {
				t.Errorf("whenTimingOK = %v, ждали %v", got, tc.want)
			}
		})
	}
}

func hasCode(verrs ValidationErrors, code string) bool {
	for _, fe := range verrs {
		if fe.Code == code {
			return true
		}
	}
	return false
}

func TestValidateFieldsTrimAndAddress(t *testing.T) {
	lat, lon := 52.0, 113.5

	// Доставка без адреса (одни пробелы) → invalid_address, имя обрезается.
	in := CreateInput{
		Fulfillment: FulfillmentDelivery, PaymentMethod: PaymentOnDelivery,
		Name: "  Иван  ", Phone: "89991234567", Address: "   ",
		Lat: &lat, Lon: &lon, Items: []ItemInput{{DishID: 1, Qty: 1}},
	}
	verrs := validateFields(&in)
	if in.Name != "Иван" {
		t.Errorf("имя не обрезано: %q", in.Name)
	}
	if !hasCode(verrs, "invalid_address") {
		t.Errorf("ждали invalid_address, получили %+v", verrs)
	}

	// Адрес с пробелами по краям сохраняется обрезанным, ошибки нет.
	in2 := in
	in2.Address = "  ул. Ленина, 1  "
	verrs2 := validateFields(&in2)
	if in2.Address != "ул. Ленина, 1" {
		t.Errorf("адрес не обрезан: %q", in2.Address)
	}
	if hasCode(verrs2, "invalid_address") {
		t.Errorf("не ждали invalid_address: %+v", verrs2)
	}

	// Для самовывоза адрес не обязателен.
	pickup := CreateInput{
		Fulfillment: FulfillmentPickup, PaymentMethod: PaymentOnDelivery,
		Name: "Иван", Phone: "89991234567", Items: []ItemInput{{DishID: 1, Qty: 1}},
	}
	if hasCode(validateFields(&pickup), "invalid_address") {
		t.Error("для самовывоза адрес не должен требоваться")
	}
}

func TestSubtotalAndLineTotal(t *testing.T) {
	items := []pricedItem{
		{PriceKop: 39000, Qty: 2}, // 78000
		{PriceKop: 22000, Qty: 1}, // 22000
		{PriceKop: 15000, Qty: 3}, // 45000
	}
	if got := items[0].lineTotal(); got != 78000 {
		t.Errorf("lineTotal = %d, ждали 78000", got)
	}
	if got := subtotal(items); got != 145000 {
		t.Errorf("subtotal = %d, ждали 145000", got)
	}
	if got := subtotal(nil); got != 0 {
		t.Errorf("subtotal пустого заказа = %d, ждали 0", got)
	}
}

func TestNormalizePhone(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"8 (999) 123-45-67", "+79991234567", true},
		{"+7 999 123 45 67", "+79991234567", true},
		{"79991234567", "+79991234567", true},
		{"9991234567", "+79991234567", true},
		{"123", "", false},
		{"+7999", "", false},
		{"", "", false},
		{"8999123456789", "", false}, // слишком длинный
	}
	for _, tc := range cases {
		got, ok := normalizePhone(tc.in)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("normalizePhone(%q) = (%q, %v); ждали (%q, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestValidEmail(t *testing.T) {
	cases := map[string]bool{
		"guest@example.com": true,
		"a.b+c@mail.ru":     true,
		"":                  false,
		"not-an-email":      false,
		"Имя <a@b.ru>":      false,
	}
	for in, want := range cases {
		if got := validEmail(in); got != want {
			t.Errorf("validEmail(%q) = %v, ждали %v", in, got, want)
		}
	}
}
