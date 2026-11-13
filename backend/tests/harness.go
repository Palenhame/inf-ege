package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"backend/internal/domain/assignments"
	"backend/internal/domain/auth"
	"backend/internal/domain/proctoring"
	"backend/internal/domain/recommendations"
	"backend/internal/domain/rooms"
	"backend/internal/domain/subscriptions"
	"backend/internal/domain/tasks"
	"backend/internal/domain/users"
	httpTransport "backend/internal/transport/http"
	"backend/internal/transport/http/handlers"
	"backend/internal/transport/http/middleware"
	"backend/internal/transport/ws"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Well-known test fixture UUIDs (all valid RFC 4122 UUIDs).
const (
	SuperuserUUID      = "550e8400-e29b-41d4-a716-446655440001"
	TeacherUUID        = "550e8400-e29b-41d4-a716-446655440002"
	ExpiredTeacherUUID = "550e8400-e29b-41d4-a716-446655440003"
	StudentUUID        = "550e8400-e29b-41d4-a716-446655440004"
	TargetStudentUUID  = "550e8400-e29b-41d4-a716-446655440005"
	DummyStudent1UUID  = "550e8400-e29b-41d4-a716-446655440006"
	DummyStudent2UUID  = "550e8400-e29b-41d4-a716-446655440007"

	RoomFullUUID     = "660e8400-e29b-41d4-a716-446655440001"
	RoomStandardUUID = "660e8400-e29b-41d4-a716-446655440002"
	ExpiredRoomUUID  = "660e8400-e29b-41d4-a716-446655440003"

	TestAssignUUID    = "770e8400-e29b-41d4-a716-446655440001"
	ExpiredAssignUUID = "770e8400-e29b-41d4-a716-446655440002"

	TestSessionUUID    = "880e8400-e29b-41d4-a716-446655440001"
	ExpiredSessionUUID = "880e8400-e29b-41d4-a716-446655440002"
	LiveSessionUUID    = "880e8400-e29b-41d4-a716-446655440003"

	TestRefreshToken = "valid-session-jti-token"
	TestJWTSecret    = "test-jwt-secret-key-32bytes-inf-ege-secure"
)

// UserClaims defines JWT claims matching backend auth.
type UserClaims struct {
	Role string `json:"role"`
	jwt.RegisteredClaims
}

// GenerateTestToken produces a signed JWT for testing.
func GenerateTestToken(userID string, role string, expired bool) string {
	var exp time.Time
	if expired {
		exp = time.Now().Add(-1 * time.Hour)
	} else {
		exp = time.Now().Add(15 * time.Minute)
	}

	claims := UserClaims{
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			ExpiresAt: jwt.NewNumericDate(exp),
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-1 * time.Minute)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, _ := token.SignedString([]byte(TestJWTSecret))
	return tokenStr
}

