# Стадия 1: сборка TMA. Бандл уезжает в стадию Go и встраивается в бинарник
# через //go:embed (internal/platform/tma/dist).
FROM node:22-alpine AS web-build
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Поверх плейсхолдера dist/index.html (он в репозитории, чтобы go build работал
# на чистом клоне) кладём настоящий бандл — строго до компиляции, иначе embed
# зафиксирует заглушку.
COPY --from=web-build /web/dist ./internal/platform/tma/dist
RUN CGO_ENABLED=0 go build -o /bin/deadliner ./cmd/deadliner

FROM alpine:3.20
# tzdata нужен LoadLocation("Europe/Moscow"): дефолтный tz пользователя и все
# расчёты напоминаний идут через IANA-зону, которую alpine не содержит.
RUN apk add --no-cache tzdata && adduser -D -u 10001 deadliner
COPY --from=builder /bin/deadliner /usr/local/bin/deadliner
COPY migrations /migrations
USER deadliner
ENTRYPOINT ["deadliner"]
CMD ["serve"]