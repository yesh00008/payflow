package main
  import (
	"context" 	"log" 	"net/http" 	"net/http/httputil" 	"net/url" 	"os" 	"os/signal" 	"strings" 	"syscall" 	"time"  	"github.com/gin-gonic/gin" 	"github.com/go-redis/redis/v8" 	"github.com/prometheus/client_golang/prometheus" 	"github.com/prometheus/client_golang/prometheus/promauto" 	"github.com/prometheus/client_golang/prometheus/promhttp" 	"golang.org/x/time/rate"
)
  // ═══════════════════════════════════════════════════════════════════════════ // METRICS // ═══════════════════════════════════════════════════════════════════════════ 
var ( 	requestsTotal = promauto.NewCounterVec( 		prometheus.CounterOpts{ 			Name: "api_gateway_requests_total", 			Help: "Total number of requests through API gateway", 		}, 		[]string{"service", "method", "status"}, 	)  	requestDuration = promauto.NewHistogramVec( 		prometheus.HistogramOpts{ 			Name:    "api_gateway_request_duration_seconds", 			Help:    "Request duration in seconds", 			Buckets: prometheus.DefBuckets, 		}, 		[]string{"service", "method"}, 	)  	rateLimitHits = promauto.NewCounter( 		prometheus.CounterOpts{ 			Name: "api_gateway_rate_limit_hits_total", 			Help: "Total number of rate limit violations", 		}, 	)
)
  // ═══════════════════════════════════════════════════════════════════════════ // API GATEWAY // ═══════════════════════════════════════════════════════════════════════════  type APIGateway struct {
	redis    *redis.Client 	limiters map[string]*rate.Limiter 	services map[string]string
}
  // Rate limiting middleware
func (gw *APIGateway) RateLimitMiddleware() gin.HandlerFunc {
	return
func(c *gin.Context) {
		// Get user ID from JWT (or IP address) 		userID := c.GetString("user_id") 		if userID == "" {
			userID = c.ClientIP() 		}  		// Create or get rate limiter for user 		limiter, exists := gw.limiters[userID] 		if !exists {
			// 1000 requests per minute 			limiter = rate.NewLimiter(rate.Every(time.Minute/1000), 1000) 			gw.limiters[userID] = limiter 		}  		if !limiter.Allow() {
			rateLimitHits.Inc() 			c.JSON(http.StatusTooManyRequests, gin.H{ 				"error":   "rate_limit_exceeded", 				"message": "Too many requests, please try again later", 			}) 			c.Abort() 			return 		}  		c.Next() 	}
}
  // JWT validation middleware
func (gw *APIGateway) AuthMiddleware() gin.HandlerFunc {
	return
func(c *gin.Context) {
		// Skip auth for login/register endpoints 		if strings.Contains(c.Request.URL.Path, "/auth/login") || 			strings.Contains(c.Request.URL.Path, "/auth/register") || 			strings.Contains(c.Request.URL.Path, "/health") {
			c.Next() 			return 		}  		// Get token from header 		authHeader := c.GetHeader("Authorization") 		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "missing_authorization"}) 			c.Abort() 			return 		}  		// Extract token 		parts := strings.Split(authHeader, " ") 		if len(parts) != 2 || parts[0] != "Bearer" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_authorization_format"}) 			c.Abort() 			return 		}  		token := parts[1]  		// Validate token with Auth Service 		authURL := gw.services["auth"] + "/auth/validate" 		req, _ := http.NewRequest("GET", authURL, nil) 		req.Header.Set("Authorization", "Bearer "+token)  		client := &http.Client{Timeout: 2 * time.Second} 		resp, err := client.Do(req) 		if err != nil || resp.StatusCode != http.StatusOK {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_token"}) 			c.Abort() 			return 		} 		defer resp.Body.Close()  		c.Next() 	}
}
  // Proxy middleware
