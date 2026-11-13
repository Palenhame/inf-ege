package redis

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"backend/internal/config"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Client wraps the go-redis client with platform helpers.
type Client struct {
	rdb *redis.Client
}

// New initializes a Redis client connection and checks readiness via Ping.
func New(ctx context.Context, cfg config.RedisConfig) (*Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("redis: connection failed: %w", err)
	}

	return &Client{rdb: rdb}, nil
}

// Wrap wraps an existing *redis.Client.
func Wrap(rdb *redis.Client) *Client {
	return &Client{rdb: rdb}
}

// Underlying returns the underlying *redis.Client.
func (c *Client) Underlying() *redis.Client {
	return c.rdb
}

// Ping checks if Redis is reachable.
func (c *Client) Ping(ctx context.Context) error {
	return c.rdb.Ping(ctx).Err()
}

// Close closes the Redis connection.
func (c *Client) Close() error {
	return c.rdb.Close()
}

// Get retrieves a string value by key.
func (c *Client) Get(ctx context.Context, key string) (string, error) {
	return c.rdb.Get(ctx, key).Result()
}

// Set stores a key-value pair with an expiration TTL.
func (c *Client) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	return c.rdb.Set(ctx, key, value, ttl).Err()
}

// SetNX stores a key-value pair only if it does not already exist.
func (c *Client) SetNX(ctx context.Context, key string, value interface{}, ttl time.Duration) (bool, error) {
	return c.rdb.SetNX(ctx, key, value, ttl).Result()
}

// Del removes one or more keys.
func (c *Client) Del(ctx context.Context, keys ...string) error {
	return c.rdb.Del(ctx, keys...).Err()
}

// Expire sets an expiration timeout on an existing key.
func (c *Client) Expire(ctx context.Context, key string, ttl time.Duration) error {
	return c.rdb.Expire(ctx, key, ttl).Err()
}

// TTL returns the remaining time-to-live for a key.
func (c *Client) TTL(ctx context.Context, key string) (time.Duration, error) {
	return c.rdb.TTL(ctx, key).Result()
}

// RateLimitResult contains the outcome of a sliding window rate limit evaluation.
type RateLimitResult struct {
	Allowed   bool
	Remaining int64
	ResetIn   time.Duration
}

// AllowSlidingWindow evaluates a sliding window rate limit using the current timestamp.
func (c *Client) AllowSlidingWindow(ctx context.Context, key string, limit int64, window time.Duration) (*RateLimitResult, error) {
	return c.AllowSlidingWindowAt(ctx, key, limit, window, time.Now())
}

// AllowSlidingWindowAt evaluates a sliding window rate limit at a specific timestamp using a Redis sorted set.
// key: identifier (e.g. "ratelimit:auth:192.168.1.1")
// limit: maximum allowed events in window
// window: duration of the sliding window
// now: current reference timestamp
func (c *Client) AllowSlidingWindowAt(ctx context.Context, key string, limit int64, window time.Duration, now time.Time) (*RateLimitResult, error) {
	nowMicro := now.UnixNano() / 1000
	windowStartMicro := now.Add(-window).UnixNano() / 1000

	pipe := c.rdb.TxPipeline()
	// 1. Remove entries older than windowStart
	pipe.ZRemRangeByScore(ctx, key, "-inf", strconv.FormatInt(windowStartMicro, 10))
	// 2. Count current elements in window
	countCmd := pipe.ZCard(ctx, key)
	// 3. Set TTL to keep Redis memory bounded
	pipe.Expire(ctx, key, window*2)

	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("redis: rate limit check failed: %w", err)
	}

	currentCount := countCmd.Val()
	if currentCount >= limit {
		return &RateLimitResult{
			Allowed:   false,
			Remaining: 0,
			ResetIn:   window,
		}, nil
	}

	// Add current event timestamp with unique member
	member := fmt.Sprintf("%d:%s", nowMicro, uuid.New().String())
	addPipe := c.rdb.TxPipeline()
	addPipe.ZAdd(ctx, key, redis.Z{
		Score:  float64(nowMicro),
		Member: member,
	})
	addPipe.Expire(ctx, key, window*2)
	if _, err := addPipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("redis: rate limit record failed: %w", err)
	}

	remaining := limit - (currentCount + 1)
	if remaining < 0 {
		remaining = 0
	}

	return &RateLimitResult{
		Allowed:   true,
		Remaining: remaining,
		ResetIn:   window,
	}, nil
}

// AcquireLock attempts to acquire a distributed lock using Redis SET NX.
// Returns acquired bool and a release function.
func (c *Client) AcquireLock(ctx context.Context, key string, ttl time.Duration) (bool, func(), error) {
	lockVal := uuid.New().String()
	acquired, err := c.rdb.SetNX(ctx, key, lockVal, ttl).Result()
	if err != nil {
		return false, nil, fmt.Errorf("redis: acquire lock error: %w", err)
	}
	if !acquired {
		return false, nil, nil
	}

	release := func() {
		// Atomic unlock via Lua script: only delete if value matches
		const unlockLua = `
			if redis.call("get", KEYS[1]) == ARGV[1] then
				return redis.call("del", KEYS[1])
			else
				return 0
			end
		`
		_ = c.rdb.Eval(context.Background(), unlockLua, []string{key}, lockVal).Err()
	}

	return true, release, nil
}
