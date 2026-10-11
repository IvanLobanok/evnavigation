FROM golang:alpine AS builder

WORKDIR /app

RUN apk add --no-cache git

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o /app/bin/evnav-api ./cmd/api/main.go

FROM alpine:3.20

WORKDIR /app

COPY --from=builder /app/bin/evnav-api /app/evnav-api

COPY --from=builder /app/migrations /app/migrations

EXPOSE 8080

CMD ["/app/evnav-api"]