// MakeAccessCookie builds an ege_access cookie for HTTP/WS requests.
func MakeAccessCookie(token string) *http.Cookie {
	return &http.Cookie{
		Name:     "ege_access",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
}

// MakeRefreshCookie builds an ege_refresh cookie for HTTP requests.
func MakeRefreshCookie(token string) *http.Cookie {
	return &http.Cookie{
		Name:     "ege_refresh",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
}

// TestHarness encapsulates the contract testing environment against the real backend router & services.
type TestHarness struct {
	Server   *httptest.Server
	BaseURL  string
	IsRemote bool
	Hub      *ws.Hub
}

// GetTestHarness initializes or discovers the target test server.
func GetTestHarness(t *testing.T) *TestHarness {
	t.Helper()

	targetURL := os.Getenv("TEST_SERVER_URL")
	if targetURL == "" {
		targetURL = os.Getenv("API_URL")
	}

	if targetURL != "" {
		targetURL = strings.TrimRight(targetURL, "/")
		return &TestHarness{
			BaseURL:  targetURL,
			IsRemote: true,
		}
	}

	store := newMemoryStore()
	store.seedInitialData()

	// 1. Domain Services
	subService := subscriptions.NewService(&memSubRepo{s: store})
	userService := users.NewService(&memUsersRepo{s: store}, subService)

	redisStub := &memRedisClient{
		data: make(map[string]string),
	}
	// Pre-seed refresh session expected by TestAuthRefresh_Success
	jtiKey := "auth:refresh_jti:" + TestRefreshToken
	sessionKey := fmt.Sprintf("auth:refresh:%s:%s", StudentUUID, TestRefreshToken)
	sessionDataBytes, _ := json.Marshal(auth.SessionData{
		UserID:    StudentUUID,
		JTI:       TestRefreshToken,
		CreatedAt: time.Now().UTC(),
	})
	redisStub.data[jtiKey] = StudentUUID
	redisStub.data[sessionKey] = string(sessionDataBytes)

	jwtMgr := auth.NewJWTManager(TestJWTSecret, 15*time.Minute)
	sessionMgr := auth.NewSessionManager(redisStub, auth.DefaultRefreshTokenTTL)
	yandexProvider := auth.NewYandexProvider(auth.YandexConfig{
		ClientID:     "test-yandex-id",
		ClientSecret: "test-yandex-secret",
		RedirectURI:  "http://localhost/api/v1/auth/oauth/yandex/callback",
	})
	vkProvider := auth.NewVKProvider(auth.VKConfig{
		ClientID:     "test-vk-id",
		ClientSecret: "test-vk-secret",
		RedirectURI:  "http://localhost/api/v1/auth/oauth/vk/callback",
	})
	authService := auth.NewService(
		auth.ServiceConfig{
			IsDevMode:   true,
			BaseURL:     "http://localhost",
			FrontendURL: "http://localhost",
		},
		jwtMgr,
		sessionMgr,
		&memAuthUserRepo{s: store},
		yandexProvider,
		vkProvider,
	)

	taskService := tasks.NewService(&memTasksRepo{s: store})
	recService := recommendations.NewService(&memRecRepo{s: store})

	codeStorage := assignments.NewMemoryCodeStorage()
	assignmentService := assignments.NewService(&memAssignRepo{s: store}, codeStorage)
	proctoringService := proctoring.NewService(&memProctoringRepo{s: store}, codeStorage)

	roomStorage := &memRoomsStorage{objects: make(map[string][]byte)}
	roomService := rooms.NewService(&memRoomsRepo{s: store}, roomStorage, rooms.NoopPresenceChecker{}, rooms.ServiceConfig{
		PrivateBucket: "egeinf-private",
	})

	// 2. WebSocket Hub & Handler
	wsHub := ws.NewHub(nil, &noopTelemetryBatcher{}, nil)
	wsHub.SetSessionAssignment(uuid.MustParse(TestSessionUUID), uuid.MustParse(TestAssignUUID))
	wsHub.SetSessionAssignment(uuid.MustParse(ExpiredSessionUUID), uuid.MustParse(ExpiredAssignUUID))
	wsHub.SetSessionAssignment(uuid.MustParse(LiveSessionUUID), uuid.MustParse(TestAssignUUID))
	wsHub.SetSessionResolver(func(sessionID uuid.UUID) uuid.UUID {
		store.mu.RLock()
		defer store.mu.RUnlock()
		if sess, ok := store.sessions[sessionID]; ok {
			return sess.AssignmentID
		}
		return uuid.Nil
	})

	// 3. Real HTTP Handlers
	cookieCfg := auth.DefaultCookieConfig(false)
	authHandler := handlers.NewAuthHandler(authService, jwtMgr, cookieCfg)
	userHandler := handlers.NewUserHandler(userService, subService)
	subHandler := handlers.NewSubscriptionHandler(subService)
	bankHandler := handlers.NewBankHandler(taskService, nil, nil, "egeinf-media")
	dashboardHandler := handlers.NewDashboardHandler(recService)
	roomHandler := handlers.NewRoomHandler(roomService)
	assignmentHandler := handlers.NewAssignmentHandler(assignmentService)
	teacherHandler := handlers.NewTeacherHandler(proctoringService)
	wsHandler := ws.NewHandler(ws.HandlerConfig{
		Hub:       wsHub,
		JWTSecret: TestJWTSecret,
	})

	// 4. Mount sub-routers identically to cmd/api/main.go
	authMux := chi.NewRouter()
	authMux.Get("/oauth/{provider}/login", authHandler.OAuthLogin)
	authMux.Get("/oauth/{provider}/callback", authHandler.OAuthCallback)
	authMux.Post("/dev-login", authHandler.DevLogin)
	authMux.Post("/refresh", authHandler.Refresh)
	authMux.Post("/logout", authHandler.Logout)
	authMux.Get("/me", authHandler.Me)
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
		Logger:              slog.New(slog.NewTextHandler(io.Discard, nil)),
		JWTSecret:           TestJWTSecret,
		AllowedOrigins:      []string{"*"},
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
	server := httptest.NewServer(router)

	t.Cleanup(func() {
		server.Close()
	})

	return &TestHarness{
		Server:   server,
		BaseURL:  server.URL,
		IsRemote: false,
		Hub:      wsHub,
	}
}

// =============================================================================
// No-op WebSocket Telemetry Batcher
// =============================================================================

type noopTelemetryBatcher struct{}

func (b *noopTelemetryBatcher) EnqueueBatch(sessionID, assignmentID, userID uuid.UUID, events []ws.TelemetryEventItem) {
}

// =============================================================================
// In-Memory Redis Client Stub (auth.RedisCmdClient)
// =============================================================================

type memRedisClient struct {
	mu   sync.RWMutex
	data map[string]string
}

func (r *memRedisClient) Set(_ context.Context, key string, value interface{}, _ time.Duration) *redis.StatusCmd {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch v := value.(type) {
	case string:
		r.data[key] = v
	case []byte:
		r.data[key] = string(v)
	default:
		r.data[key] = fmt.Sprintf("%v", v)
	}
	return redis.NewStatusResult("OK", nil)
}

func (r *memRedisClient) Get(_ context.Context, key string) *redis.StringCmd {
	r.mu.RLock()
	defer r.mu.RUnlock()
	val, ok := r.data[key]
	if !ok {
		return redis.NewStringResult("", redis.Nil)
	}
	return redis.NewStringResult(val, nil)
}

func (r *memRedisClient) Del(_ context.Context, keys ...string) *redis.IntCmd {
	r.mu.Lock()
	defer r.mu.Unlock()
	var count int64
	for _, k := range keys {
		if _, ok := r.data[k]; ok {
			delete(r.data, k)
			count++
		}
	}
	return redis.NewIntResult(count, nil)
}

func (r *memRedisClient) Keys(_ context.Context, pattern string) *redis.StringSliceCmd {
	r.mu.RLock()
	defer r.mu.RUnlock()
	prefix := strings.TrimSuffix(pattern, "*")
	var matched []string
	for k := range r.data {
		if strings.HasPrefix(k, prefix) {
			matched = append(matched, k)
		}
	}
	return redis.NewStringSliceResult(matched, nil)
}

// =============================================================================
// In-Memory S3 Storage Stub for Rooms
// =============================================================================

type memRoomsStorage struct {
	mu      sync.RWMutex
	objects map[string][]byte
}

func (s *memRoomsStorage) PutObject(_ context.Context, bucket, objectKey string, reader io.Reader, _ int64, _ string) error {
	b, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[bucket+"/"+objectKey] = b
	return nil
}

func (s *memRoomsStorage) GetObject(_ context.Context, bucket, objectKey string) (io.ReadCloser, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.objects[bucket+"/"+objectKey]
	if !ok {
		return nil, fmt.Errorf("object not found: %s/%s", bucket, objectKey)
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

// =============================================================================
// Shared Thread-Safe In-Memory Domain Store
// =============================================================================

type memUserTaskState struct {
	TaskID         int
	TopicID        int
	IsCorrect      bool
	BestScore      int
	TotalAttempts  int
	LastUserAnswer any
}

type memRoomMemberRecord struct {
	UserID        uuid.UUID
	FreeCodeS3Key *string
	JoinedAt      time.Time
}

type memEventRecord struct {
	ID         int64
	SessionID  uuid.UUID
	TaskID     *int
	Event      string
	Payload    json.RawMessage
	OccurredAt time.Time
}

type memoryStore struct {
	mu sync.RWMutex

	users             map[uuid.UUID]*users.User
	tiers             map[int]*subscriptions.SubscriptionTier
	userSubscriptions map[uuid.UUID][]*subscriptions.UserSubscription
	invites           map[uuid.UUID]*subscriptions.SubscriptionInvite
	inviteClaims      map[uuid.UUID]map[uuid.UUID]bool // inviteID -> userID

	topics      map[int]*tasks.TopicDTO
	nextTopicID int

	tasks      map[int]*tasks.Task
	nextTaskID int
	userTasks  map[string]*memUserTaskState // "userID:taskID"

	rooms       map[uuid.UUID]*rooms.Room
	roomMembers map[uuid.UUID]map[uuid.UUID]*memRoomMemberRecord // roomID -> userID

	assignments     map[uuid.UUID]*assignments.Assignment
	assignmentTasks map[uuid.UUID][]int // assignmentID -> ordered taskIDs

	sessions           map[uuid.UUID]*assignments.AssignmentSession
	userAssignTasks    map[uuid.UUID]map[int]*assignments.UserAssignmentTask // sessionID -> taskID
	sessionEvents      map[uuid.UUID][]memEventRecord
	nextEventID        int64
	nextRecLogID       int64
	examScale          map[int16]int16
}

func newMemoryStore() *memoryStore {
	defaultScale := map[int16]int16{
		0: 0, 1: 7, 2: 14, 3: 20, 4: 27, 5: 34, 6: 40, 7: 43, 8: 46, 9: 48,
		10: 51, 11: 54, 12: 56, 13: 59, 14: 62, 15: 64, 16: 67, 17: 70, 18: 72, 19: 75,
		20: 78, 21: 80, 22: 83, 23: 85, 24: 88, 25: 90, 26: 93, 27: 95, 28: 98, 29: 100,
	}
	return &memoryStore{
		users:             make(map[uuid.UUID]*users.User),
		tiers:             make(map[int]*subscriptions.SubscriptionTier),
		userSubscriptions: make(map[uuid.UUID][]*subscriptions.UserSubscription),
		invites:           make(map[uuid.UUID]*subscriptions.SubscriptionInvite),
		inviteClaims:      make(map[uuid.UUID]map[uuid.UUID]bool),
		topics:            make(map[int]*tasks.TopicDTO),
		nextTopicID:       3000,
		tasks:             make(map[int]*tasks.Task),
		nextTaskID:        3000,
		userTasks:         make(map[string]*memUserTaskState),
		rooms:             make(map[uuid.UUID]*rooms.Room),
		roomMembers:       make(map[uuid.UUID]map[uuid.UUID]*memRoomMemberRecord),
		assignments:       make(map[uuid.UUID]*assignments.Assignment),
		assignmentTasks:   make(map[uuid.UUID][]int),
		sessions:          make(map[uuid.UUID]*assignments.AssignmentSession),
		userAssignTasks:   make(map[uuid.UUID]map[int]*assignments.UserAssignmentTask),
		sessionEvents:     make(map[uuid.UUID][]memEventRecord),
		nextEventID:       1,
		nextRecLogID:      1,
		examScale:         defaultScale,
	}
}

func (s *memoryStore) seedInitialData() {
	now := time.Now().UTC()
	yandexSuper := "yandex-super"
	yandexTeacher := "yandex-teacher"
	yandexExpired := "yandex-expired"
	yandexStudent := "yandex-student"
	yandexTarget := "yandex-target"

	// 1. Seed Users
	seedUsers := []*users.User{
		{
			ID: uuid.MustParse(SuperuserUUID), Username: "superuser_admin",
			FirstName: "Админ", LastName: "Системы", IsOnboarded: true,
			YandexID: &yandexSuper, Role: users.RoleSuperuser, CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: uuid.MustParse(TeacherUUID), Username: "teacher_active",
			FirstName: "Иван", LastName: "Учителей", IsOnboarded: true,
			YandexID: &yandexTeacher, Role: users.RoleTeacher, CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: uuid.MustParse(ExpiredTeacherUUID), Username: "teacher_expired",
			FirstName: "Пётр", LastName: "Истёкший", IsOnboarded: true,
			YandexID: &yandexExpired, Role: users.RoleTeacher, CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: uuid.MustParse(StudentUUID), Username: "student_main",
			FirstName: "Алексей", LastName: "Учеников", IsOnboarded: true,
			YandexID: &yandexStudent, Role: users.RoleStudent, CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: uuid.MustParse(TargetStudentUUID), Username: "student_target",
			FirstName: "Мария", LastName: "Целевая", IsOnboarded: false,
			YandexID: &yandexTarget, Role: users.RoleStudent, CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: uuid.MustParse(DummyStudent1UUID), Username: "student_dummy1",
			FirstName: "Дмитрий", LastName: "Первый", IsOnboarded: true,
			Role: users.RoleStudent, CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: uuid.MustParse(DummyStudent2UUID), Username: "student_dummy2",
			FirstName: "Елена", LastName: "Вторая", IsOnboarded: true,
			Role: users.RoleStudent, CreatedAt: now, UpdatedAt: now,
		},
	}
	for _, u := range seedUsers {
		s.users[u.ID] = u
	}

	// 2. Seed Subscription Tiers & Subscriptions
	tierFree := &subscriptions.SubscriptionTier{
		ID:          1,
		Slug:        "free",
		Name:        "Базовый",
		Description: "Бесплатный тариф",
		PriceRub:    0,
		IsPublic:    true,
		SortOrder:   1,
		Features:    subscriptions.DefaultFreeFeatures,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	tierPro := &subscriptions.SubscriptionTier{
		ID:          2,
		Slug:        "pro_teacher",
		Name:        "Учитель ПРО",
		Description: "Полный доступ для преподавателей",
		PriceRub:    990,
		IsPublic:    true,
		SortOrder:   2,
		Features: subscriptions.TierFeatures{
			MaxRooms:             15,
			MaxMembers:           40,
			MaxStudentsPerRoom:   40,
			CustomTasksEnabled:   true,
			ProctoringEnabled:    true,
			LiveCodeWatchEnabled: true,
			LiveStreamEnabled:    true,
			MCPEnabled:           true,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.tiers[1] = tierFree
	s.tiers[2] = tierPro

	teacherID := uuid.MustParse(TeacherUUID)
	s.userSubscriptions[teacherID] = []*subscriptions.UserSubscription{
		{
			ID:        uuid.New(),
			UserID:    teacherID,
			TierID:    2,
			StartsAt:  now.Add(-24 * time.Hour),
			ExpiresAt: now.Add(30 * 24 * time.Hour),
			CreatedAt: now,
			Tier:      tierPro,
		},
	}

	expiredTeacherID := uuid.MustParse(ExpiredTeacherUUID)
	s.userSubscriptions[expiredTeacherID] = []*subscriptions.UserSubscription{
		{
			ID:        uuid.New(),
			UserID:    expiredTeacherID,
			TierID:    2,
			StartsAt:  now.Add(-60 * 24 * time.Hour),
			ExpiresAt: now.Add(-7 * 24 * time.Hour),
			CreatedAt: now.Add(-60 * 24 * time.Hour),
			Tier:      tierPro,
		},
	}

	// 3. Seed Topics for all KIM numbers 1..27
	s.topics[101] = &tasks.TopicDTO{ID: 101, KimNumber: 1, Name: "Анализ информационных моделей", SortOrder: 1}
	s.topics[2401] = &tasks.TopicDTO{ID: 2401, KimNumber: 24, Name: "Обработка символьных строк", SortOrder: 24}
	s.topics[2601] = &tasks.TopicDTO{ID: 2601, KimNumber: 26, Name: "Обработка целочисленной информации", SortOrder: 26}
	for kim := 1; kim <= 27; kim++ {
		if kim == 1 || kim == 24 || kim == 26 {
			continue
		}
		topicID := kim * 100
		s.topics[topicID] = &tasks.TopicDTO{
			ID:        topicID,
			KimNumber: kim,
			Name:      fmt.Sprintf("Задание КИМ №%d", kim),
			SortOrder: kim,
		}
	}

	// 4. Seed Bank Tasks (1042, 1043, 1044 + full 1..27 coverage for SelfStartExam)
	srcWave := "Основная волна 2025"
	solMD24 := "Решение методом двух указателей"
	solCode24 := "with open('24.txt') as f:\n    print(1024)"
	s.tasks[1042] = &tasks.Task{
		ID: 1042, IsPublicBank: true, KimNumbers: []int{24}, TopicID: 2401, Difficulty: 3,
		Source: &srcWave, TextMD: "Текстовый файл 24.txt состоит из символов...",
		AnswerConfig:  tasks.AnswerConfig{AnswerType: "single", MaxScore: 1, TrimWhitespace: true},
		CorrectAnswer: tasks.CorrectAnswer{AnswerType: "single", Accepted: []string{"1024", "1024.0"}},
		SolutionMD:    &solMD24, SolutionCode: &solCode24, CreatedAt: now, UpdatedAt: now,
	}

	srcDemo := "Демоверсия 2026"
	solMD1 := "Сопоставим вершины таблицы и графа"
	solCode1 := "print(42)"
	s.tasks[1043] = &tasks.Task{
		ID: 1043, IsPublicBank: true, KimNumbers: []int{1}, TopicID: 101, Difficulty: 1,
		Source: &srcDemo, TextMD: "На рисунке схема дорог изображена в виде графа...",
		AnswerConfig:  tasks.AnswerConfig{AnswerType: "single", MaxScore: 1, TrimWhitespace: true},
		CorrectAnswer: tasks.CorrectAnswer{AnswerType: "single", Accepted: []string{"42"}},
		SolutionMD:    &solMD1, SolutionCode: &solCode1, CreatedAt: now, UpdatedAt: now,
	}

	srcProb := "Апробация 2025"
	solMD26 := "Жадный алгоритм"
	solCode26 := "print(15, 840)"
	s.tasks[1044] = &tasks.Task{
		ID: 1044, IsPublicBank: true, KimNumbers: []int{26}, TopicID: 2601, Difficulty: 2,
		Source: &srcProb, TextMD: "В кондитерской выпекают пирожные...",
		AnswerConfig:  tasks.AnswerConfig{AnswerType: "pair", MaxScore: 2, ScoringRule: "per_item", TrimWhitespace: true},
		CorrectAnswer: tasks.CorrectAnswer{AnswerType: "pair", Values: []string{"15", "840"}},
		SolutionMD:    &solMD26, SolutionCode: &solCode26, CreatedAt: now, UpdatedAt: now,
	}

	// Additional public KIM 24 task (unlocked) + remaining KIMs 2..27 so SelfStartExam selects 25 cards cleanly.
	srcBank := "Банк заданий"
	solCommon := "Базовое решение"
	s.tasks[1045] = &tasks.Task{
		ID: 1045, IsPublicBank: true, KimNumbers: []int{24}, TopicID: 2401, Difficulty: 2,
		Source: &srcBank, TextMD: "Текстовый файл состоит не более чем из 10^6 символов...",
		AnswerConfig:  tasks.AnswerConfig{AnswerType: "single", MaxScore: 1, TrimWhitespace: true},
		CorrectAnswer: tasks.CorrectAnswer{AnswerType: "single", Accepted: []string{"128"}},
		SolutionMD:    &solCommon, CreatedAt: now, UpdatedAt: now,
	}
	for kim := 2; kim <= 18; kim++ {
		id := 2000 + kim
		s.tasks[id] = &tasks.Task{
			ID: id, IsPublicBank: true, KimNumbers: []int{kim}, TopicID: kim * 100, Difficulty: (kim % 3) + 1,
			Source: &srcBank, TextMD: fmt.Sprintf("Типовая задача КИМ №%d", kim),
			AnswerConfig:  tasks.AnswerConfig{AnswerType: "single", MaxScore: 1, TrimWhitespace: true},
			CorrectAnswer: tasks.CorrectAnswer{AnswerType: "single", Accepted: []string{strconv.Itoa(kim * 10)}},
			SolutionMD:    &solCommon, CreatedAt: now, UpdatedAt: now,
		}
	}
	// Triplet 19-21 card
	s.tasks[2019] = &tasks.Task{
		ID: 2019, IsPublicBank: true, KimNumbers: []int{19, 20, 21}, TopicID: 1900, Difficulty: 2,
		Source: &srcBank, TextMD: "Теория игр: два игрока играют в камни...",
		AnswerConfig:  tasks.AnswerConfig{AnswerType: "triplet_19_21", MaxScore: 3, ScoringRule: "per_item", TrimWhitespace: true},
		CorrectAnswer: tasks.CorrectAnswer{AnswerType: "triplet_19_21", Ans19: []string{"10"}, Ans20: []string{"19 20"}, Ans21: []string{"18"}},
		SolutionMD:    &solCommon, CreatedAt: now, UpdatedAt: now,
	}
	for _, kim := range []int{22, 23, 25, 27} {
		id := 2000 + kim
		maxScore := 1
		if kim == 27 {
			maxScore = 2
		}
		s.tasks[id] = &tasks.Task{
			ID: id, IsPublicBank: true, KimNumbers: []int{kim}, TopicID: kim * 100, Difficulty: 2,
			Source: &srcBank, TextMD: fmt.Sprintf("Типовая задача КИМ №%d", kim),
			AnswerConfig:  tasks.AnswerConfig{AnswerType: "single", MaxScore: maxScore, TrimWhitespace: true},
			CorrectAnswer: tasks.CorrectAnswer{AnswerType: "single", Accepted: []string{strconv.Itoa(kim * 10)}},
			SolutionMD:    &solCommon, CreatedAt: now, UpdatedAt: now,
		}
	}

	// 5. Seed Rooms
	roomFullID := uuid.MustParse(RoomFullUUID)
	s.rooms[roomFullID] = &rooms.Room{
		ID: roomFullID, Name: "Full Class", CreatorID: teacherID,
		InviteCode: "FULL1234", MaxMembers: 2, CreatedAt: now, UpdatedAt: now,
	}
	s.roomMembers[roomFullID] = map[uuid.UUID]*memRoomMemberRecord{
		uuid.MustParse(DummyStudent1UUID): {UserID: uuid.MustParse(DummyStudent1UUID), JoinedAt: now},
		uuid.MustParse(DummyStudent2UUID): {UserID: uuid.MustParse(DummyStudent2UUID), JoinedAt: now},
	}

	roomStdID := uuid.MustParse(RoomStandardUUID)
	studentUUID := uuid.MustParse(StudentUUID)
	s.rooms[roomStdID] = &rooms.Room{
		ID: roomStdID, Name: "11-A Info", CreatorID: teacherID,
		InviteCode: "K7X9M2P4", MaxMembers: 30, CreatedAt: now, UpdatedAt: now,
	}
	s.roomMembers[roomStdID] = map[uuid.UUID]*memRoomMemberRecord{
		studentUUID: {UserID: studentUUID, JoinedAt: now},
	}

	expiredRoomID := uuid.MustParse(ExpiredRoomUUID)
	s.rooms[expiredRoomID] = &rooms.Room{
		ID: expiredRoomID, Name: "Archived 11-B", CreatorID: expiredTeacherID,
		InviteCode: "EXPR1234", MaxMembers: 25, CreatedAt: now.Add(-30 * 24 * time.Hour), UpdatedAt: now,
	}
	s.roomMembers[expiredRoomID] = map[uuid.UUID]*memRoomMemberRecord{
		studentUUID: {UserID: studentUUID, JoinedAt: now.Add(-20 * 24 * time.Hour)},
	}

	// 6. Seed Assignments & Active Exam Session locking task #1042 for StudentUUID
	dur := 235
	testAssignID := uuid.MustParse(TestAssignUUID)
	s.assignments[testAssignID] = &assignments.Assignment{
		ID: testAssignID, RoomID: &roomStdID, CreatorID: teacherID,
		Title: "Контрольный пробник (матрица и античит)", Mode: assignments.ModeMockExam,
		Settings: assignments.AssignmentSettings{
			MaxAttempts: 1, DurationMinutes: &dur, RevealPolicy: assignments.RevealAfterSubmit,
			ShowSecondaryScore: true, ProctoringEnabled: true,
		},
		TasksCount: 1, CreatedAt: now, UpdatedAt: now,
	}
	// Locking task 1042 for StudentUUID in an active mock_exam session!
	s.assignmentTasks[testAssignID] = []int{1042}

	testSessExp := now.Add(3 * time.Hour)
	testSessID := uuid.MustParse(TestSessionUUID)
	s.sessions[testSessID] = &assignments.AssignmentSession{
		ID: testSessID, AssignmentID: testAssignID, UserID: studentUUID,
		Status: assignments.SessionInProgress, StartedAt: now, ExpiresAt: &testSessExp,
	}

	// Live streaming fixture session for ws_test.go (owned by TargetStudentUUID or StudentUUID)
	liveSessID := uuid.MustParse(LiveSessionUUID)
	s.sessions[liveSessID] = &assignments.AssignmentSession{
		ID: liveSessID, AssignmentID: testAssignID, UserID: uuid.MustParse(TargetStudentUUID),
		Status: assignments.SessionInProgress, StartedAt: now, ExpiresAt: &testSessExp,
	}

	// Expired teacher's assignment & session for read-only inspection tests
	expAssignID := uuid.MustParse(ExpiredAssignUUID)
	s.assignments[expAssignID] = &assignments.Assignment{
		ID: expAssignID, RoomID: &expiredRoomID, CreatorID: expiredTeacherID,
		Title: "Архивная работа", Mode: assignments.ModeHomework,
		Settings: assignments.AssignmentSettings{
			MaxAttempts: 3, RevealPolicy: assignments.RevealImmediate,
		},
		TasksCount: 1, CreatedAt: now.Add(-15 * 24 * time.Hour), UpdatedAt: now,
	}
	s.assignmentTasks[expAssignID] = []int{1043}

	expSessID := uuid.MustParse(ExpiredSessionUUID)
	expFin := now.Add(-10 * 24 * time.Hour)
	secScore := int16(7)
	s.sessions[expSessID] = &assignments.AssignmentSession{
		ID: expSessID, AssignmentID: expAssignID, UserID: studentUUID,
		Status: assignments.SessionFinished, StartedAt: now.Add(-11 * 24 * time.Hour),
		FinishedAt: &expFin, PrimaryScore: 1, SecondaryScore: &secScore,
	}
}

func (s *memoryStore) isTaskLockedLocked(taskID int, userID uuid.UUID) bool {
	for _, sess := range s.sessions {
		if sess.UserID != userID || sess.Status != assignments.SessionInProgress {
			continue
		}
		a, ok := s.assignments[sess.AssignmentID]
		if !ok || a.DeletedAt != nil {
			continue
		}
		if a.Mode != assignments.ModeMockExam && a.Mode != assignments.ModeProctoredTest {
			continue
		}
		for _, tid := range s.assignmentTasks[a.ID] {
			if tid == taskID {
				return true
			}
		}
	}
	return false
}

func (s *memoryStore) activeSubscriptionLocked(userID uuid.UUID) *subscriptions.UserSubscription {
	list := s.userSubscriptions[userID]
	now := time.Now()
	var best *subscriptions.UserSubscription
	for _, sub := range list {
		if sub.RevokedAt == nil && sub.ExpiresAt.After(now) {
			if best == nil || sub.ExpiresAt.After(best.ExpiresAt) {
				best = sub
			}
		}
	}
	return best
}

func (s *memoryStore) latestSubscriptionLocked(userID uuid.UUID) *subscriptions.UserSubscription {
	list := s.userSubscriptions[userID]
	var best *subscriptions.UserSubscription
	for _, sub := range list {
		if best == nil || sub.ExpiresAt.After(best.ExpiresAt) {
			best = sub
		}
	}
	return best
}

func (s *memoryStore) buildTaskDTOLocked(t *tasks.Task, userID *uuid.UUID, forceShowSolution bool) tasks.TaskDTO {
	var topic tasks.TopicDTO
	if tp, ok := s.topics[t.TopicID]; ok {
		topic = *tp
	}

	var userState *tasks.UserState
	canViewSolution := forceShowSolution
	if userID != nil {
		key := fmt.Sprintf("%s:%d", userID.String(), t.ID)
		if ut, ok := s.userTasks[key]; ok && ut.TotalAttempts > 0 {
			canView := ut.IsCorrect || ut.TotalAttempts >= 3
			if canView {
				canViewSolution = true
			}
			userState = &tasks.UserState{
				IsAttempted:     true,
				IsCorrect:       ut.IsCorrect,
				EarnedScore:     ut.BestScore,
				TotalAttempts:   ut.TotalAttempts,
				LastUserAnswer:  ut.LastUserAnswer,
				CanViewSolution: canView,
			}
		}
	}

	var solDTO *tasks.SolutionDTO
	if canViewSolution {
		corr := t.CorrectAnswer
		solDTO = &tasks.SolutionDTO{
			CorrectAnswer: &corr,
			SolutionMD:    t.SolutionMD,
			SolutionCode:  t.SolutionCode,
		}
	}

	imgs := t.Images
	if imgs == nil {
		imgs = []string{}
	}
	files := t.Files
	if files == nil {
		files = []tasks.FileAttachment{}
	}

	return tasks.TaskDTO{
		ID:           t.ID,
		OwnerID:      t.OwnerID,
		IsPublicBank: t.IsPublicBank,
		KimNumbers:   append([]int(nil), t.KimNumbers...),
		Topic:        topic,
		Difficulty:   t.Difficulty,
		Source:       t.Source,
		TextMD:       t.TextMD,
		Images:       imgs,
		Files:        files,
		AnswerConfig: t.AnswerConfig,
		Stats:        tasks.TaskStats{},
		UserState:    userState,
		Solution:     solDTO,
		CreatedAt:    t.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// =============================================================================
// Adapter: auth.UserRepository
// =============================================================================

type memAuthUserRepo struct {
	s *memoryStore
}

func toAuthUser(u *users.User) *auth.User {
	cp := &auth.User{
		ID:          u.ID.String(),
		Username:    u.Username,
		FirstName:   u.FirstName,
		LastName:    u.LastName,
		IsOnboarded: u.IsOnboarded,
		YandexID:    u.YandexID,
		VKID:        u.VKID,
		Role:        string(u.Role),
		CreatedAt:   u.CreatedAt,
		UpdatedAt:   u.UpdatedAt,
		DeletedAt:   u.DeletedAt,
	}
	return cp
}

func (r *memAuthUserRepo) GetByID(_ context.Context, id string) (*auth.User, error) {
	uid, err := uuid.Parse(id)
	if err != nil {
		return nil, auth.ErrUserNotFound
	}
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	u, ok := r.s.users[uid]
	if !ok || u.DeletedAt != nil {
		return nil, auth.ErrUserNotFound
	}
	return toAuthUser(u), nil
}

func (r *memAuthUserRepo) GetByYandexID(_ context.Context, yandexID string) (*auth.User, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	for _, u := range r.s.users {
		if u.DeletedAt == nil && u.YandexID != nil && *u.YandexID == yandexID {
			return toAuthUser(u), nil
		}
	}
	return nil, auth.ErrUserNotFound
}

func (r *memAuthUserRepo) GetByVKID(_ context.Context, vkID string) (*auth.User, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	for _, u := range r.s.users {
		if u.DeletedAt == nil && u.VKID != nil && *u.VKID == vkID {
			return toAuthUser(u), nil
		}
	}
	return nil, auth.ErrUserNotFound
}

func (r *memAuthUserRepo) GetByUsername(_ context.Context, username string) (*auth.User, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	for _, u := range r.s.users {
		if u.DeletedAt == nil && u.Username == username {
			return toAuthUser(u), nil
		}
	}
	return nil, auth.ErrUserNotFound
}

func (r *memAuthUserRepo) Create(_ context.Context, u *auth.User) error {
	uid, err := uuid.Parse(u.ID)
	if err != nil {
		uid = uuid.New()
		u.ID = uid.String()
	}
	now := time.Now().UTC()
	if u.CreatedAt.IsZero() {
		u.CreatedAt = now
	}
	if u.UpdatedAt.IsZero() {
		u.UpdatedAt = now
	}
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	r.s.users[uid] = &users.User{
		ID:          uid,
		Username:    u.Username,
		FirstName:   u.FirstName,
		LastName:    u.LastName,
		IsOnboarded: u.IsOnboarded,
		YandexID:    u.YandexID,
		VKID:        u.VKID,
		Role:        users.UserRole(u.Role),
		CreatedAt:   u.CreatedAt,
		UpdatedAt:   u.UpdatedAt,
	}
	return nil
}

func (r *memAuthUserRepo) Update(_ context.Context, u *auth.User) error {
	uid, err := uuid.Parse(u.ID)
	if err != nil {
		return auth.ErrUserNotFound
	}
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	existing, ok := r.s.users[uid]
	if !ok {
		return auth.ErrUserNotFound
	}
	existing.Username = u.Username
	existing.FirstName = u.FirstName
	existing.LastName = u.LastName
	existing.IsOnboarded = u.IsOnboarded
	existing.Role = users.UserRole(u.Role)
	existing.UpdatedAt = time.Now().UTC()
	return nil
}

func (r *memAuthUserRepo) GetUserSubscription(_ context.Context, userID string) (*auth.UserSubscriptionDTO, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return nil, nil
	}
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()

	sub := r.s.activeSubscriptionLocked(uid)
	if sub == nil {
		sub = r.s.latestSubscriptionLocked(uid)
	}
	if sub == nil {
		return nil, nil
	}

	var tierDTO *auth.SubscriptionTierDTO
	if sub.Tier != nil {
		b, _ := json.Marshal(sub.Tier.Features)
		featMap := make(map[string]any)
		_ = json.Unmarshal(b, &featMap)
		tierDTO = &auth.SubscriptionTierDTO{
			ID:       sub.Tier.ID,
			Slug:     sub.Tier.Slug,
			Name:     sub.Tier.Name,
			PriceRub: sub.Tier.PriceRub,
			Features: featMap,
		}
	}

	isActive := sub.IsActive()
	starts := sub.StartsAt
	exp := sub.ExpiresAt
	return &auth.UserSubscriptionDTO{
		IsActive:   isActive,
		IsReadOnly: !isActive,
		Tier:       tierDTO,
		StartsAt:   &starts,
		ExpiresAt:  &exp,
	}, nil
}

// =============================================================================
// Adapter: users.Repository
// =============================================================================

type memUsersRepo struct {
	s *memoryStore
}

func cloneUser(u *users.User) *users.User {
	cp := *u
	return &cp
}

func (r *memUsersRepo) GetByID(_ context.Context, id uuid.UUID) (*users.User, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	u, ok := r.s.users[id]
	if !ok || u.DeletedAt != nil {
		return nil, users.ErrNotFound
	}
	return cloneUser(u), nil
}

func (r *memUsersRepo) GetByUsername(_ context.Context, username string) (*users.User, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	for _, u := range r.s.users {
		if u.DeletedAt == nil && u.Username == username {
			return cloneUser(u), nil
		}
	}
	return nil, users.ErrNotFound
}

func (r *memUsersRepo) GetByYandexID(_ context.Context, yandexID string) (*users.User, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	for _, u := range r.s.users {
		if u.DeletedAt == nil && u.YandexID != nil && *u.YandexID == yandexID {
			return cloneUser(u), nil
		}
	}
	return nil, users.ErrNotFound
}

func (r *memUsersRepo) GetByVKID(_ context.Context, vkID string) (*users.User, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	for _, u := range r.s.users {
		if u.DeletedAt == nil && u.VKID != nil && *u.VKID == vkID {
			return cloneUser(u), nil
		}
	}
	return nil, users.ErrNotFound
}

func (r *memUsersRepo) Create(_ context.Context, user *users.User) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if user.ID == uuid.Nil {
		user.ID = uuid.New()
	}
	for _, existing := range r.s.users {
		if existing.DeletedAt == nil && existing.Username == user.Username {
			return users.ErrUsernameTaken
		}
	}
	now := time.Now().UTC()
	user.CreatedAt = now
	user.UpdatedAt = now
	r.s.users[user.ID] = cloneUser(user)
	return nil
}

func (r *memUsersRepo) UpdateOnboarding(_ context.Context, id uuid.UUID, firstName, lastName, username string) (*users.User, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	u, ok := r.s.users[id]
	if !ok || u.DeletedAt != nil {
		return nil, users.ErrNotFound
	}
	for oid, other := range r.s.users {
		if oid != id && other.DeletedAt == nil && other.Username == username {
			return nil, users.ErrUsernameTaken
		}
	}
	u.FirstName = firstName
	u.LastName = lastName
	u.Username = username
	u.IsOnboarded = true
	u.UpdatedAt = time.Now().UTC()
	return cloneUser(u), nil
}

func (r *memUsersRepo) UpdateProfile(_ context.Context, id uuid.UUID, firstName, lastName, username *string) (*users.User, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	u, ok := r.s.users[id]
	if !ok || u.DeletedAt != nil {
		return nil, users.ErrNotFound
	}
	if username != nil {
		for oid, other := range r.s.users {
			if oid != id && other.DeletedAt == nil && other.Username == *username {
				return nil, users.ErrUsernameTaken
			}
		}
		u.Username = *username
	}
	if firstName != nil {
		u.FirstName = *firstName
	}
	if lastName != nil {
		u.LastName = *lastName
	}
	u.UpdatedAt = time.Now().UTC()
	return cloneUser(u), nil
}

func (r *memUsersRepo) UpdateRole(_ context.Context, id uuid.UUID, role users.UserRole) (*users.User, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	u, ok := r.s.users[id]
	if !ok || u.DeletedAt != nil {
		return nil, users.ErrNotFound
	}
	u.Role = role
	u.UpdatedAt = time.Now().UTC()
	return cloneUser(u), nil
}

func (r *memUsersRepo) List(_ context.Context, q, role string, limit, offset int) ([]*users.User, int, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	qLower := strings.ToLower(strings.TrimSpace(q))
	var filtered []*users.User
	for _, u := range r.s.users {
		if u.DeletedAt != nil {
			continue
		}
		if role != "" && string(u.Role) != role {
			continue
		}
		if qLower != "" {
			if !strings.Contains(strings.ToLower(u.Username), qLower) &&
				!strings.Contains(strings.ToLower(u.FirstName), qLower) &&
				!strings.Contains(strings.ToLower(u.LastName), qLower) {
				continue
			}
		}
		filtered = append(filtered, cloneUser(u))
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].ID.String() < filtered[j].ID.String() })
	total := len(filtered)
	if offset >= total {
		return []*users.User{}, total, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return filtered[offset:end], total, nil
}

// =============================================================================
// Adapter: subscriptions.Repository
// =============================================================================

type memSubRepo struct {
	s *memoryStore
}

func (r *memSubRepo) GetTierByID(_ context.Context, id int) (*subscriptions.SubscriptionTier, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	t, ok := r.s.tiers[id]
	if !ok {
		return nil, subscriptions.ErrTierNotFound
	}
	cp := *t
	return &cp, nil
}

func (r *memSubRepo) GetTierBySlug(_ context.Context, slug string) (*subscriptions.SubscriptionTier, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	for _, t := range r.s.tiers {
		if t.Slug == slug {
			cp := *t
			return &cp, nil
		}
	}
	return nil, subscriptions.ErrTierNotFound
}

func (r *memSubRepo) ListPublicTiers(_ context.Context) ([]*subscriptions.SubscriptionTier, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	var res []*subscriptions.SubscriptionTier
	for _, t := range r.s.tiers {
		if t.IsPublic {
			cp := *t
			res = append(res, &cp)
		}
	}
	sort.Slice(res, func(i, j int) bool { return res[i].SortOrder < res[j].SortOrder })
	return res, nil
}

func (r *memSubRepo) ListAllTiers(_ context.Context) ([]*subscriptions.SubscriptionTier, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	var res []*subscriptions.SubscriptionTier
	for _, t := range r.s.tiers {
		cp := *t
		res = append(res, &cp)
	}
	sort.Slice(res, func(i, j int) bool { return res[i].SortOrder < res[j].SortOrder })
	return res, nil
}

func (r *memSubRepo) CreateTier(_ context.Context, tier *subscriptions.SubscriptionTier) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for _, existing := range r.s.tiers {
		if existing.Slug == tier.Slug {
			return subscriptions.ErrTierSlugTaken
		}
	}
	maxID := 0
	for id := range r.s.tiers {
		if id > maxID {
			maxID = id
		}
	}
	tier.ID = maxID + 1
	now := time.Now().UTC()
	tier.CreatedAt = now
	tier.UpdatedAt = now
	cp := *tier
	r.s.tiers[tier.ID] = &cp
	return nil
}

func (r *memSubRepo) UpdateTier(_ context.Context, tier *subscriptions.SubscriptionTier) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if _, ok := r.s.tiers[tier.ID]; !ok {
		return subscriptions.ErrTierNotFound
	}
	tier.UpdatedAt = time.Now().UTC()
	cp := *tier
	r.s.tiers[tier.ID] = &cp
	return nil
}

func (r *memSubRepo) GetActiveSubscription(_ context.Context, userID uuid.UUID) (*subscriptions.UserSubscription, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	sub := r.s.activeSubscriptionLocked(userID)
	if sub == nil {
		return nil, subscriptions.ErrNoActiveSubscription
	}
	cp := *sub
	return &cp, nil
}

func (r *memSubRepo) GetLatestSubscription(_ context.Context, userID uuid.UUID) (*subscriptions.UserSubscription, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	sub := r.s.latestSubscriptionLocked(userID)
	if sub == nil {
		return nil, subscriptions.ErrNoActiveSubscription
	}
	cp := *sub
	return &cp, nil
}

func (r *memSubRepo) CreateUserSubscription(_ context.Context, sub *subscriptions.UserSubscription) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if sub.ID == uuid.Nil {
		sub.ID = uuid.New()
	}
	sub.CreatedAt = time.Now().UTC()
	if t, ok := r.s.tiers[sub.TierID]; ok {
		cpT := *t
		sub.Tier = &cpT
	}
	cp := *sub
	r.s.userSubscriptions[sub.UserID] = append(r.s.userSubscriptions[sub.UserID], &cp)
	return nil
}

func (r *memSubRepo) ExtendUserSubscription(_ context.Context, userID uuid.UUID, tierID int, durationDays int, grantedBy *uuid.UUID, note *string) (*subscriptions.UserSubscription, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	now := time.Now().UTC()
	startsAt := now
	expiresAt := now.AddDate(0, 0, durationDays)
	if active := r.s.activeSubscriptionLocked(userID); active != nil {
		startsAt = active.StartsAt
		expiresAt = active.ExpiresAt.AddDate(0, 0, durationDays)
	}
	var tierPtr *subscriptions.SubscriptionTier
	if t, ok := r.s.tiers[tierID]; ok {
		cp := *t
		tierPtr = &cp
	}
	newSub := &subscriptions.UserSubscription{
		ID:        uuid.New(),
		UserID:    userID,
		TierID:    tierID,
		StartsAt:  startsAt,
		ExpiresAt: expiresAt,
		GrantedBy: grantedBy,
		Note:      note,
		CreatedAt: now,
		Tier:      tierPtr,
	}
	r.s.userSubscriptions[userID] = append(r.s.userSubscriptions[userID], newSub)
	cp := *newSub
	return &cp, nil
}

func (r *memSubRepo) GetInviteByCode(_ context.Context, code string) (*subscriptions.SubscriptionInvite, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	for _, inv := range r.s.invites {
		if inv.DeletedAt == nil && strings.EqualFold(inv.Code, code) {
			cp := *inv
			return &cp, nil
		}
	}
	return nil, subscriptions.ErrInviteNotFound
}

func (r *memSubRepo) GetInviteByID(_ context.Context, id uuid.UUID) (*subscriptions.SubscriptionInvite, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	inv, ok := r.s.invites[id]
	if !ok || inv.DeletedAt != nil {
		return nil, subscriptions.ErrInviteNotFound
	}
	cp := *inv
	return &cp, nil
}

func (r *memSubRepo) ListInvites(_ context.Context, limit, offset int) ([]*subscriptions.SubscriptionInvite, int, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	var list []*subscriptions.SubscriptionInvite
	for _, inv := range r.s.invites {
		if inv.DeletedAt == nil {
			cp := *inv
			list = append(list, &cp)
		}
	}
	total := len(list)
	if offset >= total {
		return []*subscriptions.SubscriptionInvite{}, total, nil
	}
	end := offset + limit
	if limit <= 0 || end > total {
		end = total
	}
	return list[offset:end], total, nil
}

func (r *memSubRepo) CreateInvite(_ context.Context, invite *subscriptions.SubscriptionInvite) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if invite.ID == uuid.Nil {
		invite.ID = uuid.New()
	}
	invite.CreatedAt = time.Now().UTC()
	cp := *invite
	r.s.invites[invite.ID] = &cp
	return nil
}

func (r *memSubRepo) DeleteInvite(_ context.Context, id uuid.UUID) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	inv, ok := r.s.invites[id]
	if !ok || inv.DeletedAt != nil {
		return subscriptions.ErrInviteNotFound
	}
	now := time.Now().UTC()
	inv.DeletedAt = &now
	return nil
}

func (r *memSubRepo) ClaimInviteTx(_ context.Context, code string, userID uuid.UUID) (*subscriptions.UserSubscription, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	var found *subscriptions.SubscriptionInvite
	for _, inv := range r.s.invites {
		if inv.DeletedAt == nil && strings.EqualFold(inv.Code, code) {
			found = inv
			break
		}
	}
	if found == nil {
		return nil, subscriptions.ErrInviteNotFound
	}
	if found.ExpiresAt != nil && found.ExpiresAt.Before(time.Now()) {
		return nil, subscriptions.ErrInviteExpired
	}
	if claims := r.s.inviteClaims[found.ID]; claims != nil && claims[userID] {
		return nil, subscriptions.ErrInviteAlreadyClaimed
	}
	if found.UsedCount >= found.MaxUses {
		return nil, subscriptions.ErrInviteExhausted
	}
	if r.s.inviteClaims[found.ID] == nil {
		r.s.inviteClaims[found.ID] = make(map[uuid.UUID]bool)
	}
	r.s.inviteClaims[found.ID][userID] = true
	found.UsedCount++

	now := time.Now().UTC()
	var tierPtr *subscriptions.SubscriptionTier
	if t, ok := r.s.tiers[found.TierID]; ok {
		cp := *t
		tierPtr = &cp
	}
	newSub := &subscriptions.UserSubscription{
		ID:        uuid.New(),
		UserID:    userID,
		TierID:    found.TierID,
		StartsAt:  now,
		ExpiresAt: now.AddDate(0, 0, found.DurationDays),
		CreatedAt: now,
		Tier:      tierPtr,
	}
	r.s.userSubscriptions[userID] = append(r.s.userSubscriptions[userID], newSub)
	if u, ok := r.s.users[userID]; ok && u.Role == users.RoleStudent {
		u.Role = users.RoleTeacher
	}
	cp := *newSub
	return &cp, nil
}

// =============================================================================
// Adapter: tasks.Repository + tasks.GradingProvider
// =============================================================================

type memTasksRepo struct {
	s *memoryStore
}

func (r *memTasksRepo) GetGradingData(_ context.Context, id int) (tasks.AnswerConfig, tasks.CorrectAnswer, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	t, ok := r.s.tasks[id]
	if !ok || t.DeletedAt != nil {
		return tasks.AnswerConfig{}, tasks.CorrectAnswer{}, tasks.ErrTaskNotFound
	}
	return t.AnswerConfig, t.CorrectAnswer, nil
}

func (r *memTasksRepo) IsTaskInActiveExam(_ context.Context, taskID int, userID uuid.UUID) (bool, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	return r.s.isTaskLockedLocked(taskID, userID), nil
}

func (r *memTasksRepo) ListBankTasks(_ context.Context, filter tasks.ListTasksFilter) ([]tasks.TaskDTO, int, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()

	var all []*tasks.Task
	for _, t := range r.s.tasks {
		if t.DeletedAt != nil {
			continue
		}
		if filter.IsPublicBank != nil && t.IsPublicBank != *filter.IsPublicBank {
			continue
		}
		if filter.OwnerID != nil && (t.OwnerID == nil || *t.OwnerID != *filter.OwnerID) {
			continue
		}
		if filter.Kim != nil {
			matched := false
			for _, kn := range t.KimNumbers {
				if kn == *filter.Kim {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		if filter.TopicID != nil && t.TopicID != *filter.TopicID {
			continue
		}
		if filter.Difficulty != nil && t.Difficulty != *filter.Difficulty {
			continue
		}
		if q := strings.TrimSpace(filter.Query); q != "" {
			if strings.HasPrefix(q, "#") {
				if id, err := strconv.Atoi(strings.TrimPrefix(q, "#")); err == nil && t.ID != id {
					continue
				}
			} else if !strings.Contains(strings.ToLower(t.TextMD), strings.ToLower(q)) && strconv.Itoa(t.ID) != q {
				continue
			}
		}
		if filter.UserID != nil && r.s.isTaskLockedLocked(t.ID, *filter.UserID) {
			continue
		}
		all = append(all, t)
	}

	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	total := len(all)

	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	if offset >= total {
		return []tasks.TaskDTO{}, total, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}

	items := make([]tasks.TaskDTO, 0, end-offset)
	for _, t := range all[offset:end] {
		items = append(items, r.s.buildTaskDTOLocked(t, filter.UserID, false))
	}
	return items, total, nil
}

func (r *memTasksRepo) GetTaskByID(_ context.Context, id int, userID *uuid.UUID) (*tasks.TaskDTO, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	t, ok := r.s.tasks[id]
	if !ok || t.DeletedAt != nil {
		return nil, tasks.ErrTaskNotFound
	}
	dto := r.s.buildTaskDTOLocked(t, userID, false)
	return &dto, nil
}

func (r *memTasksRepo) RecordSubmission(
	_ context.Context,
	taskID int,
	userID uuid.UUID,
	res tasks.GradeResult,
	answerJSON []byte,
	_ *int64,
) (*tasks.TaskSubmitResponse, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	t, ok := r.s.tasks[taskID]
	if !ok || t.DeletedAt != nil {
		return nil, tasks.ErrTaskNotFound
	}

	key := fmt.Sprintf("%s:%d", userID.String(), taskID)
	ut, exists := r.s.userTasks[key]
	if !exists {
		ut = &memUserTaskState{TaskID: taskID, TopicID: t.TopicID}
		r.s.userTasks[key] = ut
	}
	ut.TotalAttempts++
	ut.IsCorrect = ut.IsCorrect || res.IsCorrect
	if res.Score > ut.BestScore {
		ut.BestScore = res.Score
	}
	var parsedAns any
	_ = json.Unmarshal(answerJSON, &parsedAns)
	ut.LastUserAnswer = parsedAns

	canView := ut.IsCorrect || ut.TotalAttempts >= 3
	var solDTO *tasks.SolutionDTO
	if canView {
		corr := t.CorrectAnswer
		solDTO = &tasks.SolutionDTO{
			CorrectAnswer: &corr,
			SolutionMD:    t.SolutionMD,
			SolutionCode:  t.SolutionCode,
		}
	}

	return &tasks.TaskSubmitResponse{
		IsCorrect:       res.IsCorrect,
		EarnedScore:     res.Score,
		MaxScore:        res.MaxScore,
		TotalAttempts:   ut.TotalAttempts,
		CanViewSolution: canView,
		Solution:        solDTO,
	}, nil
}

func (r *memTasksRepo) CreateTask(_ context.Context, task *tasks.Task) (int, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	r.s.nextTaskID++
	id := r.s.nextTaskID
	now := time.Now().UTC()
	cp := *task
	cp.ID = id
	cp.CreatedAt = now
	cp.UpdatedAt = now
	r.s.tasks[id] = &cp
	return id, nil
}

func (r *memTasksRepo) UpdateTask(_ context.Context, id int, task *tasks.Task) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	existing, ok := r.s.tasks[id]
	if !ok || existing.DeletedAt != nil {
		return tasks.ErrTaskNotFound
	}
	existing.KimNumbers = task.KimNumbers
	existing.TopicID = task.TopicID
	existing.Difficulty = task.Difficulty
	existing.Source = task.Source
	existing.TextMD = task.TextMD
	existing.Images = task.Images
	existing.Files = task.Files
	existing.AnswerConfig = task.AnswerConfig
	existing.CorrectAnswer = task.CorrectAnswer
	existing.SolutionMD = task.SolutionMD
	existing.SolutionCode = task.SolutionCode
	existing.UpdatedAt = time.Now().UTC()
	return nil
}

func (r *memTasksRepo) DeleteTask(_ context.Context, id int) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	existing, ok := r.s.tasks[id]
	if !ok || existing.DeletedAt != nil {
		return tasks.ErrTaskNotFound
	}
	now := time.Now().UTC()
	existing.DeletedAt = &now
	return nil
}

func (r *memTasksRepo) ListAuthorTasks(ctx context.Context, ownerID uuid.UUID, filter tasks.ListTasksFilter) ([]tasks.TaskDTO, int, error) {
	filter.OwnerID = &ownerID
	isPub := false
	filter.IsPublicBank = &isPub
	return r.ListBankTasks(ctx, filter)
}

func (r *memTasksRepo) GetTopics(_ context.Context) ([]tasks.KimGroupDTO, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	byKim := make(map[int][]tasks.TopicDTO)
	for _, tp := range r.s.topics {
		byKim[tp.KimNumber] = append(byKim[tp.KimNumber], *tp)
	}
	var groups []tasks.KimGroupDTO
	for kim := 1; kim <= 27; kim++ {
		topicsList := byKim[kim]
		sort.Slice(topicsList, func(i, j int) bool { return topicsList[i].SortOrder < topicsList[j].SortOrder })
		if topicsList == nil {
			topicsList = []tasks.TopicDTO{}
		}
		groups = append(groups, tasks.KimGroupDTO{
			KimNumber: kim,
			Topics:    topicsList,
		})
	}
	return groups, nil
}

func (r *memTasksRepo) CreateTopic(_ context.Context, kimNumber int, name string, sortOrder int) (*tasks.TopicDTO, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	r.s.nextTopicID++
	tp := &tasks.TopicDTO{
		ID:        r.s.nextTopicID,
		KimNumber: kimNumber,
		Name:      name,
		SortOrder: sortOrder,
	}
	r.s.topics[tp.ID] = tp
	cp := *tp
	return &cp, nil
}

func (r *memTasksRepo) UpdateTopic(_ context.Context, id int, name string, sortOrder int) (*tasks.TopicDTO, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	tp, ok := r.s.topics[id]
	if !ok {
		return nil, tasks.ErrTopicNotFound
	}
	tp.Name = name
	tp.SortOrder = sortOrder
	cp := *tp
	return &cp, nil
}

func (r *memTasksRepo) DeleteTopic(_ context.Context, id int) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if _, ok := r.s.topics[id]; !ok {
		return tasks.ErrTopicNotFound
	}
	delete(r.s.topics, id)
	return nil
}

func (r *memTasksRepo) GetOrCreateTopic(_ context.Context, kimNumber int, name string) (int, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for _, tp := range r.s.topics {
		if tp.KimNumber == kimNumber && tp.Name == name {
			return tp.ID, nil
		}
	}
	r.s.nextTopicID++
	tp := &tasks.TopicDTO{ID: r.s.nextTopicID, KimNumber: kimNumber, Name: name, SortOrder: kimNumber}
	r.s.topics[tp.ID] = tp
	return tp.ID, nil
}

// =============================================================================
// Adapter: recommendations.Repository
// =============================================================================

type memRecRepo struct {
	s *memoryStore
}

func (r *memRecRepo) GetActiveAssignments(_ context.Context, userID uuid.UUID) ([]recommendations.ActiveAssignmentDTO, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	var res []recommendations.ActiveAssignmentDTO
	for _, sess := range r.s.sessions {
		if sess.UserID != userID || sess.Status != assignments.SessionInProgress {
			continue
		}
		a, ok := r.s.assignments[sess.AssignmentID]
		if !ok || a.DeletedAt != nil {
			continue
		}
		var roomIDStr *string
		roomName := "Самостоятельный пробник"
		if a.RoomID != nil {
			sID := a.RoomID.String()
			roomIDStr = &sID
			if rm, ok := r.s.rooms[*a.RoomID]; ok {
				roomName = rm.Name
			}
		}
		var expStr *string
		if sess.ExpiresAt != nil {
			st := sess.ExpiresAt.UTC().Format(time.RFC3339)
			expStr = &st
		}
		res = append(res, recommendations.ActiveAssignmentDTO{
			ID:       a.ID.String(),
			RoomID:   roomIDStr,
			RoomName: roomName,
			Title:    a.Title,
			Mode:     string(a.Mode),
			Session: recommendations.ActiveSessionDTO{
				ID:        sess.ID.String(),
				Status:    string(sess.Status),
				StartedAt: sess.StartedAt.UTC().Format(time.RFC3339),
				ExpiresAt: expStr,
			},
		})
	}
	if res == nil {
		res = []recommendations.ActiveAssignmentDTO{}
	}
	return res, nil
}

func (r *memRecRepo) GetKimMatrix(_ context.Context, userID uuid.UUID) ([]recommendations.KimMatrixCellDTO, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	cells := make([]recommendations.KimMatrixCellDTO, 0, 27)
	for kim := 1; kim <= 27; kim++ {
		solved := 0
		attempted := 0
		for _, ut := range r.s.userTasks {
			// Match prefix for userID
			t, ok := r.s.tasks[ut.TaskID]
			if !ok {
				continue
			}
			key := fmt.Sprintf("%s:%d", userID.String(), ut.TaskID)
			if r.s.userTasks[key] != ut {
				continue
			}
			hasKim := false
			for _, kn := range t.KimNumbers {
				if kn == kim {
					hasKim = true
					break
				}
			}
			if hasKim && ut.TotalAttempts > 0 {
				attempted++
				if ut.IsCorrect {
					solved++
				}
			}
		}
		rate := 0.0
		if attempted > 0 {
			rate = (float64(solved) / float64(attempted)) * 100
		}
		cells = append(cells, recommendations.KimMatrixCellDTO{
			KimNumber:   kim,
			Solved:      solved,
			Attempted:   attempted,
			SuccessRate: rate,
		})
	}
	return cells, nil
}

func (r *memRecRepo) GetWeakTopics(_ context.Context, _ uuid.UUID, _ int) ([]recommendations.WeakTopicDTO, error) {
	return []recommendations.WeakTopicDTO{}, nil
}

func (r *memRecRepo) GetUserOverallSuccessRate(_ context.Context, userID uuid.UUID) (float64, int, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	solved, attempted := 0, 0
	prefix := userID.String() + ":"
	for k, ut := range r.s.userTasks {
		if strings.HasPrefix(k, prefix) && ut.TotalAttempts > 0 {
			attempted++
			if ut.IsCorrect {
				solved++
			}
		}
	}
	if attempted == 0 {
		return 0, 0, nil
	}
	return (float64(solved) / float64(attempted)) * 100.0, attempted, nil
}

func (r *memRecRepo) FindWeakTopicTask(_ context.Context, userID uuid.UUID) (*recommendations.TaskPreviewDTO, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	for _, t := range r.s.tasks {
		if t.DeletedAt == nil && t.IsPublicBank && !r.s.isTaskLockedLocked(t.ID, userID) {
			return &recommendations.TaskPreviewDTO{
				ID:         t.ID,
				KimNumbers: append([]int(nil), t.KimNumbers...),
				Difficulty: t.Difficulty,
				TextMD:     t.TextMD,
			}, nil
		}
	}
	return nil, nil
}

func (r *memRecRepo) FindAdaptiveDifficultyTask(_ context.Context, userID uuid.UUID, _ int, excludeTaskID int) (*recommendations.TaskPreviewDTO, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	for _, t := range r.s.tasks {
		if t.DeletedAt == nil && t.IsPublicBank && t.ID != excludeTaskID && !r.s.isTaskLockedLocked(t.ID, userID) {
			return &recommendations.TaskPreviewDTO{
				ID:         t.ID,
				KimNumbers: append([]int(nil), t.KimNumbers...),
				Difficulty: t.Difficulty,
				TextMD:     t.TextMD,
			}, nil
		}
	}
	return nil, nil
}

func (r *memRecRepo) LogRecommendation(_ context.Context, _ uuid.UUID, _ int, _ string) (int64, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	r.s.nextRecLogID++
	return r.s.nextRecLogID, nil
}

// =============================================================================
// Adapter: rooms.Repository
// =============================================================================

type memRoomsRepo struct {
	s *memoryStore
}

func (r *memRoomsRepo) CreateRoom(_ context.Context, room *rooms.Room) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	cp := *room
	r.s.rooms[room.ID] = &cp
	if r.s.roomMembers[room.ID] == nil {
		r.s.roomMembers[room.ID] = make(map[uuid.UUID]*memRoomMemberRecord)
	}
	return nil
}

