# Короткие команды для частых действий. Запуск: make <цель>, например make test.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: run test lint build fmt

run: ## запустить API локально
	@go run ./cmd/api

test: ## все тесты; -race ищет гонки данных между горутинами
	@go test -race -count=1 ./...

lint: ## статические проверки: форматирование и go vet
	@test -z "$$(gofmt -l .)" || (echo "Не отформатированы файлы:"; gofmt -l .; exit 1)
	@go vet ./...

fmt: ## отформатировать код
	@gofmt -w .

build: ## собрать бинарник в bin/api с версией внутри
	@CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=$(VERSION)" -o bin/api ./cmd/api
