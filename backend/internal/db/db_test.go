package db

import (
	"context"
	"os"
	"testing"

	"venskoe/backend/migrations"
)

func TestMigrate(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL не задан, пропускаем тест с базой")
	}
	ctx := context.Background()

	pool, err := Open(ctx, url)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()

	for i := range 2 {
		if err := Migrate(ctx, pool, migrations.FS); err != nil {
			t.Fatalf("Migrate (запуск %d): %v", i+1, err)
		}
	}

	for _, table := range []string{"categories", "dishes"} {
		var exists bool
		err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = $1)`,
			table,
		).Scan(&exists)
		if err != nil {
			t.Fatalf("проверка таблицы %s: %v", table, err)
		}
		if !exists {
			t.Errorf("таблица %s не создана", table)
		}
	}
}

func TestOpenBadURL(t *testing.T) {
	_, err := Open(context.Background(), "postgres://x:x@127.0.0.1:1/x?connect_timeout=1")
	if err == nil {
		t.Fatal("ожидали ошибку подключения")
	}
}
