package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
)

// Suggestion — одна подсказка адреса: что показать и координаты для карты/зоны.
// Lat/Lon — указатели: если у адреса нет координат, отдаём null, а не 0 (иначе точка
// уехала бы в Гвинейский залив). Precise — координаты до дома (можно сразу считать зону).
type Suggestion struct {
	Value   string   `json:"value"`
	Lat     *float64 `json:"lat"`
	Lon     *float64 `json:"lon"`
	Precise bool     `json:"precise"`
}

// ErrNoAPIKey — ключ DaData не задан. Обработчик превратит это в понятный 503,
// чтобы сайт не падал, если токен забыли положить в .env.
var ErrNoAPIKey = errors.New("delivery: не задан ключ DaData")

const dadataSuggestURL = "https://suggestions.dadata.ru/suggestions/api/4_1/rs/suggest/address"

// maxSuggestions — сколько подсказок максимум просим и отдаём.
const maxSuggestions = 10

// DaData — клиент подсказок адресов. Токен живёт только здесь, в браузер не уходит.
type DaData struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

// NewDaData создаёт клиент. baseURL можно переопределить в тестах (httptest.Server);
// пустой — боевой адрес DaData. client == nil — клиент по умолчанию.
func NewDaData(apiKey, baseURL string, client *http.Client) *DaData {
	if baseURL == "" {
		baseURL = dadataSuggestURL
	}
	if client == nil {
		client = &http.Client{}
	}
	return &DaData{apiKey: apiKey, baseURL: baseURL, client: client}
}

// Suggest запрашивает у DaData подсказки адресов, ограничиваясь городом Чита.
func (d *DaData) Suggest(ctx context.Context, query string) ([]Suggestion, error) {
	if d.apiKey == "" {
		return nil, ErrNoAPIKey
	}

	reqBody, err := json.Marshal(map[string]any{
		"query":     query,
		"count":     maxSuggestions,
		"locations": []map[string]string{{"city": "Чита"}},
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.baseURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Token "+d.apiKey)

	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("delivery: запрос в DaData: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("delivery: DaData вернула код %d", resp.StatusCode)
	}

	var out struct {
		Suggestions []struct {
			Value string `json:"value"`
			Data  struct {
				GeoLat string `json:"geo_lat"`
				GeoLon string `json:"geo_lon"`
				// qc_geo: "0" — координаты до дома, остальное — грубее или их нет.
				QCGeo string `json:"qc_geo"`
			} `json:"data"`
		} `json:"suggestions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("delivery: ответ DaData: %w", err)
	}

	res := make([]Suggestion, 0, len(out.Suggestions))
	for _, s := range out.Suggestions {
		sg := Suggestion{Value: s.Value}
		// DaData отдаёт координаты строками. Если их нет — оставляем Lat/Lon нулевыми
		// указателями (null), чтобы не выдать фальшивые нулевые координаты.
		if s.Data.GeoLat != "" && s.Data.GeoLon != "" {
			lat, errLat := strconv.ParseFloat(s.Data.GeoLat, 64)
			lon, errLon := strconv.ParseFloat(s.Data.GeoLon, 64)
			if errLat == nil && errLon == nil {
				sg.Lat = &lat
				sg.Lon = &lon
				sg.Precise = s.Data.QCGeo == "0"
			}
		}
		res = append(res, sg)
		if len(res) >= maxSuggestions {
			break
		}
	}
	return res, nil
}
