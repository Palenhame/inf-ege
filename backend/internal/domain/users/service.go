package users

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

var (
	ErrInvalidUsername = errors.New("username must be 3-64 alphanumeric characters or underscores")
	ErrInvalidRole     = errors.New("invalid role")
	ErrInvalidName     = errors.New("first and last name must not be empty or exceed 100 characters")
)

var usernameRegex = regexp.MustCompile(`^[a-zA-Z0-9_]{3,64}$`)

// SubscriptionProvider defines the interface to fetch subscription info for UserDTO.
type SubscriptionProvider interface {
	GetUserSubscriptionInfo(ctx context.Context, userID uuid.UUID) (*UserSubscriptionInfo, error)
}

// Service provides user domain business logic.
type Service struct {
	repo         Repository
	subProvider  SubscriptionProvider
}

// NewService creates a new users domain Service.
func NewService(repo Repository, subProvider SubscriptionProvider) *Service {
	return &Service{
		repo:        repo,
		subProvider: subProvider,
	}
}

// SetSubscriptionProvider sets or updates the subscription provider.
func (s *Service) SetSubscriptionProvider(sp SubscriptionProvider) {
	s.subProvider = sp
}

// ValidateUsername checks if the username conforms to format rules.
func ValidateUsername(username string) error {
	username = strings.TrimSpace(username)
	if !usernameRegex.MatchString(username) {
		return ErrInvalidUsername
	}
	return nil
}

// ValidateRole checks if the role is a valid enum value.
func ValidateRole(role UserRole) error {
	if !role.Valid() {
		return ErrInvalidRole
	}
	return nil
}

// GetByID returns the user with the given ID.
func (s *Service) GetByID(ctx context.Context, id uuid.UUID) (*User, error) {
	return s.repo.GetByID(ctx, id)
}

// GetByUsername returns the user with the given username.
func (s *Service) GetByUsername(ctx context.Context, username string) (*User, error) {
	return s.repo.GetByUsername(ctx, username)
}

// GetByYandexID returns the user associated with a Yandex account.
func (s *Service) GetByYandexID(ctx context.Context, yandexID string) (*User, error) {
	return s.repo.GetByYandexID(ctx, yandexID)
}

// GetByVKID returns the user associated with a VK account.
func (s *Service) GetByVKID(ctx context.Context, vkID string) (*User, error) {
	return s.repo.GetByVKID(ctx, vkID)
}

// CreateUser creates a new user.
func (s *Service) CreateUser(ctx context.Context, user *User) error {
	if err := ValidateUsername(user.Username); err != nil {
		return err
	}
	if user.Role == "" {
		user.Role = RoleStudent
	}
	if err := ValidateRole(user.Role); err != nil {
		return err
	}
	return s.repo.Create(ctx, user)
}

// OnboardUser completes onboarding by saving names and username.
func (s *Service) OnboardUser(ctx context.Context, id uuid.UUID, firstName, lastName, username string) (*User, error) {
	firstName = strings.TrimSpace(firstName)
	lastName = strings.TrimSpace(lastName)
	username = strings.TrimSpace(username)

	if firstName == "" || len(firstName) > 100 || len(lastName) > 100 {
		return nil, ErrInvalidName
	}

	if err := ValidateUsername(username); err != nil {
		return nil, err
	}

	return s.repo.UpdateOnboarding(ctx, id, firstName, lastName, username)
}

// UpdateProfile updates user profile fields.
func (s *Service) UpdateProfile(ctx context.Context, id uuid.UUID, firstName, lastName, username *string) (*User, error) {
	var cleanFirst, cleanLast, cleanUser *string

	if firstName != nil {
		f := strings.TrimSpace(*firstName)
		if len(f) > 100 {
			return nil, ErrInvalidName
		}
		cleanFirst = &f
	}

	if lastName != nil {
		l := strings.TrimSpace(*lastName)
		if len(l) > 100 {
			return nil, ErrInvalidName
		}
		cleanLast = &l
	}

	if username != nil {
		u := strings.TrimSpace(*username)
		if err := ValidateUsername(u); err != nil {
			return nil, err
		}
		cleanUser = &u
	}

	return s.repo.UpdateProfile(ctx, id, cleanFirst, cleanLast, cleanUser)
}

// UpdateRole updates the role of a user (admin operation).
func (s *Service) UpdateRole(ctx context.Context, id uuid.UUID, role UserRole) (*User, error) {
	if err := ValidateRole(role); err != nil {
		return nil, err
	}
	return s.repo.UpdateRole(ctx, id, role)
}

// ListUsers lists and searches users with pagination.
func (s *Service) ListUsers(ctx context.Context, q, role string, limit, offset int) ([]*User, int, error) {
	if role != "" && !UserRole(role).Valid() {
		return nil, 0, ErrInvalidRole
	}
	return s.repo.List(ctx, q, role, limit, offset)
}

// BuildUserDTO constructs a UserDTO with attached subscription information.
func (s *Service) BuildUserDTO(ctx context.Context, u *User, impersonatedBy *uuid.UUID) (*UserDTO, error) {
	var subInfo *UserSubscriptionInfo
	if s.subProvider != nil {
		var err error
		subInfo, err = s.subProvider.GetUserSubscriptionInfo(ctx, u.ID)
		if err != nil {
			// Do not fail user DTO on subscription lookup error, log or leave nil
			subInfo = &UserSubscriptionInfo{IsActive: false, IsReadOnly: false}
		}
	} else {
		subInfo = &UserSubscriptionInfo{IsActive: false, IsReadOnly: false}
	}

	return u.ToDTO(subInfo, impersonatedBy), nil
}
