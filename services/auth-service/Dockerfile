# ════════════════════════════════════════════════════════════════════════════
# Multi-stage Dockerfile for Go services
# ════════════════════════════════════════════════════════════════════════════

# ═══════════════════════════════════════════════════════════════════════════
# Stage 1: Build
# ═══════════════════════════════════════════════════════════════════════════
FROM golang:1.21-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

# Set working directory
WORKDIR /build

# Copy go mod files
COPY go.mod ./
RUN go mod download

# Copy source code
COPY . .
RUN go mod tidy

# Build the binary
ARG SERVICE_NAME=auth-service
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-w -s -extldflags "-static"' \
    -o /app/service \
    ./cmd/${SERVICE_NAME}/main.go

# ═══════════════════════════════════════════════════════════════════════════
# Stage 2: Runtime
# ═══════════════════════════════════════════════════════════════════════════
FROM alpine:3.19

# Install runtime dependencies
RUN apk add --no-cache ca-certificates tzdata curl

# Create non-root user
RUN addgroup -g 1000 appuser && \
    adduser -D -u 1000 -G appuser appuser

# Set working directory
WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/service /app/service

# Set ownership
RUN chown -R appuser:appuser /app

# Switch to non-root user
USER appuser

# Expose port
EXPOSE 8080
EXPOSE 50051

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:8080/health || exit 1

# Run the service
ENTRYPOINT ["/app/service"]

