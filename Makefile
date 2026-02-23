GO ?= go
PKG ?= ./...
APP_BIN ?= bin/deadliner_bot
COMPOSE_FILE ?= docker-compose.yml
DC ?= docker compose -f $(COMPOSE_FILE)

.PHONY: help fmt test test-race vet tidy run build clean \
        compose-up compose-up-build compose-down compose-restart \
        compose-logs compose-ps compose-build compose-pull

help: ## Show available commands
	@grep -E '^[a-zA-Z0-9_.-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "%-20s %s\n", $$1, $$2}'

fmt: ## Format Go code
	$(GO) fmt $(PKG)

test: ## Run tests
	$(GO) test $(PKG)

test-race: ## Run tests with race detector
	$(GO) test -race $(PKG)

vet: ## Run go vet
	$(GO) vet $(PKG)

tidy: ## Tidy module dependencies
	$(GO) mod tidy

run: ## Run bot locally
	$(GO) run ./cmd/bot

build: ## Build binary
	$(GO) build -o $(APP_BIN) ./cmd/bot

clean: ## Remove build artifacts
	@rm -f $(APP_BIN)

compose-up: ## Start services in detached mode
	$(DC) up -d

compose-up-build: ## Rebuild and start services
	$(DC) up -d --build

compose-down: ## Stop and remove services
	$(DC) down

compose-restart: ## Restart all services
	$(DC) restart

compose-logs: ## Tail service logs
	$(DC) logs -f --tail=100

compose-ps: ## Show service status
	$(DC) ps

compose-build: ## Build compose services
	$(DC) build

compose-pull: ## Pull service images
	$(DC) pull
