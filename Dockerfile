FROM golang:1.25.7 AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /app/bot ./cmd/bot

FROM gcr.io/distroless/base-debian12

WORKDIR /app

COPY --from=build /app/bot /app/bot

EXPOSE 8080

ENTRYPOINT ["/app/bot"]
