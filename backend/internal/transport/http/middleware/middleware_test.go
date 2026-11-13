package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"backend/internal/transport/http/response"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func createTestToken(secret string, userID uuid.UUID, role string, exp time.Duration) string {
	claims := UserClaims{
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(exp)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	str, _ := token.SignedString([]byte(secret))
	return str
}

func TestAuthCookie(t *testing.T) {
	jwtSecret := "test-secret-key-for-jwt-signing"
	userUUID := uuid.New()

	t.Run("valid access cookie", func(t *testing.T) {
		tokenStr := createTestToken(jwtSecret, userUUID, "teacher", time.Hour)

		handler := AuthCookie(jwtSecret)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u := MustGetUserContext(r.Context())
			if !u.IsAuthenticated {
				t.Fatalf("expected authenticated user")
			}
			if u.UserID != userUUID {
				t.Fatalf("expected user ID %s, got %s", userUUID, u.UserID)
			}
			if u.Role != "teacher" {
				t.Fatalf("expected role teacher, got %s", u.Role)
			}
			if u.IsSuperuser {
				t.Fatalf("expected IsSuperuser to be false")
			}
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
		req.AddCookie(&http.Cookie{Name: "ege_access", Value: tokenStr})
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
	})

	t.Run("superuser role sets IsSuperuser true", func(t *testing.T) {
		tokenStr := createTestToken(jwtSecret, userUUID, "superuser", time.Hour)

		handler := AuthCookie(jwtSecret)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u := MustGetUserContext(r.Context())
			if !u.IsAuthenticated || !u.IsSuperuser {
				t.Fatalf("expected superuser context")
			}
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
		req.AddCookie(&http.Cookie{Name: "ege_access", Value: tokenStr})
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
	})

	t.Run("missing cookie leaves context unauthenticated", func(t *testing.T) {
		handler := AuthCookie(jwtSecret)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u := MustGetUserContext(r.Context())
			if u.IsAuthenticated {
				t.Fatalf("expected unauthenticated context")
			}
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/api/v1/bank/tasks", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
	})

	t.Run("expired cookie sets TokenExpired true", func(t *testing.T) {
		expiredTokenStr := createTestToken(jwtSecret, userUUID, "student", -time.Minute)

		handler := AuthCookie(jwtSecret)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u := MustGetUserContext(r.Context())
			if u.IsAuthenticated {
				t.Fatalf("expected unauthenticated")
			}
			if !u.TokenExpired {
				t.Fatalf("expected TokenExpired to be true")
			}
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
		req.AddCookie(&http.Cookie{Name: "ege_access", Value: expiredTokenStr})
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
	})
}

func TestRequireAuth(t *testing.T) {
	t.Run("authenticated passes through", func(t *testing.T) {
		handler := RequireAuth()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		ctx := ContextWithUser(req.Context(), UserContext{
			UserID:          uuid.New(),
			IsAuthenticated: true,
		})
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req.WithContext(ctx))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
	})

	t.Run("unauthenticated returns 401 UNAUTHORIZED", func(t *testing.T) {
		handler := RequireAuth()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected status 401, got %d", rec.Code)
		}

		var env response.ErrorEnvelope
		_ = json.Unmarshal(rec.Body.Bytes(), &env)
		if env.Error.Code != "UNAUTHORIZED" {
			t.Fatalf("expected UNAUTHORIZED, got %s", env.Error.Code)
		}
	})

	t.Run("expired token returns 401 TOKEN_EXPIRED", func(t *testing.T) {
		handler := RequireAuth()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		ctx := ContextWithUser(req.Context(), UserContext{
			IsAuthenticated: false,
			TokenExpired:    true,
		})
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req.WithContext(ctx))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected status 401, got %d", rec.Code)
		}

		var env response.ErrorEnvelope
		_ = json.Unmarshal(rec.Body.Bytes(), &env)
		if env.Error.Code != "TOKEN_EXPIRED" {
			t.Fatalf("expected TOKEN_EXPIRED, got %s", env.Error.Code)
		}
	})
}

