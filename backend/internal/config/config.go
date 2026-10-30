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

	// Базовый адрес фронтенда для редиректов (например https://venskoe-cafe.ru).
	PublicBaseURL string `env:"PUBLIC_BASE_URL" envDefault:"http://localhost:3000"`

	Sber SberConfig
}

// SberConfig — настройки интернет-эквайринга Сбербанка. Если SBER_USERNAME пуст,
// онлайн-оплата отключена: приложение стартует, но /pay честно отвечает, что оплата
// недоступна.
type SberConfig struct {
	BaseURL  string `env:"SBER_BASE_URL" envDefault:"https://ecomtest.sberbank.ru/ecomm/gw/partner/api/v1"`
	Username string `env:"SBER_USERNAME"`
	Password string `env:"SBER_PASSWORD"`
	// Путь к корневому сертификату НУЦ Минцифры (Russian Trusted Root CA). Файл в репозиторий
	// не кладём, владелец скачивает с gosuslugi.ru/crt.
	CAFile    string `env:"SBER_CA_FILE"`
	ReturnURL string `env:"SBER_RETURN_URL"`
	FailURL   string `env:"SBER_FAIL_URL"`

	// Реквизиты чека 54-ФЗ. Значения уточняются у владельца/кассы.
	FFDVersion string `env:"SBER_FFD_VERSION" envDefault:"1.05"`
	TaxSystem  string `env:"SBER_TAX_SYSTEM" envDefault:"osn"`
	VAT        int    `env:"SBER_VAT" envDefault:"6"`
	INN        string `env:"SBER_INN"`
}

// SberEnabled — настроен ли эквайринг (есть логин).
func (c Config) SberEnabled() bool {
	return c.Sber.Username != ""
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
