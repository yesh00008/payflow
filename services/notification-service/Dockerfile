# Multi-stage Dockerfile for Notification Service
FROM golang:1.21-alpine AS builder
RUN apk add --no-cache git ca-certificates tzdata
WORKDIR /build
COPY go.mod ./
RUN go mod download
COPY . .
RUN go mod tidy
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-w -s -extldflags "-static"' \
    -o /app/service \
    ./services/notification-service/main.go

FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata curl
RUN addgroup -g 1000 appuser && adduser -D -u 1000 -G appuser appuser
WORKDIR /app
COPY --from=builder /app/service /app/service
RUN chown -R appuser:appuser /app
USER appuser
EXPOSE 8088
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:8088/health || exit 1
ENTRYPOINT ["/app/service"]

