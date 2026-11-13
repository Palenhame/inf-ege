package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"backend/internal/domain/subscriptions"
	"backend/internal/transport/http/middleware"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type fullMockSubRepo struct {
	tiers        map[int]*subscriptions.SubscriptionTier
	userSubs     map[uuid.UUID]*subscriptions.UserSubscription
	invites      map[string]*subscriptions.SubscriptionInvite
	invitesByID  map[uuid.UUID]*subscriptions.SubscriptionInvite
	inviteUses   map[string]bool
}

func newFullMockSubRepo() *fullMockSubRepo {
	return &fullMockSubRepo{
		tiers:       make(map[int]*subscriptions.SubscriptionTier),
		userSubs:    make(map[uuid.UUID]*subscriptions.UserSubscription),
		invites:     make(map[string]*subscriptions.SubscriptionInvite),
		invitesByID: make(map[uuid.UUID]*subscriptions.SubscriptionInvite),
		inviteUses:  make(map[string]bool),
	}
}

func (m *fullMockSubRepo) GetTierByID(ctx context.Context, id int) (*subscriptions.SubscriptionTier, error) {
	t, ok := m.tiers[id]
	if !ok {
		return nil, subscriptions.ErrTierNotFound
	}
	return t, nil
}

func (m *fullMockSubRepo) GetTierBySlug(ctx context.Context, slug string) (*subscriptions.SubscriptionTier, error) {
	for _, t := range m.tiers {
		if t.Slug == slug {
			return t, nil
		}
	}
	return nil, subscriptions.ErrTierNotFound
}

func (m *fullMockSubRepo) ListPublicTiers(ctx context.Context) ([]*subscriptions.SubscriptionTier, error) {
	res := make([]*subscriptions.SubscriptionTier, 0)
	for _, t := range m.tiers {
		if t.IsPublic {
			res = append(res, t)
		}
	}
	return res, nil
}

func (m *fullMockSubRepo) ListAllTiers(ctx context.Context) ([]*subscriptions.SubscriptionTier, error) {
	res := make([]*subscriptions.SubscriptionTier, 0)
	for _, t := range m.tiers {
		res = append(res, t)
	}
	return res, nil
}

func (m *fullMockSubRepo) CreateTier(ctx context.Context, tier *subscriptions.SubscriptionTier) error {
	tier.ID = len(m.tiers) + 1
	m.tiers[tier.ID] = tier
	return nil
}

func (m *fullMockSubRepo) UpdateTier(ctx context.Context, tier *subscriptions.SubscriptionTier) error {
	m.tiers[tier.ID] = tier
	return nil
}

func (m *fullMockSubRepo) GetActiveSubscription(ctx context.Context, userID uuid.UUID) (*subscriptions.UserSubscription, error) {
	sub, ok := m.userSubs[userID]
	if !ok || !sub.IsActive() {
		return nil, subscriptions.ErrNoActiveSubscription
	}
	return sub, nil
}

func (m *fullMockSubRepo) GetLatestSubscription(ctx context.Context, userID uuid.UUID) (*subscriptions.UserSubscription, error) {
	sub, ok := m.userSubs[userID]
	if !ok {
		return nil, subscriptions.ErrNoActiveSubscription
	}
	return sub, nil
}

func (m *fullMockSubRepo) CreateUserSubscription(ctx context.Context, sub *subscriptions.UserSubscription) error {
	m.userSubs[sub.UserID] = sub
	return nil
}

func (m *fullMockSubRepo) ExtendUserSubscription(ctx context.Context, userID uuid.UUID, tierID int, durationDays int, grantedBy *uuid.UUID, note *string) (*subscriptions.UserSubscription, error) {
	tier, ok := m.tiers[tierID]
	if !ok {
		return nil, subscriptions.ErrTierNotFound
	}
	now := time.Now()
	sub := &subscriptions.UserSubscription{
		ID:        uuid.New(),
		UserID:    userID,
		TierID:    tierID,
		StartsAt:  now,
		ExpiresAt: now.AddDate(0, 0, durationDays),
		Tier:      tier,
	}
	m.userSubs[userID] = sub
	return sub, nil
}

func (m *fullMockSubRepo) GetInviteByCode(ctx context.Context, code string) (*subscriptions.SubscriptionInvite, error) {
	inv, ok := m.invites[code]
	if !ok || inv.DeletedAt != nil {
		return nil, subscriptions.ErrInviteNotFound
	}
	return inv, nil
}

