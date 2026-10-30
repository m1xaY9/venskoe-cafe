package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	// tzdata встраивает базу часовых поясов в бинарник: в distroless-образе её нет,
	// а нам нужен Asia/Chita для расчёта часов работы.
	_ "time/tzdata"

	"venskoe/backend/internal/config"
	"venskoe/backend/internal/db"
	"venskoe/backend/internal/delivery"
	"venskoe/backend/internal/httpapi"
	"venskoe/backend/internal/menu"
	"venskoe/backend/internal/orders"
	"venskoe/backend/internal/payments"
	"venskoe/backend/internal/sber"
	"venskoe/backend/migrations"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		slog.Error("сервер остановлен с ошибкой", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// Часы работы, «сегодня» и сводки считаем в часовом поясе Читы.
	chita, err := time.LoadLocation("Asia/Chita")
	if err != nil {
		return fmt.Errorf("не удалось загрузить часовой пояс Asia/Chita: %w", err)
	}

	var log *slog.Logger
	if cfg.Env == "local" {
		log = slog.New(slog.NewTextHandler(os.Stdout, nil))
	} else {
		log = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool, migrations.FS); err != nil {
		return err
	}
	log.Info("база готова, миграции применены")

	trustedProxies, err := httpapi.ParseCIDRs(cfg.TrustedProxies)
	if err != nil {
		return fmt.Errorf("TRUSTED_PROXIES: %w", err)
	}

	deliverySvc := delivery.NewService(delivery.NewStore(pool), chita)

	// Клиент Сбера создаём только если эквайринг настроен (есть логин). Иначе онлайн-оплата
	// выключена, остальной сайт работает.
	sberEnabled := cfg.SberEnabled()
	var sberClient payments.SberClient
	if sberEnabled {
		client, err := sber.New(sber.Config{
			BaseURL:  cfg.Sber.BaseURL,
			Username: cfg.Sber.Username,
			Password: cfg.Sber.Password,
			CAFile:   cfg.Sber.CAFile,
			Fiscal: sber.FiscalConfig{
				FFDVersion: cfg.Sber.FFDVersion,
				TaxSystem:  cfg.Sber.TaxSystem,
				INN:        cfg.Sber.INN,
				VAT:        cfg.Sber.VAT,
			},
		}, log)
		if err != nil {
			return err
		}
		sberClient = client
	} else {
		log.Warn("Сбер не настроен (нет SBER_USERNAME): онлайн-оплата отключена")
	}
	paymentsSvc := payments.NewService(pool, sberClient, cfg.Sber.ReturnURL, cfg.Sber.FailURL, cfg.PublicBaseURL, sberEnabled)

	srv := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: httpapi.NewHandler(httpapi.Deps{
			Log:            log,
			Version:        version,
			DB:             pool,
			Menu:           menu.NewStore(pool),
			Delivery:       deliverySvc,
			Address:        delivery.NewDaData(cfg.DaDataAPIKey, "", nil),
			Orders:         orders.NewService(pool, deliverySvc),
			Payments:       paymentsSvc,
			TrustedProxies: trustedProxies,
			PublicBaseURL:  cfg.PublicBaseURL,
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Фоновые задачи оплаты: сверка статусов и автоотмена неоплаченных. Запускаются только
	// при настроенном Сбере и останавливаются вместе с сервером по SIGTERM.
	var wg sync.WaitGroup
	if sberEnabled {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runPaymentJobs(ctx, log, paymentsSvc)
		}()
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("API запущен", "addr", cfg.HTTPAddr, "env", cfg.Env, "version", version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	log.Info("останавливаемся", "timeout", cfg.ShutdownTimeout)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	err = srv.Shutdown(shutdownCtx)
	wg.Wait() // дожидаемся остановки фоновых задач (ctx уже отменён сигналом)
	return err
}

// runPaymentJobs раз в минуту сверяет статусы оплат и отменяет неоплаченные заказы.
// Завершается, когда ctx отменён (SIGTERM).
func runPaymentJobs(ctx context.Context, log *slog.Logger, svc *payments.Service) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := svc.ReconcilePending(ctx); err != nil && ctx.Err() == nil {
				log.Error("фоновая сверка оплат", "err", err)
			}
			if err := svc.AutoCancel(ctx, time.Now()); err != nil && ctx.Err() == nil {
				log.Error("автоотмена неоплаченных", "err", err)
			}
		}
	}
}
