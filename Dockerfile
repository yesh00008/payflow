# ============================================================================
# PayFlow - Multi-stage Dockerfile for Go microservices
# Usage: docker build --build-arg SERVICE=<service-name> -t payflow/<service-name> .
# Run from apps/payflow/ directory
# ============================================================================

FROM golang:1.21-alpine AS builder
ARG SERVICE
RUN apk add --no-cache git ca-certificates

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app ./cmd/${SERVICE}

# ─── Runtime Stage ─────────────────────────────────────────────────────────────
FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata
RUN addgroup -S appgroup && adduser -S appuser -G appgroup

COPY --from=builder /app /app

USER appuser
EXPOSE 8080

ENTRYPOINT ["/app"]
