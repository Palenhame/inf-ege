package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"backend/internal/transport/http/middleware"
	"backend/internal/transport/http/response"
	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func TestNewRouter_Health(t *testing.T) {
	router := NewRouter(RouterConfig{}, nil)

	t.Run("/health returns ok", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}

		var res map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		if res["status"] != "ok" {
			t.Fatalf("expected status: ok, got %s", res["status"])
		}
	})

	t.Run("/api/v1/health returns ok", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}

		var res map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		if res["status"] != "ok" {
			t.Fatalf("expected status: ok, got %s", res["status"])
		}
	})
}

func TestNewRouter_CORS(t *testing.T) {
	allowedOrigin := "https://egeinf.ru"
	router := NewRouter(RouterConfig{
		AllowedOrigins: []string{allowedOrigin},
	}, nil)

	t.Run("OPTIONS preflight returns 204 with credentials and methods", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/api/v1/health", nil)
		req.Header.Set("Origin", allowedOrigin)
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected 204, got %d", rec.Code)
		}
		if rec.Header().Get("Access-Control-Allow-Origin") != allowedOrigin {
			t.Fatalf("expected allowed origin %s, got %s", allowedOrigin, rec.Header().Get("Access-Control-Allow-Origin"))
		}
		if rec.Header().Get("Access-Control-Allow-Credentials") != "true" {
			t.Fatalf("expected credentials true")
		}
	})

	t.Run("Disallowed origin does not get allow-origin header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		req.Header.Set("Origin", "https://malicious-site.com")
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		if rec.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("expected empty allow origin for disallowed origin")
		}
	})
}

func TestNewRouter_Storage(t *testing.T) {
	var requestedPath string
	storageMock := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("image-data"))
	})

	router := NewRouter(RouterConfig{
		PublicStorage: storageMock,
	}, nil)

	req := httptest.NewRequest(http.MethodGet, "/storage/tasks/images/diagram.png", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if requestedPath != "tasks/images/diagram.png" {
		t.Fatalf("expected stripped path tasks/images/diagram.png, got %s", requestedPath)
	}
}

func TestNewRouter_DomainMounts(t *testing.T) {
	reg := &HandlerRegistry{
		AuthHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			response.JSON(w, http.StatusOK, map[string]string{"module": "auth"})
		}),
		BankHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			response.JSON(w, http.StatusOK, map[string]string{"module": "bank"})
		}),
		RoomHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			response.JSON(w, http.StatusOK, map[string]string{"module": "rooms"})
		}),
	}

	router := NewRouter(RouterConfig{}, reg)

	tests := []struct {
		url        string
		wantModule string
	}{
		{"/api/v1/auth/login", "auth"},
		{"/api/v1/bank/tasks", "bank"},
		{"/api/v1/rooms", "rooms"},
	}

	for _, tc := range tests {
		t.Run(tc.url, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.url, nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", rec.Code)
			}

			var body map[string]string
			_ = json.Unmarshal(rec.Body.Bytes(), &body)
			if body["module"] != tc.wantModule {
				t.Fatalf("expected module %s, got %s", tc.wantModule, body["module"])
			}
		})
	}
}

func TestNewMinIOStorageReverseProxy(t *testing.T) {
	proxy, err := NewMinIOStorageReverseProxy("http://minio:9000", "egeinf-media")
	if err != nil {
		t.Fatalf("unexpected error creating proxy: %v", err)
	}
	if proxy == nil {
		t.Fatalf("expected non-nil proxy")
	}
}

func makeTestCookie(t *testing.T, secret string, userID uuid.UUID, role string) *http.Cookie {
	t.Helper()
	claims := middleware.UserClaims{
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign test token: %v", err)
	}
	return &http.Cookie{Name: "ege_access", Value: signed}
}

func TestNewRouter_TopicsAndAliases(t *testing.T) {
	topicMux := chi.NewRouter()
	topicMux.Get("/", func(w http.ResponseWriter, r *http.Request) {
		response.JSON(w, http.StatusOK, map[string]string{"module": "topics"})
	})

	examMux := chi.NewRouter()
	examMux.Post("/self-start", func(w http.ResponseWriter, r *http.Request) {
		response.JSON(w, http.StatusCreated, map[string]string{"module": "exams"})
	})

	sessionMux := chi.NewRouter()
	sessionMux.Get("/{id}", func(w http.ResponseWriter, r *http.Request) {
		response.JSON(w, http.StatusOK, map[string]string{"session_id": chi.URLParam(r, "id")})
	})

	userAliasMux := chi.NewRouter()
	userAliasMux.Post("/onboarding", func(w http.ResponseWriter, r *http.Request) {
		response.JSON(w, http.StatusOK, map[string]string{"module": "user_alias"})
	})

	router := NewRouter(RouterConfig{}, &HandlerRegistry{
		TopicHandler:     topicMux,
		ExamHandler:      examMux,
		SessionHandler:   sessionMux,
		UserAliasHandler: userAliasMux,
	})

	t.Run("/api/v1/topics without trailing slash", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/topics", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("/api/v1/topics/ with trailing slash", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/topics/", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("/api/v1/exams/self-start", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/exams/self-start", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("/api/v1/sessions/{id}", func(t *testing.T) {
		sid := uuid.New().String()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sid, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		var body map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if body["session_id"] != sid {
			t.Fatalf("expected session_id %s, got %s", sid, body["session_id"])
		}
	})

	t.Run("/api/v1/user/onboarding", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/user/onboarding", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
	})
}

