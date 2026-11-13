package middleware

import (
	"context"
	"errors"
	"net/http"

	"backend/internal/transport/http/response"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type contextKey string

const userContextKey contextKey = "user_context"

// UserClaims defines the JWT claims payload stored in ege_access cookie.
type UserClaims struct {
	Role string `json:"role"`
	jwt.RegisteredClaims
}

// UserContext holds authenticated user state for downstream handlers.
type UserContext struct {
	UserID          uuid.UUID
	Role            string
	IsSuperuser     bool
	ImpersonatedBy  *uuid.UUID
	IsAuthenticated bool
	TokenExpired    bool
}

// ContextWithUser injects UserContext into request context.
func ContextWithUser(ctx context.Context, u UserContext) context.Context {
	return context.WithValue(ctx, userContextKey, u)
}

// GetUserContext extracts UserContext from context, returning false if not present.
func GetUserContext(ctx context.Context) (UserContext, bool) {
	val := ctx.Value(userContextKey)
	if val == nil {
		return UserContext{}, false
	}
	u, ok := val.(UserContext)
	return u, ok
}

// MustGetUserContext extracts UserContext from context, returning empty unauthenticated context if absent.
func MustGetUserContext(ctx context.Context) UserContext {
	u, ok := GetUserContext(ctx)
	if !ok {
		return UserContext{IsAuthenticated: false}
	}
	return u
}

// AuthCookie parses the "ege_access" cookie, validates HMAC JWT claims,
// and injects UserContext into request context.
// If cookie is missing or invalid, request continues as unauthenticated.
func AuthCookie(jwtSecret string) func(http.Handler) http.Handler {
	secretBytes := []byte(jwtSecret)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie("ege_access")
			if err != nil || cookie == nil || cookie.Value == "" {
				// Unauthenticated request
				ctx := ContextWithUser(r.Context(), UserContext{IsAuthenticated: false})
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			claims := &UserClaims{}
			token, parseErr := jwt.ParseWithClaims(cookie.Value, claims, func(t *jwt.Token) (any, error) {
				if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, errors.New("unexpected signing method")
				}
				return secretBytes, nil
			})

			if parseErr != nil {
				tokenExpired := errors.Is(parseErr, jwt.ErrTokenExpired)
				ctx := ContextWithUser(r.Context(), UserContext{
					IsAuthenticated: false,
					TokenExpired:    tokenExpired,
				})
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			if !token.Valid || claims.Subject == "" {
				ctx := ContextWithUser(r.Context(), UserContext{IsAuthenticated: false})
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			parsedUUID, err := uuid.Parse(claims.Subject)
			if err != nil {
				ctx := ContextWithUser(r.Context(), UserContext{IsAuthenticated: false})
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			userCtx := UserContext{
				UserID:          parsedUUID,
				Role:            claims.Role,
				IsSuperuser:     claims.Role == "superuser",
				IsAuthenticated: true,
				TokenExpired:    false,
			}

			ctx := ContextWithUser(r.Context(), userCtx)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireAuth enforces that the request is authenticated.
// If unauthenticated and token was expired, returns 401 with code TOKEN_EXPIRED.
// Otherwise returns 401 with code UNAUTHORIZED.
func RequireAuth() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u := MustGetUserContext(r.Context())
			if !u.IsAuthenticated {
				if u.TokenExpired {
					response.Error(w, http.StatusUnauthorized, "TOKEN_EXPIRED", "Токен доступа истёк", nil)
					return
				}
				response.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "Требуется авторизация", nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
