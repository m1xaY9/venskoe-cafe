package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiterSweep(t *testing.T) {
	rl := newRateLimiter(5, time.Minute)
	t0 := time.Now()

	rl.allow("a", t0)
	// Прошло больше окна и пришёл другой ключ — старый ключ «a» должен вычиститься.
	rl.allow("b", t0.Add(2*time.Minute))

	rl.mu.Lock()
	_, hasA := rl.hits["a"]
	n := len(rl.hits)
	rl.mu.Unlock()

	if hasA {
		t.Error("устаревший ключ a должен быть удалён")
	}
	if n != 1 {
		t.Errorf("в карте %d ключей, ждали 1", n)
	}
}

func TestRateLimiterLimit(t *testing.T) {
	rl := newRateLimiter(2, time.Minute)
	now := time.Now()
	for i := range 2 {
		if !rl.allow("x", now) {
			t.Fatalf("запрос %d должен пройти", i+1)
		}
	}
	if rl.allow("x", now) {
		t.Error("третий запрос должен быть отклонён")
	}
}

func TestClientIP(t *testing.T) {
	trusted, err := ParseCIDRs([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}

	// Запрос от доверенного прокси: берём самый правый недоверенный адрес из XFF.
	req := httptest.NewRequest(http.MethodPost, "/api/orders", nil)
	req.RemoteAddr = "10.0.0.5:4567"
	req.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.1")
	if got := clientIP(req, trusted); got != "203.0.113.7" {
		t.Errorf("clientIP = %q, ждали 203.0.113.7", got)
	}

	// Запрос от недоверенного адреса: XFF игнорируем, берём адрес соединения.
	req2 := httptest.NewRequest(http.MethodPost, "/api/orders", nil)
	req2.RemoteAddr = "8.8.8.8:1234"
	req2.Header.Set("X-Forwarded-For", "1.1.1.1")
	if got := clientIP(req2, trusted); got != "8.8.8.8" {
		t.Errorf("clientIP = %q, ждали 8.8.8.8", got)
	}

	// Без доверенных прокси всегда адрес соединения.
	req3 := httptest.NewRequest(http.MethodPost, "/api/orders", nil)
	req3.RemoteAddr = "10.0.0.5:4567"
	req3.Header.Set("X-Forwarded-For", "203.0.113.7")
	if got := clientIP(req3, nil); got != "10.0.0.5" {
		t.Errorf("clientIP = %q, ждали 10.0.0.5", got)
	}
}

func TestIsUUID(t *testing.T) {
	cases := map[string]bool{
		"11111111-1111-1111-1111-111111111111": true,
		"A1B2C3D4-0000-0000-0000-000000000000": true,
		"":                                     false,
		"not-a-uuid":                           false,
		"11111111-1111-1111-1111-11111111111":  false, // короче
		"11111111x1111-1111-1111-111111111111": false, // нет дефиса
	}
	for in, want := range cases {
		if got := isUUID(in); got != want {
			t.Errorf("isUUID(%q) = %v, ждали %v", in, got, want)
		}
	}
}
