package ws

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"backend/internal/domain/auth"
	"backend/internal/transport/http/middleware"

	"github.com/coder/websocket"
	"github.com/google/uuid"
)

func TestHandler_Unauthenticated_Rejected(t *testing.T) {
	hub := NewHub(nil, nil, nil)
	handler := NewHandler(HandlerConfig{
		Hub:       hub,
		JWTSecret: "test_secret_must_be_long_enough_32bytes",
	})

	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "UNAUTHORIZED") {
		t.Errorf("expected UNAUTHORIZED code in response body, got %s", rec.Body.String())
	}
}

func TestHandler_ExpiredToken_Rejected(t *testing.T) {
	jwtSecret := "test_secret_must_be_long_enough_32bytes"
	jwtMgr := auth.NewJWTManager(jwtSecret, -1*time.Minute) // expired

	expiredToken, err := jwtMgr.GenerateAccessToken(uuid.New().String(), "student")
	if err != nil {
		t.Fatal(err)
	}

	hub := NewHub(nil, nil, nil)
	handler := NewHandler(HandlerConfig{
		Hub:       hub,
		JWTSecret: jwtSecret,
	})

	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.AddCookie(&http.Cookie{
		Name:  "ege_access",
		Value: expiredToken,
	})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "TOKEN_EXPIRED") {
		t.Errorf("expected TOKEN_EXPIRED code in response body, got %s", rec.Body.String())
	}
}

func TestHandler_SuccessfulUpgrade_ViaCookie(t *testing.T) {
	jwtSecret := "test_secret_must_be_long_enough_32bytes"
	jwtMgr := auth.NewJWTManager(jwtSecret, 15*time.Minute)

	userID := uuid.New()
	token, err := jwtMgr.GenerateAccessToken(userID.String(), "student")
	if err != nil {
		t.Fatal(err)
	}

	hub := NewHub(nil, nil, nil)
	handler := NewHandler(HandlerConfig{
		Hub:       hub,
		JWTSecret: jwtSecret,
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	header := http.Header{}
	header.Set("Cookie", "ege_access="+token)

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: header,
	})
	if err != nil {
		t.Fatalf("failed to dial WebSocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "test done")

	time.Sleep(30 * time.Millisecond)

	clientsCount, _, _, _ := hub.Stats()
	if clientsCount != 1 {
		t.Fatalf("expected 1 registered client in Hub, got %d", clientsCount)
	}
}

func TestHandler_SuccessfulUpgrade_ViaQueryParam(t *testing.T) {
	jwtSecret := "test_secret_must_be_long_enough_32bytes"
	jwtMgr := auth.NewJWTManager(jwtSecret, 15*time.Minute)

	userID := uuid.New()
	token, err := jwtMgr.GenerateAccessToken(userID.String(), "teacher")
	if err != nil {
		t.Fatal(err)
	}

	hub := NewHub(nil, nil, nil)
	handler := NewHandler(HandlerConfig{
		Hub:       hub,
		JWTSecret: jwtSecret,
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "?token=" + token

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial WebSocket with query token: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "test done")

	time.Sleep(30 * time.Millisecond)

	clientsCount, _, _, _ := hub.Stats()
	if clientsCount != 1 {
		t.Fatalf("expected 1 registered client in Hub, got %d", clientsCount)
	}
}

func TestHandler_SuccessfulUpgrade_ViaContext(t *testing.T) {
	jwtSecret := "test_secret_must_be_long_enough_32bytes"
	hub := NewHub(nil, nil, nil)
	handler := NewHandler(HandlerConfig{
		Hub:       hub,
		JWTSecret: jwtSecret,
	})

	userID := uuid.New()
	wrappedHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := middleware.ContextWithUser(r.Context(), middleware.UserContext{
			UserID:          userID,
			Role:            "superuser",
			IsAuthenticated: true,
		})
		handler.ServeHTTP(w, r.WithContext(ctx))
	})

	server := httptest.NewServer(wrappedHandler)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial WebSocket with context user: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "test done")

	time.Sleep(30 * time.Millisecond)

	clientsCount, _, _, _ := hub.Stats()
	if clientsCount != 1 {
		t.Fatalf("expected 1 registered client in Hub, got %d", clientsCount)
	}
}
