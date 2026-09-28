FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY go.mod ./
# TODO(Task 13): add web build stage (node) and COPY web/dist for embed.FS
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /bin/deadliner ./cmd/deadliner

FROM alpine:3.20
RUN adduser -D -u 10001 deadliner
COPY --from=builder /bin/deadliner /usr/local/bin/deadliner
COPY migrations /migrations
USER deadliner
ENTRYPOINT ["deadliner"]
CMD ["serve"]
