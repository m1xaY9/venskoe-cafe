package sber

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

// Config — настройки клиента Сбера.
type Config struct {
	BaseURL  string
	Username string
	Password string
	CAFile   string
	Fiscal   FiscalConfig
	// FiscalReady — заданы ли реквизиты чека (решает вызывающий код, т.к. VAT=0 валиден).
	FiscalReady bool
}

// FiscalConfig — реквизиты чека 54-ФЗ из настроек.
type FiscalConfig struct {
	FFDVersion string
	TaxSystem  string // sno
	INN        string
	VAT        int // taxType
}

// Client — клиент платёжного шлюза Сбера.
type Client struct {
	baseURL     string
	username    string
	password    string
	http        *http.Client
	log         *slog.Logger
	fiscal      FiscalConfig
	fiscalReady bool
}

// New создаёт клиент. HTTP-таймаут 15 с, TLS доверяет системным сертификатам плюс
// корневому сертификату НУЦ Минцифры из CAFile.
func New(cfg Config, log *slog.Logger) (*Client, error) {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("sber: чтение SBER_CA_FILE: %w", err)
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("sber: не удалось добавить сертификат из %s", cfg.CAFile)
		}
	}

	httpClient := &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		},
	}

	return &Client{
		baseURL:     cfg.BaseURL,
		username:    cfg.Username,
		password:    cfg.Password,
		http:        httpClient,
		log:         log,
		fiscal:      cfg.Fiscal,
		fiscalReady: cfg.FiscalReady,
	}, nil
}

// do отправляет POST-запрос method (например "register.do") и разбирает ответ в out.
// В лог пишет запрос без userName/password и ответ целиком.
func (c *Client) do(ctx context.Context, method string, reqBody, out any) error {
	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("sber: сериализация запроса: %w", err)
	}

	c.log.Info("sber запрос", "method", method, "body", sanitizeForLog(body))

	url := c.baseURL + "/" + method
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("sber: создание запроса: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("sber: %s: %w", method, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("sber: чтение ответа %s: %w", method, err)
	}

	c.log.Info("sber ответ", "method", method, "status", resp.StatusCode, "body", sanitizeForLog(respBody))

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sber: %s: код ответа %d", method, resp.StatusCode)
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("sber: разбор ответа %s: %w", method, err)
	}
	return nil
}

// sanitizeForLog готовит JSON к записи в лог: убирает userName/password и маскирует
// email и phone на любом уровне вложенности.
func sanitizeForLog(raw []byte) string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "<не разбирается>"
	}
	out, err := json.Marshal(sanitize(v))
	if err != nil {
		return "<не сериализуется>"
	}
	return string(out)
}

func sanitize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			switch strings.ToLower(k) {
			case "username", "password":
				delete(t, k)
			case "email", "phone":
				if s, ok := val.(string); ok {
					t[k] = maskContact(s)
				}
			default:
				t[k] = sanitize(val)
			}
		}
		return t
	case []any:
		for i := range t {
			t[i] = sanitize(t[i])
		}
		return t
	}
	return v
}

// maskContact оставляет первые 2 и последние 2 символа, остальное заменяет на звёздочки.
func maskContact(s string) string {
	r := []rune(s)
	if len(r) <= 4 {
		return strings.Repeat("*", len(r))
	}
	return string(r[:2]) + strings.Repeat("*", len(r)-4) + string(r[len(r)-2:])
}
