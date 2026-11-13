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
	"backend/internal/domain/users"
	"backend/internal/transport/http/middleware"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// mockUserRepo implements users.Repository for testing.
type mockUserRepo struct {
	users map[uuid.UUID]*users.User
}

func newMockUserRepo() *mockUserRepo {
	return &mockUserRepo{users: make(map[uuid.UUID]*users.User)}
}

func (m *mockUserRepo) GetByID(ctx context.Context, id uuid.UUID) (*users.User, error) {
	u, ok := m.users[id]
	if !ok {
		return nil, users.ErrNotFound
	}
	return u, nil
}

func (m *mockUserRepo) GetByUsername(ctx context.Context, username string) (*users.User, error) {
	for _, u := range m.users {
		if u.Username == username {
			return u, nil
		}
	}
	return nil, users.ErrNotFound
}

func (m *mockUserRepo) GetByYandexID(ctx context.Context, yandexID string) (*users.User, error) {
	return nil, users.ErrNotFound
}

func (m *mockUserRepo) GetByVKID(ctx context.Context, vkID string) (*users.User, error) {
	return nil, users.ErrNotFound
}

func (m *mockUserRepo) Create(ctx context.Context, user *users.User) error {
	if user.ID == uuid.Nil {
		user.ID = uuid.New()
	}
	m.users[user.ID] = user
	return nil
}

func (m *mockUserRepo) UpdateOnboarding(ctx context.Context, id uuid.UUID, firstName, lastName, username string) (*users.User, error) {
	u, ok := m.users[id]
	if !ok {
		return nil, users.ErrNotFound
	}
	for uid, existing := range m.users {
		if uid != id && existing.Username == username {
			return nil, users.ErrUsernameTaken
		}
	}
	u.FirstName = firstName
	u.LastName = lastName
	u.Username = username
	u.IsOnboarded = true
	return u, nil
}

func (m *mockUserRepo) UpdateProfile(ctx context.Context, id uuid.UUID, firstName, lastName, username *string) (*users.User, error) {
	u, ok := m.users[id]
	if !ok {
		return nil, users.ErrNotFound
	}
	if username != nil {
		for uid, existing := range m.users {
			if uid != id && existing.Username == *username {
				return nil, users.ErrUsernameTaken
			}
		}
		u.Username = *username
	}
	if firstName != nil {
		u.FirstName = *firstName
	}
	if lastName != nil {
		u.LastName = *lastName
	}
	return u, nil
}

func (m *mockUserRepo) UpdateRole(ctx context.Context, id uuid.UUID, role users.UserRole) (*users.User, error) {
	u, ok := m.users[id]
	if !ok {
		return nil, users.ErrNotFound
	}
	u.Role = role
	return u, nil
}

func (m *mockUserRepo) List(ctx context.Context, q, role string, limit, offset int) ([]*users.User, int, error) {
	res := make([]*users.User, 0)
	for _, u := range m.users {
		if role != "" && string(u.Role) != role {
			continue
		}
		res = append(res, u)
	}
	return res, len(res), nil
}

// mockSubRepo implements subscriptions.Repository for testing.
type mockSubRepo struct {
	tiers    map[int]*subscriptions.SubscriptionTier
	userSubs map[uuid.UUID]*subscriptions.UserSubscription
}

func newMockSubRepo() *mockSubRepo {
	return &mockSubRepo{
		tiers:    make(map[int]*subscriptions.SubscriptionTier),
		userSubs: make(map[uuid.UUID]*subscriptions.UserSubscription),
	}
}

func (m *mockSubRepo) GetTierByID(ctx context.Context, id int) (*subscriptions.SubscriptionTier, error) {
	t, ok := m.tiers[id]
	if !ok {
		return nil, subscriptions.ErrTierNotFound
	}
	return t, nil
}

