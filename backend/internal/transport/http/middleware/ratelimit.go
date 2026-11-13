package middleware

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"backend/internal/transport/http/response"

	"github.com/redis/go-redis/v9"
)

var slidingWindowScript = redis.NewScript(`
local key = KEYS[1]
local now = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local limit = tonumber(ARGV[3])
local expireSec = tonumber(ARGV[4])

local clearBefore = now - window
redis.call('ZREMRANGEBYSCORE', key, '-inf', clearBefore)

local currentRequests = redis.call('ZCARD', key)
if currentRequests < limit then
    redis.call('ZADD', key, now, now)
    redis.call('EXPIRE', key, expireSec)
    return {1, limit - currentRequests - 1}
else
    return {0, 0}
end
`)

// KeyFunc extracts a unique rate limiting key from an HTTP request.
type KeyFunc func(r *http.Request) string

// RateLimitOptions configures the rate limiter.
type RateLimitOptions struct {
	KeyFunc KeyFunc
}

// RateLimitOption applies configuration to RateLimitOptions.
type RateLimitOption func(*RateLimitOptions)

// WithKeyFunc overrides the key generation function.
func WithKeyFunc(fn KeyFunc) RateLimitOption {
	return func(o *RateLimitOptions) {
		o.KeyFunc = fn
	}
}

// DefaultKeyFunc extracts user ID if authenticated, or client IP otherwise.
func DefaultKeyFunc(r *http.Request) string {
	if userCtx, ok := GetUserContext(r.Context()); ok && userCtx.IsAuthenticated {
		return "user:" + userCtx.UserID.String()
	}
	return "ip:" + ClientIP(r)
}

// ClientIP extracts the client IP address from proxy headers or RemoteAddr.
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if len(parts) > 0 {
			ip := strings.TrimSpace(parts[0])
			if ip != "" {
				return ip
			}
		}
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return ip
	}
	return r.RemoteAddr
}

type memoryLimiter struct {
	mu      sync.Mutex
	history map[string][]time.Time
}

func newMemoryLimiter() *memoryLimiter {
	return &memoryLimiter{
		history: make(map[string][]time.Time),
	}
}

func (m *memoryLimiter) allow(key string, limit int, window time.Duration) (bool, int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-window)

	timestamps := m.history[key]
	var valid []time.Time
	for _, ts := range timestamps {
		if ts.After(cutoff) {
			valid = append(valid, ts)
		}
	}

	if len(valid) >= limit {
		m.history[key] = valid
		return false, 0
	}

	valid = append(valid, now)
	m.history[key] = valid
	remaining := limit - len(valid)
	return true, remaining
}

// RateLimit returns a sliding window rate limiter middleware.
// If rdb is provided, it uses Redis sorted sets via atomic Lua script.
// If rdb is nil or Redis returns an error, it falls back to an in-memory sliding window limiter.
func RateLimit(rdb redis.Cmdable, limit int, window time.Duration, opts ...RateLimitOption) func(http.Handler) http.Handler {
	cfg := RateLimitOptions{
		KeyFunc: DefaultKeyFunc,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	memFallback := newMemoryLimiter()
	expireSec := int(window.Seconds())
	if expireSec < 1 {
		expireSec = 1
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			keyIdentifier := cfg.KeyFunc(r)
			redisKey := fmt.Sprintf("ratelimit:%s", keyIdentifier)

			var allowed bool
			var remaining int

			if rdb != nil {
				nowNano := time.Now().UnixNano()
				windowNano := window.Nanoseconds()

				res, err := slidingWindowScript.Run(
					r.Context(),
					rdb,
					[]string{redisKey},
					nowNano,
					windowNano,
					limit,
					expireSec,
				).Slice()

				if err == nil && len(res) == 2 {
					allowedInt, _ := res[0].(int64)
					remainingInt, _ := res[1].(int64)
					allowed = allowedInt == 1
					remaining = int(remainingInt)
				} else {
					// Fallback to in-memory sliding window on Redis connection failure
					allowed, remaining = memFallback.allow(keyIdentifier, limit, window)
				}
			} else {
				// Standalone in-memory mode
				allowed, remaining = memFallback.allow(keyIdentifier, limit, window)
			}

			w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", limit))
			w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))

			if !allowed {
				retryAfterSec := int(window.Seconds())
				if retryAfterSec < 1 {
					retryAfterSec = 1
				}
				w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfterSec))
				response.Error(w, http.StatusTooManyRequests, "RATE_LIMITED",
					"Слишком много запросов. Пожалуйста, повторите попытку позже.",
					map[string]any{
						"limit":           limit,
						"window_sec":      int(window.Seconds()),
						"retry_after_sec": retryAfterSec,
					},
				)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
