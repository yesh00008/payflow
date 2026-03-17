package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
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

type Notification struct {
	ID        string     `json:"notification_id"`
	UserID    string     `json:"user_id"`
	Channel   string     `json:"channel"` // email, sms, push, webhook
	Subject   string     `json:"subject"`
	Body      string     `json:"body"`
	Status    string     `json:"status"` // pending, sent, failed
	EventType string     `json:"event_type"`
	CreatedAt time.Time  `json:"created_at"`
	SentAt    *time.Time `json:"sent_at,omitempty"`
}

type NotificationService struct {
	redis    *redis.Client
	rabbitMQ *amqp.Connection
}

// ─── Tracer ───────────────────────────────────────────────────────────────────

func initTracer() (*sdktrace.TracerProvider, error) {
	ctx := context.Background()
	res, _ := resource.New(ctx, resource.WithAttributes(
		semconv.ServiceName("notification-service"), semconv.ServiceVersion("1.0.0"),
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

func (s *NotificationService) SendNotification(c *gin.Context) {
	var req struct {
		UserID  string `json:"user_id" binding:"required"`
		Channel string `json:"channel" binding:"required,oneof=email sms push webhook"`
		Subject string `json:"subject" binding:"required"`
		Body    string `json:"body" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	notif := Notification{
		ID:        uuid.New().String(),
		UserID:    req.UserID,
		Channel:   req.Channel,
		Subject:   req.Subject,
		Body:      req.Body,
		Status:    "pending",
		EventType: "manual",
		CreatedAt: time.Now(),
	}

	// Store in Redis for tracking
	data, _ := json.Marshal(notif)
	s.redis.Set(c.Request.Context(), "notif:"+notif.ID, data, 48*time.Hour)

	// Simulate sending
	go func() {
		time.Sleep(500 * time.Millisecond)
		now := time.Now()
		notif.Status = "sent"
		notif.SentAt = &now
		updated, _ := json.Marshal(notif)
		s.redis.Set(context.Background(), "notif:"+notif.ID, updated, 48*time.Hour)
		log.Printf("[notification] Sent %s notification to user %s via %s", notif.ID, notif.UserID, notif.Channel)
	}()

	c.JSON(http.StatusCreated, notif)
}

func (s *NotificationService) GetNotification(c *gin.Context) {
	notifID := c.Param("notification_id")
	data, err := s.redis.Get(c.Request.Context(), "notif:"+notifID).Bytes()
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "notification not found"})
		return
	}
	var notif Notification
	json.Unmarshal(data, &notif)
	c.JSON(http.StatusOK, notif)
}

// consumeEvents listens for domain events and triggers notifications.
func (s *NotificationService) consumeEvents() {
	ch, err := s.rabbitMQ.Channel()
	if err != nil {
		log.Printf("RabbitMQ channel error: %v", err)
		return
	}

	// Declare exchange + queue
	ch.ExchangeDeclare("events", "topic", true, false, false, false, nil)
	q, _ := ch.QueueDeclare("notification-events", true, false, false, false, nil)
	ch.QueueBind(q.Name, "transfer.*", "events", false, nil)
	ch.QueueBind(q.Name, "payment.*", "events", false, nil)
	ch.QueueBind(q.Name, "account.*", "events", false, nil)

	msgs, err := ch.Consume(q.Name, "", false, false, false, false, nil)
	if err != nil {
		log.Printf("Failed to start consuming: %v", err)
		return
	}

	log.Println("[notification] Started consuming events from RabbitMQ")

	go func() {
		for d := range msgs {
			var event map[string]interface{}
			if err := json.Unmarshal(d.Body, &event); err != nil {
				d.Nack(false, false)
				continue
			}

			eventType, _ := event["event_type"].(string)
			log.Printf("[notification] Received event: %s", eventType)

			// Create notification based on event type
			notif := Notification{
				ID:        uuid.New().String(),
				Channel:   "email",
				Subject:   "Event: " + eventType,
				Body:      string(d.Body),
				Status:    "sent",
				EventType: eventType,
				CreatedAt: time.Now(),
			}
			now := time.Now()
			notif.SentAt = &now

			data, _ := json.Marshal(notif)
			s.redis.Set(context.Background(), "notif:"+notif.ID, data, 48*time.Hour)

			d.Ack(false)
		}
	}()
}

func (s *NotificationService) HealthCheck(c *gin.Context) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.redis.Ping(ctx).Err(); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "healthy", "service": "notification-service"})
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

	rdb := redis.NewClient(&redis.Options{
		Addr:     getEnv("REDIS_ADDR", "localhost:6379"),
		Password: getEnv("REDIS_PASSWORD", "redis123"),
		DB:       3,
	})

	rabbitConn, err := amqp.Dial(getEnv("RABBITMQ_URL", "amqp://fintech:rabbit123@localhost:5672/"))
	if err != nil {
		log.Fatal("RabbitMQ:", err)
	}
	defer rabbitConn.Close()

	svc := &NotificationService{redis: rdb, rabbitMQ: rabbitConn}

	// Start consuming domain events
	svc.consumeEvents()

	r := gin.Default()
	r.GET("/health", svc.HealthCheck)
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	r.POST("/v1/notifications", svc.SendNotification)
	r.GET("/v1/notifications/:notification_id", svc.GetNotification)

	port := getEnv("PORT", "8087")
	server := &http.Server{Addr: ":" + port, Handler: r}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		log.Printf("Notification service started on port %s", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
	<-quit
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server.Shutdown(ctx)
}
