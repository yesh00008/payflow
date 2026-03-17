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

type AuditLog struct {
	ID            string                 `json:"id"`
	EntityType    string                 `json:"entity_type"`
	EntityID      string                 `json:"entity_id"`
	Action        string                 `json:"action"`
	ActorID       string                 `json:"actor_id"`
	ActorType     string                 `json:"actor_type"`
	Changes       map[string]interface{} `json:"changes,omitempty"`
	IPAddress     string                 `json:"ip_address,omitempty"`
	UserAgent     string                 `json:"user_agent,omitempty"`
	Timestamp     time.Time              `json:"timestamp"`
	ComplianceTag string                 `json:"compliance_tag,omitempty"`
}

type CreateAuditLogRequest struct {
	EntityType    string                 `json:"entity_type" binding:"required"`
	EntityID      string                 `json:"entity_id" binding:"required"`
	Action        string                 `json:"action" binding:"required"`
	ActorID       string                 `json:"actor_id" binding:"required"`
	ActorType     string                 `json:"actor_type" binding:"required"`
	Changes       map[string]interface{} `json:"changes"`
	IPAddress     string                 `json:"ip_address"`
	UserAgent     string                 `json:"user_agent"`
	ComplianceTag string                 `json:"compliance_tag"`
}

type ComplianceReport struct {
	ReportID    string    `json:"report_id"`
	ReportType  string    `json:"report_type"`
	StartDate   time.Time `json:"start_date"`
	EndDate     time.Time `json:"end_date"`
	TotalLogs   int       `json:"total_logs"`
	Summary     string    `json:"summary"`
	GeneratedAt time.Time `json:"generated_at"`
}

type AuditEvent struct {
	EventType  string                 `json:"event_type"`
	EntityType string                 `json:"entity_type"`
	EntityID   string                 `json:"entity_id"`
	ActorID    string                 `json:"actor_id"`
	Data       map[string]interface{} `json:"data"`
	Timestamp  time.Time              `json:"timestamp"`
}

type AuditService struct {
	db   *sql.DB
	amqp *amqp.Connection
}

// ─── Tracer ───────────────────────────────────────────────────────────────────

func initTracer() (*sdktrace.TracerProvider, error) {
	ctx := context.Background()
	res, err := resource.New(ctx, resource.WithAttributes(
		attribute.String("service.name", "audit-compliance-service"),
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

func (s *AuditService) HealthCheck(c *gin.Context) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.db.PingContext(ctx); err != nil {
		log.Printf("Database ping failed: %v", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy", "database": "unreachable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "healthy", "service": "audit-compliance-service"})
}

// CreateAuditLog creates a new audit log entry
func (s *AuditService) CreateAuditLog(c *gin.Context) {
	var req CreateAuditLogRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	logID := uuid.New().String()
	now := time.Now()

	changesJSON, _ := json.Marshal(req.Changes)

	query := `
		INSERT INTO audit_logs (id, entity_type, entity_id, action, actor_id, actor_type, changes, ip_address, user_agent, timestamp, compliance_tag)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id
	`

	err := s.db.QueryRow(query, logID, req.EntityType, req.EntityID, req.Action, req.ActorID, req.ActorType,
		string(changesJSON), req.IPAddress, req.UserAgent, now, req.ComplianceTag).Scan(&logID)

	if err != nil {
		log.Printf("Failed to create audit log: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create audit log"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":        logID,
		"message":   "Audit log created",
		"timestamp": now,
	})
}

// GetAuditLogs retrieves audit logs with filters
func (s *AuditService) GetAuditLogs(c *gin.Context) {
	entityType := c.Query("entity_type")
	entityID := c.Query("entity_id")
	actorID := c.Query("actor_id")
	startDate := c.Query("start_date")
	endDate := c.Query("end_date")
	limit := c.DefaultQuery("limit", "100")

	query := `
		SELECT id, entity_type, entity_id, action, actor_id, actor_type, changes, ip_address, user_agent, timestamp, compliance_tag
		FROM audit_logs
		WHERE 1=1
	`
	args := []interface{}{}
	argCount := 1

	if entityType != "" {
		query += " AND entity_type = $" + string(rune(argCount+'0'))
		args = append(args, entityType)
		argCount++
	}
	if entityID != "" {
		query += " AND entity_id = $" + string(rune(argCount+'0'))
		args = append(args, entityID)
		argCount++
	}
	if actorID != "" {
		query += " AND actor_id = $" + string(rune(argCount+'0'))
		args = append(args, actorID)
		argCount++
	}
	if startDate != "" {
		query += " AND timestamp >= $" + string(rune(argCount+'0'))
		args = append(args, startDate)
		argCount++
	}
	if endDate != "" {
		query += " AND timestamp <= $" + string(rune(argCount+'0'))
		args = append(args, endDate)
		argCount++
	}

	query += " ORDER BY timestamp DESC LIMIT $" + string(rune(argCount+'0'))
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		log.Printf("Failed to get audit logs: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get audit logs"})
		return
	}
	defer rows.Close()

	var logs []AuditLog
	for rows.Next() {
		var auditLog AuditLog
		var changesJSON string
		err := rows.Scan(&auditLog.ID, &auditLog.EntityType, &auditLog.EntityID, &auditLog.Action, &auditLog.ActorID, &auditLog.ActorType,
			&changesJSON, &auditLog.IPAddress, &auditLog.UserAgent, &auditLog.Timestamp, &auditLog.ComplianceTag)
		if err != nil {
			log.Printf("Failed to scan audit log: %v", err)
			continue
		}
		json.Unmarshal([]byte(changesJSON), &auditLog.Changes)
		logs = append(logs, auditLog)
	}

	c.JSON(http.StatusOK, gin.H{"audit_logs": logs, "count": len(logs)})
}

