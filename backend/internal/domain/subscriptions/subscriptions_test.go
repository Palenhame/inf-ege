package subscriptions

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

type mockSubRepo struct {
	tiers        map[int]*SubscriptionTier
	userSubs     map[uuid.UUID]*UserSubscription
	invites      map[string]*SubscriptionInvite
	invitesByID  map[uuid.UUID]*SubscriptionInvite
	inviteUses   map[string]bool // key: inviteID:userID
}

func newMockSubRepo() *mockSubRepo {
	return &mockSubRepo{
		tiers:       make(map[int]*SubscriptionTier),
		userSubs:    make(map[uuid.UUID]*UserSubscription),
		invites:     make(map[string]*SubscriptionInvite),
		invitesByID: make(map[uuid.UUID]*SubscriptionInvite),
		inviteUses:  make(map[string]bool),
	}
}

func (m *mockSubRepo) GetTierByID(ctx context.Context, id int) (*SubscriptionTier, error) {
	t, ok := m.tiers[id]
	if !ok {
		return nil, ErrTierNotFound
	}
	return t, nil
}

func (m *mockSubRepo) GetTierBySlug(ctx context.Context, slug string) (*SubscriptionTier, error) {
	for _, t := range m.tiers {
		if t.Slug == slug {
			return t, nil
		}
	}
	return nil, ErrTierNotFound
}

func (m *mockSubRepo) ListPublicTiers(ctx context.Context) ([]*SubscriptionTier, error) {
	res := make([]*SubscriptionTier, 0)
	for _, t := range m.tiers {
		if t.IsPublic {
			res = append(res, t)
		}
	}
	return res, nil
}

func (m *mockSubRepo) ListAllTiers(ctx context.Context) ([]*SubscriptionTier, error) {
	res := make([]*SubscriptionTier, 0)
	for _, t := range m.tiers {
		res = append(res, t)
	}
	return res, nil
}

func (m *mockSubRepo) CreateTier(ctx context.Context, tier *SubscriptionTier) error {
	tier.ID = len(m.tiers) + 1
	m.tiers[tier.ID] = tier
	return nil
}

func (m *mockSubRepo) UpdateTier(ctx context.Context, tier *SubscriptionTier) error {
	m.tiers[tier.ID] = tier
	return nil
}

func (m *mockSubRepo) GetActiveSubscription(ctx context.Context, userID uuid.UUID) (*UserSubscription, error) {
	sub, ok := m.userSubs[userID]
	if !ok || !sub.IsActive() {
		return nil, ErrNoActiveSubscription
	}
	return sub, nil
}

func (m *mockSubRepo) GetLatestSubscription(ctx context.Context, userID uuid.UUID) (*UserSubscription, error) {
	sub, ok := m.userSubs[userID]
	if !ok {
		return nil, ErrNoActiveSubscription
	}
	return sub, nil
}

func (m *mockSubRepo) CreateUserSubscription(ctx context.Context, sub *UserSubscription) error {
	m.userSubs[sub.UserID] = sub
	return nil
}

func (m *mockSubRepo) ExtendUserSubscription(ctx context.Context, userID uuid.UUID, tierID int, durationDays int, grantedBy *uuid.UUID, note *string) (*UserSubscription, error) {
	tier, ok := m.tiers[tierID]
	if !ok {
		return nil, ErrTierNotFound
	}
	now := time.Now()
	sub := &UserSubscription{
		ID:        uuid.New(),
		UserID:    userID,
		TierID:    tierID,
		StartsAt:  now,
		ExpiresAt: now.AddDate(0, 0, durationDays),
		GrantedBy: grantedBy,
		Note:      note,
		Tier:      tier,
	}
	m.userSubs[userID] = sub
	return sub, nil
}

func (m *mockSubRepo) GetInviteByCode(ctx context.Context, code string) (*SubscriptionInvite, error) {
	inv, ok := m.invites[code]
	if !ok || inv.DeletedAt != nil {
		return nil, ErrInviteNotFound
	}
	return inv, nil
}

func (m *mockSubRepo) GetInviteByID(ctx context.Context, id uuid.UUID) (*SubscriptionInvite, error) {
	inv, ok := m.invitesByID[id]
	if !ok || inv.DeletedAt != nil {
		return nil, ErrInviteNotFound
	}
	return inv, nil
}

func (m *mockSubRepo) ListInvites(ctx context.Context, limit, offset int) ([]*SubscriptionInvite, int, error) {
	res := make([]*SubscriptionInvite, 0)
	for _, inv := range m.invites {
		if inv.DeletedAt == nil {
			res = append(res, inv)
		}
	}
	return res, len(res), nil
}

func (m *mockSubRepo) CreateInvite(ctx context.Context, invite *SubscriptionInvite) error {
	m.invites[invite.Code] = invite
	m.invitesByID[invite.ID] = invite
	return nil
}

func (m *mockSubRepo) DeleteInvite(ctx context.Context, id uuid.UUID) error {
	inv, ok := m.invitesByID[id]
	if !ok || inv.DeletedAt != nil {
		return ErrInviteNotFound
	}
	now := time.Now()
	inv.DeletedAt = &now
	return nil
}