func TestImpersonation(t *testing.T) {
	superuserID := uuid.New()
	targetUserID := uuid.New()

	t.Run("no impersonation header passes through", func(t *testing.T) {
		handler := Impersonation()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u := MustGetUserContext(r.Context())
			if u.UserID != superuserID {
				t.Fatalf("expected superuserID untouched")
			}
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		ctx := ContextWithUser(req.Context(), UserContext{
			UserID:          superuserID,
			Role:            "superuser",
			IsSuperuser:     true,
			IsAuthenticated: true,
		})
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req.WithContext(ctx))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
	})

	t.Run("non-superuser receives 403 FORBIDDEN", func(t *testing.T) {
		handler := Impersonation()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("X-Impersonate-User", targetUserID.String())
		ctx := ContextWithUser(req.Context(), UserContext{
			UserID:          uuid.New(),
			Role:            "teacher",
			IsSuperuser:     false,
			IsAuthenticated: true,
		})
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req.WithContext(ctx))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected status 403, got %d", rec.Code)
		}

		var env response.ErrorEnvelope
		_ = json.Unmarshal(rec.Body.Bytes(), &env)
		if env.Error.Code != "FORBIDDEN" {
			t.Fatalf("expected FORBIDDEN code, got %s", env.Error.Code)
		}
	})

	t.Run("superuser with invalid UUID receives 400 VALIDATION_ERROR", func(t *testing.T) {
		handler := Impersonation()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("X-Impersonate-User", "not-a-uuid")
		ctx := ContextWithUser(req.Context(), UserContext{
			UserID:          superuserID,
			Role:            "superuser",
			IsSuperuser:     true,
			IsAuthenticated: true,
		})
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req.WithContext(ctx))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d", rec.Code)
		}
	})

	t.Run("superuser successfully impersonates target user", func(t *testing.T) {
		resolver := func(ctx context.Context, id uuid.UUID) (string, error) {
			if id == targetUserID {
				return "student", nil
			}
			return "", errors.New("not found")
		}

		handler := Impersonation(WithRoleResolver(resolver))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u := MustGetUserContext(r.Context())
			if u.UserID != targetUserID {
				t.Fatalf("expected UserID %s, got %s", targetUserID, u.UserID)
			}
			if u.ImpersonatedBy == nil || *u.ImpersonatedBy != superuserID {
				t.Fatalf("expected ImpersonatedBy %s", superuserID)
			}
			if u.Role != "student" {
				t.Fatalf("expected effective role student, got %s", u.Role)
			}
			if u.IsSuperuser {
				t.Fatalf("expected effective IsSuperuser to be false")
			}
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("X-Impersonate-User", targetUserID.String())
		ctx := ContextWithUser(req.Context(), UserContext{
			UserID:          superuserID,
			Role:            "superuser",
			IsSuperuser:     true,
			IsAuthenticated: true,
		})
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req.WithContext(ctx))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
	})
}

func TestRequireRole(t *testing.T) {
	t.Run("unauthenticated gets 401", func(t *testing.T) {
		handler := RequireRole("teacher")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected status 401, got %d", rec.Code)
		}
	})

	t.Run("matching role gets 200", func(t *testing.T) {
		handler := RequireRole("teacher", "admin")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		ctx := ContextWithUser(req.Context(), UserContext{
			UserID:          uuid.New(),
			Role:            "teacher",
			IsAuthenticated: true,
		})
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req.WithContext(ctx))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
	})

	t.Run("mismatching role gets 403 FORBIDDEN", func(t *testing.T) {
		handler := RequireRole("teacher")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		ctx := ContextWithUser(req.Context(), UserContext{
			UserID:          uuid.New(),
			Role:            "student",
			IsAuthenticated: true,
		})
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req.WithContext(ctx))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected status 403, got %d", rec.Code)
		}
	})

	t.Run("superuser role bypasses role restriction", func(t *testing.T) {
		handler := RequireRole("teacher")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		ctx := ContextWithUser(req.Context(), UserContext{
			UserID:          uuid.New(),
			Role:            "superuser",
			IsSuperuser:     true,
			IsAuthenticated: true,
		})
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req.WithContext(ctx))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
	})
}