func (r *memRoomsRepo) GetRoomByID(_ context.Context, id uuid.UUID) (*rooms.Room, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	rm, ok := r.s.rooms[id]
	if !ok || rm.DeletedAt != nil {
		return nil, rooms.ErrRoomNotFound
	}
	cp := *rm
	return &cp, nil
}

func (r *memRoomsRepo) GetRoomByInviteCode(_ context.Context, code string) (*rooms.Room, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	for _, rm := range r.s.rooms {
		if rm.DeletedAt == nil && strings.EqualFold(rm.InviteCode, strings.TrimSpace(code)) {
			cp := *rm
			return &cp, nil
		}
	}
	return nil, rooms.ErrRoomNotFound
}

func (r *memRoomsRepo) ListTeacherRooms(_ context.Context, teacherID uuid.UUID) ([]rooms.RoomListItem, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	items := make([]rooms.RoomListItem, 0)
	for _, rm := range r.s.rooms {
		if rm.DeletedAt != nil || rm.CreatorID != teacherID {
			continue
		}
		creatorName := ""
		if u, ok := r.s.users[rm.CreatorID]; ok {
			creatorName = strings.TrimSpace(u.FirstName + " " + u.LastName)
		}
		items = append(items, rooms.RoomListItem{
			ID:           rm.ID,
			Name:         rm.Name,
			CreatorID:    rm.CreatorID,
			CreatorName:  creatorName,
			InviteCode:   rm.InviteCode,
			MaxMembers:   rm.MaxMembers,
			MembersCount: len(r.s.roomMembers[rm.ID]),
			CreatedAt:    rm.CreatedAt,
		})
	}
	return items, nil
}