func (m *fullMockSubRepo) GetInviteByID(ctx context.Context, id uuid.UUID) (*subscriptions.SubscriptionInvite, error) {
	inv, ok := m.invitesByID[id]
	if !ok || inv.DeletedAt != nil {
		return nil, subscriptions.ErrInviteNotFound
	}
	return inv, nil
}

func (m *fullMockSubRepo) ListInvites(ctx context.Context, limit, offset int) ([]*subscriptions.SubscriptionInvite, int, error) {
	res := make([]*subscriptions.SubscriptionInvite, 0)
	for _, inv := range m.invites {
		if inv.DeletedAt == nil {
			res = append(res, inv)
		}
	}
	return res, len(res), nil
}

func (m *fullMockSubRepo) CreateInvite(ctx context.Context, invite *subscriptions.SubscriptionInvite) error {
	m.invites[invite.Code] = invite
	m.invitesByID[invite.ID] = invite
	return nil
}

func (m *fullMockSubRepo) DeleteInvite(ctx context.Context, id uuid.UUID) error {
	inv, ok := m.invitesByID[id]
	if !ok || inv.DeletedAt != nil {
		return subscriptions.ErrInviteNotFound
	}
	now := time.Now()
	inv.DeletedAt = &now
	return nil
}

func (m *fullMockSubRepo) ClaimInviteTx(ctx context.Context, code string, userID uuid.UUID) (*subscriptions.UserSubscription, error) {
	inv, ok := m.invites[code]
	if !ok || inv.DeletedAt != nil {
		return nil, subscriptions.ErrInviteNotFound
	}
	if inv.ExpiresAt != nil && inv.ExpiresAt.Before(time.Now()) {
		return nil, subscriptions.ErrInviteExpired
	}

	key := inv.ID.String() + ":" + userID.String()
	if m.inviteUses[key] {
		return nil, subscriptions.ErrInviteAlreadyClaimed
	}

	if inv.UsedCount >= inv.MaxUses {
		return nil, subscriptions.ErrInviteExhausted
	}

	m.inviteUses[key] = true
	inv.UsedCount++

	tier := m.tiers[inv.TierID]
	now := time.Now()
	sub := &subscriptions.UserSubscription{
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

func setupSubHandler() (*chi.Mux, *fullMockSubRepo) {
	repo := newFullMockSubRepo()
	svc := subscriptions.NewService(repo)
	h := NewSubscriptionHandler(svc)

	r := chi.NewRouter()
	h.RegisterRoutes(r)
	return r, repo
}

func TestSubHandler_PublicRoutes(t *testing.T) {
	r, repo := setupSubHandler()

	repo.tiers[1] = &subscriptions.SubscriptionTier{
		ID:       1,
		Slug:     "pro_teacher",
		Name:     "Учитель ПРО",
		PriceRub: 1490,
		IsPublic: true,
	}
	repo.tiers[2] = &subscriptions.SubscriptionTier{
		ID:       2,
		Slug:     "internal_hidden",
		Name:     "Внутренний тариф",
		PriceRub: 0,
		IsPublic: false,
	}

	// 1. GET /api/v1/subscriptions/tiers -> only public tier
	req := httptest.NewRequest(http.MethodGet, "/api/v1/subscriptions/tiers", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}

	var resp struct {
		Items []subscriptions.SubscriptionTier `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode tiers response: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].Slug != "pro_teacher" {
		t.Errorf("expected 1 public tier, got %v", resp.Items)
	}

	// 2. GET /api/v1/subscriptions/invites/{code} -> Preview
	invID := uuid.New()
	future := time.Now().Add(24 * time.Hour)
	repo.invites["WELCOME"] = &subscriptions.SubscriptionInvite{
		ID:           invID,
		Code:         "WELCOME",
		TierID:       1,
		TierName:     "Учитель ПРО",
		DurationDays: 30,
		MaxUses:      10,
		UsedCount:    2,
		ExpiresAt:    &future,
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/subscriptions/invites/WELCOME", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for preview, got %d: %s", w.Code, w.Body.String())
	}
	var preview subscriptions.InvitePreviewDTO
	if err := json.NewDecoder(w.Body).Decode(&preview); err != nil {
		t.Fatalf("failed to decode preview: %v", err)
	}
	if !preview.IsValid || preview.DurationDays != 30 {
		t.Errorf("unexpected preview data: %+v", preview)
	}
}

func TestSubHandler_ActivateInvite(t *testing.T) {
	r, repo := setupSubHandler()

	repo.tiers[1] = &subscriptions.SubscriptionTier{
		ID:       1,
		Slug:     "pro_teacher",
		Name:     "Учитель ПРО",
		PriceRub: 1490,
	}

	invID := uuid.New()
	repo.invites["PROMO100"] = &subscriptions.SubscriptionInvite{
		ID:           invID,
		Code:         "PROMO100",
		TierID:       1,
		TierName:     "Учитель ПРО",
		DurationDays: 60,
		MaxUses:      1,
		UsedCount:    0,
	}

	userID := uuid.New()

	// 1. Unauthenticated -> 401
	body := `{"code": "PROMO100"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/subscriptions/activate", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}

	// 2. Authenticated -> 200 OK
	req = httptest.NewRequest(http.MethodPost, "/api/v1/subscriptions/activate", bytes.NewBufferString(body))
	ctx := middleware.ContextWithUser(req.Context(), middleware.UserContext{
		UserID:          userID,
		Role:            "student",
		IsAuthenticated: true,
	})
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req.WithContext(ctx))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var res subscriptions.ClaimInviteResult
	if err := json.NewDecoder(w.Body).Decode(&res); err != nil {
		t.Fatalf("failed to decode claim result: %v", err)
	}
	if !res.OK || res.Role != "teacher" || res.Subscription == nil || !res.Subscription.IsActive {
		t.Errorf("unexpected claim result: %+v", res)
	}

	// 3. Second claim by same user -> 409 CONFLICT
	req = httptest.NewRequest(http.MethodPost, "/api/v1/subscriptions/activate", bytes.NewBufferString(body))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req.WithContext(ctx))

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict, got %d", w.Code)
	}
}

func TestSubHandler_AdminTiersAndInvites(t *testing.T) {
	r, repo := setupSubHandler()

	// Ensure tier 1 exists for invite creation
	repo.tiers[1] = &subscriptions.SubscriptionTier{
		ID:       1,
		Slug:     "base_tier",
		Name:     "Базовый",
		PriceRub: 500,
	}

	adminID := uuid.New()
	adminCtx := middleware.ContextWithUser(context.Background(), middleware.UserContext{
		UserID:          adminID,
		Role:            "superuser",
		IsAuthenticated: true,
		IsSuperuser:     true,
	})

	// 1. Admin Create Tier -> 201 Created
	createTierBody := `{
		"slug": "custom_school",
		"name": "Школа Плюс",
		"description": "Пакет для школ",
		"price_rub": 9900,
		"is_public": true,
		"features": {
			"max_rooms": 50,
			"max_students_per_room": 100,
			"custom_tasks_enabled": true
		}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/subscriptions/tiers", bytes.NewBufferString(createTierBody))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req.WithContext(adminCtx))

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", w.Code, w.Body.String())
	}

	// 2. Admin Create Invite -> 201 Created
	createInvBody := `{
		"code": "SCHOOL2026",
		"tier_id": 1,
		"duration_days": 90,
		"max_uses": 5,
		"note": "Акция"
	}`
	req = httptest.NewRequest(http.MethodPost, "/api/v1/admin/subscriptions/invites", bytes.NewBufferString(createInvBody))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req.WithContext(adminCtx))

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for invite, got %d: %s", w.Code, w.Body.String())
	}

	var createdInv subscriptions.SubscriptionInvite
	if err := json.NewDecoder(w.Body).Decode(&createdInv); err != nil {
		t.Fatalf("failed to decode created invite: %v", err)
	}
	if createdInv.Code != "SCHOOL2026" {
		t.Errorf("expected code SCHOOL2026, got %s", createdInv.Code)
	}

	// 3. Admin Delete Invite -> 200 OK
	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/subscriptions/invites/"+createdInv.ID.String(), nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, delReq.WithContext(adminCtx))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on delete invite, got %d", w.Code)
	}

	// 4. Try preview deleted invite -> 404
	prevReq := httptest.NewRequest(http.MethodGet, "/api/v1/subscriptions/invites/SCHOOL2026", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, prevReq)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for deleted invite, got %d", w.Code)
	}
}
