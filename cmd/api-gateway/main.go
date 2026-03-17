package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// ─── Prometheus Metrics ──────────────────────────────────────────────────────

var (
	requestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "gateway_requests_total",
		Help: "Total requests by method, path, and status",
	}, []string{"method", "path", "status"})

	requestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "gateway_request_duration_seconds",
		Help:    "Request duration in seconds",
		Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
	}, []string{"method", "path"})

	authFailures = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "gateway_auth_failures_total",
		Help: "Authentication failures by reason",
	}, []string{"reason"})

	rateLimitHits = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "gateway_rate_limit_hits_total",
		Help: "Number of requests rejected by rate limiting",
	})
)

func init() {
	prometheus.MustRegister(requestsTotal, requestDuration, authFailures, rateLimitHits)
}

// ─── JWT Claims ──────────────────────────────────────────────────────────────

type Claims struct {
	UserID string   `json:"user_id"`
	Email  string   `json:"email"`
	Roles  []string `json:"roles"`
	jwt.RegisteredClaims
}

// ─── Gateway ─────────────────────────────────────────────────────────────────

type APIGateway struct {
	proxies     map[string]*httputil.ReverseProxy
	redis       *redis.Client
	jwtSecret   string
	serviceKeys map[string]string // service_name -> hashed key
}

func initTracer() (*sdktrace.TracerProvider, error) {
	ctx := context.Background()
	res, err := resource.New(ctx, resource.WithAttributes(
		semconv.ServiceName("api-gateway"), semconv.ServiceVersion("2.0.0"),
	))
	if err != nil {
		return nil, err
	}
	dialCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(dialCtx, getEnv("OTEL_EXPORTER_OTLP_ENDPOINT", "tempo:4317"),
		grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	if err != nil {
		return nil, err
	}
	exp, err := otlptracegrpc.New(ctx, otlptracegrpc.WithGRPCConn(conn))
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp), sdktrace.WithResource(res))
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	return tp, nil
}

func hashAPIKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

func NewAPIGateway() *APIGateway {
	services := map[string]string{
		"auth":         getEnv("AUTH_SERVICE_URL", "http://localhost:8081"),
		"kyc":          getEnv("KYC_SERVICE_URL", "http://localhost:8082"),
		"account":      getEnv("ACCOUNT_SERVICE_URL", "http://localhost:8083"),
		"transaction":  getEnv("TRANSACTION_SERVICE_URL", "http://localhost:8084"),
		"ledger":       getEnv("LEDGER_SERVICE_URL", "http://localhost:8085"),
		"payment":      getEnv("PAYMENT_SERVICE_URL", "http://localhost:8086"),
		"notification": getEnv("NOTIFICATION_SERVICE_URL", "http://localhost:8087"),
		"audit":        getEnv("AUDIT_SERVICE_URL", "http://localhost:8088"),
		"fraud":        getEnv("FRAUD_SERVICE_URL", "http://localhost:8089"),
		"risk":         getEnv("RISK_SERVICE_URL", "http://localhost:8090"),
		"realtime":     getEnv("REALTIME_SERVICE_URL", "http://localhost:8091"),
	}

	proxies := make(map[string]*httputil.ReverseProxy)
	for name, svcURL := range services {
		target, err := url.Parse(svcURL)
		if err != nil {
			log.Printf("Warning: invalid URL for %s: %v", name, err)
			continue
		}
		svcName := name
		proxy := httputil.NewSingleHostReverseProxy(target)
		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("[gateway] proxy error for %s: %v", svcName, err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			json.NewEncoder(w).Encode(gin.H{
				"error":      "service_unavailable",
				"message":    fmt.Sprintf("Backend service %s is unavailable", svcName),
				"request_id": r.Header.Get("X-Request-ID"),
			})
		}
		proxies[name] = proxy
	}

	rdb := redis.NewClient(&redis.Options{
		Addr:     getEnv("REDIS_ADDR", "localhost:6379"),
		Password: getEnv("REDIS_PASSWORD", "redis123"),
		DB:       7,
	})

	svcKeys := map[string]string{
		"api-gateway":         hashAPIKey("gw-key-2024-payflow-secure"),
		"auth-service":        hashAPIKey("auth-key-2024-payflow-secure"),
		"account-service":     hashAPIKey("acct-key-2024-payflow-secure"),
		"transaction-service": hashAPIKey("txn-key-2024-payflow-secure"),
		"ledger-service":      hashAPIKey("ldgr-key-2024-payflow-secure"),
		"payment-service":     hashAPIKey("pay-key-2024-payflow-secure"),
		"fraud-service":       hashAPIKey("fraud-key-2024-payflow-secure"),
		"risk-service":        hashAPIKey("risk-key-2024-payflow-secure"),
		"realtime-service":    hashAPIKey("rt-key-2024-payflow-secure"),
	}

	return &APIGateway{
		proxies:     proxies,
		redis:       rdb,
		jwtSecret:   getEnv("JWT_SECRET", "supersecretkey123"),
		serviceKeys: svcKeys,
	}
}