func (m *mockSubRepo) GetTierBySlug(ctx context.Context, slug string) (*subscriptions.SubscriptionTier, error) {
	for _, t := range m.tiers {
		if t.Slug == slug {
			return t, nil
		}
	}
	return nil, subscriptions.ErrTierNotFound
}

func (m *mockSubRepo) ListPublicTiers(ctx context.Context) ([]*subscriptions.SubscriptionTier, error) {
	res := make([]*subscriptions.SubscriptionTier, 0)
	for _, t := range m.tiers {
		if t.IsPublic {
			res = append(res, t)
		}
	}
	return res, nil
}

func (m *mockSubRepo) ListAllTiers(ctx context.Context) ([]*subscriptions.SubscriptionTier, error) {
	res := make([]*subscriptions.SubscriptionTier, 0)
	for _, t := range m.tiers {
		res = append(res, t)
	}
	return res, nil
}

func (m *mockSubRepo) CreateTier(ctx context.Context, tier *subscriptions.SubscriptionTier) error {
	tier.ID = len(m.tiers) + 1
	m.tiers[tier.ID] = tier
	return nil
}

func (m *mockSubRepo) UpdateTier(ctx context.Context, tier *subscriptions.SubscriptionTier) error {
	m.tiers[tier.ID] = tier
	return nil
}

func (m *mockSubRepo) GetActiveSubscription(ctx context.Context, userID uuid.UUID) (*subscriptions.UserSubscription, error) {
	sub, ok := m.userSubs[userID]
	if !ok || !sub.IsActive() {
		return nil, subscriptions.ErrNoActiveSubscription
	}
	return sub, nil
}

func (m *mockSubRepo) GetLatestSubscription(ctx context.Context, userID uuid.UUID) (*subscriptions.UserSubscription, error) {
	sub, ok := m.userSubs[userID]
	if !ok {
		return nil, subscriptions.ErrNoActiveSubscription
	}
	return sub, nil
}

func (m *mockSubRepo) CreateUserSubscription(ctx context.Context, sub *subscriptions.UserSubscription) error {
	m.userSubs[sub.UserID] = sub
	return nil
}

func (m *mockSubRepo) ExtendUserSubscription(ctx context.Context, userID uuid.UUID, tierID int, durationDays int, grantedBy *uuid.UUID, note *string) (*subscriptions.UserSubscription, error) {
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
		GrantedBy: grantedBy,
		Note:      note,
		Tier:      tier,
	}
	m.userSubs[userID] = sub
	return sub, nil
}

func (m *mockSubRepo) GetInviteByCode(ctx context.Context, code string) (*subscriptions.SubscriptionInvite, error) {
	return nil, subscriptions.ErrInviteNotFound
}

func (m *mockSubRepo) GetInviteByID(ctx context.Context, id uuid.UUID) (*subscriptions.SubscriptionInvite, error) {
	return nil, subscriptions.ErrInviteNotFound
}

func (m *mockSubRepo) ListInvites(ctx context.Context, limit, offset int) ([]*subscriptions.SubscriptionInvite, int, error) {
	return nil, 0, nil
}

func (m *mockSubRepo) CreateInvite(ctx context.Context, invite *subscriptions.SubscriptionInvite) error {
	return nil
}

func (m *mockSubRepo) DeleteInvite(ctx context.Context, id uuid.UUID) error {
	return nil
}

func (m *mockSubRepo) ClaimInviteTx(ctx context.Context, code string, userID uuid.UUID) (*subscriptions.UserSubscription, error) {
	return nil, nil
}

// setupUserHandler creates a test router with UserHandler mounted.
func setupUserHandler() (*chi.Mux, *mockUserRepo, *mockSubRepo) {
	userRepo := newMockUserRepo()
	subRepo := newMockSubRepo()

	subService := subscriptions.NewService(subRepo)
	userService := users.NewService(userRepo, subService)

	h := NewUserHandler(userService, subService)

	r := chi.NewRouter()
	h.RegisterRoutes(r)

	return r, userRepo, subRepo
}

