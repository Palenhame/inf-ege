package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/redis/go-redis/v9"
)

// Job defines a runnable scheduled job.
type Job interface {
	Name() string
	Run(ctx context.Context) error
}

// JobFunc is an adapter to allow the use of ordinary functions as Jobs.
type JobFunc struct {
	JobName string
	RunFunc func(ctx context.Context) error
}

func (j JobFunc) Name() string {
	return j.JobName
}

func (j JobFunc) Run(ctx context.Context) error {
	return j.RunFunc(ctx)
}

// DB represents a minimal pgx interface for database operations.
// Both *pgxpool.Pool and pgx.Tx implement this interface.
type DB interface {
	Exec(ctx context.Context, sql string, arguments ...any) (commandTag pgconn.CommandTag, err error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// RedisClient represents the Redis commands required by the jobs package.
// Satisfied by *redis.Client and redis.UniversalClient.
type RedisClient interface {
	SetNX(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.BoolCmd
	Get(ctx context.Context, key string) *redis.StringCmd
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd
	Eval(ctx context.Context, script string, keys []string, args ...interface{}) *redis.Cmd
	Del(ctx context.Context, keys ...string) *redis.IntCmd
}

// Locker handles distributed mutual exclusion for background jobs.
type Locker interface {
	Acquire(ctx context.Context, jobName string, ttl time.Duration) (unlock func(), acquired bool, err error)
}

// RedisLocker implements Locker using Redis SET lock:scheduler:{job} EX {ttl} NX.
type RedisLocker struct {
	client     RedisClient
	instanceID string
	logger     *slog.Logger
}

// NewRedisLocker creates a new Redis-backed distributed locker.
func NewRedisLocker(client RedisClient, logger *slog.Logger) *RedisLocker {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	instanceID := hex.EncodeToString(b)

	if logger == nil {
		logger = slog.Default()
	}

	return &RedisLocker{
		client:     client,
		instanceID: instanceID,
		logger:     logger.With("component", "redis_locker"),
	}
}

const releaseLockLua = `
if redis.call("get", KEYS[1]) == ARGV[1] then
    return redis.call("del", KEYS[1])
else
    return 0
end
`

// Acquire attempts to acquire a distributed lock for jobName with the specified TTL.
func (r *RedisLocker) Acquire(ctx context.Context, jobName string, ttl time.Duration) (func(), bool, error) {
	if r.client == nil {
		return func() {}, true, nil
	}

	key := fmt.Sprintf("lock:scheduler:%s", jobName)
	token := fmt.Sprintf("%s:%d", r.instanceID, time.Now().UnixNano())

	acquired, err := r.client.SetNX(ctx, key, token, ttl).Result()
	if err != nil {
		return nil, false, fmt.Errorf("failed to acquire lock %s: %w", key, err)
	}

	if !acquired {
		return nil, false, nil
	}

	unlock := func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		if err := r.client.Eval(unlockCtx, releaseLockLua, []string{key}, token).Err(); err != nil {
			r.logger.WarnContext(unlockCtx, "failed to release lock cleanly",
				"key", key,
				"error", err,
			)
		}
	}

	return unlock, true, nil
}

// NoopLocker is an in-memory fallback locker that always grants locks.
type NoopLocker struct{}

func (n NoopLocker) Acquire(ctx context.Context, jobName string, ttl time.Duration) (func(), bool, error) {
	return func() {}, true, nil
}

// ScheduleKind denotes how a job is scheduled.
type ScheduleKind int

const (
	ScheduleInterval ScheduleKind = iota
	ScheduleDailyUTC
)

// ScheduleSpec configures the execution schedule and distributed locking of a job.
type ScheduleSpec struct {
	Kind        ScheduleKind
	Interval    time.Duration
	DailyHour   int // 0-23 UTC
	DailyMinute int // 0-59 UTC
	LockTTL     time.Duration
}
