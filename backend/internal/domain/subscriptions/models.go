package subscriptions

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"backend/internal/domain/users"
	"github.com/google/uuid"
)

// TierFeatures holds the JSONB feature flags for a subscription tier.
type TierFeatures struct {
	MaxRooms             int  `json:"max_rooms"`
	MaxMembers           int  `json:"max_members,omitempty"`
	MaxStudentsPerRoom   int  `json:"max_students_per_room,omitempty"`
	CustomTasksEnabled   bool `json:"custom_tasks_enabled"`
	ProctoringEnabled    bool `json:"proctoring_enabled"`
	LiveCodeWatchEnabled bool `json:"live_code_watch_enabled"`
	LiveStreamEnabled    bool `json:"live_stream_enabled"`
	MCPEnabled           bool `json:"mcp_enabled"`
}

// GetMaxRooms returns the maximum number of rooms allowed.
func (f TierFeatures) GetMaxRooms() int {
	return f.MaxRooms
}

// GetMaxMembers returns max members/students per room.
func (f TierFeatures) GetMaxMembers() int {
	if f.MaxMembers > 0 {
		return f.MaxMembers
	}
	return f.MaxStudentsPerRoom
}

// HasCustomTasks returns whether custom tasks creation is enabled.
func (f TierFeatures) HasCustomTasks() bool {
	return f.CustomTasksEnabled
}

// HasLiveStream returns whether live stream/watch is enabled.
func (f TierFeatures) HasLiveStream() bool {
	return f.LiveStreamEnabled || f.LiveCodeWatchEnabled
}

// Value implements driver.Valuer for storing in JSONB.
func (f TierFeatures) Value() (driver.Value, error) {
	return json.Marshal(f)
}

// Scan implements sql.Scanner for reading from JSONB.
func (f *TierFeatures) Scan(value any) error {
	if value == nil {
		*f = TierFeatures{}
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		return fmt.Errorf("scan TierFeatures: expected []byte, got %T", value)
	}
	return json.Unmarshal(bytes, f)
}

// DefaultFreeFeatures defines limits for teachers without an active paid subscription.
var DefaultFreeFeatures = TierFeatures{
	MaxRooms:             1,
	MaxMembers:           10,
	MaxStudentsPerRoom:   10,
	CustomTasksEnabled:   false,
	ProctoringEnabled:    false,
	LiveCodeWatchEnabled: false,
	LiveStreamEnabled:    false,
	MCPEnabled:           false,
}

// SubscriptionTier represents subscription_tiers table entity.
type SubscriptionTier struct {
	ID          int          `json:"id"`
	Slug        string       `json:"slug"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	PriceRub    int          `json:"price_rub"`
	IsPublic    bool         `json:"is_public"`
	SortOrder   int          `json:"sort_order"`
	Features    TierFeatures `json:"features"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

// UserSubscription represents user_subscriptions table entity.
type UserSubscription struct {
	ID        uuid.UUID         `json:"id"`
	UserID    uuid.UUID         `json:"user_id"`
	TierID    int               `json:"tier_id"`
	StartsAt  time.Time         `json:"starts_at"`
	ExpiresAt time.Time         `json:"expires_at"`
	GrantedBy *uuid.UUID        `json:"granted_by,omitempty"`
	Note      *string           `json:"note,omitempty"`
	RevokedAt *time.Time        `json:"revoked_at,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	Tier      *SubscriptionTier `json:"tier,omitempty"`
}

// IsActive returns whether this subscription is currently valid.
func (s *UserSubscription) IsActive() bool {
	return s.RevokedAt == nil && s.ExpiresAt.After(time.Now())
}

// SubscriptionInvite represents subscription_invites table entity.
type SubscriptionInvite struct {
	ID           uuid.UUID  `json:"id"`
	Code         string     `json:"code"`
	TierID       int        `json:"tier_id"`
	TierName     string     `json:"tier_name,omitempty"`
	DurationDays int        `json:"duration_days"`
	MaxUses      int        `json:"max_uses"`
	UsedCount    int        `json:"used_count"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	Note         *string    `json:"note,omitempty"`
	CreatedBy    *uuid.UUID `json:"created_by,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	DeletedAt    *time.Time `json:"deleted_at,omitempty"`
}

// IsValid checks if invite is unexpired and has remaining uses.
func (inv *SubscriptionInvite) IsValid() bool {
	if inv.DeletedAt != nil {
		return false
	}
	if inv.ExpiresAt != nil && inv.ExpiresAt.Before(time.Now()) {
		return false
	}
	return inv.UsedCount < inv.MaxUses
}

// SubscriptionInviteUse represents subscription_invite_uses table entity.
type SubscriptionInviteUse struct {
	InviteID uuid.UUID `json:"invite_id"`
	UserID   uuid.UUID `json:"user_id"`
	UsedAt   time.Time `json:"used_at"`
}

// CreateTierRequest represents body of POST /api/v1/admin/subscriptions/tiers.
type CreateTierRequest struct {
	Slug        string       `json:"slug"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	PriceRub    int          `json:"price_rub"`
	IsPublic    bool         `json:"is_public"`
	SortOrder   int          `json:"sort_order"`
	Features    TierFeatures `json:"features"`
}

// UpdateTierRequest represents body of PATCH /api/v1/admin/subscriptions/tiers/{id}.
type UpdateTierRequest struct {
	Slug        *string       `json:"slug,omitempty"`
	Name        *string       `json:"name,omitempty"`
	Description *string       `json:"description,omitempty"`
	PriceRub    *int          `json:"price_rub,omitempty"`
	IsPublic    *bool         `json:"is_public,omitempty"`
	SortOrder   *int          `json:"sort_order,omitempty"`
	Features    *TierFeatures `json:"features,omitempty"`
}

// CreateInviteRequest represents body of POST /api/v1/admin/subscriptions/invites.
type CreateInviteRequest struct {
	Code         string     `json:"code,omitempty"`
	TierID       int        `json:"tier_id"`
	DurationDays int        `json:"duration_days"`
	MaxUses      int        `json:"max_uses"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	Note         *string    `json:"note,omitempty"`
}

// InvitePreviewDTO represents response of GET /api/v1/subscriptions/invites/{code}.
type InvitePreviewDTO struct {
	TierName     string     `json:"tier_name"`
	DurationDays int        `json:"duration_days"`
	IsValid      bool       `json:"is_valid"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
}

// ClaimInviteResult represents response of POST /api/v1/subscriptions/invites/{code}/claim
// or POST /api/v1/subscriptions/activate.
type ClaimInviteResult struct {
	OK           bool                       `json:"ok"`
	Role         string                     `json:"role"`
	Subscription *users.UserSubscriptionInfo `json:"subscription"`
}
