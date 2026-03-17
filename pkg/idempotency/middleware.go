package idempotency

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// Record represents a stored idempotency record
type Record struct {
	Key          string    `json:"idempotency_key"`
	ResponseCode int      `json:"response_code"`
	ResponseBody string   `json:"response_body"`
	CreatedAt    time.Time `json:"created_at"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// Middleware creates a Gin middleware that enforces idempotency for POST/PUT requests.
// It stores response results keyed by the Idempotency-Key header in PostgreSQL.
// If the same key is sent again within the TTL, the cached response is returned.
func Middleware(db *sql.DB, ttl time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method != http.MethodPost && c.Request.Method != http.MethodPut {
			c.Next()
			return
		}

		key := c.GetHeader("Idempotency-Key")
		if key == "" {
			c.Next()
			return
		}

		// Hash the key for safety
		hash := sha256.Sum256([]byte(key))
		hashedKey := hex.EncodeToString(hash[:])

		// Check existing record
		var rec Record
		err := db.QueryRowContext(c.Request.Context(),
			`SELECT idempotency_key, response_code, response_body, created_at, expires_at
			 FROM idempotency_records WHERE idempotency_key = $1 AND expires_at > NOW()`,
			hashedKey,
		).Scan(&rec.Key, &rec.ResponseCode, &rec.ResponseBody, &rec.CreatedAt, &rec.ExpiresAt)

		if err == nil {
			// Return cached response
			c.Header("X-Idempotency-Replayed", "true")
			c.Data(rec.ResponseCode, "application/json", []byte(rec.ResponseBody))
			c.Abort()
			return
		}

		// Capture the response
		writer := &responseCapture{ResponseWriter: c.Writer}
		c.Writer = writer

		c.Next()

		// Store the response
		if writer.statusCode >= 200 && writer.statusCode < 500 {
			_, _ = db.ExecContext(c.Request.Context(),
				`INSERT INTO idempotency_records (idempotency_key, response_code, response_body, created_at, expires_at)
				 VALUES ($1, $2, $3, NOW(), $4)
				 ON CONFLICT (idempotency_key) DO NOTHING`,
				hashedKey, writer.statusCode, string(writer.body), time.Now().Add(ttl),
			)
		}
	}
}

// GenerateKey creates an idempotency key from arbitrary data
func GenerateKey(data ...interface{}) string {
	b, _ := json.Marshal(data)
	hash := sha256.Sum256(b)
	return hex.EncodeToString(hash[:])
}

// ─── Response Capture Writer ───────────────────────────────────────────────

type responseCapture struct {
	gin.ResponseWriter
	body       []byte
	statusCode int
}

func (w *responseCapture) Write(b []byte) (int, error) {
	w.body = append(w.body, b...)
	return w.ResponseWriter.Write(b)
}

func (w *responseCapture) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}
