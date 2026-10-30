package orders

import "testing"

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