func (r *memRoomsRepo) ListStudentRooms(_ context.Context, studentID uuid.UUID) ([]rooms.RoomListItem, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	items := make([]rooms.RoomListItem, 0)
	for roomID, members := range r.s.roomMembers {
		if _, ok := members[studentID]; !ok {
			continue
		}
		rm, ok := r.s.rooms[roomID]
		if !ok || rm.DeletedAt != nil {
			continue
		}
		creatorName := ""
		if u, ok := r.s.users[rm.CreatorID]; ok {
			creatorName = strings.TrimSpace(u.FirstName + " " + u.LastName)
		}
		items = append(items, rooms.RoomListItem{
			ID:           rm.ID,
			Name:         rm.Name,
			CreatorID:    rm.CreatorID,
			CreatorName:  creatorName,
			InviteCode:   rm.InviteCode,
			MaxMembers:   rm.MaxMembers,
			MembersCount: len(members),
			CreatedAt:    rm.CreatedAt,
		})
	}
	return items, nil
}

func (r *memRoomsRepo) UpdateRoom(_ context.Context, room *rooms.Room) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	existing, ok := r.s.rooms[room.ID]
	if !ok || existing.DeletedAt != nil {
		return rooms.ErrRoomNotFound
	}
	existing.Name = room.Name
	existing.MaxMembers = room.MaxMembers
	existing.InviteCode = room.InviteCode
	existing.UpdatedAt = time.Now().UTC()
	return nil
}

