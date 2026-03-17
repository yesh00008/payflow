package main

import (
	"context"
	"database/sql"
	"encoding/json"
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

// ─── Models ───────────────────────────────────────────────────────────────────

type Payment struct {
	ID            string    `json:"payment_id"`
	AccountID     string    `json:"account_id"`
	Provider      string    `json:"provider"` // stripe, wise, bank_transfer
	Amount        int64     `json:"amount"`
	Currency      string    `json:"currency"`
	Status        string    `json:"status"` // pending, processing, completed, failed
	ExternalRef   string    `json:"external_reference"`
	PaymentMethod string    `json:"payment_method"` // card, bank, wallet
	Description   string    `json:"description"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type PaymentConnectorService struct {
	db       *sql.DB
	rabbitMQ *amqp.Connection
}

// ─── Tracer ───────────────────────────────────────────────────────────────────

func initTracer() (*sdktrace.TracerProvider, error) {
	ctx := context.Background()
	res, _ := resource.New(ctx, resource.WithAttributes(
		semconv.ServiceName("payment-connector-service"), semconv.ServiceVersion("1.0.0"),
	))
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

func (s *PaymentConnectorService) InitiatePayment(c *gin.Context) {
	ctx := c.Request.Context()

	var req struct {
		AccountID     string `json:"account_id" binding:"required"`
		Provider      string `json:"provider" binding:"required,oneof=stripe wise bank_transfer"`
		Amount        int64  `json:"amount" binding:"required,gt=0"`
		Currency      string `json:"currency" binding:"required,len=3"`
		PaymentMethod string `json:"payment_method" binding:"required,oneof=card bank wallet"`
		Description   string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	payment := Payment{
		ID:            uuid.New().String(),
		AccountID:     req.AccountID,
		Provider:      req.Provider,
		Amount:        req.Amount,
		Currency:      req.Currency,
		Status:        "pending",
		ExternalRef:   "ext_" + uuid.New().String()[:8],
		PaymentMethod: req.PaymentMethod,
		Description:   req.Description,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO payments (id, account_id, provider, amount, currency, status, external_reference, payment_method, description, created_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		payment.ID, payment.AccountID, payment.Provider, payment.Amount, payment.Currency,
		payment.Status, payment.ExternalRef, payment.PaymentMethod, payment.Description,
		payment.CreatedAt, payment.UpdatedAt)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to initiate payment"})
		return
	}

	// Simulate async processing - publish event
	s.publishPaymentEvent(ctx, "payment.initiated", payment)

	// Simulate provider processing and mark as completed
	go func() {
		time.Sleep(2 * time.Second) // simulate external provider latency
		s.db.Exec(
			`UPDATE payments SET status = 'completed', updated_at = $1 WHERE id = $2`,
			time.Now(), payment.ID)
		payment.Status = "completed"
		s.publishPaymentEvent(context.Background(), "payment.completed", payment)
	}()

	c.JSON(http.StatusCreated, payment)
}

func (s *PaymentConnectorService) GetPayment(c *gin.Context) {
	paymentID := c.Param("payment_id")
	var p Payment
	err := s.db.QueryRowContext(c.Request.Context(),
		`SELECT id, account_id, provider, amount, currency, status, external_reference, payment_method, description, created_at, updated_at
		 FROM payments WHERE id = $1`, paymentID).
		Scan(&p.ID, &p.AccountID, &p.Provider, &p.Amount, &p.Currency, &p.Status,
			&p.ExternalRef, &p.PaymentMethod, &p.Description, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "payment not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}
	c.JSON(http.StatusOK, p)
}

func (s *PaymentConnectorService) ListPayments(c *gin.Context) {
	accountID := c.Query("account_id")
	query := `SELECT id, account_id, provider, amount, currency, status, external_reference, payment_method, description, created_at, updated_at
	          FROM payments WHERE account_id = $1 ORDER BY created_at DESC LIMIT 50`
	rows, err := s.db.QueryContext(c.Request.Context(), query, accountID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query failed"})
		return
	}
	defer rows.Close()

	var payments []Payment
	for rows.Next() {
		var p Payment
		rows.Scan(&p.ID, &p.AccountID, &p.Provider, &p.Amount, &p.Currency, &p.Status,
			&p.ExternalRef, &p.PaymentMethod, &p.Description, &p.CreatedAt, &p.UpdatedAt)
		payments = append(payments, p)
	}
	if payments == nil {
		payments = []Payment{}
	}
	c.JSON(http.StatusOK, gin.H{"payments": payments})
}

func (s *PaymentConnectorService) publishPaymentEvent(ctx context.Context, eventType string, p Payment) {
	ch, err := s.rabbitMQ.Channel()
	if err != nil {
		log.Printf("RabbitMQ channel error: %v", err)
		return
	}
	defer ch.Close()

	body, _ := json.Marshal(map[string]interface{}{
		"event_type": eventType,
		"payment_id": p.ID,
		"account_id": p.AccountID,
		"amount":     p.Amount,
		"currency":   p.Currency,
		"status":     p.Status,
		"timestamp":  time.Now().Format(time.RFC3339),
	})

	ch.PublishWithContext(ctx, "events", "payment", false, false, amqp.Publishing{
		ContentType: "application/json", Body: body,
	})
}

func (s *PaymentConnectorService) HealthCheck(c *gin.Context) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.db.PingContext(ctx); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "healthy", "service": "payment-connector-service"})
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

	rabbitConn, err := amqp.Dial(getEnv("RABBITMQ_URL", "amqp://fintech:rabbit123@localhost:5672/"))
	if err != nil {
		log.Fatal("RabbitMQ:", err)
	}
	defer rabbitConn.Close()

	svc := &PaymentConnectorService{db: db, rabbitMQ: rabbitConn}

	r := gin.Default()
	r.GET("/health", svc.HealthCheck)
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	r.POST("/v1/payments", svc.InitiatePayment)
	r.GET("/v1/payments/:payment_id", svc.GetPayment)
	r.GET("/v1/payments", svc.ListPayments)

	port := getEnv("PORT", "8086")
	server := &http.Server{Addr: ":" + port, Handler: r}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		log.Printf("Payment connector service started on port %s", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
	<-quit
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server.Shutdown(ctx)
}
