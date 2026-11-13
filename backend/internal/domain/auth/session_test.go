package auth

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

type fakeRedis struct {
	mu   sync.RWMutex
	data map[string]string
}

func newFakeRedis() *fakeRedis {
	return &fakeRedis{
		data: make(map[string]string),
	}
}

func (f *fakeRedis) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd {
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

func (f *fakeRedis) Get(ctx context.Context, key string) *redis.StringCmd {
	f.mu.RLock()
	defer f.mu.RUnlock()
	val, ok := f.data[key]
	if !ok {
		return redis.NewStringResult("", redis.Nil)
	}
	return redis.NewStringResult(val, nil)
}

func (f *fakeRedis) Del(ctx context.Context, keys ...string) *redis.IntCmd {
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

func (f *fakeRedis) Keys(ctx context.Context, pattern string) *redis.StringSliceCmd {
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

func TestSessionManager_CreateAndValidate(t *testing.T) {
	rdb := newFakeRedis()
	mgr := NewSessionManager(rdb, 30*24*time.Hour)
	ctx := context.Background()

	userID := "550e8400-e29b-41d4-a716-446655440000"

	token, err := mgr.CreateSession(ctx, userID)
	if err != nil {
		t.Fatalf("unexpected error creating session: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty refresh token")
	}

	// Validate using JTI alone
	gotUserID, jti, err := mgr.ValidateSession(ctx, token)
	if err != nil {
		t.Fatalf("unexpected error validating session: %v", err)
	}
	if gotUserID != userID {
		t.Errorf("expected userID %s, got %s", userID, gotUserID)
	}
	if jti != token {
		t.Errorf("expected jti %s, got %s", token, jti)
	}

	// Validate using userID:jti format
	compoundToken := fmt.Sprintf("%s:%s", userID, token)
	gotUserID2, jti2, err := mgr.ValidateSession(ctx, compoundToken)
	if err != nil {
		t.Fatalf("unexpected error validating compound session: %v", err)
	}
	if gotUserID2 != userID || jti2 != token {
		t.Errorf("unexpected validation result: %s, %s", gotUserID2, jti2)
	}
}

func TestSessionManager_RotateSession(t *testing.T) {
	rdb := newFakeRedis()
	mgr := NewSessionManager(rdb, 30*24*time.Hour)
	ctx := context.Background()

	userID := "user-123"
	token1, err := mgr.CreateSession(ctx, userID)
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	token2, rotatedUserID, err := mgr.RotateSession(ctx, token1)
	if err != nil {
		t.Fatalf("failed to rotate session: %v", err)
	}
	if rotatedUserID != userID {
		t.Errorf("expected rotated userID %s, got %s", userID, rotatedUserID)
	}
	if token2 == token1 {
		t.Error("expected fresh rotated token, got same token")
	}

	// Old token must be invalidated
	_, _, err = mgr.ValidateSession(ctx, token1)
	if err == nil {
		t.Fatal("expected old token to be invalid after rotation, got nil error")
	}

	// New token must be valid
	validUser, _, err := mgr.ValidateSession(ctx, token2)
	if err != nil {
		t.Fatalf("expected new token to be valid: %v", err)
	}
	if validUser != userID {
		t.Errorf("expected userID %s, got %s", userID, validUser)
	}
}

func TestSessionManager_RevokeSession(t *testing.T) {
	rdb := newFakeRedis()
	mgr := NewSessionManager(rdb, 30*24*time.Hour)
	ctx := context.Background()

	userID := "user-456"
	token, err := mgr.CreateSession(ctx, userID)
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	err = mgr.RevokeSession(ctx, token)
	if err != nil {
		t.Fatalf("failed to revoke session: %v", err)
	}

	_, _, err = mgr.ValidateSession(ctx, token)
	if err == nil {
		t.Fatal("expected session to be invalid after revocation")
	}
}

func TestSessionManager_RevokeAllUserSessions(t *testing.T) {
	rdb := newFakeRedis()
	mgr := NewSessionManager(rdb, 30*24*time.Hour)
	ctx := context.Background()

	user1 := "user-1"
	user2 := "user-2"

	t1, _ := mgr.CreateSession(ctx, user1)
	t2, _ := mgr.CreateSession(ctx, user1)
	t3, _ := mgr.CreateSession(ctx, user2)

	// Revoke all user1 sessions
	err := mgr.RevokeAllUserSessions(ctx, user1)
	if err != nil {
		t.Fatalf("failed to revoke all user sessions: %v", err)
	}

	// user1 sessions must be dead
	if _, _, err := mgr.ValidateSession(ctx, t1); err == nil {
		t.Error("expected user1 session t1 to be revoked")
	}
	if _, _, err := mgr.ValidateSession(ctx, t2); err == nil {
		t.Error("expected user1 session t2 to be revoked")
	}

	// user2 session must remain active
	if _, _, err := mgr.ValidateSession(ctx, t3); err != nil {
		t.Errorf("expected user2 session t3 to remain valid, got %v", err)
	}
}
