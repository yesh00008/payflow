package main

import (
	"context"
	"database/sql"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// ─── Models ───────────────────────────────────────────────────────────────────

type PaymentMethod struct {
	ID         string    `json:"id"`
	UserID     string    `json:"user_id"`
	Type       string    `json:"type"` // card, bank_account, upi
	Provider   string    `json:"provider"`
	Identifier string    `json:"identifier"` // masked card number or account number
	IsDefault  bool      `json:"is_default"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
}

type PaymentTransaction struct {
	ID            string     `json:"id"`
	UserID        string     `json:"user_id"`
	MethodID      string     `json:"method_id"`
	Amount        float64    `json:"amount"`
	Currency      string     `json:"currency"`
	Type          string     `json:"type"` // deposit, withdrawal
	Status        string     `json:"status"`
	ProviderTxID  string     `json:"provider_tx_id,omitempty"`
	FailureReason string     `json:"failure_reason,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
}

type AddPaymentMethodRequest struct {
	UserID     string `json:"user_id" binding:"required"`
	Type       string `json:"type" binding:"required"`
	Provider   string `json:"provider" binding:"required"`
	Identifier string `json:"identifier" binding:"required"`
	IsDefault  bool   `json:"is_default"`
}

type ProcessPaymentRequest struct {
	UserID   string  `json:"user_id" binding:"required"`
	MethodID string  `json:"method_id" binding:"required"`
	Amount   float64 `json:"amount" binding:"required,gt=0"`
	Currency string  `json:"currency" binding:"required"`
	Type     string  `json:"type" binding:"required"`
}

type PaymentService struct {
	db *sql.DB
}

// ─── Tracer ───────────────────────────────────────────────────────────────────

func initTracer() (*sdktrace.TracerProvider, error) {
	ctx := context.Background()
	res, err := resource.New(ctx, resource.WithAttributes(
		attribute.String("service.name", "payment-connector-service"),
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
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	return tp, nil
}

// ─── Handlers ─────────────────────────────────────────────────────────────────

func (s *PaymentService) HealthCheck(c *gin.Context) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.db.PingContext(ctx); err != nil {
		log.Printf("Database ping failed: %v", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy", "database": "unreachable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "healthy", "service": "payment-connector-service"})
}

// AddPaymentMethod adds a new payment method
func (s *PaymentService) AddPaymentMethod(c *gin.Context) {
	var req AddPaymentMethodRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	methodID := uuid.New().String()
	now := time.Now()

	query := `
		INSERT INTO payment_methods (id, user_id, type, provider, identifier, is_default, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, user_id, type, provider, identifier, is_default, status, created_at
	`

	var method PaymentMethod
	err := s.db.QueryRow(query, methodID, req.UserID, req.Type, req.Provider, req.Identifier, req.IsDefault, "active", now).
		Scan(&method.ID, &method.UserID, &method.Type, &method.Provider, &method.Identifier, &method.IsDefault, &method.Status, &method.CreatedAt)

	if err != nil {
		log.Printf("Failed to add payment method: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to add payment method"})
		return
	}

	c.JSON(http.StatusCreated, method)
}

// GetPaymentMethods retrieves user's payment methods
func (s *PaymentService) GetPaymentMethods(c *gin.Context) {
	userID := c.Param("user_id")

	query := `
		SELECT id, user_id, type, provider, identifier, is_default, status, created_at
		FROM payment_methods
		WHERE user_id = $1 AND status = 'active'
		ORDER BY is_default DESC, created_at DESC
	`

	rows, err := s.db.Query(query, userID)
	if err != nil {
		log.Printf("Failed to get payment methods: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get payment methods"})
		return
	}
	defer rows.Close()

	var methods []PaymentMethod
	for rows.Next() {
		var method PaymentMethod
		err := rows.Scan(&method.ID, &method.UserID, &method.Type, &method.Provider, &method.Identifier,
			&method.IsDefault, &method.Status, &method.CreatedAt)
		if err != nil {
			log.Printf("Failed to scan payment method: %v", err)
			continue
		}
		methods = append(methods, method)
	}

	c.JSON(http.StatusOK, gin.H{"payment_methods": methods})
}

