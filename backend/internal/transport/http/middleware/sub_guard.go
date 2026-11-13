package middleware

import (
	"context"
	"net/http"

	"backend/internal/transport/http/response"

	"github.com/google/uuid"
)

// SubscriptionChecker checks whether a user has an active, non-expired subscription.
type SubscriptionChecker interface {
	HasActiveSubscription(ctx context.Context, userID uuid.UUID) (bool, error)
}

// SubscriptionCheckerFunc converts a function into a SubscriptionChecker.
type SubscriptionCheckerFunc func(ctx context.Context, userID uuid.UUID) (bool, error)

// HasActiveSubscription implements SubscriptionChecker.
func (f SubscriptionCheckerFunc) HasActiveSubscription(ctx context.Context, userID uuid.UUID) (bool, error) {
	return f(ctx, userID)
}

// SubscriptionGuard checks if a teacher has an active subscription.
// If the subscription is expired or inactive:
// - Mutative requests (POST, PATCH, PUT, DELETE) return 403 FORBIDDEN with code "SUBSCRIPTION_READ_ONLY"
// - Read requests (GET, HEAD, OPTIONS) pass through.
// Superusers and non-teachers pass through unconditionally.
func SubscriptionGuard(checker SubscriptionChecker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			isMutative := r.Method == http.MethodPost ||
				r.Method == http.MethodPatch ||
				r.Method == http.MethodPut ||
				r.Method == http.MethodDelete

			// Read requests always pass through
			if !isMutative {
				next.ServeHTTP(w, r)
				return
			}

			userCtx := MustGetUserContext(r.Context())
			// Only authenticated teachers are subjected to teacher subscription guard.
			// Superusers have unlimited privileges.
			if !userCtx.IsAuthenticated || userCtx.IsSuperuser || userCtx.Role != "teacher" {
				next.ServeHTTP(w, r)
				return
			}

			if checker != nil {
				active, err := checker.HasActiveSubscription(r.Context(), userCtx.UserID)
				if err != nil || !active {
					response.Error(w, http.StatusForbidden, "SUBSCRIPTION_READ_ONLY", "Срок действия подписки истёк. Группа доступна только для чтения.", nil)
					return
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}
