package main

import (
	"context"
"database/sql"
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
"github.com/shopspring/decimal"
"go.opentelemetry.io/otel"
"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
"go.opentelemetry.io/otel/propagation"
"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
"google.golang.org/grpc"
"google.golang.org/grpc/credentials/insecure"
)

// ─── Models ───────────────────────────────────────────────────────────────────

type LedgerEntry struct {
	ID           string          `json:"entry_id"`
	AccountID    string          `json:"account_id"`
	Amount       decimal.Decimal `json:"amount"`
	Currency     string          `json:"currency"`
	EntryType    string          `json:"entry_type"` // debit / credit
	Description  string          `json:"description"`
	Reference    string          `json:"reference"`
	BalanceAfter decimal.Decimal `json:"balance_after"`
	CreatedAt    time.Time       `json:"created_at"`
}
type LedgerService struct {
	db *sql.DB
}

// ─── Tracer ───────────────────────────────────────────────────────────────────

func initTracer() (*sdktrace.TracerProvider, error) {
	ctx := context.Background()
	res, _ := resource.New(ctx, resource.WithAttributes(
		semconv.ServiceName("ledger-service"), semconv.ServiceVersion("1.0.0"),
	))
	dialCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(dialCtx, getEnv("OTEL_EXPORTER_OTLP_ENDPOINT", "jaeger:4317"),
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


func (s *LedgerService) RecordEntry(c *gin.Context) {
	ctx := c.Request.Context()

	var req struct {
		AccountID   string `json:"account_id" binding:"required"`
		Amount      int64  `json:"amount" binding:"required"`
		Currency    string `json:"currency" binding:"required,len=3"`
		EntryType   string `json:"entry_type" binding:"required,oneof=debit credit"`
		Description string `json:"description"`
		Reference   string `json:"reference" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	amount := decimal.NewFromInt(req.Amount)

	// Serializable isolation for financial integrity
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "begin tx failed"})
		return
	}
	defer tx.Rollback()

	// Get current balance with row lock
	var currentBalance decimal.Decimal
	err = tx.QueryRowContext(ctx,
		`SELECT COALESCE(balance, 0) FROM account_balances WHERE account_id = $1 FOR UPDATE`,
		req.AccountID).Scan(&currentBalance)
	if err != nil && err != sql.ErrNoRows {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "balance lookup failed"})
		return
	}

	// Calculate new balance
	var newBalance decimal.Decimal
	if req.EntryType == "debit" {
		newBalance = currentBalance.Sub(amount) // debit reduces balance
	} else {
		newBalance = currentBalance.Add(amount) // credit increases balance
	}

	// Insert ledger entry
	entryID := uuid.New().String()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO ledger_entries (id, account_id, amount, currency, entry_type, description, reference, balance_after, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		entryID, req.AccountID, amount, req.Currency, req.EntryType, req.Description, req.Reference, newBalance, time.Now())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "insert entry failed"})
		return
	}

	// Upsert account balance summary
	_, err = tx.ExecContext(ctx,
		`INSERT INTO account_balances (account_id, balance, updated_at) VALUES ($1, $2, $3)
		 ON CONFLICT (account_id) DO UPDATE SET balance = $2, updated_at = $3`,
		req.AccountID, newBalance, time.Now())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "update balance failed"})
		return
	}

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "commit failed"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"entry_id":      entryID,
		"account_id":    req.AccountID,
		"amount":        amount.String(),
		"entry_type":    req.EntryType,
		"balance_after": newBalance.String(),
	})
}

func (s *LedgerService) GetBalance(c *gin.Context) {
	accountID := c.Param("account_id")
	var balance decimal.Decimal
	err := s.db.QueryRowContext(c.Request.Context(),
		`SELECT COALESCE(balance, 0) FROM account_balances WHERE account_id = $1`, accountID).Scan(&balance)
	if err != nil && err != sql.ErrNoRows {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "lookup failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"account_id": accountID, "balance": balance.String()})
}

func (s *LedgerService) GetStatement(c *gin.Context) {
	accountID := c.Param("account_id")
	rows, err := s.db.QueryContext(c.Request.Context(),
		`SELECT id, account_id, amount, currency, entry_type, description, reference, balance_after, created_at
		 FROM ledger_entries WHERE account_id = $1 ORDER BY created_at DESC LIMIT 100`, accountID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query failed"})
		return
	}
	defer rows.Close()

	var entries []LedgerEntry
	for rows.Next() {
		var e LedgerEntry
		if err := rows.Scan(&e.ID, &e.AccountID, &e.Amount, &e.Currency, &e.EntryType,
			&e.Description, &e.Reference, &e.BalanceAfter, &e.CreatedAt); err != nil {
			continue
		}
		entries = append(entries, e)
	}
	if entries == nil {
		entries = []LedgerEntry{}
	}
	c.JSON(http.StatusOK, gin.H{"account_id": accountID, "entries": entries})
}

func (s *LedgerService) HealthCheck(c *gin.Context) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.db.PingContext(ctx); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "healthy", "service": "ledger-service"})
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
		log.Fatal("DB:", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(25)

	svc := &LedgerService{db: db}

	r := gin.Default()
	r.GET("/health", svc.HealthCheck)
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	r.POST("/v1/ledger/entries", svc.RecordEntry)
	r.GET("/v1/ledger/accounts/:account_id/balance", svc.GetBalance)
	r.GET("/v1/ledger/accounts/:account_id/statement", svc.GetStatement)

	port := getEnv("PORT", "8085")
	server := &http.Server{Addr: ":" + port, Handler: r}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go
func() {
		log.Printf("Ledger service started on port %s", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
	<-quit
	log.Println("Shutting down ledger-service...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server.Shutdown(ctx)
}
