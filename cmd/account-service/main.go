package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	_ "github.com/lib/pq"
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

type AccountService struct {
	db    *sql.DB
	redis *redis.Client
}

type Account struct {
	ID              string    `json:"account_id"`
	UserID          string    `json:"user_id"`
	AccountNumber   string    `json:"account_number"`
	Currency        string    `json:"currency"`
	AccountType     string    `json:"account_type"`
	Status          string    `json:"status"`
	Balance         int64     `json:"balance"`
	AvailableBalance int64    `json:"available_balance"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func initTracer() (*sdktrace.TracerProvider, error) {
	ctx := context.Background()

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName("account-service"),
			semconv.ServiceVersion("1.0.0"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, "tempo:4317",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC connection to collector: %w", err)
	}

	traceExporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithGRPCConn(conn))
	if err != nil {
		return nil, fmt.Errorf("failed to create trace exporter: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	return tp, nil
}

func (s *AccountService) CreateAccount(c *gin.Context) {
	ctx := c.Request.Context()
	
	var req struct {
		UserID        string `json:"user_id" binding:"required"`
		Currency      string `json:"currency" binding:"required,len=3"`
		AccountType   string `json:"account_type" binding:"required,oneof=checking savings business"`
		InitialBalance int64  `json:"initial_balance"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Generate unique account number
	accountNumber := generateAccountNumber()

	// Start transaction
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to start transaction"})
		return
	}
	defer tx.Rollback()

	// Create account record
	account := &Account{
		ID:              generateUUID(),
		UserID:          req.UserID,
		AccountNumber:   accountNumber,
		Currency:        req.Currency,
		AccountType:     req.AccountType,
		Status:          "active",
		Balance:         req.InitialBalance,
		AvailableBalance: req.InitialBalance,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	query := `
		INSERT INTO accounts (id, user_id, account_number, currency, account_type, status, balance, available_balance, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`

	_, err = tx.ExecContext(ctx, query,
		account.ID, account.UserID, account.AccountNumber, account.Currency,
		account.AccountType, account.Status, account.Balance, account.AvailableBalance,
		account.CreatedAt, account.UpdatedAt)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create account"})
		return
	}

	// Cache the account
	err = s.redis.Set(ctx, "account:"+account.ID, account.AccountNumber, 1*time.Hour).Err()
	if err != nil {
		log.Printf("Warning: failed to cache account: %v", err)
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to commit transaction"})
		return
	}

	c.JSON(http.StatusCreated, account)
}

func (s *AccountService) GetAccount(c *gin.Context) {
	ctx := c.Request.Context()
	accountID := c.Param("account_id")

	// Check cache first
	if cachedNumber, err := s.redis.Get(ctx, "account:"+accountID).Result(); err == nil {
		// If we only cached the account number, we still need to query DB
		// In production, you might cache the full account object
		log.Printf("Cache hit for account: %s (number: %s)", accountID, cachedNumber)
	}

	query := `SELECT id, user_id, account_number, currency, account_type, status, balance, available_balance, created_at, updated_at FROM accounts WHERE id = $1`

	account := &Account{}
	err := s.db.QueryRowContext(ctx, query, accountID).Scan(
		&account.ID, &account.UserID, &account.AccountNumber, &account.Currency,
		&account.AccountType, &account.Status, &account.Balance, &account.AvailableBalance,
		&account.CreatedAt, &account.UpdatedAt)

	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "account not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database error"})
		return
	}

	c.JSON(http.StatusOK, account)
}

func (s *AccountService) HealthCheck(c *gin.Context) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Check database connectivity
	if err := s.db.PingContext(ctx); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy", "database": "unreachable"})
		return
	}

	// Check Redis connectivity
	if err := s.redis.Ping(ctx).Err(); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy", "redis": "unreachable"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "healthy", "service": "account-service"})
}

func generateUUID() string {
	// In production, use a proper UUID library
	return fmt.Sprintf("acc_%d", time.Now().UnixNano())
}

func generateAccountNumber() string {
	// In production, implement proper account number generation with checksum
	return fmt.Sprintf("ACC%d", time.Now().UnixNano()%1000000000)
}

func main() {
	// Initialize tracer
	tp, err := initTracer()
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			log.Printf("Error shutting down tracer provider: %v", err)
		}
	}()

	// Database connection
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://fintech:fintech123@localhost:5432/payflow?sslmode=disable"
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatal("Failed to connect to database:", err)
	}
	defer db.Close()

	// Redis connection
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "localhost:6379"
	}

	redisClient := redis.NewClient(&redis.Options{
		Addr:     redisURL,
		Password: "", // no password set
		DB:       1,  // use default DB
	})

	// Test connections
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		log.Fatal("Database connection failed:", err)
	}

	if err := redisClient.Ping(ctx).Err(); err != nil {
		log.Fatal("Redis connection failed:", err)
	}

	// Create service
	service := &AccountService{
		db:    db,
		redis: redisClient,
	}

	// Setup routes
	r := gin.Default()
	
	// Health check
	r.GET("/health", service.HealthCheck)
	
	// Metrics endpoint
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))
	
	// Account endpoints
	r.POST("/v1/accounts", service.CreateAccount)
	r.GET("/v1/accounts/:account_id", service.GetAccount)

	// Start server
	port := os.Getenv("PORT")
	if port == "" {
		port = "8083"
	}

	server := &http.Server{
		Addr:    ":" + port,
		Handler: r,
	}

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("Server failed to start:", err)
		}
	}()

	log.Printf("Account service started on port %s", port)

	<-quit
	log.Println("Shutting down server...")

	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatal("Server forced to shutdown:", err)
	}

	log.Println("Server exited")
}