func (r *memRoomsRepo) SoftDeleteRoom(_ context.Context, id uuid.UUID) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	existing, ok := r.s.rooms[id]
	if !ok || existing.DeletedAt != nil {
		return rooms.ErrRoomNotFound
	}
	now := time.Now().UTC()
	existing.DeletedAt = &now
	return nil
}

func (r *memRoomsRepo) GetRoomMembers(_ context.Context, roomID uuid.UUID) ([]rooms.RoomMember, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	membersMap := r.s.roomMembers[roomID]
	res := make([]rooms.RoomMember, 0, len(membersMap))
	for uid, rec := range membersMap {
		u := r.s.users[uid]
		if u == nil {
			continue
		}
		res = append(res, rooms.RoomMember{
			UserID:    uid,
			Username:  u.Username,
			FirstName: u.FirstName,
			LastName:  u.LastName,
			JoinedAt:  rec.JoinedAt,
		})
	}
	return res, nil
}

func (r *memRoomsRepo) GetRoomAssignments(_ context.Context, roomID uuid.UUID) ([]rooms.RoomAssignment, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	res := make([]rooms.RoomAssignment, 0)
	for _, a := range r.s.assignments {
		if a.DeletedAt == nil && a.RoomID != nil && *a.RoomID == roomID {
			res = append(res, rooms.RoomAssignment{
				ID:              a.ID,
				Title:           a.Title,
				Mode:            string(a.Mode),
				Settings:        a.Settings,
				ResultsReleased: a.ResultsReleased,
				CreatedAt:       a.CreatedAt,
			})
		}
	}
	return res, nil
}

