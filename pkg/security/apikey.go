// Package security provides JWT, RBAC, API key, and service-to-service authentication.
package security

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// ─── API Key Authentication ──────────────────────────────────────────────────

// APIKeyConfig holds the configuration for API key validation.
type APIKeyConfig struct {
	// Service API keys: map of service_name -> hashed_api_key
	ServiceKeys map[string]string
	// Header name for API key (default: X-API-Key)
	HeaderName string
	// Whether to allow JWT as fallback
	AllowJWTFallback bool
	// JWT secret for fallback validation
	JWTSecret string
}

// ServiceIdentity represents the authenticated service.
type ServiceIdentity struct {
	ServiceName string
	APIKeyHash  string
	AuthMethod  string // "api_key" or "jwt"
}

// HashAPIKey creates a SHA-256 hash of an API key for secure storage.
func HashAPIKey(apiKey string) string {
	hash := sha256.Sum256([]byte(apiKey))
	return hex.EncodeToString(hash[:])
}

// DefaultServiceKeys returns the default service-to-service API keys (hashed).
// In production, these should come from a secrets manager (Vault, AWS Secrets Manager).
func DefaultServiceKeys() map[string]string {
	keys := map[string]string{
		"api-gateway":         "gw-key-2024-payflow-secure",
		"auth-service":        "auth-key-2024-payflow-secure",
		"account-service":     "acct-key-2024-payflow-secure",
		"transaction-service": "txn-key-2024-payflow-secure",
		"ledger-service":      "ldgr-key-2024-payflow-secure",
		"payment-service":     "pay-key-2024-payflow-secure",
		"notification-service": "notif-key-2024-payflow-secure",
		"audit-service":       "audit-key-2024-payflow-secure",
		"kyc-service":         "kyc-key-2024-payflow-secure",
		"fraud-service":       "fraud-key-2024-payflow-secure",
		"risk-service":        "risk-key-2024-payflow-secure",
		"realtime-service":    "rt-key-2024-payflow-secure",
	}
	// Hash all keys for secure comparison
	hashed := make(map[string]string)
	for name, key := range keys {
		hashed[name] = HashAPIKey(key)
	}
	return hashed
}

// APIKeyMiddleware returns Gin middleware that validates service-to-service API keys.
func APIKeyMiddleware(cfg APIKeyConfig) gin.HandlerFunc {
	if cfg.HeaderName == "" {
		cfg.HeaderName = "X-API-Key"
	}

	return func(c *gin.Context) {
		apiKey := c.GetHeader(cfg.HeaderName)

		if apiKey != "" {
			// Validate API key
			keyHash := HashAPIKey(apiKey)
			for serviceName, expectedHash := range cfg.ServiceKeys {
				if subtle.ConstantTimeCompare([]byte(keyHash), []byte(expectedHash)) == 1 {
					c.Set("service_identity", &ServiceIdentity{
						ServiceName: serviceName,
						APIKeyHash:  keyHash,
						AuthMethod:  "api_key",
					})
					c.Set("auth_method", "api_key")
					c.Set("service_name", serviceName)
					c.Next()
					return
				}
			}
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid API key"})
			return
		}

		// Fallback to JWT if configured
		if cfg.AllowJWTFallback && cfg.JWTSecret != "" {
			auth := c.GetHeader("Authorization")
			if auth != "" {
				parts := strings.SplitN(auth, " ", 2)
				if len(parts) == 2 && strings.ToLower(parts[0]) == "bearer" {
					claims, err := ValidateToken(cfg.JWTSecret, parts[1])
					if err == nil {
						c.Set("user_id", claims.UserID)
						c.Set("email", claims.Email)
						c.Set("roles", claims.Roles)
						c.Set("auth_method", "jwt")
						c.Next()
						return
					}
				}
			}
		}

		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": "authentication required: provide X-API-Key or Bearer token",
		})
	}
}

// ─── Request/Response Audit Logging ──────────────────────────────────────────

// AuditEntry represents a logged request/response pair.
type AuditEntry struct {
	RequestID    string            `json:"request_id"`
	UserID       string            `json:"user_id,omitempty"`
	ServiceName  string            `json:"service_name,omitempty"`
	Method       string            `json:"method"`
	Path         string            `json:"path"`
	StatusCode   int               `json:"status_code"`
	ClientIP     string            `json:"client_ip"`
	UserAgent    string            `json:"user_agent"`
	Duration     time.Duration     `json:"duration_ms"`
	RequestBody  string            `json:"request_body,omitempty"`
	ResponseBody string            `json:"response_body,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	Timestamp    time.Time         `json:"timestamp"`
	AuthMethod   string            `json:"auth_method,omitempty"`
}

// AuditLogger collects audit entries and flushes them periodically.
type AuditLogger struct {
	mu      sync.Mutex
	entries []AuditEntry
	maxSize int
	OnFlush func(entries []AuditEntry) // Callback to persist entries
}

// NewAuditLogger creates a new audit logger.
func NewAuditLogger(maxSize int, onFlush func([]AuditEntry)) *AuditLogger {
	al := &AuditLogger{
		maxSize: maxSize,
		OnFlush: onFlush,
	}
	// Start periodic flush
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			al.Flush()
		}
	}()
	return al
}

// Add adds an audit entry to the buffer.
func (al *AuditLogger) Add(entry AuditEntry) {
	al.mu.Lock()
	al.entries = append(al.entries, entry)
	shouldFlush := len(al.entries) >= al.maxSize
	al.mu.Unlock()
	if shouldFlush {
		al.Flush()
	}
}

// Flush sends buffered entries to the OnFlush callback.
func (al *AuditLogger) Flush() {
	al.mu.Lock()
	if len(al.entries) == 0 {
		al.mu.Unlock()
		return
	}
	entries := al.entries
	al.entries = nil
	al.mu.Unlock()

	if al.OnFlush != nil {
		al.OnFlush(entries)
	}
}

// ─── Combined Auth Middleware ────────────────────────────────────────────────

// CombinedAuthConfig supports both user JWT auth and service-to-service API key auth.
type CombinedAuthConfig struct {
	JWTSecret   string
	ServiceKeys map[string]string
	// Paths that don't require authentication
	PublicPaths []string
}

// CombinedAuthMiddleware checks for either JWT bearer token or API key.
func CombinedAuthMiddleware(cfg CombinedAuthConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Check if path is public
		for _, path := range cfg.PublicPaths {
			if strings.HasPrefix(c.Request.URL.Path, path) {
				c.Next()
				return
			}
		}

		// Try API key first (service-to-service)
		apiKey := c.GetHeader("X-API-Key")
		if apiKey != "" {
			keyHash := HashAPIKey(apiKey)
			for serviceName, expectedHash := range cfg.ServiceKeys {
				if subtle.ConstantTimeCompare([]byte(keyHash), []byte(expectedHash)) == 1 {
					c.Set("auth_method", "api_key")
					c.Set("service_name", serviceName)
					c.Next()
					return
				}
			}
		}

		// Try JWT (user-facing)
		auth := c.GetHeader("Authorization")
		if auth != "" {
			parts := strings.SplitN(auth, " ", 2)
			if len(parts) == 2 && strings.ToLower(parts[0]) == "bearer" {
				claims, err := ValidateToken(cfg.JWTSecret, parts[1])
				if err == nil {
					c.Set("user_id", claims.UserID)
					c.Set("email", claims.Email)
					c.Set("roles", claims.Roles)
					c.Set("auth_method", "jwt")
					c.Next()
					return
				}
			}
		}

		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": "authentication required",
		})
	}
}