// ─── Request ID Middleware ───────────────────────────────────────────────────

func (g *APIGateway) RequestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader("X-Request-ID")
		if requestID == "" {
			requestID = uuid.New().String()
		}
		c.Set("request_id", requestID)
		c.Header("X-Request-ID", requestID)
		c.Request.Header.Set("X-Request-ID", requestID)
		c.Next()
	}
}

// ─── Security Headers ───────────────────────────────────────────────────────

func (g *APIGateway) SecurityHeadersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-XSS-Protection", "1; mode=block")
		c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Header("Cache-Control", "no-store")
		c.Next()
	}
}

// ─── CORS Middleware ────────────────────────────────────────────────────────

func (g *APIGateway) CORSMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", getEnv("CORS_ORIGIN", "*"))
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Accept, Authorization, X-API-Key, X-Request-ID, Idempotency-Key")
		c.Header("Access-Control-Expose-Headers", "X-Request-ID, X-RateLimit-Limit, X-RateLimit-Remaining")
		c.Header("Access-Control-Max-Age", "86400")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// ─── JWT Authentication Middleware ──────────────────────────────────────────

func (g *APIGateway) JWTAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Check API key first (service-to-service)
		apiKey := c.GetHeader("X-API-Key")
		if apiKey != "" {
			keyHash := hashAPIKey(apiKey)
			for svcName, expectedHash := range g.serviceKeys {
				if keyHash == expectedHash {
					c.Set("auth_method", "api_key")
					c.Set("service_name", svcName)
					c.Next()
					return
				}
			}
			authFailures.WithLabelValues("invalid_api_key").Inc()
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid API key"})
			return
		}

		// Check JWT Bearer token
		auth := c.GetHeader("Authorization")
		if auth == "" {
			authFailures.WithLabelValues("missing_token").Inc()
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error":   "authentication required",
				"details": "Provide Authorization: Bearer <token> or X-API-Key header",
			})
			return
		}

		parts := strings.SplitN(auth, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			authFailures.WithLabelValues("invalid_format").Inc()
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid authorization format"})
			return
		}

		token, err := jwt.ParseWithClaims(parts[1], &Claims{}, func(t *jwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return []byte(g.jwtSecret), nil
		})
		if err != nil {
			authFailures.WithLabelValues("invalid_token").Inc()
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}

		claims, ok := token.Claims.(*Claims)
		if !ok || !token.Valid {
			authFailures.WithLabelValues("invalid_claims").Inc()
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token claims"})
			return
		}

		c.Set("user_id", claims.UserID)
		c.Set("email", claims.Email)
		c.Set("roles", claims.Roles)
		c.Set("auth_method", "jwt")

		// Forward user info to backend services via headers
		c.Request.Header.Set("X-User-ID", claims.UserID)
		c.Request.Header.Set("X-User-Email", claims.Email)
		c.Request.Header.Set("X-User-Roles", strings.Join(claims.Roles, ","))

		c.Next()
	}
}

// ─── RBAC Middleware ────────────────────────────────────────────────────────

func (g *APIGateway) RequireRole(role string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if authMethod, _ := c.Get("auth_method"); authMethod == "api_key" {
			c.Next()
			return
		}
		rolesVal, exists := c.Get("roles")
		if !exists {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "no roles found"})
			return
		}
		roles, ok := rolesVal.([]string)
		if !ok {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "invalid roles"})
			return
		}
		for _, r := range roles {
			if r == role || r == "admin" {
				c.Next()
				return
			}
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error":         "insufficient permissions",
			"required_role": role,
		})
	}
}

// ─── Rate Limiting (Redis-backed) ───────────────────────────────────────────