func TestSubscriptionGuard(t *testing.T) {
	teacherID := uuid.New()

	checker := SubscriptionCheckerFunc(func(ctx context.Context, id uuid.UUID) (bool, error) {
		if id == teacherID {
			return false, nil // Expired subscription
		}
		return true, nil
	})

	guard := SubscriptionGuard(checker)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	t.Run("GET request passes through even with expired subscription", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/rooms", nil)
		ctx := ContextWithUser(req.Context(), UserContext{
			UserID:          teacherID,
			Role:            "teacher",
			IsAuthenticated: true,
		})
		rec := httptest.NewRecorder()

		guard.ServeHTTP(rec, req.WithContext(ctx))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200 for GET, got %d", rec.Code)
		}
	})

	t.Run("POST request blocked with 403 SUBSCRIPTION_READ_ONLY", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/rooms", nil)
		ctx := ContextWithUser(req.Context(), UserContext{
			UserID:          teacherID,
			Role:            "teacher",
			IsAuthenticated: true,
		})
		rec := httptest.NewRecorder()

		guard.ServeHTTP(rec, req.WithContext(ctx))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected status 403 for POST, got %d", rec.Code)
		}

		var env response.ErrorEnvelope
		_ = json.Unmarshal(rec.Body.Bytes(), &env)
		if env.Error.Code != "SUBSCRIPTION_READ_ONLY" {
			t.Fatalf("expected SUBSCRIPTION_READ_ONLY, got %s", env.Error.Code)
		}
	})

	t.Run("Superuser exempt from subscription guard on mutative request", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/rooms", nil)
		ctx := ContextWithUser(req.Context(), UserContext{
			UserID:          teacherID,
			Role:            "superuser",
			IsSuperuser:     true,
			IsAuthenticated: true,
		})
		rec := httptest.NewRecorder()

		guard.ServeHTTP(rec, req.WithContext(ctx))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200 for superuser, got %d", rec.Code)
		}
	})
}

func TestRateLimit(t *testing.T) {
	limit := 3
	window := time.Second

	limiter := RateLimit(nil, limit, window, WithKeyFunc(func(r *http.Request) string {
		return "test-client"
	}))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First 3 requests should pass
	for i := 1; i <= 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		rec := httptest.NewRecorder()
		limiter.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("request %d expected status 200, got %d", i, rec.Code)
		}
	}

	// 4th request should be rate-limited
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	limiter.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected status 429, got %d", rec.Code)
	}

	var env response.ErrorEnvelope
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Error.Code != "RATE_LIMITED" {
		t.Fatalf("expected RATE_LIMITED, got %s", env.Error.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatalf("expected Retry-After header")
	}
}

func TestLoggerAndRecoverer(t *testing.T) {
	var buf bytes.Buffer
	testLogger := slog.New(slog.NewTextHandler(&buf, nil))

	t.Run("Logger logs request", func(t *testing.T) {
		handler := Logger(testLogger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		}))

		req := httptest.NewRequest(http.MethodGet, "/test-log", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
		if !bytes.Contains(buf.Bytes(), []byte("/test-log")) {
			t.Fatalf("expected log buffer to contain /test-log")
		}
	})

	t.Run("Recoverer recovers panic and returns 500 error envelope", func(t *testing.T) {
		handler := Recoverer(testLogger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			panic("unexpected boom!")
		}))

		req := httptest.NewRequest(http.MethodGet, "/panic", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected status 500, got %d", rec.Code)
		}

		var env response.ErrorEnvelope
		_ = json.Unmarshal(rec.Body.Bytes(), &env)
		if env.Error.Code != "INTERNAL_SERVER_ERROR" {
			t.Fatalf("expected INTERNAL_SERVER_ERROR, got %s", env.Error.Code)
		}
	})
}
