package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type TransactionService struct {
	db         *sql.DB
	rabbitMQ   *amqp.Connection
	accountURL string
	ledgerURL  string
}

type TransferRequest struct {
	SenderAccountID   string `json:"sender_account_id" binding:"required"`
	ReceiverAccountID string `json:"receiver_account_id" binding:"required"`
	Amount            int64  `json:"amount" binding:"required,gt=0"`
	Currency          string `json:"currency" binding:"required,len=3"`
	Reference         string `json:"reference"`
	Description       string `json:"description"`
}

type TransferResponse struct {
	TransferID string `json:"transfer_id"`
	Status     string `json:"status"`
	Reference  string `json:"reference"`
	CreatedAt  string `json:"created_at"`
}

type IdempotencyRecord struct {
	Key         string    `db:"key"`
	PayloadHash string    `db:"payload_hash"`
	Result      []byte    `db:"result"`
	ExpiresAt   time.Time `db:"expires_at"`
	CreatedAt   time.Time `db:"created_at"`
}

type TransferSaga struct {
	SagaID            string     `json:"saga_id"`
	SenderAccountID   string     `json:"sender_account_id"`
	ReceiverAccountID string     `json:"receiver_account_id"`
	Amount            int64      `json:"amount"`
	Currency          string     `json:"currency"`
	Status            string     `json:"status"`
	Steps             []SagaStep `json:"steps"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type SagaStep struct {
	StepID      string     `json:"step_id"`
	Service     string     `json:"service"`
	Action      string     `json:"action"`
	Status      string     `json:"status"`
	Error       *string    `json:"error,omitempty"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

func initTracer() (*sdktrace.TracerProvider, error) {
	ctx := context.Background()

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName("transaction-service"),
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

func (s *TransactionService) CreateTransfer(c *gin.Context) {
	ctx := c.Request.Context()

	idempotencyKey := c.GetHeader("Idempotency-Key")
	if idempotencyKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Idempotency-Key header is required"})
		return
	}

	var req TransferRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Check idempotency
	if existing, err := s.checkIdempotency(ctx, idempotencyKey); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "idempotency check failed"})
		return
	} else if existing != nil {
		c.JSON(http.StatusOK, existing)
		return
	}

	// Validate accounts exist and are in same currency
	if !s.validateAccounts(ctx, req.SenderAccountID, req.ReceiverAccountID, req.Currency) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid accounts or currency mismatch"})
		return
	}

	// Create saga for transfer coordination
	saga := &TransferSaga{
		SagaID:            uuid.New().String(),
		SenderAccountID:   req.SenderAccountID,
		ReceiverAccountID: req.ReceiverAccountID,
		Amount:            req.Amount,
		Currency:          req.Currency,
		Status:            "started",
		Steps: []SagaStep{
			{StepID: uuid.New().String(), Service: "account", Action: "debit_sender", Status: "pending"},
			{StepID: uuid.New().String(), Service: "ledger", Action: "record_debit", Status: "pending"},
			{StepID: uuid.New().String(), Service: "account", Action: "credit_receiver", Status: "pending"},
			{StepID: uuid.New().String(), Service: "ledger", Action: "record_credit", Status: "pending"},
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// Execute saga with compensation
	result, err := s.executeTransferSaga(ctx, saga)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("transfer failed: %v", err)})
		return
	}

	if result.Status != "completed" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("transfer saga failed: %s", result.Error)})
		return
	}

	// Store idempotency record
	response := TransferResponse{
		TransferID: result.TransferID,
		Status:     "completed",
		Reference:  req.Reference,
		CreatedAt:  time.Now().Format(time.RFC3339),
	}

	if err := s.storeIdempotency(ctx, idempotencyKey, response); err != nil {
		log.Printf("Warning: failed to store idempotency key: %v", err)
	}

	// Publish event
	s.publishTransferEvent(ctx, response)

	c.JSON(http.StatusCreated, response)
}