func TestNewRouter_SubscriptionGuard(t *testing.T) {
	const jwtSecret = "router-test-jwt-secret"

	expiredTeacherID := uuid.New()
	activeTeacherID := uuid.New()
	superuserID := uuid.New()

	checker := middleware.SubscriptionCheckerFunc(func(ctx context.Context, userID uuid.UUID) (bool, error) {
		return userID == activeTeacherID, nil
	})

	okHandler := func(w http.ResponseWriter, r *http.Request) {
		response.JSON(w, http.StatusOK, map[string]bool{"ok": true})
	}

	roomMux := chi.NewRouter()
	roomMux.Get("/", okHandler)
	roomMux.Post("/", okHandler)
	roomMux.Get("/{id}", okHandler)
	roomMux.Patch("/{id}", okHandler)
	roomMux.Delete("/{id}", okHandler)
	roomMux.Post("/join/{code}", okHandler)
	roomMux.Put("/{id}/free-code", okHandler)
	roomMux.Delete("/{id}/members/{user_id}", okHandler)
	roomMux.Post("/{room_id}/assignments", okHandler)

	assignMux := chi.NewRouter()
	assignMux.Patch("/{id}", okHandler)
	assignMux.Delete("/{id}", okHandler)
	assignMux.Post("/{id}/start", okHandler)
	assignMux.Get("/{id}/matrix", okHandler)

	teacherMux := chi.NewRouter()
	teacherMux.Post("/tasks", okHandler)
	teacherMux.Get("/tasks", okHandler)
	teacherMux.Get("/assignments/{id}/matrix", okHandler)
	teacherMux.Get("/sessions/{id}/inspect", okHandler)

	router := NewRouter(RouterConfig{
		JWTSecret:           jwtSecret,
		SubscriptionChecker: checker,
	}, &HandlerRegistry{
		RoomHandler:       roomMux,
		AssignmentHandler: assignMux,
		TeacherHandler:    teacherMux,
	})

	expiredCookie := makeTestCookie(t, jwtSecret, expiredTeacherID, "teacher")
	activeCookie := makeTestCookie(t, jwtSecret, activeTeacherID, "teacher")
	superCookie := makeTestCookie(t, jwtSecret, superuserID, "superuser")

	id1 := uuid.New().String()
	id2 := uuid.New().String()

	blockedForExpired := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/rooms"},
		{http.MethodPatch, "/api/v1/rooms/" + id1},
		{http.MethodPost, "/api/v1/rooms/" + id1 + "/assignments"},
		{http.MethodPatch, "/api/v1/assignments/" + id1},
		{http.MethodPost, "/api/v1/teacher/tasks"},
	}

	for _, tc := range blockedForExpired {
		t.Run("expired teacher blocked: "+tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.AddCookie(expiredCookie)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
			}
			var errEnv response.ErrorEnvelope
			if err := json.Unmarshal(rec.Body.Bytes(), &errEnv); err != nil {
				t.Fatalf("unmarshal error: %v", err)
			}
			if errEnv.Error.Code != "SUBSCRIPTION_READ_ONLY" {
				t.Fatalf("expected SUBSCRIPTION_READ_ONLY, got %s", errEnv.Error.Code)
			}
		})

		t.Run("active teacher allowed: "+tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.AddCookie(activeCookie)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
			}
		})

		t.Run("superuser allowed: "+tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.AddCookie(superCookie)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}

	allowedForExpired := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/rooms"},
		{http.MethodGet, "/api/v1/rooms/" + id1},
		{http.MethodDelete, "/api/v1/rooms/" + id1},
		{http.MethodPost, "/api/v1/rooms/join/ABC12345"},
		{http.MethodPut, "/api/v1/rooms/" + id1 + "/free-code"},
		{http.MethodDelete, "/api/v1/rooms/" + id1 + "/members/" + id2},
		{http.MethodDelete, "/api/v1/assignments/" + id1},
		{http.MethodPost, "/api/v1/assignments/" + id1 + "/start"},
		{http.MethodGet, "/api/v1/assignments/" + id1 + "/matrix"},
		{http.MethodGet, "/api/v1/teacher/tasks"},
		{http.MethodGet, "/api/v1/teacher/assignments/" + id1 + "/matrix"},
		{http.MethodGet, "/api/v1/teacher/sessions/" + id1 + "/inspect"},
	}

	for _, tc := range allowedForExpired {
		t.Run("expired teacher allowed: "+tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.AddCookie(expiredCookie)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}
