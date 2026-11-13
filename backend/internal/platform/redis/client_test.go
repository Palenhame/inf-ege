package redis

import (
	"context"
	"testing"
	"time"

	"backend/internal/config"
	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

func setupTestRedis(t *testing.T) (*Client, *miniredis.Miniredis) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}

	rdb := goredis.NewClient(&goredis.Options{
		Addr: mr.Addr(),
	})
	client := Wrap(rdb)
	return client, mr
}

func TestRedisBasicOps(t *testing.T) {
	client, mr := setupTestRedis(t)
	defer mr.Close()
	defer client.Close()

	ctx := context.Background()

	// 1. Ping
	if err := client.Ping(ctx); err != nil {
		t.Fatalf("Ping failed: %v", err)
	}

	// 2. Set and Get
	err := client.Set(ctx, "test_key", "hello_world", time.Minute)
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	val, err := client.Get(ctx, "test_key")
	if err != nil || val != "hello_world" {
		t.Fatalf("Get failed: expected 'hello_world', got '%s', err: %v", val, err)
	}

	// 3. SetNX
	ok, err := client.SetNX(ctx, "test_key", "another_val", time.Minute)
	if err != nil || ok {
		t.Fatalf("SetNX on existing key should return false, got %v, err: %v", ok, err)
	}

	ok, err = client.SetNX(ctx, "new_key", "first_val", time.Minute)
	if err != nil || !ok {
		t.Fatalf("SetNX on new key should return true, got %v, err: %v", ok, err)
	}

	// 4. TTL and Expire
	err = client.Expire(ctx, "new_key", 10*time.Second)
	if err != nil {
		t.Fatalf("Expire failed: %v", err)
	}

	ttl, err := client.TTL(ctx, "new_key")
	if err != nil || ttl <= 0 {
		t.Fatalf("expected positive TTL, got %v, err: %v", ttl, err)
	}

	// 5. Del
	err = client.Del(ctx, "test_key", "new_key")
	if err != nil {
		t.Fatalf("Del failed: %v", err)
	}

	_, err = client.Get(ctx, "test_key")
	if err == nil {
		t.Fatalf("expected error getting deleted key")
	}
}

func TestSlidingWindowRateLimiter(t *testing.T) {
	client, mr := setupTestRedis(t)
	defer mr.Close()
	defer client.Close()

	ctx := context.Background()
	key := "ratelimit:test:user1"
	limit := int64(3)
	window := 10 * time.Second

	baseTime := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)

	// Attempt 1: should be allowed (remaining 2)
	res, err := client.AllowSlidingWindowAt(ctx, key, limit, window, baseTime)
	if err != nil || !res.Allowed || res.Remaining != 2 {
		t.Fatalf("attempt 1: expected allowed=true, rem=2; got %+v, err=%v", res, err)
	}

	// Attempt 2 at +1s: should be allowed (remaining 1)
	res, err = client.AllowSlidingWindowAt(ctx, key, limit, window, baseTime.Add(1*time.Second))
	if err != nil || !res.Allowed || res.Remaining != 1 {
		t.Fatalf("attempt 2: expected allowed=true, rem=1; got %+v, err=%v", res, err)
	}

	// Attempt 3 at +2s: should be allowed (remaining 0)
	res, err = client.AllowSlidingWindowAt(ctx, key, limit, window, baseTime.Add(2*time.Second))
	if err != nil || !res.Allowed || res.Remaining != 0 {
		t.Fatalf("attempt 3: expected allowed=true, rem=0; got %+v, err=%v", res, err)
	}

	// Attempt 4 at +3s: should be rejected
	res, err = client.AllowSlidingWindowAt(ctx, key, limit, window, baseTime.Add(3*time.Second))
	if err != nil || res.Allowed {
		t.Fatalf("attempt 4: expected allowed=false; got %+v, err=%v", res, err)
	}

	// Attempt 5 at +11s: attempt 1 (at 0s) is now outside window [1s, 11s]
	// Remaining should be 0 because attempt 2 (+1s) and attempt 3 (+2s) are still inside
	res, err = client.AllowSlidingWindowAt(ctx, key, limit, window, baseTime.Add(11*time.Second))
	if err != nil || !res.Allowed || res.Remaining != 1 {
		t.Fatalf("attempt 5: expected allowed=true, rem=0; got %+v, err=%v", res, err)
	}

	// Attempt 6 at +20s: all prior attempts outside window [10s, 20s]
	res, err = client.AllowSlidingWindowAt(ctx, key, limit, window, baseTime.Add(25*time.Second))
	if err != nil || !res.Allowed || res.Remaining != 2 {
		t.Fatalf("attempt 6: expected allowed=true, rem=2; got %+v, err=%v", res, err)
	}

	// Also test standard AllowSlidingWindow (using time.Now())
	resNow, errNow := client.AllowSlidingWindow(ctx, "ratelimit:test:now", 1, time.Second)
	if errNow != nil || !resNow.Allowed {
		t.Fatalf("AllowSlidingWindow failed: %+v, err=%v", resNow, errNow)
	}
}

func TestAcquireLock(t *testing.T) {
	client, mr := setupTestRedis(t)
	defer mr.Close()
	defer client.Close()

	ctx := context.Background()
	lockKey := "lock:scheduler"

	// First acquire: should succeed
	acquired, release, err := client.AcquireLock(ctx, lockKey, 5*time.Second)
	if err != nil || !acquired || release == nil {
		t.Fatalf("failed to acquire initial lock: acquired=%v, err=%v", acquired, err)
	}

	// Second acquire while held: should fail
	acquired2, _, err2 := client.AcquireLock(ctx, lockKey, 5*time.Second)
	if err2 != nil || acquired2 {
		t.Fatalf("expected lock collision: acquired=%v, err=%v", acquired2, err2)
	}

	// Release first lock
	release()

	// Third acquire: should succeed now
	acquired3, release3, err3 := client.AcquireLock(ctx, lockKey, 5*time.Second)
	if err3 != nil || !acquired3 {
		t.Fatalf("expected to re-acquire lock after release: acquired=%v, err=%v", acquired3, err3)
	}
	defer release3()
}

func TestNewConnectionInvalidAddr(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	cfg := config.RedisConfig{
		Addr: "127.0.0.1:59998",
	}

	_, err := New(ctx, cfg)
	if err == nil {
		t.Errorf("expected error connecting to invalid redis addr")
	}
}