func (s *TransactionService) executeTransferSaga(ctx context.Context, saga *TransferSaga) (*TransferResult, error) {
	transferID := uuid.New().String()

	// Step 1: Debit sender account
	step1 := &saga.Steps[0]
	step1.Status = "processing"
	step1.StartedAt = time.Now()

	if err := s.debitAccount(ctx, saga.SenderAccountID, saga.Amount); err != nil {
		step1.Status = "failed"
		errMsg1 := err.Error()
		step1.Error = &errMsg1
		return &TransferResult{TransferID: transferID, Status: "failed", Error: err.Error()}, s.compensateSaga(ctx, saga, 0)
	}

	step1.Status = "completed"
	completedAt := time.Now()
	step1.CompletedAt = &completedAt

	// Step 2: Record debit in ledger
	step2 := &saga.Steps[1]
	step2.Status = "processing"
	step2.StartedAt = time.Now()

	if err := s.recordLedgerEntry(ctx, saga.SenderAccountID, -saga.Amount, saga.Currency, "Transfer to "+saga.ReceiverAccountID); err != nil {
		step2.Status = "failed"
		errMsg2 := err.Error()
		step2.Error = &errMsg2
		return &TransferResult{TransferID: transferID, Status: "failed", Error: err.Error()}, s.compensateSaga(ctx, saga, 1)
	}

	step2.Status = "completed"
	completedAt = time.Now()
	step2.CompletedAt = &completedAt

	// Step 3: Credit receiver account
	step3 := &saga.Steps[2]
	step3.Status = "processing"
	step3.StartedAt = time.Now()

	if err := s.creditAccount(ctx, saga.ReceiverAccountID, saga.Amount); err != nil {
		step3.Status = "failed"
		errMsg3 := err.Error()
		step3.Error = &errMsg3
		return &TransferResult{TransferID: transferID, Status: "failed", Error: err.Error()}, s.compensateSaga(ctx, saga, 2)
	}

	step3.Status = "completed"
	completedAt = time.Now()
	step3.CompletedAt = &completedAt

	// Step 4: Record credit in ledger
	step4 := &saga.Steps[3]
	step4.Status = "processing"
	step4.StartedAt = time.Now()

	if err := s.recordLedgerEntry(ctx, saga.ReceiverAccountID, saga.Amount, saga.Currency, "Transfer from "+saga.SenderAccountID); err != nil {
		step4.Status = "failed"
		errMsg4 := err.Error()
		step4.Error = &errMsg4
		return &TransferResult{TransferID: transferID, Status: "failed", Error: err.Error()}, s.compensateSaga(ctx, saga, 3)
	}

	step4.Status = "completed"
	completedAt = time.Now()
	step4.CompletedAt = &completedAt

	saga.Status = "completed"
	saga.UpdatedAt = time.Now()

	return &TransferResult{TransferID: transferID, Status: "completed"}, nil
}

func (s *TransactionService) compensateSaga(ctx context.Context, saga *TransferSaga, failedStep int) error {
	// Reverse completed steps in reverse order
	for i := failedStep - 1; i >= 0; i-- {
		step := &saga.Steps[i]
		if step.Status == "completed" {
			var err error
			switch step.Action {
			case "record_credit":
				err = s.recordLedgerEntry(ctx, saga.ReceiverAccountID, -saga.Amount, saga.Currency, "Transfer compensation - reverse credit")
			case "credit_receiver":
				err = s.debitAccount(ctx, saga.ReceiverAccountID, saga.Amount)
			case "record_debit":
				err = s.recordLedgerEntry(ctx, saga.SenderAccountID, saga.Amount, saga.Currency, "Transfer compensation - reverse debit")
			case "debit_sender":
				err = s.creditAccount(ctx, saga.SenderAccountID, saga.Amount)
			}
			if err != nil {
				log.Printf("Compensation failed for step %s: %v", step.Action, err)
			}
		}
	}
	return fmt.Errorf("transfer failed at step %d", failedStep)
}

func (s *TransactionService) debitAccount(ctx context.Context, accountID string, amount int64) error {
	// In production, this would call the account service via HTTP/gRPC
	// For now, we'll simulate the call
	log.Printf("Debiting account %s with amount %d", accountID, amount)
	// Simulate API call
	// client := &http.Client{Timeout: 10 * time.Second}
	// resp, err := client.Post(s.accountURL+"/debit", "application/json", bytes.NewReader(payload))
	return nil
}

func (s *TransactionService) creditAccount(ctx context.Context, accountID string, amount int64) error {
	log.Printf("Crediting account %s with amount %d", accountID, amount)
	return nil
}