func (g *APIGateway) RateLimitMiddleware(maxRequests int, window time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := fmt.Sprintf("ratelimit:gw:%s:%s", c.Request.URL.Path, c.ClientIP())
		ctx := context.Background()

		count, err := g.redis.Incr(ctx, key).Result()
		if err != nil {
			c.Next()
			return
		}
		if count == 1 {
			g.redis.Expire(ctx, key, window)
		}

		c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", maxRequests))
		remaining := int64(maxRequests) - count
		if remaining < 0 {
			remaining = 0
		}
		c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))

		if count > int64(maxRequests) {
			rateLimitHits.Inc()
			c.Header("Retry-After", fmt.Sprintf("%d", int(window.Seconds())))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":       "rate limit exceeded",
				"retry_after": window.Seconds(),
			})
			return
		}
		c.Next()
	}
}

// ─── Audit Logging Middleware ───────────────────────────────────────────────

func (g *APIGateway) AuditMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		duration := time.Since(start)

		userID, _ := c.Get("user_id")
		authMethod, _ := c.Get("auth_method")
		requestID, _ := c.Get("request_id")

		log.Printf("[AUDIT] request_id=%v method=%s path=%s status=%d duration=%v user=%v auth=%v ip=%s",
			requestID, c.Request.Method, c.Request.URL.Path, c.Writer.Status(),
			duration, userID, authMethod, c.ClientIP())

		status := fmt.Sprintf("%d", c.Writer.Status())
		path := normalizePath(c.Request.URL.Path)
		requestsTotal.WithLabelValues(c.Request.Method, path, status).Inc()
		requestDuration.WithLabelValues(c.Request.Method, path).Observe(duration.Seconds())
	}
}

// ─── Proxy Helper ───────────────────────────────────────────────────────────

func (g *APIGateway) proxyTo(serviceName string) gin.HandlerFunc {
	return func(c *gin.Context) {
		proxy, ok := g.proxies[serviceName]
		if !ok {
			c.JSON(http.StatusBadGateway, gin.H{"error": "service not configured: " + serviceName})
			return
		}
		proxy.ServeHTTP(c.Writer, c.Request)
	}
}

// ─── Health Check ───────────────────────────────────────────────────────────

func (g *APIGateway) HealthCheck(c *gin.Context) {
	redisStatus := "healthy"
	if err := g.redis.Ping(context.Background()).Err(); err != nil {
		redisStatus = "unhealthy"
	}
	c.JSON(http.StatusOK, gin.H{
		"status":  "healthy",
		"service": "api-gateway",
		"version": "2.0.0",
		"features": gin.H{
			"jwt_auth":            true,
			"rbac":                true,
			"rate_limiting":       true,
			"api_key_auth":        true,
			"audit_logging":       true,
			"security_headers":    true,
			"tls_ready":           true,
			"grpc_ready":          true,
			"service_to_service":  true,
		},
		"dependencies": gin.H{"redis": redisStatus},
	})
}

// ─── Service Health Aggregator ──────────────────────────────────────────────

func (g *APIGateway) ServiceHealth(c *gin.Context) {
	type svcHealth struct {
		Name   string `json:"name"`
		Status string `json:"status"`
		Port   int    `json:"port"`
		Lang   string `json:"language"`
	}

	checks := []svcHealth{
		{Name: "auth-service", Port: 8081, Lang: "Go"},
		{Name: "user-kyc-service", Port: 8082, Lang: "Go"},
		{Name: "account-service", Port: 8083, Lang: "Go"},
		{Name: "transaction-service", Port: 8084, Lang: "Go"},
		{Name: "ledger-service", Port: 8085, Lang: "Go"},
		{Name: "payment-service", Port: 8086, Lang: "Go"},
		{Name: "notification-service", Port: 8087, Lang: "Go"},
		{Name: "audit-service", Port: 8088, Lang: "Go"},
		{Name: "fraud-detection", Port: 8089, Lang: "Python"},
		{Name: "risk-scoring", Port: 8090, Lang: "Python"},
		{Name: "realtime-bff", Port: 8091, Lang: "Node.js"},
	}

	for i, svc := range checks {
		svcURL := fmt.Sprintf("http://localhost:%d/health", svc.Port)
		client := &http.Client{Timeout: 2 * time.Second}
		resp, err := client.Get(svcURL)
		if err != nil || resp.StatusCode != http.StatusOK {
			checks[i].Status = "unhealthy"
		} else {
			checks[i].Status = "healthy"
		}
		if resp != nil {
			resp.Body.Close()
		}
	}
	c.JSON(http.StatusOK, gin.H{"services": checks})
}

// ─── Helpers ────────────────────────────────────────────────────────────────

func normalizePath(path string) string {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		if len(p) > 10 && !strings.HasPrefix(p, "v") {
			parts[i] = ":id"
		}
	}
	return strings.Join(parts, "/")
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// ─── Main ───────────────────────────────────────────────────────────────────

