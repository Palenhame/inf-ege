package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"backend/internal/config"
	"backend/internal/domain/assignments"
	"backend/internal/domain/auth"
	"backend/internal/domain/proctoring"
	"backend/internal/domain/recommendations"
	"backend/internal/domain/rooms"
	"backend/internal/domain/subscriptions"
	"backend/internal/domain/tasks"
	"backend/internal/domain/users"
	"backend/internal/jobs"
	"backend/internal/platform/postgres"
	"backend/internal/platform/redis"
	"backend/internal/platform/storage"
	httpTransport "backend/internal/transport/http"
	"backend/internal/transport/http/handlers"
	"backend/internal/transport/http/middleware"
	"backend/internal/transport/ws"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// roomsStorageAdapter adapts platform *storage.Storage to rooms.StorageClient.
type roomsStorageAdapter struct {
	storage *storage.Storage
}

func (a *roomsStorageAdapter) PutObject(ctx context.Context, bucket, objectKey string, reader io.Reader, objectSize int64, contentType string) error {
	if a.storage == nil {
		return nil
	}
	_, err := a.storage.Upload(ctx, bucket, objectKey, reader, objectSize, contentType)
	return err
}

func (a *roomsStorageAdapter) GetObject(ctx context.Context, bucket, objectKey string) (io.ReadCloser, error) {
	if a.storage == nil {
		return io.NopCloser(bytes.NewReader(nil)), nil
	}
	return a.storage.Download(ctx, bucket, objectKey)
}

// redisPresenceChecker checks user online presence using Redis keys.
type redisPresenceChecker struct {
	rdb *redis.Client
}

func (p *redisPresenceChecker) IsUserOnline(ctx context.Context, userID uuid.UUID) bool {
	if p.rdb == nil {
		return false
	}
	key := fmt.Sprintf("presence:%s", userID.String())
	val, err := p.rdb.Get(ctx, key)
	return err == nil && val != ""
}

// hubBroadcasterProxy breaks the circular dependency between Hub and TelemetryBatcher.
type hubBroadcasterProxy struct {
	hub *ws.Hub
}

func (p *hubBroadcasterProxy) BroadcastTimelineEvent(assignmentID uuid.UUID, payload *ws.MatrixTimelineEventPayload) {
	if p.hub != nil {
		p.hub.BroadcastTimelineEvent(assignmentID, payload)
	}
}

func (p *hubBroadcasterProxy) UpdatePresenceFromEvent(row ws.EventRow) {
	if p.hub != nil {
		p.hub.UpdatePresenceFromEvent(row)
	}
}

// wsExpiredNotifier bridges background session expiration jobs to WebSocket live matrix updates.
type wsExpiredNotifier struct {
	hub    *ws.Hub
	logger *slog.Logger
}

func (n *wsExpiredNotifier) NotifySessionExpired(ctx context.Context, event jobs.SessionExpiredEvent) error {
	if n.hub != nil {
		n.hub.BroadcastPresencePatch(event.AssignmentID, &ws.MatrixPresencePatchPayload{
			AssignmentID:   event.AssignmentID,
			UserID:         event.UserID,
			SessionID:      event.SessionID,
			Online:         false,
			LastActivityAt: event.FinishedAt,
		})
	}
	if n.logger != nil {
		n.logger.Info("session expired event processed",
			"session_id", event.SessionID,
			"user_id", event.UserID,
			"assignment_id", event.AssignmentID,
		)
	}
	return nil
}

