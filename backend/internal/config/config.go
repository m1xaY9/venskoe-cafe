package config

import (
	"fmt"
	"strings"
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

	// Реквизиты чека 54-ФЗ. Без значений по умолчанию: в production при включённом Сбере
	// обязательны, локально без них просто не передаём orderBundle.
	FFDVersion string `env:"SBER_FFD_VERSION"`
	TaxSystem  string `env:"SBER_TAX_SYSTEM"`
	// VAT — тип НДС (taxType, тег ФФД 1199). Указатель, чтобы отличить «не задано» (nil)
	// от валидного 0 («без НДС»). Допустимы значения 0..13 (см. docs/sber-ecom-api.yaml).
	VAT *int   `env:"SBER_VAT"`
	INN string `env:"SBER_INN"`
}

// maxTaxType — верхняя граница taxType по спецификации (0 = без НДС ... 13).
const maxTaxType = 13

// SberEnabled — настроен ли эквайринг (есть логин).
func (c Config) SberEnabled() bool {
	return c.Sber.Username != ""
}

// FiscalReady — заданы ли все реквизиты чека 54-ФЗ (VAT считается заданным, если не nil,
// в том числе 0 — «без НДС»).
func (c Config) FiscalReady() bool {
	return c.Sber.FFDVersion != "" && c.Sber.TaxSystem != "" && c.Sber.INN != "" && c.Sber.VAT != nil
}

// VATValue возвращает тип НДС (0, если не задан — используется только при FiscalReady).
func (c Config) VATValue() int {
	if c.Sber.VAT == nil {
		return 0
	}
	return *c.Sber.VAT
}

// validateSber проверяет настройки Сбера: диапазон НДС всегда, а в production при включённом
// Сбере — боевой адрес и полноту реквизитов чека.
func (c Config) validateSber() error {
	if c.Sber.VAT != nil && (*c.Sber.VAT < 0 || *c.Sber.VAT > maxTaxType) {
		return fmt.Errorf("SBER_VAT должен быть 0..%d (taxType), а не %d", maxTaxType, *c.Sber.VAT)
	}

	if c.Env != "production" || !c.SberEnabled() {
		return nil
	}
	if !strings.HasPrefix(c.Sber.BaseURL, "https://epay.sberbank.ru") {
		return fmt.Errorf("SBER_BASE_URL в production должен начинаться с https://epay.sberbank.ru, а не %q", c.Sber.BaseURL)
	}
	var missing []string
	if c.Sber.FFDVersion == "" {
		missing = append(missing, "SBER_FFD_VERSION")
	}
	if c.Sber.TaxSystem == "" {
		missing = append(missing, "SBER_TAX_SYSTEM")
	}
	if c.Sber.VAT == nil {
		missing = append(missing, "SBER_VAT")
	}
	if c.Sber.INN == "" {
		missing = append(missing, "SBER_INN")
	}
	if len(missing) > 0 {
		return fmt.Errorf("в production при включённом Сбере обязательны: %s", strings.Join(missing, ", "))
	}
	return nil
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

	if err := cfg.validateSber(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}