// ProcessPayment processes a payment transaction
func (s *PaymentService) ProcessPayment(c *gin.Context) {
	var req ProcessPaymentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	txID := uuid.New().String()
	now := time.Now()

	// Simulate provider transaction ID
	providerTxID := "PGW-" + uuid.New().String()[:8]

	query := `
		INSERT INTO payment_transactions (id, user_id, method_id, amount, currency, type, status, provider_tx_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, user_id, method_id, amount, currency, type, status, provider_tx_id, created_at
	`

	var tx PaymentTransaction
	err := s.db.QueryRow(query, txID, req.UserID, req.MethodID, req.Amount, req.Currency, req.Type, "processing", providerTxID, now).
		Scan(&tx.ID, &tx.UserID, &tx.MethodID, &tx.Amount, &tx.Currency, &tx.Type, &tx.Status, &tx.ProviderTxID, &tx.CreatedAt)

	if err != nil {
		log.Printf("Failed to create payment transaction: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to process payment"})
		return
	}

	// Simulate async payment processing
	go s.simulatePaymentProcessing(tx.ID, req.Amount)

	c.JSON(http.StatusAccepted, tx)
}

// GetPaymentTransaction retrieves payment transaction details
func (s *PaymentService) GetPaymentTransaction(c *gin.Context) {
	txID := c.Param("transaction_id")

	query := `
		SELECT id, user_id, method_id, amount, currency, type, status, provider_tx_id, failure_reason, created_at, completed_at
		FROM payment_transactions
		WHERE id = $1
	`

	var tx PaymentTransaction
	err := s.db.QueryRow(query, txID).Scan(
		&tx.ID, &tx.UserID, &tx.MethodID, &tx.Amount, &tx.Currency, &tx.Type,
		&tx.Status, &tx.ProviderTxID, &tx.FailureReason, &tx.CreatedAt, &tx.CompletedAt,
	)

	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "Transaction not found"})
		return
	}
	if err != nil {
		log.Printf("Failed to get transaction: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get transaction"})
		return
	}

	c.JSON(http.StatusOK, tx)
}

// GetUserTransactions retrieves user's payment transactions
func (s *PaymentService) GetUserTransactions(c *gin.Context) {
	userID := c.Param("user_id")

	query := `
		SELECT id, user_id, method_id, amount, currency, type, status, provider_tx_id, failure_reason, created_at, completed_at
		FROM payment_transactions
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT 50
	`

	rows, err := s.db.Query(query, userID)
	if err != nil {
		log.Printf("Failed to get transactions: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get transactions"})
		return
	}
	defer rows.Close()

	var transactions []PaymentTransaction
	for rows.Next() {
		var tx PaymentTransaction
		err := rows.Scan(&tx.ID, &tx.UserID, &tx.MethodID, &tx.Amount, &tx.Currency, &tx.Type,
			&tx.Status, &tx.ProviderTxID, &tx.FailureReason, &tx.CreatedAt, &tx.CompletedAt)
		if err != nil {
			log.Printf("Failed to scan transaction: %v", err)
			continue
		}
		transactions = append(transactions, tx)
	}

	c.JSON(http.StatusOK, gin.H{"transactions": transactions})
}

// ─── Background Processing ────────────────────────────────────────────────────

// simulatePaymentProcessing simulates external payment gateway processing
func (s *PaymentService) simulatePaymentProcessing(txID string, amount float64) {
	// Simulate processing delay
	time.Sleep(3 * time.Second)

	// 90% success rate
	success := rand.Float64() < 0.9

	completedAt := time.Now()
	status := "completed"
	failureReason := ""

	if !success {
		status = "failed"
		failureReason = "Payment declined by provider"
	}

	query := `UPDATE payment_transactions SET status = $1, failure_reason = $2, completed_at = $3 WHERE id = $4`
	_, err := s.db.Exec(query, status, failureReason, completedAt, txID)
	if err != nil {
		log.Printf("Failed to update transaction status: %v", err)
		return
	}

	log.Printf("Payment processed: %s (status: %s, amount: %.2f)", txID, status, amount)
}

// ─── Main ─────────────────────────────────────────────────────────────────────

func main() {
	rand.Seed(time.Now().UnixNano())

	tp, err := initTracer()
	if err != nil {
		log.Printf("Failed to initialize tracer: %v", err)
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

	svc := &PaymentService{db: db}

	r := gin.Default()
	r.GET("/health", svc.HealthCheck)
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// Payment method endpoints
	r.POST("/payment-methods", svc.AddPaymentMethod)
	r.GET("/users/:user_id/payment-methods", svc.GetPaymentMethods)

	// Payment transaction endpoints
	r.POST("/payments", svc.ProcessPayment)
	r.GET("/payments/:transaction_id", svc.GetPaymentTransaction)
	r.GET("/users/:user_id/payments", svc.GetUserTransactions)

	port := getEnv("PORT", "8087")
	server := &http.Server{Addr: ":" + port, Handler: r}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Printf("Payment Connector service started on port %s", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	<-quit
	log.Println("Shutting down payment-connector-service...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server.Shutdown(ctx)
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
