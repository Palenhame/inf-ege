package subscriptions

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"backend/internal/domain/users"
	"github.com/google/uuid"
)

var (
	ErrInvalidTierData   = errors.New("invalid subscription tier data")
	ErrInvalidInviteData = errors.New("invalid subscription invite data")
)

// ActiveSubscription represents the domain-level active subscription status.
type ActiveSubscription struct {
	IsActive   bool              `json:"is_active"`
	IsReadOnly bool              `json:"is_read_only"`
	Tier       *SubscriptionTier `json:"tier,omitempty"`
	StartsAt   *time.Time        `json:"starts_at,omitempty"`
	ExpiresAt  *time.Time        `json:"expires_at,omitempty"`
}

// Service provides subscription business logic and quota enforcement.
type Service struct {
	repo Repository
}

// NewService creates a new subscriptions domain Service.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// HasActiveSubscription satisfies middleware.SubscriptionChecker interface.
func (s *Service) HasActiveSubscription(ctx context.Context, userID uuid.UUID) (bool, error) {
	sub, err := s.repo.GetActiveSubscription(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrNoActiveSubscription) {
			return false, nil
		}
		return false, err
	}
	return sub != nil && sub.IsActive(), nil
}

// GetUserSubscriptionInfo satisfies users.SubscriptionProvider interface.
func (s *Service) GetUserSubscriptionInfo(ctx context.Context, userID uuid.UUID) (*users.UserSubscriptionInfo, error) {
	sub, err := s.repo.GetActiveSubscription(ctx, userID)
	if err == nil && sub != nil && sub.IsActive() {
		var tierInfo *users.SubscriptionTier
		if sub.Tier != nil {
			tierInfo = &users.SubscriptionTier{
				ID:       sub.Tier.ID,
				Slug:     sub.Tier.Slug,
				Name:     sub.Tier.Name,
				PriceRub: sub.Tier.PriceRub,
				Features: sub.Tier.Features,
			}
		}
		return &users.UserSubscriptionInfo{
			IsActive:   true,
			IsReadOnly: false,
			Tier:       tierInfo,
			StartsAt:   &sub.StartsAt,
			ExpiresAt:  &sub.ExpiresAt,
		}, nil
	}

	// Check if user had an expired subscription -> is_read_only = true
	latest, errLatest := s.repo.GetLatestSubscription(ctx, userID)
	if errLatest == nil && latest != nil {
		var tierInfo *users.SubscriptionTier
		if latest.Tier != nil {
			tierInfo = &users.SubscriptionTier{
				ID:       latest.Tier.ID,
				Slug:     latest.Tier.Slug,
				Name:     latest.Tier.Name,
				PriceRub: latest.Tier.PriceRub,
				Features: latest.Tier.Features,
			}
		}
		return &users.UserSubscriptionInfo{
			IsActive:   false,
			IsReadOnly: true,
			Tier:       tierInfo,
			StartsAt:   &latest.StartsAt,
			ExpiresAt:  &latest.ExpiresAt,
		}, nil
	}

	return &users.UserSubscriptionInfo{
		IsActive:   false,
		IsReadOnly: false,
	}, nil
}

// GetActiveSubscription retrieves the full active subscription or read-only state.
func (s *Service) GetActiveSubscription(ctx context.Context, userID uuid.UUID) (*ActiveSubscription, error) {
	sub, err := s.repo.GetActiveSubscription(ctx, userID)
	if err == nil && sub != nil && sub.IsActive() {
		return &ActiveSubscription{
			IsActive:   true,
			IsReadOnly: false,
			Tier:       sub.Tier,
			StartsAt:   &sub.StartsAt,
			ExpiresAt:  &sub.ExpiresAt,
		}, nil
	}

	latest, errLatest := s.repo.GetLatestSubscription(ctx, userID)
	if errLatest == nil && latest != nil {
		return &ActiveSubscription{
			IsActive:   false,
			IsReadOnly: true,
			Tier:       latest.Tier,
			StartsAt:   &latest.StartsAt,
			ExpiresAt:  &latest.ExpiresAt,
		}, nil
	}

	return &ActiveSubscription{
		IsActive:   false,
		IsReadOnly: false,
	}, nil
}

