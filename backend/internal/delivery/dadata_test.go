package delivery

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDaDataSuggest(t *testing.T) {
	const response = `{"suggestions":[
		{"value":"г Чита, ул Ленина, д 1","data":{"geo_lat":"52.033","geo_lon":"113.501"}},
		{"value":"г Чита, ул Ленина, д 2","data":{"geo_lat":"52.034","geo_lon":"113.502"}}
	]}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Token test-key" {
			t.Errorf("Authorization = %q, ждали Token test-key", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(response))
	}))
	defer srv.Close()

	client := NewDaData("test-key", srv.URL, srv.Client())
	got, err := client.Suggest(context.Background(), "Чита Ленина")
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("подсказок %d, ждали 2", len(got))
	}
	if got[0].Value != "г Чита, ул Ленина, д 1" || got[0].Lat != 52.033 || got[0].Lon != 113.501 {
		t.Errorf("первая подсказка разобрана неверно: %+v", got[0])
	}
}

func TestDaDataSuggestNoKey(t *testing.T) {
	client := NewDaData("", "", nil)
	_, err := client.Suggest(context.Background(), "Чита")
	if !errors.Is(err, ErrNoAPIKey) {
		t.Fatalf("без ключа ждали ErrNoAPIKey, получили %v", err)
	}
}

func TestDaDataSuggestServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := NewDaData("test-key", srv.URL, srv.Client())
	_, err := client.Suggest(context.Background(), "Чита")
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("ждали ошибку с кодом 500, получили %v", err)
	}
}
