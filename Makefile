SHELL         := /usr/bin/env bash
.SHELLFLAGS   := -eu -o pipefail -c
.DEFAULT_GOAL := help
.DELETE_ON_ERROR:

# ---------------------------------------------------------------------------
# Параметры (любой можно переопределить: make build BINARY=foo)
# ---------------------------------------------------------------------------
BINARY      ?= evnav-api
CMD         ?= ./cmd/api
BIN_DIR     ?= bin
PKG         ?= ./...
COVER_FILE  ?= coverage.out
COVER_HTML  ?= coverage.html
ENV_FILE    ?= .env
API_URL     ?= http://localhost:8080
MIGRATIONS  ?= migrations
IMAGE       ?= evnavigation
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

GO ?= go

# Инструменты запускаются через `go run pkg@version`: ничего ставить заранее
# не нужно, версия зафиксирована, собираются тем же Go, что и проект.
GOLANGCI_VER    ?= v2.12.2
MIGRATE_VER     ?= v4.20.1
GOVULNCHECK_VER ?= latest

GOLANGCI_LINT := $(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VER)
MIGRATE       := $(GO) run -tags 'pgx5' github.com/golang-migrate/migrate/v4/cmd/migrate@$(MIGRATE_VER)
GOVULNCHECK   := $(GO) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VER)

# Подхватить .env (если есть) для команд, которым нужны переменные окружения.
WITH_ENV := set -a; [ -f $(ENV_FILE) ] && . ./$(ENV_FILE); set +a;

# URL базы для migrate CLI: те же переменные и дефолты, что в internal/config.
DB_URL = pgx5://$${DB_USER:-postgres}:$${DB_PASSWORD:-evnav_dev_pass}@$${DB_HOST:-localhost}:$${DB_PORT:-5432}/$${DB_NAME:-evnavigation}?sslmode=$${DB_SSLMODE:-disable}

.PHONY: help
help: ## Показать все команды
	@awk 'BEGIN {FS = ":.*##"; printf "Использование: make \033[36m<цель>\033[0m\n"} \
		/^[a-zA-Z0-9_-]+:.*##/ { printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2 } \
		/^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) }' $(MAKEFILE_LIST)

##@ Главное

.PHONY: check
check: tidy-check fmt-check lint test ## Всё, что должно пройти перед коммитом (то же гоняет CI)

.PHONY: run
run: ## Запустить сервис локально (подхватит .env; БД: make db-up)
	@$(WITH_ENV) $(GO) run $(CMD)

.PHONY: build
build: ## Собрать статический бинарник в bin/
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o $(BIN_DIR)/$(BINARY) $(CMD)

##@ Тесты и качество кода

.PHONY: test
test: ## Тесты под -race с покрытием
	$(GO) test -race -shuffle=on -covermode=atomic -coverprofile=$(COVER_FILE) $(PKG)

.PHONY: test-fast
test-fast: ## Быстрый прогон без -race и покрытия
	$(GO) test -shuffle=on $(PKG)

.PHONY: test-integration
test-integration: ## Интеграционные тесты (файлы с //go:build integration, нужен Docker)
	$(GO) test -race -tags=integration -count=1 $(PKG)

.PHONY: race
race: ## Пять прогонов под -race: гонки любят прятаться
	$(GO) test -race -count=5 $(PKG)

.PHONY: cover
cover: test ## Открыть отчёт о покрытии в HTML
	$(GO) tool cover -html=$(COVER_FILE) -o $(COVER_HTML)
	@echo "Отчёт: $(COVER_HTML)"

.PHONY: cover-func
cover-func: test ## Покрытие по функциям и итоговый процент
	$(GO) tool cover -func=$(COVER_FILE)

.PHONY: lint
lint: ## Линтер golangci-lint (go vet входит в него)
	$(GOLANGCI_LINT) run

.PHONY: lint-fix
lint-fix: ## Линтер с автоисправлением
	$(GOLANGCI_LINT) run --fix