// GetAuditLog retrieves a single audit log
func (s *AuditService) GetAuditLog(c *gin.Context) {
	logID := c.Param("log_id")

	query := `
		SELECT id, entity_type, entity_id, action, actor_id, actor_type, changes, ip_address, user_agent, timestamp, compliance_tag
		FROM audit_logs
		WHERE id = $1
	`

	var auditLog AuditLog
	var changesJSON string
	err := s.db.QueryRow(query, logID).Scan(
		&auditLog.ID, &auditLog.EntityType, &auditLog.EntityID, &auditLog.Action, &auditLog.ActorID, &auditLog.ActorType,
		&changesJSON, &auditLog.IPAddress, &auditLog.UserAgent, &auditLog.Timestamp, &auditLog.ComplianceTag,
	)

	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "Audit log not found"})
		return
	}
	if err != nil {
		log.Printf("Failed to get audit log: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get audit log"})
		return
	}

	json.Unmarshal([]byte(changesJSON), &auditLog.Changes)

	c.JSON(http.StatusOK, auditLog)
}

// GenerateComplianceReport generates a compliance report
func (s *AuditService) GenerateComplianceReport(c *gin.Context) {
	reportType := c.Query("report_type")
	startDate := c.Query("start_date")
	endDate := c.Query("end_date")

	if reportType == "" || startDate == "" || endDate == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "report_type, start_date, and end_date are required"})
		return
	}

	query := `
		SELECT COUNT(*) 
		FROM audit_logs 
		WHERE timestamp >= $1 AND timestamp <= $2
	`

	var totalLogs int
	err := s.db.QueryRow(query, startDate, endDate).Scan(&totalLogs)
	if err != nil {
		log.Printf("Failed to count logs: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate report"})
		return
	}

	report := ComplianceReport{
		ReportID:    uuid.New().String(),
		ReportType:  reportType,
		StartDate:   parseTime(startDate),
		EndDate:     parseTime(endDate),
		TotalLogs:   totalLogs,
		Summary:     "Compliance report generated successfully",
		GeneratedAt: time.Now(),
	}

	c.JSON(http.StatusOK, report)
}

