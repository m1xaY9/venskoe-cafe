package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
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

	deliverySvc := delivery.NewService(delivery.NewStore(pool), chita)

	srv := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: httpapi.NewHandler(httpapi.Deps{
			Log:      log,
			Version:  version,
			DB:       pool,
			Menu:     menu.NewStore(pool),
			Delivery: deliverySvc,
			Address:  delivery.NewDaData(cfg.DaDataAPIKey, "", nil),
			Orders:   orders.NewService(pool, deliverySvc),
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
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
	return srv.Shutdown(shutdownCtx)
}
