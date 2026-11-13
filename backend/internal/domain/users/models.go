package users

import (
	"time"

	"github.com/google/uuid"
)

// UserRole defines system access levels matching PostgreSQL enum user_role.
type UserRole string

const (
	RoleStudent   UserRole = "student"
	RoleTeacher   UserRole = "teacher"
	RoleSuperuser UserRole = "superuser"
)

// Valid returns whether the role is a valid recognized user_role.
func (r UserRole) Valid() bool {
	switch r {
	case RoleStudent, RoleTeacher, RoleSuperuser:
		return true
	default:
		return false
	}
}

// User represents the full database entity from the "users" table.
type User struct {
	ID          uuid.UUID  `json:"id"`
	Username    string     `json:"username"`
	FirstName   string     `json:"first_name"`
	LastName    string     `json:"last_name"`
	IsOnboarded bool       `json:"is_onboarded"`
	YandexID    *string    `json:"yandex_id,omitempty"`
	VKID        *string    `json:"vk_id,omitempty"`
	Role        UserRole   `json:"role"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	DeletedAt   *time.Time `json:"deleted_at,omitempty"`
}

// UserSubscriptionInfo represents the subscription summary inside UserDTO.
type UserSubscriptionInfo struct {
	IsActive   bool              `json:"is_active"`
	IsReadOnly bool              `json:"is_read_only"`
	Tier       *SubscriptionTier `json:"tier,omitempty"`
	StartsAt   *time.Time        `json:"starts_at,omitempty"`
	ExpiresAt  *time.Time        `json:"expires_at,omitempty"`
}

// SubscriptionTier represents subscription tier data nested in UserDTO.
type SubscriptionTier struct {
	ID       int    `json:"id"`
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	PriceRub int    `json:"price_rub,omitempty"`
	Features any    `json:"features,omitempty"`
}

// UserDTO matches Contract.md user payload for /auth/me, /user/onboarding, /admin/users.
type UserDTO struct {
	ID              uuid.UUID             `json:"id"`
	Username        string                `json:"username"`
	FirstName       string                `json:"first_name"`
	LastName        string                `json:"last_name"`
	Role            UserRole              `json:"role"`
	IsOnboarded     bool                  `json:"is_onboarded"`
	LinkedProviders []string              `json:"linked_providers,omitempty"`
	Subscription    *UserSubscriptionInfo `json:"subscription,omitempty"`
	ImpersonatedBy  *uuid.UUID            `json:"impersonated_by"`
	CreatedAt       time.Time             `json:"created_at"`
}

// ToDTO converts User into UserDTO.
func (u *User) ToDTO(subInfo *UserSubscriptionInfo, impersonatedBy *uuid.UUID) *UserDTO {
	providers := make([]string, 0, 2)
	if u.YandexID != nil && *u.YandexID != "" {
		providers = append(providers, "yandex")
	}
	if u.VKID != nil && *u.VKID != "" {
		providers = append(providers, "vk")
	}

	return &UserDTO{
		ID:              u.ID,
		Username:        u.Username,
		FirstName:       u.FirstName,
		LastName:        u.LastName,
		Role:            u.Role,
		IsOnboarded:     u.IsOnboarded,
		LinkedProviders: providers,
		Subscription:    subInfo,
		ImpersonatedBy:  impersonatedBy,
		CreatedAt:       u.CreatedAt,
	}
}

// OnboardingRequest represents body of POST /api/v1/user/onboarding.
type OnboardingRequest struct {
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

// UpdateProfileRequest represents body of PATCH /api/v1/user/profile.
type UpdateProfileRequest struct {
	FirstName *string `json:"first_name"`
	LastName  *string `json:"last_name"`
	Username  *string `json:"username"`
}

// GrantSubscriptionRequest represents optional grant_subscription in admin update.
type GrantSubscriptionRequest struct {
	TierID       int     `json:"tier_id"`
	DurationDays int     `json:"duration_days"`
	Note         *string `json:"note,omitempty"`
}

// AdminUpdateUserRequest represents body of PATCH /api/v1/admin/users/{id}.
type AdminUpdateUserRequest struct {
	Role              *UserRole                 `json:"role"`
	GrantSubscription *GrantSubscriptionRequest `json:"grant_subscription"`
}