func main() {
	// 1. Initialize Logger
	logHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	logger := slog.New(logHandler)
	slog.SetDefault(logger)

	logger.Info("Starting inf-ege backend service...")

	// 2. Load Configuration
	cfg, err := config.Load()
	if err != nil {
		logger.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	appCtx, appCancel := context.WithCancel(context.Background())
	defer appCancel()

	// 3. Platform Clients: PostgreSQL
	pgClient, err := postgres.New(appCtx, cfg.Postgres)
	if err != nil {
		logger.Error("Failed to initialize PostgreSQL pool", "error", err)
		os.Exit(1)
	}
	defer pgClient.Close()

	// Run Database Migrations
	if err := postgres.RunMigrations(appCtx, pgClient.Pool, cfg.Migrations.Dir); err != nil {
		logger.Warn("Database migrations warning (goose up)", "error", err)
	} else {
		logger.Info("Database migrations applied successfully")
	}

	// 4. Platform Clients: Redis
	redisClient, err := redis.New(appCtx, cfg.Redis)
	if err != nil {
		logger.Error("Failed to initialize Redis client", "error", err)
		os.Exit(1)
	}
	defer func() {
		_ = redisClient.Close()
	}()

	// 5. Platform Clients: S3 / MinIO
	s3Storage, err := storage.New(appCtx, cfg.S3)
	if err != nil {
		logger.Warn("Failed to initialize S3 storage (running without S3)", "error", err)
	} else {
		if err := s3Storage.EnsureBuckets(appCtx); err != nil {
			logger.Warn("S3 EnsureBuckets warning", "error", err)
		}
	}

	// 6. Domain Services Initialization
	// Subscriptions & Users
	subRepo := subscriptions.NewRepository(pgClient.Pool)
	subService := subscriptions.NewService(subRepo)

	userRepo := users.NewRepository(pgClient.Pool)
	userService := users.NewService(userRepo, subService)

	// Auth
	jwtMgr := auth.NewJWTManager(cfg.Auth.JWTSecret, cfg.Auth.AccessTTL)
	sessionMgr := auth.NewSessionManager(redisClient.Underlying(), cfg.Auth.RefreshTTL)
	authUserRepo := auth.NewPGUserRepository(pgClient.Pool)
	yandexProvider := auth.NewYandexProvider(auth.YandexConfig{
		ClientID:           cfg.OAuth.YandexClientID,
		ClientSecret:       cfg.OAuth.YandexClientSecret,
		RedirectURI:        cfg.OAuth.YandexRedirectURL,
		BootstrapSuperuser: cfg.Bootstrap.SuperuserYandexID,
	})
	vkProvider := auth.NewVKProvider(auth.VKConfig{
		ClientID:           cfg.OAuth.VKClientID,
		ClientSecret:       cfg.OAuth.VKClientSecret,
		RedirectURI:        cfg.OAuth.VKRedirectURL,
		BootstrapSuperuser: cfg.Bootstrap.SuperuserVKID,
	})
	authService := auth.NewService(
		auth.ServiceConfig{
			IsDevMode:                  cfg.IsDevelopment(),
			BaseURL:                    cfg.App.BaseURL,
			FrontendURL:                cfg.App.BaseURL,
			BootstrapSuperuserYandexID: cfg.Bootstrap.SuperuserYandexID,
			BootstrapSuperuserVKID:     cfg.Bootstrap.SuperuserVKID,
		},
		jwtMgr,
		sessionMgr,
		authUserRepo,
		yandexProvider,
		vkProvider,
	)

	// Tasks & Importer
	taskRepo := tasks.NewRepository(pgClient.Pool)
	taskService := tasks.NewService(taskRepo)
	taskImporter := tasks.NewImporter(taskRepo, s3Storage, s3Storage.MediaBucket())

	// Recommendations
	recRepo := recommendations.NewRepository(pgClient.Pool)
	recService := recommendations.NewService(recRepo)

	// Code Storage & Assignments
	codeStorage := assignments.NewPlatformCodeStorage(s3Storage)
	assignmentRepo := assignments.NewRepository(pgClient.Pool)
	assignmentService := assignments.NewService(assignmentRepo, codeStorage)

	// Proctoring
	proctoringRepo := proctoring.NewRepository(pgClient.Pool)
	proctoringService := proctoring.NewService(proctoringRepo, codeStorage)

	// Rooms
	roomRepo := rooms.NewPostgresRepository(pgClient.Pool)
	roomStorageAdapter := &roomsStorageAdapter{storage: s3Storage}
	roomPresenceAdapter := &redisPresenceChecker{rdb: redisClient}
	roomService := rooms.NewService(roomRepo, roomStorageAdapter, roomPresenceAdapter, rooms.ServiceConfig{
		PrivateBucket: s3Storage.PrivateBucket(),
	})

	// 7. Realtime: WebSocket Hub & Telemetry Batcher
	broadcasterProxy := &hubBroadcasterProxy{}
	telemetryBatcher := ws.NewTelemetryBatcher(pgClient.Pool, broadcasterProxy)
	wsHub := ws.NewHub(redisClient.Underlying(), telemetryBatcher, nil)
	broadcasterProxy.hub = wsHub

	// Start TelemetryBatcher background queue processor
	telemetryBatcher.Start(appCtx)
	logger.Info("TelemetryBatcher started")

	// 8. Background Scheduler
	redisLocker := jobs.NewRedisLocker(redisClient.Underlying(), logger.With("component", "redis_locker"))
	scheduler := jobs.NewScheduler(jobs.SchedulerConfig{
		Logger: logger.With("component", "scheduler"),
		Locker: redisLocker,
	})
	notifier := &wsExpiredNotifier{hub: wsHub, logger: logger}
	if err := scheduler.RegisterDefaults(pgClient.Pool, redisClient.Underlying(), notifier); err != nil {
		logger.Warn("Register background jobs warning", "error", err)
	}
	scheduler.Start(appCtx)
	logger.Info("Background scheduler started")

	// 9. HTTP Handlers & Router
	cookieCfg := auth.CookieConfig{
		Secure:   cfg.Auth.CookieSecure,
		Domain:   cfg.Auth.CookieDomain,
		SameSite: http.SameSiteLaxMode,
	}

	authHandler := handlers.NewAuthHandler(authService, jwtMgr, cookieCfg)
	userHandler := handlers.NewUserHandler(userService, subService)
	subHandler := handlers.NewSubscriptionHandler(subService)
	bankHandler := handlers.NewBankHandler(taskService, taskImporter, s3Storage, s3Storage.MediaBucket())
	dashboardHandler := handlers.NewDashboardHandler(recService)
	roomHandler := handlers.NewRoomHandler(roomService)
	assignmentHandler := handlers.NewAssignmentHandler(assignmentService)
	teacherHandler := handlers.NewTeacherHandler(proctoringService)

	wsHandler := ws.NewHandler(ws.HandlerConfig{
		Hub:       wsHub,
		JWTSecret: cfg.Auth.JWTSecret,
	})

	storageProxy, err := httpTransport.NewMinIOStorageReverseProxy(cfg.S3.Endpoint, cfg.S3.MediaBucket)
	if err != nil {
		logger.Warn("MinIO storage reverse proxy init warning", "error", err)
	}

	// Build sub-routers for HandlerRegistry
	authMux := chi.NewRouter()
	authMux.Get("/oauth/{provider}/login", authHandler.OAuthLogin)
	authMux.Get("/oauth/{provider}/callback", authHandler.OAuthCallback)
	authMux.Post("/dev-login", authHandler.DevLogin)
	authMux.Post("/refresh", authHandler.Refresh)
	authMux.Post("/logout", authHandler.Logout)
	authMux.Get("/me", authHandler.Me)
	// Redundant mounting with /auth prefix in case matched from router root
	authMux.Get("/auth/oauth/{provider}/login", authHandler.OAuthLogin)
	authMux.Get("/auth/oauth/{provider}/callback", authHandler.OAuthCallback)
	authMux.Post("/auth/dev-login", authHandler.DevLogin)
	authMux.Post("/auth/refresh", authHandler.Refresh)
	authMux.Post("/auth/logout", authHandler.Logout)
	authMux.Get("/auth/me", authHandler.Me)

	bankMux := chi.NewRouter()
	bankMux.Get("/tasks", bankHandler.ListBankTasks)
	bankMux.Get("/tasks/{id}", bankHandler.GetBankTask)
	bankMux.Post("/tasks/{id}/submit", bankHandler.SubmitBankTask)

	topicMux := chi.NewRouter()
	topicMux.Get("/", bankHandler.GetTopics)

	dashMux := chi.NewRouter()
	dashMux.Get("/student", dashboardHandler.GetStudentDashboard)

	roomMux := chi.NewRouter()
	roomMux.Mount("/", roomHandler.Routes())
	roomMux.Post("/{room_id}/assignments", assignmentHandler.CreateAssignment)

	assignMux := chi.NewRouter()
	assignMux.Mount("/", assignmentHandler.Routes())
	assignMux.Get("/{id}/matrix", teacherHandler.GetMatrixSnapshot)

	teacherMux := chi.NewRouter()
	teacherMux.Mount("/", teacherHandler.Routes())
	teacherMux.Get("/assignments/{id}/matrix", teacherHandler.GetMatrixSnapshot)
	teacherMux.Get("/sessions/{id}/inspect", teacherHandler.GetSessionDossier)

	adminMux := chi.NewRouter()
	adminMux.Post("/tasks", bankHandler.CreateAdminTask)
	adminMux.Patch("/tasks/{id}", bankHandler.UpdateAdminTask)
	adminMux.Delete("/tasks/{id}", bankHandler.DeleteAdminTask)
	adminMux.Post("/tasks/import", bankHandler.ImportTasks)
	adminMux.Post("/tasks/upload-media", bankHandler.UploadMedia)
	adminMux.Post("/media/upload", bankHandler.UploadMedia)
	adminMux.Post("/topics", bankHandler.CreateTopic)
	adminMux.Patch("/topics/{id}", bankHandler.UpdateTopic)
	adminMux.Delete("/topics/{id}", bankHandler.DeleteTopic)
	adminMux.Get("/exam-scale", assignmentHandler.GetExamScale)
	adminMux.Put("/exam-scale", assignmentHandler.UpdateExamScale)
	adminMux.Mount("/subscriptions", subHandler.AdminRoutes())
	adminMux.Mount("/users", userHandler.AdminRoutes())

	roleResolver := func(ctx context.Context, userID uuid.UUID) (string, error) {
		u, err := userService.GetByID(ctx, userID)
		if err != nil {
			return "", err
		}
		return string(u.Role), nil
	}

	routerCfg := httpTransport.RouterConfig{
		Logger:              logger,
		JWTSecret:           cfg.Auth.JWTSecret,
		AllowedOrigins:      []string{cfg.App.BaseURL, "http://localhost:5173", "http://localhost:3000", "*"},
		PublicStorage:       storageProxy,
		RateLimiter:         middleware.RateLimit(redisClient.Underlying(), 120, time.Minute),
		SubscriptionChecker: subService,
		ImpersonateOpts: []middleware.ImpersonationOption{
			middleware.WithRoleResolver(roleResolver),
		},
	}

	reg := &httpTransport.HandlerRegistry{
		AuthHandler:       authMux,
		UserHandler:       userHandler,
		UserAliasHandler:  userHandler,
		SubHandler:        subHandler,
		TopicHandler:      topicMux,
		BankHandler:       bankMux,
		DashboardHandler:  dashMux,
		RoomHandler:       roomMux,
		AssignmentHandler: assignMux,
		ExamHandler:       assignmentHandler.ExamRoutes(),
		SessionHandler:    assignmentHandler.SessionRoutes(teacherHandler.GetSessionDossier),
		TeacherHandler:    teacherMux,
		AdminHandler:      adminMux,
		WSHandler:         wsHandler,
	}

	router := httpTransport.NewRouter(routerCfg, reg)

	// 10. Start HTTP Server with Graceful Shutdown
	srv := &http.Server{
		Addr:         cfg.ListenAddr(),
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		logger.Info("HTTP server listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server exited unexpectedly", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for termination signals
	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, os.Interrupt, syscall.SIGTERM)
	sig := <-shutdownChan
	logger.Info("Received shutdown signal", "signal", sig.String())

	// Graceful shutdown with 10s deadline
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP server graceful shutdown failed", "error", err)
	} else {
		logger.Info("HTTP server stopped gracefully")
	}

	scheduler.Stop()
	telemetryBatcher.Stop()
	logger.Info("inf-ege service shutdown complete")
}
