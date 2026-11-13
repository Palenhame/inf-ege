package middleware

import (
	"net/http"

	"backend/internal/transport/http/response"
)

// RequireRole creates middleware that ensures the caller has one of the specified roles.
// If the caller is not authenticated, returns 401 UNAUTHORIZED.
// If caller role matches or caller is superuser, allows access.
// Otherwise returns 403 FORBIDDEN with code "FORBIDDEN".
func RequireRole(roles ...string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(roles))
	for _, r := range roles {
		allowed[r] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userCtx := MustGetUserContext(r.Context())
			if !userCtx.IsAuthenticated {
				response.Unauthorized(w, "UNAUTHORIZED", "Требуется авторизация")
				return
			}

			// Superuser has universal role access unless role set is empty
			if userCtx.IsSuperuser {
				next.ServeHTTP(w, r)
				return
			}

			if _, ok := allowed[userCtx.Role]; ok {
				next.ServeHTTP(w, r)
				return
			}

			response.Forbidden(w, "FORBIDDEN", "Недостаточно прав для выполнения действия")
		})
	}
}
