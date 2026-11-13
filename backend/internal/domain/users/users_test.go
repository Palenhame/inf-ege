package users

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

type mockRepo struct {
	users map[uuid.UUID]*User
}

func newMockRepo() *mockRepo {
	return &mockRepo{users: make(map[uuid.UUID]*User)}
}

func (m *mockRepo) GetByID(ctx context.Context, id uuid.UUID) (*User, error) {
	u, ok := m.users[id]
	if !ok {
		return nil, ErrNotFound
	}
	return u, nil
}

func (m *mockRepo) GetByUsername(ctx context.Context, username string) (*User, error) {
	for _, u := range m.users {
		if u.Username == username {
			return u, nil
		}
	}
	return nil, ErrNotFound
}

func (m *mockRepo) GetByYandexID(ctx context.Context, yandexID string) (*User, error) {
	for _, u := range m.users {
		if u.YandexID != nil && *u.YandexID == yandexID {
			return u, nil
		}
	}
	return nil, ErrNotFound
}

func (m *mockRepo) GetByVKID(ctx context.Context, vkID string) (*User, error) {
	for _, u := range m.users {
		if u.VKID != nil && *u.VKID == vkID {
			return u, nil
		}
	}
	return nil, ErrNotFound
}

func (m *mockRepo) Create(ctx context.Context, user *User) error {
	for _, u := range m.users {
		if u.Username == user.Username {
			return ErrUsernameTaken
		}
	}
	if user.ID == uuid.Nil {
		user.ID = uuid.New()
	}
	user.CreatedAt = time.Now()
	user.UpdatedAt = time.Now()
	m.users[user.ID] = user
	return nil
}

func (m *mockRepo) UpdateOnboarding(ctx context.Context, id uuid.UUID, firstName, lastName, username string) (*User, error) {
	u, ok := m.users[id]
	if !ok {
		return nil, ErrNotFound
	}
	for uid, existing := range m.users {
		if uid != id && existing.Username == username {
			return nil, ErrUsernameTaken
		}
	}
	u.FirstName = firstName
	u.LastName = lastName
	u.Username = username
	u.IsOnboarded = true
	u.UpdatedAt = time.Now()
	return u, nil
}

func (m *mockRepo) UpdateProfile(ctx context.Context, id uuid.UUID, firstName, lastName, username *string) (*User, error) {
	u, ok := m.users[id]
	if !ok {
		return nil, ErrNotFound
	}
	if username != nil {
		for uid, existing := range m.users {
			if uid != id && existing.Username == *username {
				return nil, ErrUsernameTaken
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
	u.UpdatedAt = time.Now()
	return u, nil
}

func (m *mockRepo) UpdateRole(ctx context.Context, id uuid.UUID, role UserRole) (*User, error) {
	u, ok := m.users[id]
	if !ok {
		return nil, ErrNotFound
	}
	u.Role = role
	u.UpdatedAt = time.Now()
	return u, nil
}

func (m *mockRepo) List(ctx context.Context, q, role string, limit, offset int) ([]*User, int, error) {
	res := make([]*User, 0)
	for _, u := range m.users {
		if role != "" && string(u.Role) != role {
			continue
		}
		res = append(res, u)
	}
	return res, len(res), nil
}

func TestValidateUsername(t *testing.T) {
	validUsernames := []string{
		"alex_123",
		"john_doe",
		"user999",
		"abc",
		"valid_user_name_with_underscores_123",
	}

	for _, u := range validUsernames {
		if err := ValidateUsername(u); err != nil {
			t.Errorf("expected username %q to be valid, got: %v", u, err)
		}
	}

	invalidUsernames := []string{
		"ab",                      // too short
		"name with spaces",        // spaces
		"user@domain",             // special chars
		"тест_юзер",               // cyrillic
		"",                        // empty
		"a-dash",                  // hyphen not allowed
		string(make([]byte, 65)),  // too long
	}

	for _, u := range invalidUsernames {
		if err := ValidateUsername(u); err == nil {
			t.Errorf("expected username %q to be invalid, got nil", u)
		}
	}
}

func TestValidateRole(t *testing.T) {
	if err := ValidateRole(RoleStudent); err != nil {
		t.Errorf("expected student to be valid role")
	}
	if err := ValidateRole(RoleTeacher); err != nil {
		t.Errorf("expected teacher to be valid role")
	}
	if err := ValidateRole(RoleSuperuser); err != nil {
		t.Errorf("expected superuser to be valid role")
	}
	if err := ValidateRole(UserRole("admin")); err == nil {
		t.Errorf("expected admin to be invalid user_role")
	}
}

func TestUserToDTO(t *testing.T) {
	yandexID := "y123"
	u := &User{
		ID:          uuid.New(),
		Username:    "test_user",
		FirstName:   "Иван",
		LastName:    "Иванов",
		Role:        RoleTeacher,
		IsOnboarded: true,
		YandexID:    &yandexID,
		CreatedAt:   time.Now(),
	}

	dto := u.ToDTO(&UserSubscriptionInfo{IsActive: true}, nil)
	if dto.Username != "test_user" {
		t.Errorf("expected username test_user, got %s", dto.Username)
	}
	if len(dto.LinkedProviders) != 1 || dto.LinkedProviders[0] != "yandex" {
		t.Errorf("expected [yandex] linked provider, got %v", dto.LinkedProviders)
	}
	if dto.Subscription == nil || !dto.Subscription.IsActive {
		t.Errorf("expected active subscription in dto")
	}
}

func TestUserService_Onboarding(t *testing.T) {
	repo := newMockRepo()
	svc := NewService(repo, nil)
	ctx := context.Background()

	uid := uuid.New()
	user := &User{
		ID:          uid,
		Username:    "tmp_user123",
		IsOnboarded: false,
		Role:        RoleStudent,
	}
	if err := repo.Create(ctx, user); err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// Onboarding with invalid username
	_, err := svc.OnboardUser(ctx, uid, "Иван", "Петров", "a")
	if err == nil {
		t.Errorf("expected error with too short username")
	}

	// Onboarding with valid data
	updated, err := svc.OnboardUser(ctx, uid, "Иван", "Петров", "ivan_p")
	if err != nil {
		t.Fatalf("unexpected error on onboarding: %v", err)
	}
	if !updated.IsOnboarded {
		t.Errorf("expected is_onboarded to be true")
	}
	if updated.Username != "ivan_p" {
		t.Errorf("expected username ivan_p, got %s", updated.Username)
	}
}
