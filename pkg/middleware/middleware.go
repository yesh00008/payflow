// Package middleware provides shared Gin middleware for request/response logging,
// audit trail, request ID tracking, and CORS.
package middleware

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ─── Request ID Middleware ───────────────────────────────────────────────────

// RequestID adds a unique X-Request-ID header to every request for traceability.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader("X-Request-ID")
		if requestID == "" {
			requestID = uuid.New().String()
		}
		c.Set("request_id", requestID)
		c.Header("X-Request-ID", requestID)
		c.Next()
	}
}

// ─── Audit Trail Middleware ──────────────────────────────────────────────────

// AuditConfig configures which information to capture in audit logs.
type AuditConfig struct {
	ServiceName    string
	LogRequestBody bool
	MaxBodySize    int // Max bytes to capture from request body
	SkipPaths      []string
	// Callback to send audit entries to the audit service or message queue
	OnAuditEntry func(entry AuditLogEntry)
}

// AuditLogEntry represents a single audit log entry.
type AuditLogEntry struct {
	RequestID    string            `json:"request_id"`
	ServiceName  string            `json:"service_name"`
	UserID       string            `json:"user_id,omitempty"`
	Method       string            `json:"method"`
	Path         string            `json:"path"`
	Query        string            `json:"query,omitempty"`
	StatusCode   int               `json:"status_code"`
	ClientIP     string            `json:"client_ip"`
	UserAgent    string            `json:"user_agent"`
	Latency      int64             `json:"latency_ms"`
	RequestBody  string            `json:"request_body,omitempty"`
	AuthMethod   string            `json:"auth_method,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	Timestamp    time.Time         `json:"timestamp"`
	ErrorMessage string            `json:"error_message,omitempty"`
}

// bodyWriter captures the response body for audit logging.
type bodyWriter struct {
	gin.ResponseWriter
	body *bytes.Buffer
}

func (w *bodyWriter) Write(b []byte) (int, error) {
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

// AuditTrail creates middleware that logs all request/response data for compliance.
func AuditTrail(cfg AuditConfig) gin.HandlerFunc {
	if cfg.MaxBodySize <= 0 {
		cfg.MaxBodySize = 10240 // 10KB default
	}

	skipPaths := make(map[string]bool)
	for _, p := range cfg.SkipPaths {
		skipPaths[p] = true
	}

	return func(c *gin.Context) {
		// Skip health and metrics endpoints
		if skipPaths[c.Request.URL.Path] {
			c.Next()
			return
		}

		start := time.Now()
		requestID, _ := c.Get("request_id")
		reqIDStr, _ := requestID.(string)

		// Capture request body
		var requestBody string
		if cfg.LogRequestBody && c.Request.Body != nil {
			bodyBytes, err := io.ReadAll(io.LimitReader(c.Request.Body, int64(cfg.MaxBodySize)))
			if err == nil {
				requestBody = string(bodyBytes)
				// Restore body for downstream handlers
				c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
			}
		}

		// Process request
		c.Next()

		// Build audit entry
		entry := AuditLogEntry{
			RequestID:   reqIDStr,
			ServiceName: cfg.ServiceName,
			Method:      c.Request.Method,
			Path:        c.Request.URL.Path,
			Query:       c.Request.URL.RawQuery,
			StatusCode:  c.Writer.Status(),
			ClientIP:    c.ClientIP(),
			UserAgent:   c.Request.UserAgent(),
			Latency:     time.Since(start).Milliseconds(),
			RequestBody: requestBody,
			Timestamp:   start,
			Headers: map[string]string{
				"Content-Type": c.GetHeader("Content-Type"),
			},
		}

		// Get user identity from context
		if userID, exists := c.Get("user_id"); exists {
			entry.UserID, _ = userID.(string)
		}
		if authMethod, exists := c.Get("auth_method"); exists {
			entry.AuthMethod, _ = authMethod.(string)
		}

		// Get error if any
		if len(c.Errors) > 0 {
			entry.ErrorMessage = c.Errors.Last().Error()
		}

		// Send audit entry
		if cfg.OnAuditEntry != nil {
			go cfg.OnAuditEntry(entry) // Non-blocking
		} else {
			log.Printf("[AUDIT] %s %s %d %dms user=%s ip=%s",
				entry.Method, entry.Path, entry.StatusCode, entry.Latency, entry.UserID, entry.ClientIP)
		}
	}
}

// ─── CORS Middleware ────────────────────────────────────────────────────────

// CORS adds Cross-Origin Resource Sharing headers.
func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Accept, Authorization, X-API-Key, X-Request-ID, Idempotency-Key")
		c.Header("Access-Control-Expose-Headers", "X-Request-ID, X-RateLimit-Limit, X-RateLimit-Remaining, X-RateLimit-Reset")
		c.Header("Access-Control-Max-Age", "86400")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// ─── Security Headers ───────────────────────────────────────────────────────

// SecurityHeaders adds standard security headers to all responses.
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-XSS-Protection", "1; mode=block")
		c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		c.Header("Content-Security-Policy", "default-src 'self'")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Header("Cache-Control", "no-store, no-cache, must-revalidate")
		c.Header("Pragma", "no-cache")
		c.Next()
	}
}

// ─── Recovery with Audit ────────────────────────────────────────────────────

// RecoveryWithAudit is a panic recovery middleware that also logs to audit trail.
func RecoveryWithAudit(serviceName string) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("[PANIC] %s: %v", serviceName, err)
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
					"error":      "internal server error",
					"request_id": c.GetString("request_id"),
				})
			}
		}()
		c.Next()
	}
}