// CheckLimits returns active tier features or free defaults if none active.
func (s *Service) CheckLimits(ctx context.Context, userID uuid.UUID) (*TierFeatures, error) {
	sub, err := s.repo.GetActiveSubscription(ctx, userID)
	if err == nil && sub != nil && sub.IsActive() && sub.Tier != nil {
		return &sub.Tier.Features, nil
	}
	return &DefaultFreeFeatures, nil
}

// CanCreateRoom checks if user can create another room given their current room count.
func (s *Service) CanCreateRoom(ctx context.Context, userID uuid.UUID, currentRoomCount int) (bool, error) {
	features, err := s.CheckLimits(ctx, userID)
	if err != nil {
		return false, err
	}
	maxRooms := features.GetMaxRooms()
	if maxRooms <= 0 {
		return false, nil
	}
	return currentRoomCount < maxRooms, nil
}

// CanAddMember checks if room has capacity for another member.
func (s *Service) CanAddMember(ctx context.Context, userID uuid.UUID, currentMemberCount int) (bool, error) {
	features, err := s.CheckLimits(ctx, userID)
	if err != nil {
		return false, err
	}
	maxMembers := features.GetMaxMembers()
	if maxMembers <= 0 {
		return false, nil
	}
	return currentMemberCount < maxMembers, nil
}

// IsCustomTasksEnabled checks if user has permission to create custom tasks.
func (s *Service) IsCustomTasksEnabled(ctx context.Context, userID uuid.UUID) (bool, error) {
	features, err := s.CheckLimits(ctx, userID)
	if err != nil {
		return false, err
	}
	return features.HasCustomTasks(), nil
}

// IsLiveStreamEnabled checks if live stream / live code watching is permitted.
func (s *Service) IsLiveStreamEnabled(ctx context.Context, userID uuid.UUID) (bool, error) {
	features, err := s.CheckLimits(ctx, userID)
	if err != nil {
		return false, err
	}
	return features.HasLiveStream(), nil
}

// ActivateInviteCode claims an invite and returns the created user subscription.
func (s *Service) ActivateInviteCode(ctx context.Context, code string, userID uuid.UUID) (*UserSubscription, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return nil, ErrInviteNotFound
	}
	return s.repo.ClaimInviteTx(ctx, code, userID)
}

// GetInvitePreview returns metadata for an invite without claiming it.
func (s *Service) GetInvitePreview(ctx context.Context, code string) (*InvitePreviewDTO, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	inv, err := s.repo.GetInviteByCode(ctx, code)
	if err != nil {
		return nil, err
	}

	return &InvitePreviewDTO{
		TierName:     inv.TierName,
		DurationDays: inv.DurationDays,
		IsValid:      inv.IsValid(),
		ExpiresAt:    inv.ExpiresAt,
	}, nil
}

// CreateTier creates a new subscription tier (admin only).
func (s *Service) CreateTier(ctx context.Context, req *CreateTierRequest) (*SubscriptionTier, error) {
	req.Slug = strings.TrimSpace(req.Slug)
	req.Name = strings.TrimSpace(req.Name)
	if req.Slug == "" || req.Name == "" || req.PriceRub < 0 {
		return nil, ErrInvalidTierData
	}

	tier := &SubscriptionTier{
		Slug:        req.Slug,
		Name:        req.Name,
		Description: req.Description,
		PriceRub:    req.PriceRub,
		IsPublic:    req.IsPublic,
		SortOrder:   req.SortOrder,
		Features:    req.Features,
	}

	if err := s.repo.CreateTier(ctx, tier); err != nil {
		return nil, err
	}
	return tier, nil
}

