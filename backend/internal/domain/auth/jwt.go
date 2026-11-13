package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	ErrTokenExpired = errors.New("token expired")
	ErrInvalidToken = errors.New("invalid token")
	ErrInvalidClaims = errors.New("invalid claims")
)

const DefaultAccessTokenTTL = 15 * time.Minute

// Claims represents JWT claims containing user ID as Subject and user role.
type Claims struct {
	jwt.RegisteredClaims
	Role string `json:"role"`
}

// JWTManager handles generating and validating JWT access tokens.
type JWTManager struct {
	secret []byte
	ttl    time.Duration
}

// NewJWTManager creates a new JWTManager.
func NewJWTManager(secret string, ttl time.Duration) *JWTManager {
	if ttl == 0 {
		ttl = DefaultAccessTokenTTL
	}
	return &JWTManager{
		secret: []byte(secret),
		ttl:    ttl,
	}
}

// GenerateAccessToken generates an HMAC-SHA256 JWT access token with 15m TTL by default.
func (m *JWTManager) GenerateAccessToken(userID string, role string) (string, error) {
	if userID == "" {
		return "", errors.New("user ID cannot be empty")
	}
	if role == "" {
		return "", errors.New("role cannot be empty")
	}

	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(m.ttl)),
		},
		Role: role,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString(m.secret)
	if err != nil {
		return "", fmt.Errorf("failed to sign access token: %w", err)
	}

	return tokenString, nil
}

// ValidateAccessToken parses and validates a JWT token string.
func (m *JWTManager) ValidateAccessToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return m.secret, nil
	})

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrInvalidClaims
	}

	return claims, nil
}
