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
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/streadway/amqp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// ═══════════════════════════════════════════════════════════════════════════
// MODELS
// ═══════════════════════════════════════════════════════════════════════════

type LedgerEntry struct {
	ID            string    `json:"entry_id"`
	TransactionID string    `json:"transaction_id"`
	AccountID     string    `json:"account_id"`
	EntryType     string    `json:"entry_type"` // debit, credit
	Amount        float64   `json:"amount"`
	Currency      string    `json:"currency"`
	BalanceAfter  float64   `json:"balance_after"`
	Description   string    `json:"description,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

type AccountBalance struct {
	AccountID       string    `json:"account_id"`
	Balance         float64   `json:"balance"`
	Currency        string    `json:"currency"`
	TotalDebits     float64   `json:"total_debits"`
	TotalCredits    float64   `json:"total_credits"`
	LastTransaction time.Time `json:"last_transaction"`
}

type LedgerService struct {
	db       *sql.DB
	amqp     *amqp.Channel
	amqpConn *amqp.Connection
}

// ═══════════════════════════════════════════════════════════════════════════
// METRICS
// ═══════════════════════════════════════════════════════════════════════════

var (
	ledgerEntriesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "payflow_ledger_entries_total",
			Help: "Total number of ledger entries by type",
		},
		[]string{"entry_type"},
	)

	ledgerOperationDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "payflow_ledger_operation_duration_seconds",
			Help:    "Ledger operation duration",
			Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5},
		},
		[]string{"operation"},
	)
)

// ═══════════════════════════════════════════════════════════════════════════
// TRACER INITIALIZATION
// ═══════════════════════════════════════════════════════════════════════════

func initTracer() (*sdktrace.TracerProvider, error) {
	ctx := context.Background()
	res, err := resource.New(ctx, resource.WithAttributes(
		semconv.ServiceName("ledger-service"),
		semconv.ServiceVersion("1.0.0"),
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
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))
	return tp, nil
}

// ═══════════════════════════════════════════════════════════════════════════
// API HANDLERS
// ═══════════════════════════════════════════════════════════════════════════

func (s *LedgerService) CreateEntry(c *gin.Context) {
	startTime := time.Now()
	var req struct {
		TransactionID string  `json:"transaction_id" binding:"required"`
		AccountID     string  `json:"account_id" binding:"required"`
		EntryType     string  `json:"entry_type" binding:"required,oneof=debit credit"`
		Amount        float64 `json:"amount" binding:"required,gt=0"`
		Currency      string  `json:"currency" binding:"required"`
		Description   string  `json:"description"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Get current balance
	var currentBalance float64
	err := s.db.QueryRowContext(c.Request.Context(),
		`SELECT COALESCE(balance, 0) FROM accounts WHERE id = $1`,
		req.AccountID,
	).Scan(&currentBalance)

	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "account not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database error"})
		return
	}

	// Calculate new balance
	newBalance := currentBalance
	if req.EntryType == "debit" {
		newBalance -= req.Amount
	} else {
		newBalance += req.Amount
	}

	// Create ledger entry
	entry := LedgerEntry{
		ID:            uuid.New().String(),
		TransactionID: req.TransactionID,
		AccountID:     req.AccountID,
		EntryType:     req.EntryType,
		Amount:        req.Amount,
		Currency:      req.Currency,
		BalanceAfter:  newBalance,
		Description:   req.Description,
		CreatedAt:     time.Now(),
	}

	// Start database transaction
	tx, err := s.db.BeginTx(c.Request.Context(), nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "transaction failed"})
		return
	}
	defer tx.Rollback()

	// Insert ledger entry
	_, err = tx.ExecContext(c.Request.Context(),
		`INSERT INTO ledger_entries (id, transaction_id, account_id, entry_type, amount, currency, balance_after, description, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		entry.ID, entry.TransactionID, entry.AccountID, entry.EntryType, entry.Amount, entry.Currency, entry.BalanceAfter, entry.Description, entry.CreatedAt,
	)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create entry"})
		return
	}

	// Update account balance
	_, err = tx.ExecContext(c.Request.Context(),
		`UPDATE accounts SET balance = $1, updated_at = $2 WHERE id = $3`,
		newBalance, time.Now(), req.AccountID,
	)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update balance"})
		return
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "commit failed"})
		return
	}

	// Publish ledger event
	go s.publishLedgerEvent("ledger.entry_created", entry)

	ledgerEntriesTotal.WithLabelValues(req.EntryType).Inc()
	ledgerOperationDuration.WithLabelValues("create_entry").Observe(time.Since(startTime).Seconds())

	c.JSON(http.StatusCreated, entry)
}

func (s *LedgerService) GetEntries(c *gin.Context) {
	startTime := time.Now()
	accountID := c.Query("account_id")
	transactionID := c.Query("transaction_id")
	limit := c.DefaultQuery("limit", "100")

	if accountID == "" && transactionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "account_id or transaction_id required"})
		return
	}

	query := `SELECT id, transaction_id, account_id, entry_type, amount, currency, balance_after, description, created_at
	          FROM ledger_entries WHERE 1=1`
	args := []interface{}{}
	argCount := 1

	if accountID != "" {
		query += fmt.Sprintf(" AND account_id = $%d", argCount)
		args = append(args, accountID)
		argCount++
	}
	if transactionID != "" {
		query += fmt.Sprintf(" AND transaction_id = $%d", argCount)
		args = append(args, transactionID)
		argCount++
	}

	query += " ORDER BY created_at DESC LIMIT " + limit

	rows, err := s.db.QueryContext(c.Request.Context(), query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database error"})
		return
	}
	defer rows.Close()

	entries := []LedgerEntry{}
	for rows.Next() {
		var entry LedgerEntry
		var description sql.NullString

		err := rows.Scan(&entry.ID, &entry.TransactionID, &entry.AccountID, &entry.EntryType,
			&entry.Amount, &entry.Currency, &entry.BalanceAfter, &description, &entry.CreatedAt)

		if err != nil {
			continue
		}

		if description.Valid {
			entry.Description = description.String
		}

		entries = append(entries, entry)
	}

	ledgerOperationDuration.WithLabelValues("get_entries").Observe(time.Since(startTime).Seconds())

	c.JSON(http.StatusOK, gin.H{"entries": entries, "count": len(entries)})
}

func (s *LedgerService) GetAccountBalance(c *gin.Context) {
	startTime := time.Now()
	accountID := c.Param("account_id")

	var balance AccountBalance
	var lastTx sql.NullTime

	err := s.db.QueryRowContext(c.Request.Context(),
		`SELECT 
			a.id, 
			COALESCE(a.balance, 0) as balance,
			a.currency,
			COALESCE(SUM(CASE WHEN le.entry_type = 'debit' THEN le.amount ELSE 0 END), 0) as total_debits,
			COALESCE(SUM(CASE WHEN le.entry_type = 'credit' THEN le.amount ELSE 0 END), 0) as total_credits,
			MAX(le.created_at) as last_transaction
		FROM accounts a
		LEFT JOIN ledger_entries le ON a.id = le.account_id
		WHERE a.id = $1
		GROUP BY a.id, a.balance, a.currency`,
		accountID,
	).Scan(&balance.AccountID, &balance.Balance, &balance.Currency, &balance.TotalDebits, &balance.TotalCredits, &lastTx)

	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "account not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database error"})
		return
	}

	if lastTx.Valid {
		balance.LastTransaction = lastTx.Time
	}

	ledgerOperationDuration.WithLabelValues("get_balance").Observe(time.Since(startTime).Seconds())

	c.JSON(http.StatusOK, balance)
}

func (s *LedgerService) GetAccountStatement(c *gin.Context) {
	startTime := time.Now()
	accountID := c.Param("account_id")
	startDate := c.Query("start_date")
	endDate := c.Query("end_date")

	query := `SELECT id, transaction_id, account_id, entry_type, amount, currency, balance_after, description, created_at
	          FROM ledger_entries WHERE account_id = $1`
	args := []interface{}{accountID}
	argCount := 2

	if startDate != "" {
		query += fmt.Sprintf(" AND created_at >= $%d", argCount)
		args = append(args, startDate)
		argCount++
	}
	if endDate != "" {
		query += fmt.Sprintf(" AND created_at <= $%d", argCount)
		args = append(args, endDate)
		argCount++
	}

	query += " ORDER BY created_at ASC"

	rows, err := s.db.QueryContext(c.Request.Context(), query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database error"})
		return
	}
	defer rows.Close()

	entries := []LedgerEntry{}
	for rows.Next() {
		var entry LedgerEntry
		var description sql.NullString

		err := rows.Scan(&entry.ID, &entry.TransactionID, &entry.AccountID, &entry.EntryType,
			&entry.Amount, &entry.Currency, &entry.BalanceAfter, &description, &entry.CreatedAt)

		if err != nil {
			continue
		}

		if description.Valid {
			entry.Description = description.String
		}

		entries = append(entries, entry)
	}

	ledgerOperationDuration.WithLabelValues("get_statement").Observe(time.Since(startTime).Seconds())

	c.JSON(http.StatusOK, gin.H{
		"account_id": accountID,
		"entries":    entries,
		"count":      len(entries),
		"start_date": startDate,
		"end_date":   endDate,
	})
}

func (s *LedgerService) ReconcileAccount(c *gin.Context) {
	startTime := time.Now()
	accountID := c.Param("account_id")

	// Calculate balance from ledger entries
	var calculatedBalance float64
	err := s.db.QueryRowContext(c.Request.Context(),
		`SELECT 
			COALESCE(SUM(CASE WHEN entry_type = 'credit' THEN amount ELSE -amount END), 0)
		FROM ledger_entries
		WHERE account_id = $1`,
		accountID,
	).Scan(&calculatedBalance)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "calculation failed"})
		return
	}

	// Get account balance
	var accountBalance float64
	err = s.db.QueryRowContext(c.Request.Context(),
		`SELECT COALESCE(balance, 0) FROM accounts WHERE id = $1`,
		accountID,
	).Scan(&accountBalance)

	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "account not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database error"})
		return
	}

	// Check if reconciliation needed
	discrepancy := accountBalance - calculatedBalance
	reconciled := (discrepancy > -0.01 && discrepancy < 0.01) // Allow 1 cent tolerance

	response := gin.H{
		"account_id":         accountID,
		"account_balance":    accountBalance,
		"calculated_balance": calculatedBalance,
		"discrepancy":        discrepancy,
		"reconciled":         reconciled,
	}

	// Auto-correct if small discrepancy
	if !reconciled && discrepancy > -1.00 && discrepancy < 1.00 {
		_, err := s.db.ExecContext(c.Request.Context(),
			`UPDATE accounts SET balance = $1 WHERE id = $2`,
			calculatedBalance, accountID,
		)
		if err == nil {
			response["auto_corrected"] = true
			response["account_balance"] = calculatedBalance
			response["reconciled"] = true
		}
	}

	ledgerOperationDuration.WithLabelValues("reconcile").Observe(time.Since(startTime).Seconds())

	c.JSON(http.StatusOK, response)
}

func (s *LedgerService) HealthCheck(c *gin.Context) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	health := gin.H{"status": "healthy", "service": "ledger-service"}

	if err := s.db.PingContext(ctx); err != nil {
		health["status"] = "unhealthy"
		health["database"] = "unreachable"
		c.JSON(http.StatusServiceUnavailable, health)
		return
	}

	c.JSON(http.StatusOK, health)
}

// ═══════════════════════════════════════════════════════════════════════════
// EVENT PUBLISHING
// ═══════════════════════════════════════════════════════════════════════════

func (s *LedgerService) publishLedgerEvent(eventType string, entry LedgerEntry) {
	if s.amqp == nil {
		return
	}

	event := map[string]interface{}{
		"entry_id":       entry.ID,
		"transaction_id": entry.TransactionID,
		"account_id":     entry.AccountID,
		"entry_type":     entry.EntryType,
		"amount":         entry.Amount,
		"currency":       entry.Currency,
		"balance_after":  entry.BalanceAfter,
		"timestamp":      time.Now().Format(time.RFC3339),
	}

	eventJSON, _ := json.Marshal(event)

	s.amqp.Publish("ledger_events", "ledger.entry", false, false, amqp.Publishing{
		ContentType: "application/json",
		Body:        eventJSON,
	})
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ═══════════════════════════════════════════════════════════════════════════
// MAIN
// ═══════════════════════════════════════════════════════════════════════════

func main() {
	// Initialize tracer
	tp, err := initTracer()
	if err != nil {
		log.Printf("Warning: tracer init failed: %v", err)
	} else {
		defer tp.Shutdown(context.Background())
	}

	// Connect to PostgreSQL
	db, err := sql.Open("postgres", getEnv("DATABASE_URL", "postgres://fintech:fintech123@localhost:5432/payflow?sslmode=disable"))
	if err != nil {
		log.Fatal("DB open:", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(50)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(time.Hour)

	// Connect to RabbitMQ
	amqpConn, err := amqp.Dial(getEnv("RABBITMQ_URL", "amqp://fintech:rabbit123@localhost:5672/"))
	if err != nil {
		log.Printf("Warning: RabbitMQ connection failed: %v", err)
	}
	var amqpCh *amqp.Channel
	if amqpConn != nil {
		amqpCh, _ = amqpConn.Channel()
		if amqpCh != nil {
			amqpCh.ExchangeDeclare("ledger_events", "topic", true, false, false, false, nil)
		}
	}

	service := &LedgerService{
		db:       db,
		amqp:     amqpCh,
		amqpConn: amqpConn,
	}

	// Setup Gin router
	r := gin.Default()
	r.GET("/health", service.HealthCheck)
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	r.POST("/ledger/entries", service.CreateEntry)
	r.GET("/ledger/entries", service.GetEntries)
	r.GET("/ledger/accounts/:account_id/balance", service.GetAccountBalance)
	r.GET("/ledger/accounts/:account_id/statement", service.GetAccountStatement)
	r.POST("/ledger/accounts/:account_id/reconcile", service.ReconcileAccount)

	port := getEnv("PORT", "8085")
	server := &http.Server{Addr: ":" + port, Handler: r}

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Printf("Ledger service started on port %s", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	<-quit
	log.Println("Shutting down ledger-service...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if amqpCh != nil {
		amqpCh.Close()
	}
	if amqpConn != nil {
		amqpConn.Close()
	}

	server.Shutdown(ctx)
}