.PHONY: vuln
vuln: ## Поиск известных уязвимостей в зависимостях и stdlib (нужна сеть)
	$(GOVULNCHECK) $(PKG)

.PHONY: fmt
fmt: ## Отформатировать код
	gofmt -s -w .

.PHONY: fmt-check
fmt-check: ## Проверить форматирование, ничего не меняя
	@files=$$(gofmt -s -l .); \
	if [ -n "$$files" ]; then \
		echo "Не отформатировано (запустите make fmt):"; echo "$$files"; exit 1; \
	fi

.PHONY: tidy
tidy: ## Привести go.mod/go.sum в порядок
	$(GO) mod tidy

.PHONY: tidy-check
tidy-check: ## Проверить, что go.mod/go.sum актуальны
	@$(GO) mod tidy -diff >/dev/null || { \
		echo "go.mod/go.sum устарели — запустите make tidy"; exit 1; }

##@ База данных и миграции

.PHONY: db-up
db-up: ## Поднять только PostgreSQL+PostGIS и дождаться healthcheck
	docker compose up -d --wait postgres

.PHONY: db-shell
db-shell: ## Открыть psql в контейнере с БД
	@$(WITH_ENV) docker compose exec postgres psql -U "$${DB_USER:-postgres}" -d "$${DB_NAME:-evnavigation}"

.PHONY: migrate-up
migrate-up: ## Применить все новые миграции
	@$(WITH_ENV) $(MIGRATE) -path $(MIGRATIONS) -database "$(DB_URL)" up

.PHONY: migrate-down
migrate-down: ## Откатить ОДНУ последнюю миграцию
	@$(WITH_ENV) $(MIGRATE) -path $(MIGRATIONS) -database "$(DB_URL)" down 1

.PHONY: migrate-version
migrate-version: ## Показать текущую версию схемы
	@$(WITH_ENV) $(MIGRATE) -path $(MIGRATIONS) -database "$(DB_URL)" version

.PHONY: migrate-force
migrate-force: ## Снять флаг dirty: make migrate-force version=N
	@test -n "$(version)" || { echo "Использование: make migrate-force version=N"; exit 1; }
	@$(WITH_ENV) $(MIGRATE) -path $(MIGRATIONS) -database "$(DB_URL)" force $(version)

.PHONY: migrate-new
migrate-new: ## Создать пару миграций: make migrate-new name=add_something
	@test -n "$(name)" || { echo "Использование: make migrate-new name=add_something"; exit 1; }
	$(MIGRATE) create -ext sql -dir $(MIGRATIONS) -seq -digits 6 $(name)

##@ Docker

.PHONY: docker-build
docker-build: ## Собрать образ приложения
	docker build -t $(IMAGE):$(VERSION) -t $(IMAGE):latest .

.PHONY: up
up: ## Поднять всё (БД + приложение) одной командой
	docker compose up --build -d --wait

.PHONY: down
down: ## Остановить контейнеры (данные БД сохраняются)
	docker compose down

.PHONY: logs
logs: ## Логи приложения
	docker compose logs -f --tail=100 app

.PHONY: ps
ps: ## Статус контейнеров
	docker compose ps

.PHONY: docker-reset
docker-reset: ## УДАЛИТЬ контейнеры вместе с данными БД (volume pgdata)
	docker compose down -v

##@ Проверка работающего API

.PHONY: smoke
smoke: ## Сквозная проверка API: регистрация -> логин -> машина -> маршрут (scripts/smoke.sh)
	@API_URL=$(API_URL) ./scripts/smoke.sh

##@ Прочее

.PHONY: tools
tools: ## Заранее скачать и собрать инструменты (lint, migrate, govulncheck)
	$(GOLANGCI_LINT) version
	$(MIGRATE) -version
	$(GOVULNCHECK) -version

.PHONY: clean
clean: ## Удалить сборку и отчёты
	rm -rf $(BIN_DIR) $(COVER_FILE) $(COVER_HTML)