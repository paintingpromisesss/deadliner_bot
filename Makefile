.PHONY: web build test up fmt clean serve dev-url

GO ?= go
NPM ?= npm

# .env читает только docker compose (env_file): config.Load берёт переменные
# из окружения процесса. Для локального запуска бинарника экспортируем файл
# через envload.sh (см. его заголовок: почему не «source»).
ENV_FILE ?= .env

# Сборка TMA и укладка бандла туда, откуда его берёт //go:embed
# (internal/platform/tma/dist). Каталог пересоздаётся целиком, иначе stale-ассеты
# прошлых сборок останутся в бинарнике.
#
# ВНИМАНИЕ: сборка перезаписывает ОТСЛЕЖИВАЕМЫЙ плейсхолдер dist/index.html
# (нужен, чтобы go build работал на чистом клоне). В конце цель возвращает
# плейсхолдер из git, иначе `git add -A` зафиксировал бы собранный бандл.
web:
	cd web && $(NPM) ci && $(NPM) run build
	rm -rf internal/platform/tma/dist
	cp -r web/dist internal/platform/tma/dist
	@git diff --exit-code -- internal/platform/tma/dist/index.html >/dev/null 2>&1 || { \
		git checkout -- internal/platform/tma/dist/index.html; \
		echo "web: плейсхолдер internal/platform/tma/dist/index.html восстановлен"; \
	}

# Бинарник в bin/ (каталог в .gitignore) — не мусорим в корне репозитория.
build:
	$(GO) build -o bin/deadliner ./cmd/deadliner

# Локальный запуск сервера с переменными из .env (см. ENV_FILE выше).
serve:
	@eval $$($(CURDIR)/envload.sh $(ENV_FILE)); exec ./bin/deadliner serve

# URL с валидным initData для теста TMA в браузере (см. cmd/devinitdata).
# Требует запущенного make serve и `cd web && npm run dev`.
dev-url:
	@eval $$($(CURDIR)/envload.sh $(ENV_FILE)); exec $(GO) run ./cmd/devinitdata

test:
	$(GO) test ./... -count=1
	cd web && $(NPM) run test

up:
	docker compose up --build -d

# Только исходники: gofmt по всему дереву заходил бы в web/node_modules.
fmt:
	gofmt -w cmd internal

clean:
	rm -rf internal/platform/tma/dist web/dist bin
	git checkout -- internal/platform/tma/dist