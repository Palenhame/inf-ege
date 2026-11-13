package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var (
	ErrSessionNotFound = errors.New("refresh session not found or expired")
	ErrInvalidSession  = errors.New("invalid refresh session token")
)

const (
	DefaultRefreshTokenTTL = 30 * 24 * time.Hour // 30 days
	refreshKeyPrefix       = "auth:refresh"
	refreshJtiKeyPrefix    = "auth:refresh_jti"
)

// SessionData represents the metadata stored in Redis for a refresh session.
type SessionData struct {
	UserID    string    `json:"user_id"`
	JTI       string    `json:"jti"`
	CreatedAt time.Time `json:"created_at"`
}

// RedisCmdClient abstracts the Redis commands needed for session management.
type RedisCmdClient interface {
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd
	Get(ctx context.Context, key string) *redis.StringCmd
	Del(ctx context.Context, keys ...string) *redis.IntCmd
	Keys(ctx context.Context, pattern string) *redis.StringSliceCmd
}

// SessionManager manages refresh token sessions in Redis.
type SessionManager struct {
	rdb RedisCmdClient
	ttl time.Duration
}

// NewSessionManager creates a new SessionManager.
func NewSessionManager(rdb RedisCmdClient, ttl time.Duration) *SessionManager {
	if ttl <= 0 {
		ttl = DefaultRefreshTokenTTL
	}
	return &SessionManager{
		rdb: rdb,
		ttl: ttl,
	}
}

// makeUserRefreshKey returns "auth:refresh:{user_id}:{jti}".
func makeUserRefreshKey(userID, jti string) string {
	return fmt.Sprintf("%s:%s:%s", refreshKeyPrefix, userID, jti)
}

// makeJtiLookupKey returns "auth:refresh_jti:{jti}".
func makeJtiLookupKey(jti string) string {
	return fmt.Sprintf("%s:%s", refreshJtiKeyPrefix, jti)
}

// parseToken extracts userID and jti from a token.
// The token may be in the format "<jti>" or "<user_id>:<jti>".
func parseToken(token string) (userID, jti string, hasUserID bool) {
	parts := strings.SplitN(token, ":", 2)
	if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
		return parts[0], parts[1], true
	}
	return "", token, false
}

// CreateSession generates a new refresh token session and stores it in Redis with 30-day TTL.
// Returns the refresh token string (jti).
func (sm *SessionManager) CreateSession(ctx context.Context, userID string) (string, error) {
	if userID == "" {
		return "", errors.New("userID cannot be empty")
	}

	jti := uuid.NewString()
	session := SessionData{
		UserID:    userID,
		JTI:       jti,
		CreatedAt: time.Now().UTC(),
	}

	sessionBytes, err := json.Marshal(session)
	if err != nil {
		return "", fmt.Errorf("failed to marshal session data: %w", err)
	}

	userKey := makeUserRefreshKey(userID, jti)
	if err := sm.rdb.Set(ctx, userKey, sessionBytes, sm.ttl).Err(); err != nil {
		return "", fmt.Errorf("failed to save user refresh session in Redis: %w", err)
	}

	jtiKey := makeJtiLookupKey(jti)
	if err := sm.rdb.Set(ctx, jtiKey, userID, sm.ttl).Err(); err != nil {
		// Clean up userKey on failure
		_ = sm.rdb.Del(ctx, userKey).Err()
		return "", fmt.Errorf("failed to save jti lookup in Redis: %w", err)
	}

	return jti, nil
}

// ValidateSession verifies if the refresh token is active and returns the associated userID.
func (sm *SessionManager) ValidateSession(ctx context.Context, token string) (string, string, error) {
	if strings.TrimSpace(token) == "" {
		return "", "", ErrInvalidSession
	}

	userID, jti, hasUserID := parseToken(token)

	// If token did not include userID, look up userID via JTI key
	if !hasUserID {
		jtiKey := makeJtiLookupKey(jti)
		fetchedUserID, err := sm.rdb.Get(ctx, jtiKey).Result()
		if err != nil {
			if errors.Is(err, redis.Nil) {
				return "", "", ErrSessionNotFound
			}
			return "", "", fmt.Errorf("redis error looking up jti: %w", err)
		}
		userID = fetchedUserID
	}

	userKey := makeUserRefreshKey(userID, jti)
	val, err := sm.rdb.Get(ctx, userKey).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", "", ErrSessionNotFound
		}
		return "", "", fmt.Errorf("redis error looking up session: %w", err)
	}

	var data SessionData
	if err := json.Unmarshal([]byte(val), &data); err != nil {
		return userID, jti, nil
	}

	return data.UserID, data.JTI, nil
}

// RotateSession validates the old refresh token, revokes it, and issues a fresh refresh token for the user.
func (sm *SessionManager) RotateSession(ctx context.Context, oldToken string) (newToken string, userID string, err error) {
	userID, oldJTI, err := sm.ValidateSession(ctx, oldToken)
	if err != nil {
		return "", "", err
	}

	// Revoke old session
	sm.revokeByJTI(ctx, userID, oldJTI)

	// Create new session
	newToken, err = sm.CreateSession(ctx, userID)
	if err != nil {
		return "", "", fmt.Errorf("failed to create rotated session: %w", err)
	}

	return newToken, userID, nil
}

// RevokeSession immediately revokes the session associated with the given refresh token.
func (sm *SessionManager) RevokeSession(ctx context.Context, token string) error {
	if strings.TrimSpace(token) == "" {
		return nil
	}

	userID, jti, hasUserID := parseToken(token)
	if !hasUserID {
		jtiKey := makeJtiLookupKey(jti)
		fetchedUserID, err := sm.rdb.Get(ctx, jtiKey).Result()
		if err == nil {
			userID = fetchedUserID
		}
	}

	sm.revokeByJTI(ctx, userID, jti)
	return nil
}

func (sm *SessionManager) revokeByJTI(ctx context.Context, userID, jti string) {
	keys := []string{makeJtiLookupKey(jti)}
	if userID != "" {
		keys = append(keys, makeUserRefreshKey(userID, jti))
	}
	_ = sm.rdb.Del(ctx, keys...).Err()
}

// RevokeAllUserSessions revokes all active refresh sessions for a specific user.
func (sm *SessionManager) RevokeAllUserSessions(ctx context.Context, userID string) error {
	if userID == "" {
		return errors.New("userID cannot be empty")
	}

	pattern := fmt.Sprintf("%s:%s:*", refreshKeyPrefix, userID)
	keys, err := sm.rdb.Keys(ctx, pattern).Result()
	if err != nil {
		return fmt.Errorf("failed to list user sessions: %w", err)
	}

	for _, k := range keys {
		// Key format: auth:refresh:{user_id}:{jti}
		parts := strings.Split(k, ":")
		if len(parts) >= 4 {
			jti := parts[3]
			_ = sm.rdb.Del(ctx, makeJtiLookupKey(jti)).Err()
		}
		_ = sm.rdb.Del(ctx, k).Err()
	}

	return nil
}
