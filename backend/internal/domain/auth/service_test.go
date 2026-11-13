package auth

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type memoryUserRepo struct {
	mu            sync.RWMutex
	usersByID     map[string]*User
	usersByYandex map[string]*User
	usersByVK     map[string]*User
	usersByUname  map[string]*User
	subscriptions map[string]*UserSubscriptionDTO
}

func newMemoryUserRepo() *memoryUserRepo {
	return &memoryUserRepo{
		usersByID:     make(map[string]*User),
		usersByYandex: make(map[string]*User),
		usersByVK:     make(map[string]*User),
		usersByUname:  make(map[string]*User),
		subscriptions: make(map[string]*UserSubscriptionDTO),
	}
}

func (r *memoryUserRepo) GetByID(ctx context.Context, id string) (*User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	u, ok := r.usersByID[id]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	return u, nil
}

func (r *memoryUserRepo) GetByYandexID(ctx context.Context, yandexID string) (*User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	u, ok := r.usersByYandex[yandexID]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	return u, nil
}

func (r *memoryUserRepo) GetByVKID(ctx context.Context, vkID string) (*User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	u, ok := r.usersByVK[vkID]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	return u, nil
}

func (r *memoryUserRepo) GetByUsername(ctx context.Context, username string) (*User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	u, ok := r.usersByUname[username]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	return u, nil
}

func (r *memoryUserRepo) Create(ctx context.Context, user *User) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.usersByID[user.ID] = user
	r.usersByUname[user.Username] = user
	if user.YandexID != nil {
		r.usersByYandex[*user.YandexID] = user
	}
	if user.VKID != nil {
		r.usersByVK[*user.VKID] = user
	}
	return nil
}

func (r *memoryUserRepo) Update(ctx context.Context, user *User) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.usersByID[user.ID] = user
	r.usersByUname[user.Username] = user
	if user.YandexID != nil {
		r.usersByYandex[*user.YandexID] = user
	}
	if user.VKID != nil {
		r.usersByVK[*user.VKID] = user
	}
	return nil
}

func (r *memoryUserRepo) GetUserSubscription(ctx context.Context, userID string) (*UserSubscriptionDTO, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.subscriptions[userID], nil
}

func TestService_DevLogin(t *testing.T) {
	jwtMgr := NewJWTManager("secret-key-123456", 15*time.Minute)
	fakeRdb := newFakeRedis()
	sessionMgr := NewSessionManager(fakeRdb, 30*24*time.Hour)
	repo := newMemoryUserRepo()

	// 1. In production mode: DevLogin must be rejected
	prodSvc := NewService(
		ServiceConfig{IsDevMode: false},
		jwtMgr, sessionMgr, repo, nil, nil,
	)
	_, _, err := prodSvc.DevLogin(context.Background(), "student", "dev_test")
	if err != ErrDevLoginDisabled {
		t.Fatalf("expected ErrDevLoginDisabled in prod, got: %v", err)
	}

	// 2. In dev mode: DevLogin creates user and returns tokens
	devSvc := NewService(
		ServiceConfig{IsDevMode: true},
		jwtMgr, sessionMgr, repo, nil, nil,
	)
	tokens, userDTO, err := devSvc.DevLogin(context.Background(), "teacher", "dev_teacher")
	if err != nil {
		t.Fatalf("unexpected error in DevLogin: %v", err)
	}

	if tokens.AccessToken == "" || tokens.RefreshToken == "" {
		t.Fatal("expected non-empty access and refresh tokens")
	}
	if userDTO.Role != "teacher" {
		t.Errorf("expected role 'teacher', got '%s'", userDTO.Role)
	}
	if userDTO.Username != "dev_teacher" {
		t.Errorf("expected username 'dev_teacher', got '%s'", userDTO.Username)
	}

	// 3. Test Refresh
	refreshedTokens, err := devSvc.RefreshToken(context.Background(), tokens.RefreshToken)
	if err != nil {
		t.Fatalf("unexpected error refreshing token: %v", err)
	}
	if refreshedTokens.AccessToken == "" || refreshedTokens.RefreshToken == "" {
		t.Fatal("expected non-empty tokens on refresh")
	}
	if refreshedTokens.RefreshToken == tokens.RefreshToken {
		t.Error("expected fresh rotated refresh token")
	}

	// 4. Test GetMe
	meResp, err := devSvc.GetMe(context.Background(), userDTO.ID, nil)
	if err != nil {
		t.Fatalf("unexpected error in GetMe: %v", err)
	}
	if !meResp.Authenticated || meResp.User == nil {
		t.Fatal("expected authenticated response")
	}
	if meResp.User.ID != userDTO.ID {
		t.Errorf("expected user ID %s, got %s", userDTO.ID, meResp.User.ID)
	}

	// 5. Test GetMe with Impersonation
	impID := "superuser-uuid-123"
	meImp, err := devSvc.GetMe(context.Background(), userDTO.ID, &impID)
	if err != nil {
		t.Fatalf("unexpected error in GetMe with impersonation: %v", err)
	}
	if meImp.User.ImpersonatedBy == nil || *meImp.User.ImpersonatedBy != impID {
		t.Errorf("expected ImpersonatedBy %s, got %v", impID, meImp.User.ImpersonatedBy)
	}

	// 6. Test Logout
	err = devSvc.Logout(context.Background(), refreshedTokens.RefreshToken)
	if err != nil {
		t.Fatalf("unexpected error in Logout: %v", err)
	}

	// After logout, refresh must fail
	_, err = devSvc.RefreshToken(context.Background(), refreshedTokens.RefreshToken)
	if err == nil {
		t.Fatal("expected error refreshing revoked token, got nil")
	}
}

func TestService_GetMe_Unauthenticated(t *testing.T) {
	jwtMgr := NewJWTManager("secret-key", 15*time.Minute)
	fakeRdb := newFakeRedis()
	sessionMgr := NewSessionManager(fakeRdb, 30*24*time.Hour)
	repo := newMemoryUserRepo()

	svc := NewService(
		ServiceConfig{IsDevMode: true},
		jwtMgr, sessionMgr, repo, nil, nil,
	)

	resp, err := svc.GetMe(context.Background(), "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Authenticated {
		t.Error("expected Authenticated=false for empty user ID")
	}
	if resp.User != nil {
		t.Error("expected User=nil for unauthenticated")
	}
}