func (r *memRoomsRepo) CountRoomMembers(_ context.Context, roomID uuid.UUID) (int, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	return len(r.s.roomMembers[roomID]), nil
}

func (r *memRoomsRepo) CountActiveTeacherRooms(_ context.Context, teacherID uuid.UUID) (int, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	cnt := 0
	for _, rm := range r.s.rooms {
		if rm.DeletedAt == nil && rm.CreatorID == teacherID {
			cnt++
		}
	}
	return cnt, nil
}

func (r *memRoomsRepo) IsRoomMember(_ context.Context, roomID, userID uuid.UUID) (bool, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	members := r.s.roomMembers[roomID]
	if members == nil {
		return false, nil
	}
	_, ok := members[userID]
	return ok, nil
}

func (r *memRoomsRepo) AddRoomMember(_ context.Context, roomID, userID uuid.UUID) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if r.s.roomMembers[roomID] == nil {
		r.s.roomMembers[roomID] = make(map[uuid.UUID]*memRoomMemberRecord)
	}
	if _, exists := r.s.roomMembers[roomID][userID]; exists {
		return rooms.ErrAlreadyMember
	}
	r.s.roomMembers[roomID][userID] = &memRoomMemberRecord{
		UserID:   userID,
		JoinedAt: time.Now().UTC(),
	}
	return nil
}