// UpdateTier updates an existing subscription tier (admin only).
func (s *Service) UpdateTier(ctx context.Context, id int, req *UpdateTierRequest) (*SubscriptionTier, error) {
	tier, err := s.repo.GetTierByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if req.Slug != nil {
		slug := strings.TrimSpace(*req.Slug)
		if slug == "" {
			return nil, ErrInvalidTierData
		}
		tier.Slug = slug
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return nil, ErrInvalidTierData
		}
		tier.Name = name
	}
	if req.Description != nil {
		tier.Description = *req.Description
	}
	if req.PriceRub != nil {
		if *req.PriceRub < 0 {
			return nil, ErrInvalidTierData
		}
		tier.PriceRub = *req.PriceRub
	}
	if req.IsPublic != nil {
		tier.IsPublic = *req.IsPublic
	}
	if req.SortOrder != nil {
		tier.SortOrder = *req.SortOrder
	}
	if req.Features != nil {
		tier.Features = *req.Features
	}

	if err := s.repo.UpdateTier(ctx, tier); err != nil {
		return nil, err
	}
	return tier, nil
}

// ListTiers lists public or all subscription tiers.
func (s *Service) ListTiers(ctx context.Context, includePrivate bool) ([]*SubscriptionTier, error) {
	if includePrivate {
		return s.repo.ListAllTiers(ctx)
	}
	return s.repo.ListPublicTiers(ctx)
}

// GenerateRandomCode generates an unambiguous 8-character uppercase code.
func GenerateRandomCode(length int) (string, error) {
	const charset = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"
	if length <= 0 {
		length = 8
	}
	result := make([]byte, length)
	charsetLen := big.NewInt(int64(len(charset)))
	for i := range result {
		num, err := rand.Int(rand.Reader, charsetLen)
		if err != nil {
			return "", fmt.Errorf("crypto rand: %w", err)
		}
		result[i] = charset[num.Int64()]
	}
	return string(result), nil
}

// CreateInvite creates a new subscription invite (admin only).
func (s *Service) CreateInvite(ctx context.Context, req *CreateInviteRequest, createdBy *uuid.UUID) (*SubscriptionInvite, error) {
	if req.TierID <= 0 || req.DurationDays <= 0 || req.MaxUses <= 0 {
		return nil, ErrInvalidInviteData
	}

	code := strings.ToUpper(strings.TrimSpace(req.Code))
	if code == "" {
		var err error
		code, err = GenerateRandomCode(8)
		if err != nil {
			return nil, err
		}
	}

	tier, err := s.repo.GetTierByID(ctx, req.TierID)
	if err != nil {
		return nil, err
	}

	invite := &SubscriptionInvite{
		ID:           uuid.New(),
		Code:         code,
		TierID:       req.TierID,
		TierName:     tier.Name,
		DurationDays: req.DurationDays,
		MaxUses:      req.MaxUses,
		UsedCount:    0,
		ExpiresAt:    req.ExpiresAt,
		Note:         req.Note,
		CreatedBy:    createdBy,
	}

	if err := s.repo.CreateInvite(ctx, invite); err != nil {
		return nil, err
	}
	return invite, nil
}

// DeleteInvite revokes an invite code (admin only).
func (s *Service) DeleteInvite(ctx context.Context, id uuid.UUID) error {
	return s.repo.DeleteInvite(ctx, id)
}

// ListInvites lists generated invites with pagination (admin only).
func (s *Service) ListInvites(ctx context.Context, limit, offset int) ([]*SubscriptionInvite, int, error) {
	return s.repo.ListInvites(ctx, limit, offset)
}

// GrantSubscription manually grants a subscription to a user (admin only).
func (s *Service) GrantSubscription(ctx context.Context, userID uuid.UUID, tierID int, durationDays int, note *string, grantedBy *uuid.UUID) (*UserSubscription, error) {
	if durationDays <= 0 {
		return nil, errors.New("duration_days must be positive")
	}
	return s.repo.ExtendUserSubscription(ctx, userID, tierID, durationDays, grantedBy, note)
}