func (gw *APIGateway) ProxyMiddleware() gin.HandlerFunc {
	return
func(c *gin.Context) {
		start := time.Now() 		path := c.Request.URL.Path  		// Determine target service 		var targetService string 		var targetURL *url.URL  		switch {
		case strings.HasPrefix(path, "/auth"): 			targetService = "auth" 			targetURL, _ = url.Parse(gw.services["auth"]) 		case strings.HasPrefix(path, "/users"): 			targetService = "user" 			targetURL, _ = url.Parse(gw.services["user"]) 		case strings.HasPrefix(path, "/accounts"): 			targetService = "account" 			targetURL, _ = url.Parse(gw.services["account"]) 		case strings.HasPrefix(path, "/transactions"): 			targetService = "transaction" 			targetURL, _ = url.Parse(gw.services["transaction"]) 		case strings.HasPrefix(path, "/payments"): 			targetService = "payment" 			targetURL, _ = url.Parse(gw.services["payment"]) 		case strings.HasPrefix(path, "/notifications"): 			targetService = "notification" 			targetURL, _ = url.Parse(gw.services["notification"]) 		default: 			c.JSON(http.StatusNotFound, gin.H{"error": "service_not_found"}) 			return 		}  		// Create reverse proxy 		proxy := httputil.NewSingleHostReverseProxy(targetURL) 		proxy.Director =
func(req *http.Request) {
			req.URL.Scheme = targetURL.Scheme 			req.URL.Host = targetURL.Host 			req.Host = targetURL.Host 			req.Header = c.Request.Header 		}  		// Serve via proxy 		proxy.ServeHTTP(c.Writer, c.Request)  		// Record metrics 		duration := time.Since(start).Seconds() 		requestsTotal.WithLabelValues(targetService, c.Request.Method, string(rune(c.Writer.Status()))).Inc() 		requestDuration.WithLabelValues(targetService, c.Request.Method).Observe(duration) 	}
}
  // Health check
func (gw *APIGateway) HealthCheck(c *gin.Context) {
	// Check Redis 	ctx := context.Background() 	if err := gw.redis.Ping(ctx).Err(); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{ 			"status": "unhealthy", 			"redis":  "unreachable", 		}) 		return 	}  	c.JSON(http.StatusOK, gin.H{ 		"status":   "healthy", 		"version":  "1.0.0", 		"services": len(gw.services), 	})
}
  // ═══════════════════════════════════════════════════════════════════════════ // MAIN // ═══════════════════════════════════════════════════════════════════════════ 
func main() {
	// Initialize Redis 	rdb := redis.NewClient(&redis.Options{ 		Addr:     getEnv("REDIS_ADDR", "localhost:6379"), 		Password: getEnv("REDIS_PASSWORD", "redis123"), 		DB:       0, 	})  	// Service registry 	services := map[string]string{ 		"auth":         getEnv("AUTH_SERVICE_URL", "http://localhost:8081"), 		"user":         getEnv("USER_SERVICE_URL", "http://localhost:8082"), 		"account":      getEnv("ACCOUNT_SERVICE_URL", "http://localhost:8083"), 		"transaction":  getEnv("TRANSACTION_SERVICE_URL", "http://localhost:8086"), 		"payment":      getEnv("PAYMENT_SERVICE_URL", "http://localhost:8087"), 		"notification": getEnv("NOTIFICATION_SERVICE_URL", "http://localhost:8088"), 	}  	gateway := &APIGateway{ 		redis:    rdb, 		limiters: make(map[string]*rate.Limiter), 		services: services, 	}  	// Setup Gin 	r := gin.Default()  	// CORS middleware 	r.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access_Control_Allow_Origin", "*") 		c.Writer.Header().Set("Access_Control_Allow_Credentials", "true") 		c.Writer.Header().Set("Access_Control_Allow_Headers", "Content_Type, Authorization") 		c.Writer.Header().Set("Access_Control_Allow_Methods", "GET, POST, PUT, DELETE, OPTIONS")  		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusOK) 			return 		}  		c.Next() 	})  	// Middlewares 	r.Use(gateway.RateLimitMiddleware()) 	r.Use(gateway.AuthMiddleware())  	// Routes 	r.GET("/health", gateway.HealthCheck) 	r.GET("/metrics", gin.WrapH(promhttp.Handler()))  	// Proxy all other requests 	r.NoRoute(gateway.ProxyMiddleware())  	// Start server 	port := getEnv("PORT", "8080") 	srv := &http.Server{ 		Addr:    ":" + port, 		Handler: r, 	}  	go
func() {
		log.Printf("API Gateway started on port %s", port) 		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("Server error:", err) 		} 	}()  	// Graceful shutdown 	quit := make(chan os.Signal, 1) 	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM) 	<_quit  	log.Println("Shutting down API Gateway...") 	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second) 	defer cancel()  	if err := srv.Shutdown(ctx); err != nil {
		log.Fatal("Server forced to shutdown:", err) 	}  	log.Println("API Gateway stopped")
}
 
func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value 	} 	return fallback
}