func (m *mockSubRepo) ClaimInviteTx(ctx context.Context, code string, userID uuid.UUID) (*UserSubscription, error) {
	inv, ok := m.invites[code]
	if !ok || inv.DeletedAt != nil {
		return nil, ErrInviteNotFound
	}
	if inv.ExpiresAt != nil && inv.ExpiresAt.Before(time.Now()) {
		return nil, ErrInviteExpired
	}

	key := inv.ID.String() + ":" + userID.String()
	if m.inviteUses[key] {
		return nil, ErrInviteAlreadyClaimed
	}

	if inv.UsedCount >= inv.MaxUses {
		return nil, ErrInviteExhausted
	}

	m.inviteUses[key] = true
	inv.UsedCount++

	tier := m.tiers[inv.TierID]
	now := time.Now()
	sub := &UserSubscription{
		ID:        uuid.New(),
		UserID:    userID,
		TierID:    inv.TierID,
		StartsAt:  now,
		ExpiresAt: now.AddDate(0, 0, inv.DurationDays),
		Tier:      tier,
	}
	m.userSubs[userID] = sub
	return sub, nil
}

func TestCheckLimits(t *testing.T) {
	repo := newMockSubRepo()
	svc := NewService(repo)
	ctx := context.Background()

	uid := uuid.New()

	// 1. Without active subscription: default free features
	freeLimits, err := svc.CheckLimits(ctx, uid)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if freeLimits.GetMaxRooms() != 1 {
		t.Errorf("expected 1 max room on free tier, got %d", freeLimits.GetMaxRooms())
	}
	if freeLimits.HasCustomTasks() {
		t.Errorf("expected custom tasks disabled on free tier")
	}

	// 2. Grant paid PRO tier
	tier := &SubscriptionTier{
		ID:       1,
		Slug:     "pro_teacher",
		Name:     "Учитель ПРО",
		PriceRub: 1490,
		Features: TierFeatures{
			MaxRooms:             15,
			MaxMembers:           40,
			CustomTasksEnabled:   true,
			LiveCodeWatchEnabled: true,
			ProctoringEnabled:    true,
		},
	}
	_ = repo.CreateTier(ctx, tier)
	_, _ = svc.GrantSubscription(ctx, uid, 1, 30, nil, nil)

	proLimits, err := svc.CheckLimits(ctx, uid)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if proLimits.GetMaxRooms() != 15 {
		t.Errorf("expected 15 max rooms on PRO tier, got %d", proLimits.GetMaxRooms())
	}
	if !proLimits.HasCustomTasks() {
		t.Errorf("expected custom tasks enabled on PRO tier")
	}
	if !proLimits.HasLiveStream() {
		t.Errorf("expected live stream enabled on PRO tier")
	}

	// 3. Test helper checks
	canCreate, err := svc.CanCreateRoom(ctx, uid, 14)
	if err != nil || !canCreate {
		t.Errorf("expected canCreateRoom=true for 14 rooms")
	}
	canCreate, err = svc.CanCreateRoom(ctx, uid, 15)
	if err != nil || canCreate {
		t.Errorf("expected canCreateRoom=false for 15 rooms")
	}
}

func TestActivateInviteCode(t *testing.T) {
	repo := newMockSubRepo()
	svc := NewService(repo)
	ctx := context.Background()

	tier := &SubscriptionTier{
		ID:       1,
		Slug:     "teacher_basic",
		Name:     "Учитель Базовый",
		Features: TierFeatures{MaxRooms: 5, MaxMembers: 20},
	}
	_ = repo.CreateTier(ctx, tier)

	invReq := &CreateInviteRequest{
		Code:         "PROMO2026",
		TierID:       1,
		DurationDays: 30,
		MaxUses:      1,
	}
	_, err := svc.CreateInvite(ctx, invReq, nil)
	if err != nil {
		t.Fatalf("failed to create invite: %v", err)
	}

	// Preview
	preview, err := svc.GetInvitePreview(ctx, "promo2026")
	if err != nil {
		t.Fatalf("failed to preview invite: %v", err)
	}
	if !preview.IsValid || preview.DurationDays != 30 {
		t.Errorf("preview mismatch: %+v", preview)
	}

	// First activation
	user1 := uuid.New()
	sub, err := svc.ActivateInviteCode(ctx, "PROMO2026", user1)
	if err != nil {
		t.Fatalf("failed to activate invite: %v", err)
	}
	if sub == nil || !sub.IsActive() {
		t.Errorf("expected active subscription after claim")
	}

	// Second activation by same user -> ErrInviteAlreadyClaimed
	_, err = svc.ActivateInviteCode(ctx, "PROMO2026", user1)
	if err != ErrInviteAlreadyClaimed && err != ErrInviteExhausted {
		t.Errorf("expected ErrInviteAlreadyClaimed, got %v", err)
	}

	// Activation by another user -> ErrInviteExhausted (max_uses was 1)
	user2 := uuid.New()
	_, err = svc.ActivateInviteCode(ctx, "PROMO2026", user2)
	if err != ErrInviteExhausted {
		t.Errorf("expected ErrInviteExhausted, got %v", err)
	}
}