func main() {
	tp, err := initTracer()
	if err != nil {
		log.Printf("Warning: tracer init failed: %v (continuing without tracing)", err)
	} else {
		defer tp.Shutdown(context.Background())
	}

	gateway := NewAPIGateway()
	r := gin.Default()

	// ─── Global Middleware ────────────────────────────────────────────────
	r.Use(gateway.RequestIDMiddleware())
	r.Use(gateway.SecurityHeadersMiddleware())
	r.Use(gateway.CORSMiddleware())
	r.Use(gateway.AuditMiddleware())
	r.Use(gateway.RateLimitMiddleware(200, time.Minute))

	// ─── Public Routes ───────────────────────────────────────────────────
	r.GET("/health", gateway.HealthCheck)
	r.GET("/health/services", gateway.ServiceHealth)
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// ─── Auth Routes (public, stricter rate limit) ───────────────────────
	auth := r.Group("/auth")
	auth.Use(gateway.RateLimitMiddleware(20, time.Minute))
	{
		auth.Any("/*path", gateway.proxyTo("auth"))
	}

	// ─── Protected Routes (JWT or API Key required) ──────────────────────
	api := r.Group("")
	api.Use(gateway.JWTAuthMiddleware())
	{
		// Account Service
		api.Any("/v1/accounts", gateway.proxyTo("account"))
		api.Any("/v1/accounts/*path", gateway.proxyTo("account"))

		// KYC/User Service
		api.Any("/v1/users", gateway.proxyTo("kyc"))
		api.Any("/v1/users/*path", gateway.proxyTo("kyc"))

		// Transfer Service (stricter rate limit)
		transfers := api.Group("/v1/transfers")
		transfers.Use(gateway.RateLimitMiddleware(30, time.Minute))
		{
			transfers.Any("", gateway.proxyTo("transaction"))
			transfers.Any("/*path", gateway.proxyTo("transaction"))
		}

		// Payment Service (stricter rate limit)
		payments := api.Group("/v1/payments")
		payments.Use(gateway.RateLimitMiddleware(30, time.Minute))
		{
			payments.Any("", gateway.proxyTo("payment"))
			payments.Any("/*path", gateway.proxyTo("payment"))
		}

		// Ledger Service
		api.Any("/v1/ledger/*path", gateway.proxyTo("ledger"))

		// Notification Service
		api.Any("/v1/notifications", gateway.proxyTo("notification"))
		api.Any("/v1/notifications/*path", gateway.proxyTo("notification"))

		// Fraud Detection (admin/analyst only)
		fraud := api.Group("/v1/fraud")
		fraud.Use(gateway.RequireRole("analyst"))
		{
			fraud.Any("", gateway.proxyTo("fraud"))
			fraud.Any("/*path", gateway.proxyTo("fraud"))
		}

		// Risk Scoring (admin/analyst only)
		risk := api.Group("/v1/risk")
		risk.Use(gateway.RequireRole("analyst"))
		{
			risk.Any("", gateway.proxyTo("risk"))
			risk.Any("/*path", gateway.proxyTo("risk"))
		}

		// Audit Service (admin/compliance only)
		auditRoutes := api.Group("/v1/audit")
		auditRoutes.Use(gateway.RequireRole("compliance"))
		{
			auditRoutes.Any("", gateway.proxyTo("audit"))
			auditRoutes.Any("/*path", gateway.proxyTo("audit"))
		}

		// BFF/Dashboard
		api.Any("/v1/bff/*path", gateway.proxyTo("realtime"))
		api.Any("/v1/sse/*path", gateway.proxyTo("realtime"))
	}

	// WebSocket (separate auth via query param)
	r.GET("/ws", gateway.proxyTo("realtime"))

	// ─── Start Server ────────────────────────────────────────────────────
	port := getEnv("PORT", "8080")
	server := &http.Server{
		Addr:         ":" + port,
		Handler:      r,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Printf("╔══════════════════════════════════════════════════════════╗")
		log.Printf("║  PayFlow API Gateway v2.0.0                             ║")
		log.Printf("║  Port: %-47s ║", port)
		log.Printf("║  Features: JWT, RBAC, Rate Limit, API Keys, Audit      ║")
		log.Printf("║  Backend: 12 services (Go, Python, Node.js)             ║")
		log.Printf("╚══════════════════════════════════════════════════════════╝")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("Failed to start server:", err)
		}
	}()

	<-quit
	log.Println("Shutting down API Gateway...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server.Shutdown(ctx)
	gateway.redis.Close()
	log.Println("API Gateway stopped")
}