func (r *memRoomsRepo) RemoveRoomMember(_ context.Context, roomID, userID uuid.UUID) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	members := r.s.roomMembers[roomID]
	if members == nil {
		return rooms.ErrNotMember
	}
	if _, exists := members[userID]; !exists {
		return rooms.ErrNotMember
	}
	delete(members, userID)
	return nil
}

func (r *memRoomsRepo) UpdateFreeCodeS3Key(_ context.Context, roomID, userID uuid.UUID, s3Key string) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	members := r.s.roomMembers[roomID]
	if members == nil {
		return rooms.ErrNotMember
	}
	rec, exists := members[userID]
	if !exists {
		return rooms.ErrNotMember
	}
	cp := s3Key
	rec.FreeCodeS3Key = &cp
	return nil
}

func (r *memRoomsRepo) GetTeacherTierLimits(_ context.Context, teacherID uuid.UUID) (int, int, bool, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	sub := r.s.activeSubscriptionLocked(teacherID)
	if sub != nil && sub.Tier != nil {
		mr := sub.Tier.Features.GetMaxRooms()
		if mr <= 0 {
			mr = 15
		}
		ms := sub.Tier.Features.GetMaxMembers()
		if ms <= 0 {
			ms = 40
		}
		return mr, ms, true, nil
	}
	latest := r.s.latestSubscriptionLocked(teacherID)
	if latest != nil && latest.Tier != nil {
		mr := latest.Tier.Features.GetMaxRooms()
		ms := latest.Tier.Features.GetMaxMembers()
		return mr, ms, false, nil
	}
	return 1, 25, false, nil
}

func (r *memRoomsRepo) GetUserName(_ context.Context, userID uuid.UUID) (string, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	u, ok := r.s.users[userID]
	if !ok {
		return "", nil
	}
	full := strings.TrimSpace(u.FirstName + " " + u.LastName)
	if full == "" {
		return u.Username, nil
	}
	return full, nil
}

// =============================================================================
// Adapter: assignments.Repository
// =============================================================================

type memAssignRepo struct {
	s *memoryStore
}

func (r *memAssignRepo) CreateAssignment(_ context.Context, a *assignments.Assignment, taskIDs []int) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}
	now := time.Now().UTC()
	a.CreatedAt = now
	a.UpdatedAt = now
	a.TasksCount = len(taskIDs)
	cp := *a
	r.s.assignments[a.ID] = &cp
	r.s.assignmentTasks[a.ID] = append([]int(nil), taskIDs...)
	return nil
}

func (r *memAssignRepo) GetAssignmentByID(_ context.Context, id uuid.UUID) (*assignments.Assignment, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	a, ok := r.s.assignments[id]
	if !ok || a.DeletedAt != nil {
		return nil, assignments.ErrNotFound
	}
	cp := *a
	cp.TasksCount = len(r.s.assignmentTasks[id])
	return &cp, nil
}

func (r *memAssignRepo) UpdateAssignment(_ context.Context, id uuid.UUID, title *string, settings *assignments.AssignmentSettings, resultsReleased *bool) (*assignments.Assignment, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	a, ok := r.s.assignments[id]
	if !ok || a.DeletedAt != nil {
		return nil, assignments.ErrNotFound
	}
	if title != nil {
		a.Title = *title
	}
	if settings != nil {
		a.Settings = *settings
	}
	if resultsReleased != nil {
		a.ResultsReleased = *resultsReleased
	}
	a.UpdatedAt = time.Now().UTC()
	cp := *a
	cp.TasksCount = len(r.s.assignmentTasks[id])
	return &cp, nil
}

func (r *memAssignRepo) DeleteAssignment(_ context.Context, id uuid.UUID) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	a, ok := r.s.assignments[id]
	if !ok || a.DeletedAt != nil {
		return assignments.ErrNotFound
	}
	now := time.Now().UTC()
	a.DeletedAt = &now
	return nil
}

func (r *memAssignRepo) CreateSession(_ context.Context, sess *assignments.AssignmentSession) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if sess.ID == uuid.Nil {
		sess.ID = uuid.New()
	}
	if sess.StartedAt.IsZero() {
		sess.StartedAt = time.Now().UTC()
	}
	cp := *sess
	r.s.sessions[sess.ID] = &cp
	return nil
}

func (r *memAssignRepo) GetSessionByID(_ context.Context, id uuid.UUID) (*assignments.AssignmentSession, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	sess, ok := r.s.sessions[id]
	if !ok {
		return nil, assignments.ErrNotFound
	}
	cp := *sess
	return &cp, nil
}

func (r *memAssignRepo) GetSessionByAssignmentAndUser(_ context.Context, assignmentID, userID uuid.UUID) (*assignments.AssignmentSession, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	for _, sess := range r.s.sessions {
		if sess.AssignmentID == assignmentID && sess.UserID == userID {
			cp := *sess
			return &cp, nil
		}
	}
	return nil, assignments.ErrNotFound
}

func (r *memAssignRepo) UpdateSessionCounts(_ context.Context, sessionID uuid.UUID, blurDelta, pasteDelta int) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	sess, ok := r.s.sessions[sessionID]
	if !ok {
		return assignments.ErrNotFound
	}
	sess.BlurCount += blurDelta
	sess.PasteCount += pasteDelta
	return nil
}

func (r *memAssignRepo) FinishSession(_ context.Context, sessionID uuid.UUID, primaryScore, secondaryScore int16) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	sess, ok := r.s.sessions[sessionID]
	if !ok {
		return assignments.ErrNotFound
	}
	now := time.Now().UTC()
	sess.Status = assignments.SessionFinished
	sess.FinishedAt = &now
	sess.PrimaryScore = primaryScore
	sec := secondaryScore
	sess.SecondaryScore = &sec
	return nil
}

func (r *memAssignRepo) ExpireSession(_ context.Context, sessionID uuid.UUID, primaryScore, secondaryScore int16) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	sess, ok := r.s.sessions[sessionID]
	if !ok {
		return assignments.ErrNotFound
	}
	now := time.Now().UTC()
	sess.Status = assignments.SessionExpired
	sess.FinishedAt = &now
	sess.PrimaryScore = primaryScore
	sec := secondaryScore
	sess.SecondaryScore = &sec
	return nil
}

func (r *memAssignRepo) GetExamSecondaryScore(_ context.Context, primaryScore int16) (int16, error) {
	if primaryScore <= 0 {
		return 0, nil
	}
	if primaryScore >= 29 {
		return 100, nil
	}
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	if sec, ok := r.s.examScale[primaryScore]; ok {
		return sec, nil
	}
	return int16((int(primaryScore) * 100) / 29), nil
}

func (r *memAssignRepo) GetExamScale(_ context.Context) ([]assignments.ExamScaleEntry, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	entries := make([]assignments.ExamScaleEntry, 0, 30)
	for p := int16(0); p <= 29; p++ {
		entries = append(entries, assignments.ExamScaleEntry{
			PrimaryScore:   p,
			SecondaryScore: r.s.examScale[p],
		})
	}
	return entries, nil
}

func (r *memAssignRepo) UpdateExamScale(_ context.Context, entries []assignments.ExamScaleEntry) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for _, e := range entries {
		r.s.examScale[e.PrimaryScore] = e.SecondaryScore
	}
	return nil
}

func (r *memAssignRepo) GetTaskByID(_ context.Context, taskID int) (*tasks.Task, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	t, ok := r.s.tasks[taskID]
	if !ok || t.DeletedAt != nil {
		return nil, assignments.ErrNotFound
	}
	cp := *t
	return &cp, nil
}

func (r *memAssignRepo) GetAssignmentTasksWithDetails(_ context.Context, assignmentID uuid.UUID) ([]assignments.SessionTaskDetailRow, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	taskIDs := r.s.assignmentTasks[assignmentID]
	rows := make([]assignments.SessionTaskDetailRow, 0, len(taskIDs))
	for idx, tid := range taskIDs {
		t, ok := r.s.tasks[tid]
		if !ok || t.DeletedAt != nil {
			continue
		}
		var kims []int16
		for _, k := range t.KimNumbers {
			kims = append(kims, int16(k))
		}
		var topicKim *int
		var topicName *string
		if tp, ok := r.s.topics[t.TopicID]; ok {
			tk := tp.KimNumber
			tn := tp.Name
			topicKim = &tk
			topicName = &tn
		}
		filesJSON, _ := json.Marshal(t.Files)
		cfgJSON, _ := json.Marshal(t.AnswerConfig)
		corrJSON, _ := json.Marshal(t.CorrectAnswer)
		rows = append(rows, assignments.SessionTaskDetailRow{
			Position:      int16(idx + 1),
			TaskID:        t.ID,
			KimNumbers:    kims,
			TopicID:       t.TopicID,
			TopicKim:      topicKim,
			TopicName:     topicName,
			Difficulty:    int16(t.Difficulty),
			Source:        t.Source,
			TextMD:        t.TextMD,
			Images:        t.Images,
			Files:         filesJSON,
			AnswerConfig:  cfgJSON,
			CorrectAnswer: corrJSON,
			SolutionMD:    t.SolutionMD,
			SolutionCode:  t.SolutionCode,
		})
	}
	return rows, nil
}

func (r *memAssignRepo) GetUserAssignmentTask(_ context.Context, sessionID uuid.UUID, taskID int) (*assignments.UserAssignmentTask, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	byTask := r.s.userAssignTasks[sessionID]
	if byTask == nil {
		return nil, assignments.ErrNotFound
	}
	uat, ok := byTask[taskID]
	if !ok {
		return nil, assignments.ErrNotFound
	}
	cp := *uat
	return &cp, nil
}

func (r *memAssignRepo) UpsertUserAssignmentTask(_ context.Context, uat *assignments.UserAssignmentTask) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if r.s.userAssignTasks[uat.SessionID] == nil {
		r.s.userAssignTasks[uat.SessionID] = make(map[int]*assignments.UserAssignmentTask)
	}
	now := time.Now().UTC()
	cp := *uat
	if existing, ok := r.s.userAssignTasks[uat.SessionID][uat.TaskID]; ok {
		if cp.CodeS3Key == nil {
			cp.CodeS3Key = existing.CodeS3Key
		}
		if cp.StudentComment == nil {
			cp.StudentComment = existing.StudentComment
		}
		cp.CreatedAt = existing.CreatedAt
	} else {
		cp.CreatedAt = now
	}
	cp.UpdatedAt = now
	r.s.userAssignTasks[uat.SessionID][uat.TaskID] = &cp
	return nil
}

