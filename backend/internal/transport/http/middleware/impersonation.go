package middleware

import (
	"context"
	"net/http"
	"strings"

	"backend/internal/transport/http/response"

	"github.com/google/uuid"
)

// RoleResolver is an optional function to look up the impersonated user's role.
type RoleResolver func(ctx context.Context, targetUserID uuid.UUID) (role string, err error)

// ImpersonationOption configures the Impersonation middleware.
type ImpersonationOption func(*impersonationConfig)

type impersonationConfig struct {
	roleResolver RoleResolver
}

// WithRoleResolver attaches a role resolver to impersonation middleware.
func WithRoleResolver(resolver RoleResolver) ImpersonationOption {
	return func(c *impersonationConfig) {
		c.roleResolver = resolver
	}
}

// Impersonation inspects the "X-Impersonate-User: <uuid>" header.
// - If header is absent: passes request through.
// - If header is present:
//   - If caller is NOT authenticated or NOT superuser: returns 403 FORBIDDEN with code "FORBIDDEN".
//   - If header UUID is invalid: returns 400 VALIDATION_ERROR.
//   - If caller IS superuser: switches context UserID to target UUID, records ImpersonatedBy,
//     and updates effective role (using resolver if provided).
func Impersonation(opts ...ImpersonationOption) func(http.Handler) http.Handler {
	cfg := &impersonationConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			targetHeader := strings.TrimSpace(r.Header.Get("X-Impersonate-User"))
			if targetHeader == "" {
				next.ServeHTTP(w, r)
				return
			}

			userCtx := MustGetUserContext(r.Context())
			if !userCtx.IsAuthenticated || !userCtx.IsSuperuser {
				response.Error(w, http.StatusForbidden, "FORBIDDEN", "Действие разрешено только суперпользователю", nil)
				return
			}

			targetUUID, err := uuid.Parse(targetHeader)
			if err != nil {
				response.BadRequest(w, "VALIDATION_ERROR", "Некорректный UUID в заголовке X-Impersonate-User", nil)
				return
			}

			originalID := userCtx.UserID
			impersonatedCtx := UserContext{
				UserID:          targetUUID,
				Role:            userCtx.Role,
				IsSuperuser:     false,
				ImpersonatedBy:  &originalID,
				IsAuthenticated: true,
				TokenExpired:    false,
			}

			if cfg.roleResolver != nil {
				resolvedRole, err := cfg.roleResolver(r.Context(), targetUUID)
				if err == nil && resolvedRole != "" {
					impersonatedCtx.Role = resolvedRole
					impersonatedCtx.IsSuperuser = (resolvedRole == "superuser")
				}
			}

			newCtx := ContextWithUser(r.Context(), impersonatedCtx)
			next.ServeHTTP(w, r.WithContext(newCtx))
		})
	}
}
