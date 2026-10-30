package config

import (
	"fmt"
	"time"

	"github.com/caarlos0/env/v11"
)

type Config struct {
	Env string `env:"APP_ENV" envDefault:"local"`

	HTTPAddr string `env:"HTTP_ADDR" envDefault:":8080"`

	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"10s"`

	DatabaseURL string `env:"DATABASE_URL,required,notEmpty"`

	// Токен DaData для подсказок адреса. Необязателен: без него подсказки просто
	// не работают (обработчик вернёт 503), остальной сайт живёт.
	DaDataAPIKey string `env:"DADATA_API_KEY"`

	// Доверенные прокси (список CIDR через запятую). Если запрос пришёл от такого
	// адреса, IP гостя берём из X-Forwarded-For. По умолчанию пусто — доверяем только
	// самому соединению. Пример: "127.0.0.1/32,10.0.0.0/8".
	TrustedProxies []string `env:"TRUSTED_PROXIES" envSeparator:","`
}

func Load() (Config, error) {
	cfg, err := env.ParseAs[Config]()
	if err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}

	switch cfg.Env {
	case "local", "staging", "production":
	default:
		return Config{}, fmt.Errorf("APP_ENV: неизвестное окружение %q", cfg.Env)
	}

	return cfg, nil
}
