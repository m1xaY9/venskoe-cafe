VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GOOSE_VERSION := v3.26.0

# Подгружает переменные из .env, если файл есть.
LOAD_ENV := if [ -f .env ]; then set -a; . ./.env; set +a; fi;

.PHONY: run test lint build fmt db-up db-down db-reset db-seed migrate-new

run: ## запустить API локально (сначала make db-up)
	@$(LOAD_ENV) go run ./cmd/api

# -p 1: пакеты идут по очереди — они делят одну тестовую базу.
test: ## все тесты (-race, последовательно)
	@$(LOAD_ENV) go test -race -count=1 -p 1 ./...

lint: ## статические проверки: форматирование и go vet
	@test -z "$$(gofmt -l .)" || (echo "Не отформатированы файлы:"; gofmt -l .; exit 1)
	@go vet ./...

fmt: ## отформатировать код
	@gofmt -w .

build: ## собрать бинарник в bin/api с версией внутри
	@CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=$(VERSION)" -o bin/api ./cmd/api

db-up: ## поднять PostgreSQL в Docker и дождаться готовности
	@docker compose -f ../docker-compose.yml up -d --wait db

db-down: ## остановить базу (данные сохраняются)
	@docker compose -f ../docker-compose.yml down

db-reset: ## удалить базу со всеми данными и поднять чистую
	@docker compose -f ../docker-compose.yml down -v
	@docker compose -f ../docker-compose.yml up -d --wait db

db-seed: ## залить тестовое меню в ЛОКАЛЬНУЮ базу (сначала запустите make run, чтобы применились миграции)
	@docker compose -f ../docker-compose.yml exec -T db psql -U venskoe -d venskoe -v ON_ERROR_STOP=1 < seed/dev.sql

migrate-new: ## создать файл миграции: make migrate-new name=add_orders
	@test -n "$(name)" || (echo "Укажите имя: make migrate-new name=add_orders"; exit 1)
	@go run github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION) -dir migrations -s create $(name) sql
