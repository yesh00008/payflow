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
"github.com/go-redis/redis/v8"
"github.com/google/uuid"
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

// ─── Models ───────────────────────────────────────────────────────────────────

type UserProfile struct {
	ID          string    `json:"user_id"`
	Email       string    `json:"email"`
	FullName    string    `json:"full_name"`
	PhoneNumber string    `json:"phone_number"`
	DateOfBirth string    `json:"date_of_birth"`
	Address     string    `json:"address"`
	KYCStatus   string    `json:"kyc_status"` // pending, verified, rejected
	RiskScore   int       `json:"risk_score"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
type KYCDocument struct {
	ID         string     `json:"document_id"`
	UserID     string     `json:"user_id"`
	DocType    string     `json:"document_type"` // passport, drivers_license, utility_bill
	Status     string     `json:"status"`        // pending, approved, rejected
	FileRef    string     `json:"file_reference"`
	UploadedAt time.Time  `json:"uploaded_at"`
	ReviewedAt *time.Time `json:"reviewed_at,omitempty"`
}
type UserKYCService struct {
	db    *sql.DB
	redis *redis.Client
}

// ─── Tracer ───────────────────────────────────────────────────────────────────

func initTracer() (*sdktrace.TracerProvider, error) {
	ctx := context.Background()
	res, _ := resource.New(ctx, resource.WithAttributes(
		semconv.ServiceName("user-kyc-service"), semconv.ServiceVersion("1.0.0"),
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


func (s *UserKYCService) CreateProfile(c *gin.Context) {
	var req struct {
		Email       string `json:"email" binding:"required,email"`
		FullName    string `json:"full_name" binding:"required"`
		PhoneNumber string `json:"phone_number"`
		DateOfBirth string `json:"date_of_birth"`
		Address     string `json:"address"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	profile := UserProfile{
		ID:          uuid.New().String(),
		Email:       req.Email,
		FullName:    req.FullName,
		PhoneNumber: req.PhoneNumber,
		DateOfBirth: req.DateOfBirth,
		Address:     req.Address,
		KYCStatus:   "pending",
		RiskScore:   0,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	_, err := s.db.ExecContext(c.Request.Context(),
		`INSERT INTO user_profiles (id, email, full_name, phone_number, date_of_birth, address, kyc_status, risk_score, created_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		profile.ID, profile.Email, profile.FullName, profile.PhoneNumber, profile.DateOfBirth,
		profile.Address, profile.KYCStatus, profile.RiskScore, profile.CreatedAt, profile.UpdatedAt)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create profile"})
		return
	}

	c.JSON(http.StatusCreated, profile)
}

func (s *UserKYCService) GetProfile(c *gin.Context) {
	userID := c.Param("user_id")

	// Cache check
	if cached, err := s.redis.Get(c.Request.Context(), "profile:"+userID).Result(); err == nil {
		log.Printf("Cache hit for profile %s: %s", userID, cached)
	}
var p UserProfile
	err := s.db.QueryRowContext(c.Request.Context(),
		`SELECT id, email, full_name, phone_number, date_of_birth, address, kyc_status, risk_score, created_at, updated_at
		 FROM user_profiles WHERE id = $1`, userID).
		Scan(&p.ID, &p.Email, &p.FullName, &p.PhoneNumber, &p.DateOfBirth, &p.Address,
			&p.KYCStatus, &p.RiskScore, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}

	s.redis.Set(c.Request.Context(), "profile:"+userID, p.Email, 30*time.Minute)
	c.JSON(http.StatusOK, p)
}

func (s *UserKYCService) SubmitKYCDocument(c *gin.Context) {
	userID := c.Param("user_id")
	var req struct {
		DocumentType  string `json:"document_type" binding:"required,oneof=passport drivers_license utility_bill"`
		FileReference string `json:"file_reference" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	doc := KYCDocument{
		ID:         uuid.New().String(),
		UserID:     userID,
		DocType:    req.DocumentType,
		Status:     "pending",
		FileRef:    req.FileReference,
		UploadedAt: time.Now(),
	}

	_, err := s.db.ExecContext(c.Request.Context(),
		`INSERT INTO kyc_documents (id, user_id, document_type, status, file_reference, uploaded_at)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		doc.ID, doc.UserID, doc.DocType, doc.Status, doc.FileRef, doc.UploadedAt)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to submit document"})
		return
	}

	c.JSON(http.StatusCreated, doc)
}

func (s *UserKYCService) GetKYCStatus(c *gin.Context) {
	userID := c.Param("user_id")

	rows, err := s.db.QueryContext(c.Request.Context(),
		`SELECT id, user_id, document_type, status, file_reference, uploaded_at, reviewed_at
		 FROM kyc_documents WHERE user_id = $1 ORDER BY uploaded_at DESC`, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query failed"})
		return
	}
	defer rows.Close()

	var docs []KYCDocument
	for rows.Next() {
		var d KYCDocument
		rows.Scan(&d.ID, &d.UserID, &d.DocType, &d.Status, &d.FileRef, &d.UploadedAt, &d.ReviewedAt)
		docs = append(docs, d)
	}
	if docs == nil {
		docs = []KYCDocument{}
	}

	// Overall KYC status
	var kycStatus string
	s.db.QueryRowContext(c.Request.Context(),
		`SELECT kyc_status FROM user_profiles WHERE id = $1`, userID).Scan(&kycStatus)

	c.JSON(http.StatusOK, gin.H{"user_id": userID, "kyc_status": kycStatus, "documents": docs})
}

func (s *UserKYCService) HealthCheck(c *gin.Context) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.db.PingContext(ctx); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "healthy", "service": "user-kyc-service"})
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

	rdb := redis.NewClient(&redis.Options{
		Addr:     getEnv("REDIS_ADDR", "localhost:6379"),
		Password: os.Getenv("REDIS_PASSWORD"),
		DB:       2,
	})

	svc := &UserKYCService{db: db, redis: rdb}

	r := gin.Default()
	r.GET("/health", svc.HealthCheck)
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	r.POST("/v1/users", svc.CreateProfile)
	r.GET("/v1/users/:user_id", svc.GetProfile)
	r.POST("/v1/users/:user_id/kyc/documents", svc.SubmitKYCDocument)
	r.GET("/v1/users/:user_id/kyc", svc.GetKYCStatus)

	port := getEnv("PORT", "8082")
	server := &http.Server{Addr: ":" + port, Handler: r}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go
func() {
		log.Printf("User/KYC service started on port %s", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
	<-quit
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server.Shutdown(ctx)
}