func (s *TransactionService) recordLedgerEntry(ctx context.Context, accountID string, amount int64, currency, description string) error {
	log.Printf("Recording ledger entry for account %s: amount=%d, description=%s", accountID, amount, description)
	// In production, this would call the ledger service
	return nil
}

func (s *TransactionService) validateAccounts(ctx context.Context, senderID, receiverID, currency string) bool {
	// In production, validate accounts exist and are in same currency
	log.Printf("Validating accounts: sender=%s, receiver=%s, currency=%s", senderID, receiverID, currency)
	return true // Simulate successful validation
}

func (s *TransactionService) checkIdempotency(ctx context.Context, key string) (*TransferResponse, error) {
	// Check if idempotency key exists
	var result []byte
	err := s.db.QueryRowContext(ctx, "SELECT result FROM idempotency_records WHERE key = $1 AND expires_at > NOW()", key).Scan(&result)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // Key doesn't exist
		}
		return nil, err
	}

	var response TransferResponse
	if err := json.Unmarshal(result, &response); err != nil {
		return nil, err
	}

	return &response, nil
}

func (s *TransactionService) storeIdempotency(ctx context.Context, key string, response interface{}) error {
	result, err := json.Marshal(response)
	if err != nil {
		return err
	}

	expiresAt := time.Now().Add(24 * time.Hour)
	_, err = s.db.ExecContext(ctx,
		"INSERT INTO idempotency_records (key, result, expires_at, created_at) VALUES ($1, $2, $3, $4)",
		key, result, expiresAt, time.Now())

	return err
}

func (s *TransactionService) publishTransferEvent(ctx context.Context, response TransferResponse) {
	ch, err := s.rabbitMQ.Channel()
	if err != nil {
		log.Printf("Failed to open RabbitMQ channel: %v", err)
		return
	}
	defer ch.Close()

	body, _ := json.Marshal(map[string]interface{}{
		"event_type":  "transfer.completed",
		"transfer_id": response.TransferID,
		"status":      response.Status,
		"timestamp":   time.Now().Format(time.RFC3339),
	})

	err = ch.PublishWithContext(ctx,
		"events",   // exchange
		"transfer", // routing key
		false,      // mandatory
		false,      // immediate
		amqp.Publishing{
			ContentType: "application/json",
			Body:        body,
		})
	if err != nil {
		log.Printf("Failed to publish transfer event: %v", err)
	}
}

func (s *TransactionService) HealthCheck(c *gin.Context) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Check database connectivity
	if err := s.db.PingContext(ctx); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy", "database": "unreachable"})
		return
	}

	// Check RabbitMQ connectivity
	ch, err := s.rabbitMQ.Channel()
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy", "rabbitmq": "unreachable"})
		return
	}
	ch.Close()

	c.JSON(http.StatusOK, gin.H{"status": "healthy", "service": "transaction-service"})
}

type TransferResult struct {
	TransferID string `json:"transfer_id"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
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

	// RabbitMQ connection
	rabbitURL := os.Getenv("RABBITMQ_URL")
	if rabbitURL == "" {
		rabbitURL = "amqp://fintech:rabbit123@localhost:5672/"
	}

	rabbitConn, err := amqp.Dial(rabbitURL)
	if err != nil {
		log.Fatal("Failed to connect to RabbitMQ:", err)
	}
	defer rabbitConn.Close()

	// Create service
	service := &TransactionService{
		db:         db,
		rabbitMQ:   rabbitConn,
		accountURL: os.Getenv("ACCOUNT_SERVICE_URL"),
		ledgerURL:  os.Getenv("LEDGER_SERVICE_URL"),
	}

	// Test connections
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		log.Fatal("Database connection failed:", err)
	}

	// Setup routes
	r := gin.Default()

	// Health check
	r.GET("/health", service.HealthCheck)

	// Metrics endpoint
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// Transfer endpoints
	r.POST("/v1/transfers", service.CreateTransfer)

	// Start server
	port := os.Getenv("PORT")
	if port == "" {
		port = "8084"
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

	log.Printf("Transaction service started on port %s", port)

	<-quit
	log.Println("Shutting down server...")

	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatal("Server forced to shutdown:", err)
	}

	log.Println("Server exited")
}
