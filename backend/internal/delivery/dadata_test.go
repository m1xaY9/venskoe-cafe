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
	// Первая подсказка — с координатами до дома (qc_geo "0"), вторая — улица без координат.
	const response = `{"suggestions":[
		{"value":"г Чита, ул Ленина, д 1","data":{"geo_lat":"52.033","geo_lon":"113.501","qc_geo":"0"}},
		{"value":"г Чита, ул Ленина","data":{"geo_lat":"","geo_lon":"","qc_geo":"5"}}
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

	// Дом: координаты есть, precise=true.
	first := got[0]
	if first.Value != "г Чита, ул Ленина, д 1" || first.Lat == nil || first.Lon == nil {
		t.Fatalf("первая подсказка разобрана неверно: %+v", first)
	}
	if *first.Lat != 52.033 || *first.Lon != 113.501 || !first.Precise {
		t.Errorf("координаты/precise первой подсказки неверны: lat=%v lon=%v precise=%v", *first.Lat, *first.Lon, first.Precise)
	}

	// Улица без координат: Lat/Lon = nil, precise=false.
	second := got[1]
	if second.Lat != nil || second.Lon != nil || second.Precise {
		t.Errorf("подсказка без координат: ждали lat/lon=null и precise=false, получили %+v", second)
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
