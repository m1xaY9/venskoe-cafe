package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	
	t.Setenv("APP_ENV", "")
	t.Setenv("HTTP_ADDR", "")
	t.Setenv("SHUTDOWN_TIMEOUT", "")
	t.Setenv("DATABASE_URL", "postgres://test")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() вернул ошибку: %v", err)
	}
	if cfg.Env != "local" || cfg.HTTPAddr != ":8080" || cfg.ShutdownTimeout != 10*time.Second {
		t.Errorf("неожиданные значения по умолчанию: %+v", cfg)
	}
}

func TestLoadShutdownTimeout(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{in: "15s", want: 15 * time.Second},
		{in: "20", wantErr: true},
		{in: "1m", want: time.Minute},
		{in: "абв", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Setenv("APP_ENV", "local")
			t.Setenv("DATABASE_URL", "postgres://test")
			t.Setenv("SHUTDOWN_TIMEOUT", tc.in)

			cfg, err := Load()
			if tc.wantErr {
				if err == nil {
					t.Fatal("ожидали ошибку, а её нет")
				}
				return
			}
			if err != nil {
				t.Fatalf("неожиданная ошибка: %v", err)
			}
			if cfg.ShutdownTimeout != tc.want {
				t.Errorf("получили %v, ждали %v", cfg.ShutdownTimeout, tc.want)
			}
		})
	}
}

func TestLoadRejectsUnknownEnv(t *testing.T) {
	t.Setenv("APP_ENV", "prod") // опечатка: правильно "production"
	t.Setenv("DATABASE_URL", "postgres://test")
	if _, err := Load(); err == nil {
		t.Fatal("ожидали ошибку на неизвестное окружение")
	}
}

func TestLoadRequiresDatabaseURL(t *testing.T) {
	t.Setenv("APP_ENV", "local")
	t.Setenv("DATABASE_URL", "")
	if _, err := Load(); err == nil {
		t.Fatal("ожидали ошибку: DATABASE_URL обязателен")
	}
}
