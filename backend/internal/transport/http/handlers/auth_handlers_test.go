package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"backend/internal/domain/auth"
	"backend/internal/transport/http/middleware"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
)

type testFakeRedis struct {
	mu   sync.RWMutex
	data map[string]string
}

func (f *testFakeRedis) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch v := value.(type) {
	case string:
		f.data[key] = v
	case []byte:
		f.data[key] = string(v)
	default:
		f.data[key] = fmt.Sprintf("%v", v)
	}
	return redis.NewStatusResult("OK", nil)
}

func (f *testFakeRedis) Get(ctx context.Context, key string) *redis.StringCmd {
	f.mu.RLock()
	defer f.mu.RUnlock()
	val, ok := f.data[key]
	if !ok {
		return redis.NewStringResult("", redis.Nil)
	}
	return redis.NewStringResult(val, nil)
}

func (f *testFakeRedis) Del(ctx context.Context, keys ...string) *redis.IntCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	var count int64
	for _, k := range keys {
		if _, ok := f.data[k]; ok {
			delete(f.data, k)
			count++
		}
	}
	return redis.NewIntResult(count, nil)
}

func (f *testFakeRedis) Keys(ctx context.Context, pattern string) *redis.StringSliceCmd {
	f.mu.RLock()
	defer f.mu.RUnlock()
	prefix := strings.TrimSuffix(pattern, "*")
	var matched []string
	for k := range f.data {
		if strings.HasPrefix(k, prefix) {
			matched = append(matched, k)
		}
	}
	return redis.NewStringSliceResult(matched, nil)
}

type testUserRepo struct {
	mu        sync.RWMutex
	usersByID map[string]*auth.User
	usersByU  map[string]*auth.User
}

func newTestUserRepo() *testUserRepo {
	return &testUserRepo{
		usersByID: make(map[string]*auth.User),
		usersByU:  make(map[string]*auth.User),
	}
}

func (r *testUserRepo) GetByID(ctx context.Context, id string) (*auth.User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	u, ok := r.usersByID[id]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	return u, nil
}

func (r *testUserRepo) GetByYandexID(ctx context.Context, yandexID string) (*auth.User, error) {
	return nil, pgx.ErrNoRows
}

func (r *testUserRepo) GetByVKID(ctx context.Context, vkID string) (*auth.User, error) {
	return nil, pgx.ErrNoRows
}

func (r *testUserRepo) GetByUsername(ctx context.Context, username string) (*auth.User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	u, ok := r.usersByU[username]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	return u, nil
}

func (r *testUserRepo) Create(ctx context.Context, user *auth.User) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.usersByID[user.ID] = user
	r.usersByU[user.Username] = user
	return nil
}

func (r *testUserRepo) Update(ctx context.Context, user *auth.User) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.usersByID[user.ID] = user
	r.usersByU[user.Username] = user
	return nil
}

func (r *testUserRepo) GetUserSubscription(ctx context.Context, userID string) (*auth.UserSubscriptionDTO, error) {
	return nil, nil
}

func setupTestRouter() (http.Handler, *auth.Service, *auth.JWTManager, *testUserRepo) {
	jwtSecret := "test-jwt-secret-for-handler-tests-12345"
	jwtMgr := auth.NewJWTManager(jwtSecret, 15*time.Minute)
	fakeRdb := &testFakeRedis{data: make(map[string]string)}
	sessionMgr := auth.NewSessionManager(fakeRdb, 30*24*time.Hour)
	repo := newTestUserRepo()

	svcCfg := auth.ServiceConfig{
		IsDevMode: true,
	}
	yandexProvider := auth.NewYandexProvider(auth.YandexConfig{
		ClientID:    "yandex-client",
		RedirectURI: "http://localhost:8000/callback",
	})
	vkProvider := auth.NewVKProvider(auth.VKConfig{
		ClientID:    "vk-client",
		RedirectURI: "http://localhost:8000/callback",
	})

	service := auth.NewService(svcCfg, jwtMgr, sessionMgr, repo, yandexProvider, vkProvider)
	cookieCfg := auth.DefaultCookieConfig(false)
	handler := NewAuthHandler(service, jwtMgr, cookieCfg)

	r := chi.NewRouter()
	r.Use(middleware.AuthCookie(jwtSecret))
	handler.RegisterRoutes(r)

	return r, service, jwtMgr, repo
}

func TestHandler_OAuthLogin(t *testing.T) {
	router, _, _, _ := setupTestRouter()

	// 1. Valid provider yandex
	req := httptest.NewRequest(http.MethodGet, "/auth/oauth/yandex/login?redirect_to=/dashboard", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("expected 302 Found, got %d", w.Code)
	}

	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "oauth.yandex.ru") {
		t.Errorf("expected location to contain oauth.yandex.ru, got %s", loc)
	}

	// Must set ege_oauth_state cookie
	cookies := w.Result().Cookies()
	var stateCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == auth.OAuthStateCookieName {
			stateCookie = c
		}
	}
	if stateCookie == nil {
		t.Fatal("expected ege_oauth_state cookie to be set")
	}

	// 2. Unsupported provider
	reqBad := httptest.NewRequest(http.MethodGet, "/auth/oauth/github/login", nil)
	wBad := httptest.NewRecorder()
	router.ServeHTTP(wBad, reqBad)

	if wBad.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for unsupported provider, got %d", wBad.Code)
	}
}

