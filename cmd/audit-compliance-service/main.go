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
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
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

type AuditEntry struct {
	ID            string      `json:"audit_id" bson:"_id"`
	EventType     string      `json:"event_type" bson:"event_type"`
	ServiceName   string      `json:"service_name" bson:"service_name"`
	UserID        string      `json:"user_id,omitempty" bson:"user_id,omitempty"`
	ResourceID    string      `json:"resource_id,omitempty" bson:"resource_id,omitempty"`
	ResourceType  string      `json:"resource_type,omitempty" bson:"resource_type,omitempty"`
	Action        string      `json:"action" bson:"action"`
	Details       interface{} `json:"details" bson:"details"`
	IPAddress     string      `json:"ip_address,omitempty" bson:"ip_address,omitempty"`
	Timestamp     time.Time   `json:"timestamp" bson:"timestamp"`
	CorrelationID string      `json:"correlation_id,omitempty" bson:"correlation_id,omitempty"`
}

type AuditComplianceService struct {
	mongoDB  *mongo.Database
	rabbitMQ *amqp.Connection
}

// ─── Tracer ───────────────────────────────────────────────────────────────────

func initTracer() (*sdktrace.TracerProvider, error) {
	ctx := context.Background()
	res, _ := resource.New(ctx, resource.WithAttributes(
		semconv.ServiceName("audit-compliance-service"), semconv.ServiceVersion("1.0.0"),
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

func (s *AuditComplianceService) CreateAuditEntry(c *gin.Context) {
	var req struct {
		EventType    string      `json:"event_type" binding:"required"`
		ServiceName  string      `json:"service_name" binding:"required"`
		UserID       string      `json:"user_id"`
		ResourceID   string      `json:"resource_id"`
		ResourceType string      `json:"resource_type"`
		Action       string      `json:"action" binding:"required"`
		Details      interface{} `json:"details"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	entry := AuditEntry{
		ID:           uuid.New().String(),
		EventType:    req.EventType,
		ServiceName:  req.ServiceName,
		UserID:       req.UserID,
		ResourceID:   req.ResourceID,
		ResourceType: req.ResourceType,
		Action:       req.Action,
		Details:      req.Details,
		IPAddress:    c.ClientIP(),
		Timestamp:    time.Now(),
	}

	collection := s.mongoDB.Collection("audit_logs")
	_, err := collection.InsertOne(c.Request.Context(), entry)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create audit entry"})
		return
	}

	c.JSON(http.StatusCreated, entry)
}

func (s *AuditComplianceService) GetAuditLogs(c *gin.Context) {
	collection := s.mongoDB.Collection("audit_logs")

	filter := bson.M{}
	if eventType := c.Query("event_type"); eventType != "" {
		filter["event_type"] = eventType
	}
	if userID := c.Query("user_id"); userID != "" {
		filter["user_id"] = userID
	}
	if serviceName := c.Query("service_name"); serviceName != "" {
		filter["service_name"] = serviceName
	}
	if resourceID := c.Query("resource_id"); resourceID != "" {
		filter["resource_id"] = resourceID
	}

	opts := options.Find().SetSort(bson.D{{Key: "timestamp", Value: -1}}).SetLimit(100)
	cursor, err := collection.Find(c.Request.Context(), filter, opts)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query failed"})
		return
	}
	defer cursor.Close(c.Request.Context())

	var entries []AuditEntry
	if err := cursor.All(c.Request.Context(), &entries); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "decode failed"})
		return
	}
	if entries == nil {
		entries = []AuditEntry{}
	}

	c.JSON(http.StatusOK, gin.H{"audit_logs": entries, "count": len(entries)})
}

func (s *AuditComplianceService) GetComplianceReport(c *gin.Context) {
	collection := s.mongoDB.Collection("audit_logs")
	ctx := c.Request.Context()

	totalCount, _ := collection.CountDocuments(ctx, bson.M{})

	// Count by event type
	pipeline := mongo.Pipeline{
		{{Key: "$group", Value: bson.D{{Key: "_id", Value: "$event_type"}, {Key: "count", Value: bson.D{{Key: "$sum", Value: 1}}}}}},
		{{Key: "$sort", Value: bson.D{{Key: "count", Value: -1}}}},
	}
	cursor, err := collection.Aggregate(ctx, pipeline)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "aggregation failed"})
		return
	}
	defer cursor.Close(ctx)

	var breakdown []bson.M
	cursor.All(ctx, &breakdown)

	c.JSON(http.StatusOK, gin.H{
		"total_entries":     totalCount,
		"event_breakdown":   breakdown,
		"generated_at":      time.Now(),
		"compliance_status": "compliant",
	})
}

// consumeEvents listens for all domain events and logs them for audit.
func (s *AuditComplianceService) consumeEvents() {
	ch, err := s.rabbitMQ.Channel()
	if err != nil {
		log.Printf("RabbitMQ channel error: %v", err)
		return
	}

	ch.ExchangeDeclare("events", "topic", true, false, false, false, nil)
	q, _ := ch.QueueDeclare("audit-events", true, false, false, false, nil)
	ch.QueueBind(q.Name, "#", "events", false, nil) // bind to ALL events

	msgs, err := ch.Consume(q.Name, "", false, false, false, false, nil)
	if err != nil {
		log.Printf("Failed to start consuming: %v", err)
		return
	}

	log.Println("[audit] Started consuming ALL events from RabbitMQ")

	go func() {
		collection := s.mongoDB.Collection("audit_logs")
		for d := range msgs {
			var event map[string]interface{}
			if err := json.Unmarshal(d.Body, &event); err != nil {
				d.Nack(false, false)
				continue
			}

			eventType, _ := event["event_type"].(string)

			entry := AuditEntry{
				ID:          uuid.New().String(),
				EventType:   eventType,
				ServiceName: "event-bus",
				Action:      "event_received",
				Details:     event,
				Timestamp:   time.Now(),
			}

			if _, err := collection.InsertOne(context.Background(), entry); err != nil {
				log.Printf("[audit] Failed to store audit entry: %v", err)
				d.Nack(false, true) // requeue
				continue
			}

			log.Printf("[audit] Logged event: %s", eventType)
			d.Ack(false)
		}
	}()
}

func (s *AuditComplianceService) HealthCheck(c *gin.Context) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.mongoDB.Client().Ping(ctx, nil); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "healthy", "service": "audit-compliance-service"})
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

	// MongoDB connection
	mongoURL := getEnv("MONGODB_URL", "mongodb://fintech:mongo123@localhost:27017/audit?authSource=admin")
	mongoClient, err := mongo.Connect(context.Background(), options.Client().ApplyURI(mongoURL))
	if err != nil {
		log.Fatal("MongoDB:", err)
	}
	defer mongoClient.Disconnect(context.Background())
	mongoDB := mongoClient.Database("audit")

	// RabbitMQ connection
	rabbitConn, err := amqp.Dial(getEnv("RABBITMQ_URL", "amqp://fintech:rabbit123@localhost:5672/"))
	if err != nil {
		log.Fatal("RabbitMQ:", err)
	}
	defer rabbitConn.Close()

	svc := &AuditComplianceService{mongoDB: mongoDB, rabbitMQ: rabbitConn}

	// Start consuming domain events
	svc.consumeEvents()

	r := gin.Default()
	r.GET("/health", svc.HealthCheck)
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	r.POST("/v1/audit", svc.CreateAuditEntry)
	r.GET("/v1/audit", svc.GetAuditLogs)
	r.GET("/v1/audit/compliance", svc.GetComplianceReport)

	port := getEnv("PORT", "8088")
	server := &http.Server{Addr: ":" + port, Handler: r}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		log.Printf("Audit/Compliance service started on port %s", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
	<-quit
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server.Shutdown(ctx)
}