func (r *memAssignRepo) ListUserAssignmentTasks(_ context.Context, sessionID uuid.UUID) ([]assignments.UserAssignmentTask, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	byTask := r.s.userAssignTasks[sessionID]
	list := make([]assignments.UserAssignmentTask, 0, len(byTask))
	for _, uat := range byTask {
		list = append(list, *uat)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].TaskID < list[j].TaskID })
	return list, nil
}

func (r *memAssignRepo) SelectSelfExamTasks(_ context.Context, _ uuid.UUID) ([]int, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	var selected []int
	findSingle := func(kim int) (int, bool) {
		// Sort task IDs deterministically so KIM 1 picks 1043 (before 2001+) and KIM 24 picks 1045 over locked 1042 if needed.
		ids := make([]int, 0, len(r.s.tasks))
		for id := range r.s.tasks {
			ids = append(ids, id)
		}
		sort.Ints(ids)
		for _, id := range ids {
			t := r.s.tasks[id]
			if t.DeletedAt != nil || !t.IsPublicBank || len(t.KimNumbers) != 1 {
				continue
			}
			if t.KimNumbers[0] == kim {
				return t.ID, true
			}
		}
		return 0, false
	}

	for kim := 1; kim <= 18; kim++ {
		if tid, ok := findSingle(kim); ok {
			selected = append(selected, tid)
		}
	}
	// Triplet 19-21
	for _, t := range r.s.tasks {
		if t.DeletedAt == nil && t.IsPublicBank && len(t.KimNumbers) == 3 && t.KimNumbers[0] == 19 {
			selected = append(selected, t.ID)
			break
		}
	}
	for kim := 22; kim <= 27; kim++ {
		if tid, ok := findSingle(kim); ok {
			selected = append(selected, tid)
		}
	}
	return selected, nil
}

func (r *memAssignRepo) LogTaskEvent(_ context.Context, sessionID, _ uuid.UUID, _ uuid.UUID, taskID *int, event string, payload any) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	b, _ := json.Marshal(payload)
	r.s.nextEventID++
	r.s.sessionEvents[sessionID] = append(r.s.sessionEvents[sessionID], memEventRecord{
		ID:         r.s.nextEventID,
		SessionID:  sessionID,
		TaskID:     taskID,
		Event:      event,
		Payload:    b,
		OccurredAt: time.Now().UTC(),
	})
	return nil
}

func (r *memAssignRepo) CheckRoomMembership(_ context.Context, roomID, userID uuid.UUID) (bool, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	members := r.s.roomMembers[roomID]
	if members == nil {
		return false, nil
	}
	_, ok := members[userID]
	return ok, nil
}

func (r *memAssignRepo) CheckRoomCreator(_ context.Context, roomID, userID uuid.UUID) (bool, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	rm, ok := r.s.rooms[roomID]
	if !ok || rm.DeletedAt != nil {
		return false, nil
	}
	return rm.CreatorID == userID, nil
}

// =============================================================================
// Adapter: proctoring.Repository
// =============================================================================

type memProctoringRepo struct {
	s *memoryStore
}

func (r *memProctoringRepo) GetAssignmentHeader(_ context.Context, assignmentID uuid.UUID) (*proctoring.AssignmentHeader, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	a, ok := r.s.assignments[assignmentID]
	if !ok || a.DeletedAt != nil {
		return nil, proctoring.ErrNotFound
	}
	return &proctoring.AssignmentHeader{
		ID:        a.ID,
		RoomID:    a.RoomID,
		CreatorID: a.CreatorID,
		Title:     a.Title,
		Mode:      string(a.Mode),
	}, nil
}

func (r *memProctoringRepo) GetMatrixStudents(_ context.Context, assignmentID uuid.UUID, roomID *uuid.UUID) ([]proctoring.StudentMatrixRow, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()

	posByTask := make(map[int]int16)
	for idx, tid := range r.s.assignmentTasks[assignmentID] {
		posByTask[tid] = int16(idx + 1)
	}

	var userIDs []uuid.UUID
	seen := make(map[uuid.UUID]bool)
	if roomID != nil && *roomID != uuid.Nil {
		for uid := range r.s.roomMembers[*roomID] {
			userIDs = append(userIDs, uid)
			seen[uid] = true
		}
	}
	for _, sess := range r.s.sessions {
		if sess.AssignmentID == assignmentID && !seen[sess.UserID] {
			userIDs = append(userIDs, sess.UserID)
			seen[sess.UserID] = true
		}
	}

	var rows []proctoring.StudentMatrixRow
	for _, uid := range userIDs {
		u := r.s.users[uid]
		if u == nil || u.DeletedAt != nil {
			continue
		}
		var sessPtr *assignments.AssignmentSession
		for _, sess := range r.s.sessions {
			if sess.AssignmentID == assignmentID && sess.UserID == uid {
				sessPtr = sess
				break
			}
		}

		if sessPtr == nil {
			rows = append(rows, proctoring.StudentMatrixRow{
				UserID:    u.ID,
				Username:  u.Username,
				FirstName: u.FirstName,
				LastName:  u.LastName,
			})
			continue
		}

		statusStr := string(sessPtr.Status)
		uats := r.s.userAssignTasks[sessPtr.ID]
		if len(uats) == 0 {
			rows = append(rows, proctoring.StudentMatrixRow{
				UserID:         u.ID,
				Username:       u.Username,
				FirstName:      u.FirstName,
				LastName:       u.LastName,
				SessionID:      &sessPtr.ID,
				Status:         &statusStr,
				BlurCount:      &sessPtr.BlurCount,
				PasteCount:     &sessPtr.PasteCount,
				PrimaryScore:   &sessPtr.PrimaryScore,
				SecondaryScore: sessPtr.SecondaryScore,
			})
			continue
		}

		for _, uat := range uats {
			pos := posByTask[uat.TaskID]
			tid := uat.TaskID
			isCorr := uat.IsCorrect
			earned := uat.EarnedScore
			att := uat.AttemptsUsed
			rows = append(rows, proctoring.StudentMatrixRow{
				UserID:         u.ID,
				Username:       u.Username,
				FirstName:      u.FirstName,
				LastName:       u.LastName,
				SessionID:      &sessPtr.ID,
				Status:         &statusStr,
				BlurCount:      &sessPtr.BlurCount,
				PasteCount:     &sessPtr.PasteCount,
				PrimaryScore:   &sessPtr.PrimaryScore,
				SecondaryScore: sessPtr.SecondaryScore,
				TaskID:         &tid,
				Position:       &pos,
				IsCorrect:      &isCorr,
				EarnedScore:    &earned,
				AttemptsUsed:   &att,
			})
		}
	}
	return rows, nil
}

func (r *memProctoringRepo) GetMatrixTaskStats(_ context.Context, assignmentID uuid.UUID) ([]proctoring.MatrixTaskStatDTO, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	taskIDs := r.s.assignmentTasks[assignmentID]
	stats := make([]proctoring.MatrixTaskStatDTO, 0, len(taskIDs))
	for idx, tid := range taskIDs {
		stats = append(stats, proctoring.MatrixTaskStatDTO{
			TaskID:   tid,
			Position: idx + 1,
		})
	}
	return stats, nil
}

func (r *memProctoringRepo) GetInspectionSession(_ context.Context, sessionID uuid.UUID) (*proctoring.InspectionSessionRow, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	sess, ok := r.s.sessions[sessionID]
	if !ok {
		return nil, proctoring.ErrNotFound
	}
	a, ok := r.s.assignments[sess.AssignmentID]
	if !ok {
		return nil, proctoring.ErrNotFound
	}
	userName := ""
	if u, ok := r.s.users[sess.UserID]; ok {
		userName = strings.TrimSpace(u.FirstName + " " + u.LastName)
	}
	return &proctoring.InspectionSessionRow{
		SessionID:      sess.ID,
		AssignmentID:   sess.AssignmentID,
		UserID:         sess.UserID,
		UserName:       userName,
		Status:         string(sess.Status),
		StartedAt:      sess.StartedAt,
		FinishedAt:     sess.FinishedAt,
		PrimaryScore:   sess.PrimaryScore,
		SecondaryScore: sess.SecondaryScore,
		BlurCount:      sess.BlurCount,
		PasteCount:     sess.PasteCount,
		CreatorID:      a.CreatorID,
	}, nil
}

func (r *memProctoringRepo) GetTimelineEvents(_ context.Context, sessionID uuid.UUID) ([]proctoring.TimelineEventDTO, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	recs := r.s.sessionEvents[sessionID]
	res := make([]proctoring.TimelineEventDTO, 0, len(recs))
	for _, rec := range recs {
		res = append(res, proctoring.TimelineEventDTO{
			ID:         rec.ID,
			TaskID:     rec.TaskID,
			Event:      rec.Event,
			Payload:    rec.Payload,
			OccurredAt: rec.OccurredAt.UTC().Format(time.RFC3339),
		})
	}
	return res, nil
}

func (r *memProctoringRepo) GetSessionTaskCodes(_ context.Context, sessionID uuid.UUID) ([]proctoring.TaskCodeRow, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	uats := r.s.userAssignTasks[sessionID]
	res := make([]proctoring.TaskCodeRow, 0, len(uats))
	for _, uat := range uats {
		res = append(res, proctoring.TaskCodeRow{
			TaskID:         uat.TaskID,
			CodeS3Key:      uat.CodeS3Key,
			PasteRanges:    uat.PasteRanges,
			StudentComment: uat.StudentComment,
			UpdatedAt:      uat.UpdatedAt,
		})
	}
	return res, nil
}

func (r *memProctoringRepo) CreateTeacherTask(_ context.Context, teacherID uuid.UUID, req proctoring.CreateTeacherTaskRequest) (*tasks.TaskDTO, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	r.s.nextTaskID++
	id := r.s.nextTaskID
	now := time.Now().UTC()
	src := req.Source
	t := &tasks.Task{
		ID:            id,
		OwnerID:       &teacherID,
		IsPublicBank:  false,
		KimNumbers:    req.KimNumbers,
		TopicID:       req.TopicID,
		Difficulty:    req.Difficulty,
		Source:        &src,
		TextMD:        req.TextMD,
		Images:        req.Images,
		Files:         req.Files,
		AnswerConfig:  req.AnswerConfig,
		CorrectAnswer: req.CorrectAnswer,
		SolutionMD:    req.SolutionMD,
		SolutionCode:  req.SolutionCode,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	r.s.tasks[id] = t
	dto := r.s.buildTaskDTOLocked(t, &teacherID, true)
	return &dto, nil
}

func (r *memProctoringRepo) ListTeacherTasks(ctx context.Context, teacherID uuid.UUID, limit, offset int, kim *int, topicID *int, q string) ([]tasks.TaskDTO, int, error) {
	tr := &memTasksRepo{s: r.s}
	return tr.ListAuthorTasks(ctx, teacherID, tasks.ListTasksFilter{
		Kim:     kim,
		TopicID: topicID,
		Query:   q,
		Limit:   limit,
		Offset:  offset,
	})
}

// =============================================================================
// HTTP Request Execution Helper for Test Assertions
// =============================================================================

func DoRequest(t *testing.T, method, url string, body any, cookies []*http.Cookie, headers map[string]string) (*http.Response, []byte) {
	t.Helper()

	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("DoRequest: marshal error: %v", err)
		}
		bodyReader = strings.NewReader(string(b))
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		t.Fatalf("DoRequest: NewRequest error: %v", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	for _, c := range cookies {
		req.AddCookie(c)
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("DoRequest: Do error: %v", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("DoRequest: ReadAll error: %v", err)
	}

	return resp, respBody
}