func TestHandler_DevLogin(t *testing.T) {
	router, _, _, _ := setupTestRouter()

	body := []byte(`{"role":"teacher","username":"teacher_maria"}`)
	req := httptest.NewRequest(http.MethodPost, "/auth/dev-login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var res struct {
		OK   bool          `json:"ok"`
		User *auth.UserDTO `json:"user"`
	}
	if err := json.NewDecoder(w.Body).Decode(&res); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !res.OK || res.User == nil {
		t.Fatal("expected ok: true and user object")
	}
	if res.User.Role != "teacher" || res.User.Username != "teacher_maria" {
		t.Errorf("unexpected user DTO: %+v", res.User)
	}

	// Verify cookies set
	cookies := w.Result().Cookies()
	var accessCookie, refreshCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == auth.AccessCookieName {
			accessCookie = c
		}
		if c.Name == auth.RefreshCookieName {
			refreshCookie = c
		}
	}
	if accessCookie == nil || refreshCookie == nil {
		t.Fatal("expected both ege_access and ege_refresh cookies to be set")
	}
}

func TestHandler_RefreshAndLogout(t *testing.T) {
	router, _, _, _ := setupTestRouter()

	// 1. Dev login to obtain cookies
	loginBody := []byte(`{"role":"student","username":"student_vasya"}`)
	loginReq := httptest.NewRequest(http.MethodPost, "/auth/dev-login", bytes.NewReader(loginBody))
	loginW := httptest.NewRecorder()
	router.ServeHTTP(loginW, loginReq)

	var refreshCookie *http.Cookie
	for _, c := range loginW.Result().Cookies() {
		if c.Name == auth.RefreshCookieName {
			refreshCookie = c
		}
	}
	if refreshCookie == nil {
		t.Fatal("missing refresh cookie")
	}

	// 2. Call /auth/refresh
	refreshReq := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
	refreshReq.AddCookie(refreshCookie)
	refreshW := httptest.NewRecorder()
	router.ServeHTTP(refreshW, refreshReq)

	if refreshW.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on refresh, got %d: %s", refreshW.Code, refreshW.Body.String())
	}

	var newRefreshCookie *http.Cookie
	for _, c := range refreshW.Result().Cookies() {
		if c.Name == auth.RefreshCookieName {
			newRefreshCookie = c
		}
	}
	if newRefreshCookie == nil {
		t.Fatal("missing new refresh cookie after rotation")
	}

	// 3. Call /auth/logout
	logoutReq := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	logoutReq.AddCookie(newRefreshCookie)
	logoutW := httptest.NewRecorder()
	router.ServeHTTP(logoutW, logoutReq)

	if logoutW.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on logout, got %d", logoutW.Code)
	}

	// 4. Try refresh after logout -> must fail with 401
	staleRefreshReq := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
	staleRefreshReq.AddCookie(newRefreshCookie)
	staleRefreshW := httptest.NewRecorder()
	router.ServeHTTP(staleRefreshW, staleRefreshReq)

	if staleRefreshW.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized after logout, got %d", staleRefreshW.Code)
	}
}

func TestHandler_Me(t *testing.T) {
	router, _, jwtMgr, repo := setupTestRouter()

	// 1. Unauthenticated /auth/me returns 200 with authenticated=false
	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for unauthenticated /auth/me, got %d", w.Code)
	}

	var unauthResp auth.MeResponse
	if err := json.NewDecoder(w.Body).Decode(&unauthResp); err != nil {
		t.Fatalf("failed to decode /auth/me: %v", err)
	}
	if unauthResp.Authenticated {
		t.Error("expected Authenticated=false")
	}
	if unauthResp.User != nil {
		t.Error("expected User=nil")
	}

	// 2. Authenticated user
	uID := uuid.NewString()
	user := &auth.User{
		ID:          uID,
		Username:    "student_anna",
		FirstName:   "Анна",
		LastName:    "Иванова",
		Role:        "student",
		IsOnboarded: true,
	}
	_ = repo.Create(context.Background(), user)

	accessToken, _ := jwtMgr.GenerateAccessToken(uID, "student")
	authReq := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	authReq.AddCookie(&http.Cookie{
		Name:  auth.AccessCookieName,
		Value: accessToken,
	})
	authW := httptest.NewRecorder()
	router.ServeHTTP(authW, authReq)

	if authW.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for authenticated /auth/me, got %d: %s", authW.Code, authW.Body.String())
	}

	var authResp auth.MeResponse
	if err := json.NewDecoder(authW.Body).Decode(&authResp); err != nil {
		t.Fatalf("failed to decode authenticated /auth/me: %v", err)
	}
	if !authResp.Authenticated || authResp.User == nil {
		t.Fatal("expected Authenticated=true and non-nil User")
	}
	if authResp.User.ID != uID || authResp.User.Username != "student_anna" {
		t.Errorf("unexpected UserDTO: %+v", authResp.User)
	}
}