func TestUserHandler_Onboarding(t *testing.T) {
	r, userRepo, _ := setupUserHandler()

	uid := uuid.New()
	userRepo.users[uid] = &users.User{
		ID:          uid,
		Username:    "init_user",
		Role:        users.RoleStudent,
		IsOnboarded: false,
	}

	// 1. Unauthenticated request -> 401
	body := `{"first_name": "Иван", "last_name": "Иванов", "username": "ivan_ivanov"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/user/onboarding", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 UNAUTHORIZED, got %d", w.Code)
	}

	// 2. Authenticated valid request -> 200
	req = httptest.NewRequest(http.MethodPost, "/api/v1/user/onboarding", bytes.NewBufferString(body))
	ctx := middleware.ContextWithUser(req.Context(), middleware.UserContext{
		UserID:          uid,
		Role:            "student",
		IsAuthenticated: true,
	})
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req.WithContext(ctx))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var resp users.UserDTO
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !resp.IsOnboarded || resp.Username != "ivan_ivanov" {
		t.Errorf("unexpected onboarding result: %+v", resp)
	}

	// 3. Duplicate username -> 409
	uid2 := uuid.New()
	userRepo.users[uid2] = &users.User{
		ID:          uid2,
		Username:    "init_user2",
		Role:        users.RoleStudent,
		IsOnboarded: false,
	}
	req = httptest.NewRequest(http.MethodPost, "/api/v1/user/onboarding", bytes.NewBufferString(body))
	ctx2 := middleware.ContextWithUser(req.Context(), middleware.UserContext{
		UserID:          uid2,
		Role:            "student",
		IsAuthenticated: true,
	})
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req.WithContext(ctx2))

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict, got %d", w.Code)
	}
}

func TestUserHandler_AdminEndpoints(t *testing.T) {
	r, userRepo, subRepo := setupUserHandler()

	adminID := uuid.New()
	targetID := uuid.New()

	userRepo.users[targetID] = &users.User{
		ID:          targetID,
		Username:    "student_1",
		Role:        users.RoleStudent,
		IsOnboarded: true,
	}

	// Setup tier in subRepo
	subRepo.tiers[1] = &subscriptions.SubscriptionTier{
		ID:       1,
		Slug:     "pro_teacher",
		Name:     "Учитель ПРО",
		PriceRub: 1490,
	}

	// 1. Non-admin accessing admin users -> 403
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil)
	ctx := middleware.ContextWithUser(req.Context(), middleware.UserContext{
		UserID:          targetID,
		Role:            "student",
		IsAuthenticated: true,
		IsSuperuser:     false,
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req.WithContext(ctx))

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden, got %d", w.Code)
	}

	// 2. Superuser list users -> 200
	req = httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil)
	adminCtx := middleware.ContextWithUser(req.Context(), middleware.UserContext{
		UserID:          adminID,
		Role:            "superuser",
		IsAuthenticated: true,
		IsSuperuser:     true,
	})
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req.WithContext(adminCtx))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}

	// 3. Superuser update role and grant sub -> 200
	updateBody := `{
		"role": "teacher",
		"grant_subscription": {
			"tier_id": 1,
			"duration_days": 30,
			"note": "Подписка от техподдержки"
		}
	}`
	req = httptest.NewRequest(http.MethodPatch, "/api/v1/admin/users/"+targetID.String(), bytes.NewBufferString(updateBody))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req.WithContext(adminCtx))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on admin update, got %d: %s", w.Code, w.Body.String())
	}

	var updatedDTO users.UserDTO
	if err := json.NewDecoder(w.Body).Decode(&updatedDTO); err != nil {
		t.Fatalf("failed to decode updated user: %v", err)
	}
	if updatedDTO.Role != users.RoleTeacher {
		t.Errorf("expected role teacher, got %s", updatedDTO.Role)
	}
	if updatedDTO.Subscription == nil || !updatedDTO.Subscription.IsActive {
		t.Errorf("expected active subscription in response")
	}
}
