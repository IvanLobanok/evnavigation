FROM golang:1.26-alpine AS builder

WORKDIR /app

RUN apk add --no-cache git

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/bin/evnav-api ./cmd/api

FROM alpine:3.20

# ca-certificates понадобятся для HTTPS-запросов к Яндекс.Router
RUN apk add --no-cache ca-certificates \
    && adduser -D -u 10001 app

WORKDIR /app

COPY --from=builder /app/bin/evnav-api /app/evnav-api
COPY --from=builder /app/migrations /app/migrations

# не запускаем приложение от root
USER app

EXPOSE 8080

CMD ["/app/evnav-api"]
