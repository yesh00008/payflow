package ratelimit

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
)

// Config holds rate limiter configuration
type Config struct {
	// Max requests per window
	MaxRequests int
	// Window duration
	Window time.Duration
	// Key function to identify the client (default: IP address)
	KeyFunc func(c *gin.Context) string
}

// DefaultKeyFunc returns the client IP address
func DefaultKeyFunc(c *gin.Context) string {
	return c.ClientIP()
}

// Middleware creates a Redis-backed sliding window rate limiter for Gin.
// Returns 429 Too Many Requests when the limit is exceeded.
func Middleware(rdb *redis.Client, cfg Config) gin.HandlerFunc {
	if cfg.KeyFunc == nil {
		cfg.KeyFunc = DefaultKeyFunc
	}
	if cfg.MaxRequests <= 0 {
		cfg.MaxRequests = 100
	}
	if cfg.Window <= 0 {
		cfg.Window = time.Minute
	}

	return func(c *gin.Context) {
		ctx := context.Background()
		key := fmt.Sprintf("ratelimit:%s", cfg.KeyFunc(c))
		now := time.Now().UnixMilli()
		windowStart := now - cfg.Window.Milliseconds()

		pipe := rdb.Pipeline()
		// Remove expired entries
		pipe.ZRemRangeByScore(ctx, key, "0", strconv.FormatInt(windowStart, 10))
		// Add current request
		pipe.ZAdd(ctx, key, &redis.Z{Score: float64(now), Member: fmt.Sprintf("%d", now)})
		// Count requests in window
		countCmd := pipe.ZCard(ctx, key)
		// Set TTL
		pipe.Expire(ctx, key, cfg.Window)

		_, err := pipe.Exec(ctx)
		if err != nil {
			// If Redis is down, allow the request (fail-open)
			c.Next()
			return
		}

		count := countCmd.Val()
		remaining := int64(cfg.MaxRequests) - count
		if remaining < 0 {
			remaining = 0
		}

		// Set rate limit headers
		c.Header("X-RateLimit-Limit", strconv.Itoa(cfg.MaxRequests))
		c.Header("X-RateLimit-Remaining", strconv.FormatInt(remaining, 10))
		c.Header("X-RateLimit-Reset", strconv.FormatInt(now+cfg.Window.Milliseconds(), 10))

		if count > int64(cfg.MaxRequests) {
			c.Header("Retry-After", strconv.Itoa(int(cfg.Window.Seconds())))
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":       "rate limit exceeded",
				"retry_after": cfg.Window.Seconds(),
			})
			c.Abort()
			return
		}

		c.Next()
	}
}
