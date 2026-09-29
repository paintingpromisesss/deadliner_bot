.PHONY: web build test up fmt clean

GO ?= go
NPM ?= npm

# Сборка TMA и укладка бандла туда, откуда его берёт //go:embed
# (internal/platform/tma/dist). Каталог пересоздаётся целиком, иначе stale-ассеты
# прошлых сборок останутся в бинарнике.
web:
	cd web && $(NPM) ci && $(NPM) run build
	rm -rf internal/platform/tma/dist
	cp -r web/dist internal/platform/tma/dist

# Бинарник в bin/ (каталог в .gitignore) — не мусорим в корне репозитория.
build:
	$(GO) build -o bin/deadliner ./cmd/deadliner

test:
	$(GO) test ./... -count=1
	cd web && $(NPM) run test

up:
	docker compose up --build -d

fmt:
	gofmt -w .

clean:
	rm -rf internal/platform/tma/dist web/dist bin