// GetEntityHistory retrieves complete history for an entity
func (s *AuditService) GetEntityHistory(c *gin.Context) {
	entityType := c.Param("entity_type")
	entityID := c.Param("entity_id")

	query := `
		SELECT id, entity_type, entity_id, action, actor_id, actor_type, changes, ip_address, user_agent, timestamp, compliance_tag
		FROM audit_logs
		WHERE entity_type = $1 AND entity_id = $2
		ORDER BY timestamp DESC
	`

	rows, err := s.db.Query(query, entityType, entityID)
	if err != nil {
		log.Printf("Failed to get entity history: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get entity history"})
		return
	}
	defer rows.Close()

	var logs []AuditLog
	for rows.Next() {
		var auditLog AuditLog
		var changesJSON string
		err := rows.Scan(&auditLog.ID, &auditLog.EntityType, &auditLog.EntityID, &auditLog.Action, &auditLog.ActorID, &auditLog.ActorType,
			&changesJSON, &auditLog.IPAddress, &auditLog.UserAgent, &auditLog.Timestamp, &auditLog.ComplianceTag)
		if err != nil {
			continue
		}
		json.Unmarshal([]byte(changesJSON), &auditLog.Changes)
		logs = append(logs, auditLog)
	}

	c.JSON(http.StatusOK, gin.H{
		"entity_type": entityType,
		"entity_id":   entityID,
		"history":     logs,
		"count":       len(logs),
	})
}

// ─── Background Workers ───────────────────────────────────────────────────────

// consumeAuditEvents listens for audit events from RabbitMQ
func (s *AuditService) consumeAuditEvents() {
	ch, err := s.amqp.Channel()
	if err != nil {
		log.Printf("Failed to open channel: %v", err)
		return
	}
	defer ch.Close()

	q, err := ch.QueueDeclare(
		"audit_events",
		true,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		log.Printf("Failed to declare queue: %v", err)
		return
	}

	msgs, err := ch.Consume(
		q.Name,
		"",
		true,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		log.Printf("Failed to register consumer: %v", err)
		return
	}

	log.Println("Started consuming audit events from RabbitMQ")

	for msg := range msgs {
		var event AuditEvent
		if err := json.Unmarshal(msg.Body, &event); err != nil {
			log.Printf("Failed to unmarshal event: %v", err)
			continue
		}

		log.Printf("Received audit event: %s for %s/%s", event.EventType, event.EntityType, event.EntityID)
		s.processAuditEvent(event)
	}
}

func (s *AuditService) processAuditEvent(event AuditEvent) {
	logID := uuid.New().String()
	dataJSON, _ := json.Marshal(event.Data)

	query := `
		INSERT INTO audit_logs (id, entity_type, entity_id, action, actor_id, actor_type, changes, timestamp, compliance_tag)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`

	_, err := s.db.Exec(query, logID, event.EntityType, event.EntityID, event.EventType, event.ActorID, "system",
		string(dataJSON), event.Timestamp, "auto_generated")

	if err != nil {
		log.Printf("Failed to create audit log from event: %v", err)
		return
	}

	log.Printf("Audit log created from event: %s", logID)
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func parseTime(timeStr string) time.Time {
	t, _ := time.Parse(time.RFC3339, timeStr)
	return t
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

	svc := &AuditService{db: db, amqp: amqpConn}

	// Start background consumer
	if amqpConn != nil {
		go svc.consumeAuditEvents()
	}

	r := gin.Default()
	r.GET("/health", svc.HealthCheck)
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// Audit log endpoints
	r.POST("/audit/logs", svc.CreateAuditLog)
	r.GET("/audit/logs", svc.GetAuditLogs)
	r.GET("/audit/logs/:log_id", svc.GetAuditLog)
	r.GET("/audit/entity/:entity_type/:entity_id", svc.GetEntityHistory)

	// Compliance endpoints
	r.GET("/compliance/report", svc.GenerateComplianceReport)

	port := getEnv("PORT", "8089")
	server := &http.Server{Addr: ":" + port, Handler: r}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Printf("Audit Compliance service started on port %s", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	<-quit
	log.Println("Shutting down audit-compliance-service...")
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
