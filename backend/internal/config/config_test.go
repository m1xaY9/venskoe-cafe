package config

import (
	"os"
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

func TestValidateSberProduction(t *testing.T) {
	base := func() {
		t.Setenv("APP_ENV", "production")
		t.Setenv("DATABASE_URL", "postgres://test")
		t.Setenv("SBER_USERNAME", "user")
		t.Setenv("SBER_PASSWORD", "pass")
		// боевой контур + полные реквизиты чека
		t.Setenv("SBER_BASE_URL", "https://epay.sberbank.ru/ecomm/gw/partner/api/v1")
		t.Setenv("SBER_FFD_VERSION", "1.2")
		t.Setenv("SBER_TAX_SYSTEM", "osn")
		t.Setenv("SBER_VAT", "6")
		t.Setenv("SBER_INN", "5027119066")
	}

	t.Run("всё задано — ок", func(t *testing.T) {
		base()
		if _, err := Load(); err != nil {
			t.Fatalf("ждали успех, получили %v", err)
		}
	})

	t.Run("тестовый контур в production — ошибка", func(t *testing.T) {
		base()
		t.Setenv("SBER_BASE_URL", "https://ecomtest.sberbank.ru/ecomm/gw/partner/api/v1")
		if _, err := Load(); err == nil {
			t.Fatal("ждали ошибку: в production нужен epay.sberbank.ru")
		}
	})

	t.Run("нет реквизитов чека — ошибка", func(t *testing.T) {
		base()
		t.Setenv("SBER_INN", "")
		if _, err := Load(); err == nil {
			t.Fatal("ждали ошибку: нет SBER_INN")
		}
	})

	t.Run("НДС 0 (без НДС) в production — ок", func(t *testing.T) {
		base()
		t.Setenv("SBER_VAT", "0")
		if _, err := Load(); err != nil {
			t.Fatalf("VAT=0 (без НДС) — валидное значение: %v", err)
		}
	})

	t.Run("НДС не задан — ошибка", func(t *testing.T) {
		base()
		// t.Setenv не умеет убирать переменную — убираем вручную, cleanup вернёт исходное.
		os.Unsetenv("SBER_VAT")
		if _, err := Load(); err == nil {
			t.Fatal("ждали ошибку: SBER_VAT не задан")
		}
	})

	t.Run("НДС вне диапазона — ошибка", func(t *testing.T) {
		base()
		t.Setenv("SBER_VAT", "99")
		if _, err := Load(); err == nil {
			t.Fatal("ждали ошибку: SBER_VAT=99 вне 0..13")
		}
	})

	t.Run("Сбер выключен — проверок нет", func(t *testing.T) {
		t.Setenv("APP_ENV", "production")
		t.Setenv("DATABASE_URL", "postgres://test")
		t.Setenv("SBER_USERNAME", "")
		t.Setenv("SBER_BASE_URL", "https://ecomtest.sberbank.ru")
		if _, err := Load(); err != nil {
			t.Fatalf("без Сбера проверок быть не должно: %v", err)
		}
	})

	t.Run("локально без реквизитов — ок", func(t *testing.T) {
		t.Setenv("APP_ENV", "local")
		t.Setenv("DATABASE_URL", "postgres://test")
		t.Setenv("SBER_USERNAME", "user")
		t.Setenv("SBER_PASSWORD", "pass")
		t.Setenv("SBER_BASE_URL", "https://ecomtest.sberbank.ru")
		t.Setenv("SBER_FFD_VERSION", "")
		t.Setenv("SBER_TAX_SYSTEM", "")
		t.Setenv("SBER_VAT", "0")
		t.Setenv("SBER_INN", "")
		if _, err := Load(); err != nil {
			t.Fatalf("локально без реквизитов должно стартовать: %v", err)
		}
	})
}
