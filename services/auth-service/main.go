package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/crypto/bcrypt"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// ─── Models ───────────────────────────────────────────────────────────────────

type User struct {
	ID           string    `json:"user_id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	FullName     string    `json:"full_name"`
	Roles        []string  `json:"roles"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Claims struct {
	UserID string   `json:"user_id"`
	Email  string   `json:"email"`
	Roles  []string `json:"roles"`
	jwt.RegisteredClaims
}

type AuthService struct {
	db        *sql.DB
	redis     *redis.Client
	jwtSecret string
}

// ─── Tracer ───────────────────────────────────────────────────────────────────

func initTracer() (*sdktrace.TracerProvider, error) {
	ctx := context.Background()
	res, err := resource.New(ctx, resource.WithAttributes(
		attribute.String("service.name", "auth-service"),
		attribute.String("service.version", "1.0.0"),
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

// ─── Handlers ─────────────────────────────────────────────────────────────────

func (s *AuthService) Register(c *gin.Context) {
	var req struct {
		Email    string `json:"email" binding:"required,email"`
		Password string `json:"password" binding:"required,min=8"`
		FullName string `json:"full_name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Check duplicate email
	var exists bool
	_ = s.db.QueryRowContext(c.Request.Context(),
		"SELECT EXISTS(SELECT 1 FROM users WHERE email=$1)", req.Email).Scan(&exists)
	if exists {
		c.JSON(http.StatusConflict, gin.H{"error": "email already registered"})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "password hash failed"})
		return
	}

	user := User{
		ID:           uuid.New().String(),
		Email:        strings.ToLower(req.Email),
		PasswordHash: string(hash),
		FullName:     req.FullName,
		Status:       "active",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	_, err = s.db.ExecContext(c.Request.Context(),
		`INSERT INTO users (id, email, password_hash, full_name, status, created_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		user.ID, user.Email, user.PasswordHash, user.FullName, user.Status, user.CreatedAt, user.UpdatedAt)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create user"})
		return
	}

	// Assign default role
	_, _ = s.db.ExecContext(c.Request.Context(),
		`INSERT INTO user_roles (user_id, role) VALUES ($1, 'user')`, user.ID)

	c.JSON(http.StatusCreated, gin.H{"user_id": user.ID, "email": user.Email, "status": user.Status})
}

func (s *AuthService) Login(c *gin.Context) {
	var req struct {
		Email    string `json:"email" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var user User
	var rolesStr sql.NullString
	err := s.db.QueryRowContext(c.Request.Context(),
		`SELECT u.id, u.email, u.password_hash, u.full_name, u.status,
		        STRING_AGG(ur.role, ',') as roles
		 FROM users u LEFT JOIN user_roles ur ON u.id = ur.user_id
		 WHERE u.email = $1 GROUP BY u.id`, strings.ToLower(req.Email)).
		Scan(&user.ID, &user.Email, &user.PasswordHash, &user.FullName, &user.Status, &rolesStr)

	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}
	if user.Status != "active" {
		c.JSON(http.StatusForbidden, gin.H{"error": "account is " + user.Status})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	var roles []string
	if rolesStr.Valid && rolesStr.String != "" {
		roles = strings.Split(rolesStr.String, ",")
	} else {
		roles = []string{"user"}
	}

	accessToken, err := s.generateToken(user.ID, user.Email, roles, 15*time.Minute)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token generation failed"})
		return
	}

	refreshToken, err := s.generateToken(user.ID, user.Email, roles, 7*24*time.Hour)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token generation failed"})
		return
	}

	// Store session in Redis
	sessionID := uuid.New().String()
	tokenHash := sha256.Sum256([]byte(refreshToken))
	s.redis.Set(c.Request.Context(), "session:"+sessionID, hex.EncodeToString(tokenHash[:]), 7*24*time.Hour)

	c.JSON(http.StatusOK, gin.H{
		"access_token":  accessToken,
		"refresh_token": refreshToken,
		"token_type":    "Bearer",
		"expires_in":    900,
		"session_id":    sessionID,
	})
}

func (s *AuthService) RefreshToken(c *gin.Context) {
	var req struct {
		RefreshToken string `json:"refresh_token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	claims, err := s.validateToken(req.RefreshToken)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid refresh token"})
		return
	}

	newAccess, _ := s.generateToken(claims.UserID, claims.Email, claims.Roles, 15*time.Minute)
	c.JSON(http.StatusOK, gin.H{
		"access_token": newAccess,
		"token_type":   "Bearer",
		"expires_in":   900,
	})
}

func (s *AuthService) ValidateToken(c *gin.Context) {
	auth := c.GetHeader("Authorization")
	if auth == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"valid": false, "error": "missing token"})
		return
	}
	parts := strings.SplitN(auth, " ", 2)
	if len(parts) != 2 {
		c.JSON(http.StatusUnauthorized, gin.H{"valid": false})
		return
	}

	claims, err := s.validateToken(parts[1])
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"valid": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"valid": true, "user_id": claims.UserID, "email": claims.Email, "roles": claims.Roles})
}

func (s *AuthService) Logout(c *gin.Context) {
	sessionID := c.Query("session_id")
	if sessionID != "" {
		s.redis.Del(c.Request.Context(), "session:"+sessionID)
	}
	c.JSON(http.StatusOK, gin.H{"message": "logged out"})
}

func (s *AuthService) HealthCheck(c *gin.Context) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.db.PingContext(ctx); err != nil {
		log.Printf("Database ping failed: %v", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy", "database": "unreachable", "error": err.Error()})
		return
	}
	if err := s.redis.Ping(ctx).Err(); err != nil {
		log.Printf("Redis ping failed: %v", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy", "redis": "unreachable", "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "healthy", "service": "auth-service"})
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func (s *AuthService) generateToken(userID, email string, roles []string, ttl time.Duration) (string, error) {
	claims := Claims{
		UserID: userID,
		Email:  email,
		Roles:  roles,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "payflow-auth",
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(s.jwtSecret))
}

func (s *AuthService) validateToken(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return []byte(s.jwtSecret), nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}
	return claims, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ─── Main ─────────────────────────────────────────────────────────────────────

func main() {
	tp, err := initTracer()
	if err != nil {
		log.Printf("Warning: tracer init failed: %v", err)
	} else {
		defer tp.Shutdown(context.Background())
	}

	db, err := sql.Open("postgres", getEnv("DATABASE_URL", "postgres://fintech:fintech123@localhost:5432/payflow?sslmode=disable"))
	if err != nil {
		log.Fatal("DB open:", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)

	rdb := redis.NewClient(&redis.Options{
		Addr:     getEnv("REDIS_ADDR", "localhost:6379"),
		Password: getEnv("REDIS_PASSWORD", "redis123"),
		DB:       0,
	})

	svc := &AuthService{
		db:        db,
		redis:     rdb,
		jwtSecret: getEnv("JWT_SECRET", "supersecretkey123"),
	}

	r := gin.Default()
	r.GET("/health", svc.HealthCheck)
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	r.POST("/auth/register", svc.Register)
	r.POST("/auth/login", svc.Login)
	r.POST("/auth/refresh", svc.RefreshToken)
	r.GET("/auth/validate", svc.ValidateToken)
	r.POST("/auth/logout", svc.Logout)

	port := getEnv("PORT", "8081")
	server := &http.Server{Addr: ":" + port, Handler: r}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Printf("Auth service started on port %s", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	<-quit
	log.Println("Shutting down auth-service...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server.Shutdown(ctx)
}
