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
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// ─── Models ───────────────────────────────────────────────────────────────────

type Notification struct {
	ID        string     `json:"id"`
	UserID    string     `json:"user_id"`
	Type      string     `json:"type"` // email, sms, push
	Channel   string     `json:"channel"`
	Recipient string     `json:"recipient"`
	Subject   string     `json:"subject,omitempty"`
	Message   string     `json:"message"`
	Status    string     `json:"status"`
	SentAt    *time.Time `json:"sent_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

type SendNotificationRequest struct {
	UserID    string `json:"user_id" binding:"required"`
	Type      string `json:"type" binding:"required"`
	Channel   string `json:"channel" binding:"required"`
	Recipient string `json:"recipient" binding:"required"`
	Subject   string `json:"subject"`
	Message   string `json:"message" binding:"required"`
}

type NotificationEvent struct {
	EventType string                 `json:"event_type"`
	UserID    string                 `json:"user_id"`
	Data      map[string]interface{} `json:"data"`
}

type NotificationService struct {
	db   *sql.DB
	amqp *amqp.Connection
}

// ─── Tracer ───────────────────────────────────────────────────────────────────

func initTracer() (*sdktrace.TracerProvider, error) {
	ctx := context.Background()
	res, err := resource.New(ctx, resource.WithAttributes(
		attribute.String("service.name", "notification-service"),
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

func (s *NotificationService) HealthCheck(c *gin.Context) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.db.PingContext(ctx); err != nil {
		log.Printf("Database ping failed: %v", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy", "database": "unreachable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "healthy", "service": "notification-service"})
}

// SendNotification sends a notification
func (s *NotificationService) SendNotification(c *gin.Context) {
	var req SendNotificationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	notifID := uuid.New().String()
	now := time.Now()

	query := `
		INSERT INTO notifications (id, user_id, type, channel, recipient, subject, message, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, user_id, type, channel, recipient, subject, message, status, created_at
	`

	var notif Notification
	err := s.db.QueryRow(query, notifID, req.UserID, req.Type, req.Channel, req.Recipient, req.Subject, req.Message, "pending", now).
		Scan(&notif.ID, &notif.UserID, &notif.Type, &notif.Channel, &notif.Recipient, &notif.Subject, &notif.Message, &notif.Status, &notif.CreatedAt)

	if err != nil {
		log.Printf("Failed to create notification: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to send notification"})
		return
	}

	// Simulate sending notification (in production, this would integrate with email/SMS providers)
	go s.simulateSend(notif.ID, req.Type)

	c.JSON(http.StatusCreated, notif)
}

// GetNotifications retrieves user notifications
func (s *NotificationService) GetNotifications(c *gin.Context) {
	userID := c.Param("user_id")

	query := `
		SELECT id, user_id, type, channel, recipient, subject, message, status, sent_at, created_at
		FROM notifications
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT 50
	`

	rows, err := s.db.Query(query, userID)
	if err != nil {
		log.Printf("Failed to get notifications: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get notifications"})
		return
	}
	defer rows.Close()

	var notifications []Notification
	for rows.Next() {
		var notif Notification
		err := rows.Scan(&notif.ID, &notif.UserID, &notif.Type, &notif.Channel, &notif.Recipient,
			&notif.Subject, &notif.Message, &notif.Status, &notif.SentAt, &notif.CreatedAt)
		if err != nil {
			log.Printf("Failed to scan notification: %v", err)
			continue
		}
		notifications = append(notifications, notif)
	}

	c.JSON(http.StatusOK, gin.H{"notifications": notifications})
}

// GetNotification retrieves a single notification
func (s *NotificationService) GetNotification(c *gin.Context) {
	notifID := c.Param("notification_id")

	query := `
		SELECT id, user_id, type, channel, recipient, subject, message, status, sent_at, created_at
		FROM notifications
		WHERE id = $1
	`

	var notif Notification
	err := s.db.QueryRow(query, notifID).Scan(
		&notif.ID, &notif.UserID, &notif.Type, &notif.Channel, &notif.Recipient,
		&notif.Subject, &notif.Message, &notif.Status, &notif.SentAt, &notif.CreatedAt,
	)

	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "Notification not found"})
		return
	}
	if err != nil {
		log.Printf("Failed to get notification: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get notification"})
		return
	}

	c.JSON(http.StatusOK, notif)
}

// ─── Background Workers ───────────────────────────────────────────────────────

// simulateSend simulates sending a notification
func (s *NotificationService) simulateSend(notifID string, notifType string) {
	// Simulate delay
	time.Sleep(2 * time.Second)

	sentAt := time.Now()
	query := `UPDATE notifications SET status = 'sent', sent_at = $1 WHERE id = $2`
	_, err := s.db.Exec(query, sentAt, notifID)
	if err != nil {
		log.Printf("Failed to update notification status: %v", err)
		return
	}

	log.Printf("Notification sent: %s (type: %s)", notifID, notifType)
}

// consumeNotificationEvents listens for notification events from RabbitMQ
func (s *NotificationService) consumeNotificationEvents() {
	ch, err := s.amqp.Channel()
	if err != nil {
		log.Printf("Failed to open channel: %v", err)
		return
	}
	defer ch.Close()

	q, err := ch.QueueDeclare(
		"notifications", // queue name
		true,            // durable
		false,           // delete when unused
		false,           // exclusive
		false,           // no-wait
		nil,             // arguments
	)
	if err != nil {
		log.Printf("Failed to declare queue: %v", err)
		return
	}

	msgs, err := ch.Consume(
		q.Name, // queue
		"",     // consumer
		true,   // auto-ack
		false,  // exclusive
		false,  // no-local
		false,  // no-wait
		nil,    // args
	)
	if err != nil {
		log.Printf("Failed to register consumer: %v", err)
		return
	}

	log.Println("Started consuming notification events from RabbitMQ")

	for msg := range msgs {
		var event NotificationEvent
		if err := json.Unmarshal(msg.Body, &event); err != nil {
			log.Printf("Failed to unmarshal event: %v", err)
			continue
		}

		log.Printf("Received notification event: %s for user %s", event.EventType, event.UserID)
		// Process event and send notification
		s.processNotificationEvent(event)
	}
}

func (s *NotificationService) processNotificationEvent(event NotificationEvent) {
	// Extract data from event
	recipient := ""
	message := ""

	switch event.EventType {
	case "transaction.completed":
		recipient = event.UserID + "@example.com"
		message = "Your transaction has been completed successfully"
	case "kyc.approved":
		recipient = event.UserID + "@example.com"
		message = "Your KYC verification has been approved"
	case "account.created":
		recipient = event.UserID + "@example.com"
		message = "Your account has been created successfully"
	default:
		recipient = event.UserID + "@example.com"
		message = "Event notification: " + event.EventType
	}

	notifID := uuid.New().String()
	now := time.Now()

	query := `
		INSERT INTO notifications (id, user_id, type, channel, recipient, subject, message, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`

	_, err := s.db.Exec(query, notifID, event.UserID, "email", "system", recipient, event.EventType, message, "pending", now)
	if err != nil {
		log.Printf("Failed to create notification from event: %v", err)
		return
	}

	go s.simulateSend(notifID, "email")
}

// ─── Main ─────────────────────────────────────────────────────────────────────

func main() {
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

	amqpConn, err := amqp.Dial(getEnv("RABBITMQ_URL", "amqp://fintech:rabbit123@localhost:5672/"))
	if err != nil {
		log.Printf("Failed to connect to RabbitMQ: %v", err)
	} else {
		defer amqpConn.Close()
	}

	svc := &NotificationService{db: db, amqp: amqpConn}

	// Start background consumer
	if amqpConn != nil {
		go svc.consumeNotificationEvents()
	}

	r := gin.Default()
	r.GET("/health", svc.HealthCheck)
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// Notification endpoints
	r.POST("/notifications", svc.SendNotification)
	r.GET("/notifications/:notification_id", svc.GetNotification)
	r.GET("/users/:user_id/notifications", svc.GetNotifications)

	port := getEnv("PORT", "8088")
	server := &http.Server{Addr: ":" + port, Handler: r}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go
func() {
		log.Printf("Notification service started on port %s", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	<-quit
	log.Println("Shutting down notification-service...")
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
