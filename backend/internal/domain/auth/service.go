package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrDevLoginDisabled  = errors.New("dev login is only permitted in development mode")
	ErrUnsupportedProvider = errors.New("unsupported oauth provider")
	ErrInvalidState      = errors.New("invalid or expired oauth state")
	ErrUserNotFound      = errors.New("user not found")
)

// User represents the users database entity.
type User struct {
	ID          string     `json:"id"`
	Username    string     `json:"username"`
	FirstName   string     `json:"first_name"`
	LastName    string     `json:"last_name"`
	IsOnboarded bool       `json:"is_onboarded"`
	YandexID    *string    `json:"yandex_id,omitempty"`
	VKID        *string    `json:"vk_id,omitempty"`
	Role        string     `json:"role"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	DeletedAt   *time.Time `json:"deleted_at,omitempty"`
}

// SubscriptionTierDTO represents the subscription tier in Contract.md.
type SubscriptionTierDTO struct {
	ID       int            `json:"id"`
	Slug     string         `json:"slug"`
	Name     string         `json:"name"`
	PriceRub int            `json:"price_rub"`
	Features map[string]any `json:"features"`
}

// UserSubscriptionDTO represents subscription details inside UserDTO.
type UserSubscriptionDTO struct {
	IsActive   bool                 `json:"is_active"`
	IsReadOnly bool                 `json:"is_read_only"`
	Tier       *SubscriptionTierDTO `json:"tier"`
	StartsAt   *time.Time           `json:"starts_at"`
	ExpiresAt  *time.Time           `json:"expires_at"`
}

// UserDTO matches Contract.md §2.1.
type UserDTO struct {
	ID              string               `json:"id"`
	Username        string               `json:"username"`
	FirstName       string               `json:"first_name"`
	LastName        string               `json:"last_name"`
	Role            string               `json:"role"`
	IsOnboarded     bool                 `json:"is_onboarded"`
	LinkedProviders []string             `json:"linked_providers"`
	Subscription    *UserSubscriptionDTO `json:"subscription"`
	ImpersonatedBy  *string              `json:"impersonated_by"`
	CreatedAt       time.Time            `json:"created_at"`
}

// MeResponse matches Contract.md §3.1 GET /auth/me.
type MeResponse struct {
	Authenticated bool     `json:"authenticated"`
	User          *UserDTO `json:"user"`
}

// TokenPair holds access and refresh token strings.
type TokenPair struct {
	AccessToken  string
	RefreshToken string
}

// OAuthStatePayload is serialized and stored in the short-lived state cookie.
type OAuthStatePayload struct {
	State        string `json:"state"`
	Provider     string `json:"provider"`
	RedirectTo   string `json:"redirect_to"`
	CodeVerifier string `json:"code_verifier,omitempty"`
	CreatedAt    int64  `json:"created_at"`
}

// UserRepository defines database operations for users and subscriptions.
type UserRepository interface {
	GetByID(ctx context.Context, id string) (*User, error)
	GetByYandexID(ctx context.Context, yandexID string) (*User, error)
	GetByVKID(ctx context.Context, vkID string) (*User, error)
	GetByUsername(ctx context.Context, username string) (*User, error)
	Create(ctx context.Context, user *User) error
	Update(ctx context.Context, user *User) error
	GetUserSubscription(ctx context.Context, userID string) (*UserSubscriptionDTO, error)
}

// ServiceConfig holds service dependencies and options.
type ServiceConfig struct {
	IsDevMode          bool
	BaseURL            string
	FrontendURL        string
	BootstrapSuperuserYandexID string
	BootstrapSuperuserVKID     string
}

// Service coordinates authentication workflows.
type Service struct {
	cfg        ServiceConfig
	jwtMgr     *JWTManager
	sessionMgr *SessionManager
	userRepo   UserRepository
	yandex     *YandexProvider
	vk         *VKProvider
}

// NewService creates a new auth Service.
func NewService(
	cfg ServiceConfig,
	jwtMgr *JWTManager,
	sessionMgr *SessionManager,
	userRepo UserRepository,
	yandex *YandexProvider,
	vk *VKProvider,
) *Service {
	return &Service{
		cfg:        cfg,
		jwtMgr:     jwtMgr,
		sessionMgr: sessionMgr,
		userRepo:   userRepo,
		yandex:     yandex,
		vk:         vk,
	}
}

// InitiateOAuth generates the authorization URL and state cookie payload for provider login.
func (s *Service) InitiateOAuth(providerName, redirectTo string) (authURL string, stateCookieValue string, err error) {
	stateBytes := make([]byte, 16)
	if _, err := rand.Read(stateBytes); err != nil {
		return "", "", fmt.Errorf("failed to generate random state: %w", err)
	}
	state := hex.EncodeToString(stateBytes)

	payload := OAuthStatePayload{
		State:      state,
		Provider:   providerName,
		RedirectTo: redirectTo,
		CreatedAt:  time.Now().Unix(),
	}

	switch providerName {
	case "yandex":
		if s.yandex == nil {
			return "", "", errors.New("yandex oauth provider is not configured")
		}
		authURL, err = s.yandex.BuildAuthURL(state)
		if err != nil {
			return "", "", err
		}

	case "vk":
		if s.vk == nil {
			return "", "", errors.New("vk oauth provider is not configured")
		}
		pkce, err := GeneratePKCE()
		if err != nil {
			return "", "", fmt.Errorf("failed to generate pkce for vk: %w", err)
		}
		payload.CodeVerifier = pkce.Verifier
		authURL, err = s.vk.BuildAuthURL(state, pkce.Challenge)
		if err != nil {
			return "", "", err
		}

	default:
		return "", "", ErrUnsupportedProvider
	}

	bytes, err := json.Marshal(payload)
	if err != nil {
		return "", "", fmt.Errorf("failed to marshal state payload: %w", err)
	}

	encoded := base64.RawURLEncoding.EncodeToString(bytes)
	return authURL, encoded, nil
}

// HandleOAuthCallback processes the callback from an OAuth provider, creates/finds the user, and issues tokens.
func (s *Service) HandleOAuthCallback(
	ctx context.Context,
	providerName, code, state, stateCookieValue string,
) (tokens TokenPair, redirectURL string, userDTO *UserDTO, err error) {
	if stateCookieValue == "" {
		return tokens, "", nil, fmt.Errorf("%w: missing state cookie", ErrInvalidState)
	}

	decodedBytes, decodeErr := base64.RawURLEncoding.DecodeString(stateCookieValue)
	if decodeErr != nil {
		// Fallback to raw string in case cookie was set unencoded
		decodedBytes = []byte(stateCookieValue)
	}

	var statePayload OAuthStatePayload
	if err := json.Unmarshal(decodedBytes, &statePayload); err != nil {
		return tokens, "", nil, fmt.Errorf("%w: malformed state cookie", ErrInvalidState)
	}

	if statePayload.State != state || statePayload.Provider != providerName {
		return tokens, "", nil, fmt.Errorf("%w: state or provider mismatch", ErrInvalidState)
	}

	var userInfo *OAuthUserInfo
	switch providerName {
	case "yandex":
		if s.yandex == nil {
			return tokens, "", nil, errors.New("yandex provider not configured")
		}
		userInfo, err = s.yandex.ExchangeCodeAndGetUserInfo(ctx, code)
		if err != nil {
			return tokens, "", nil, fmt.Errorf("yandex exchange failed: %w", err)
		}

	case "vk":
		if s.vk == nil {
			return tokens, "", nil, errors.New("vk provider not configured")
		}
		userInfo, err = s.vk.ExchangeCodeAndGetUserInfo(ctx, code, statePayload.CodeVerifier)
		if err != nil {
			return tokens, "", nil, fmt.Errorf("vk exchange failed: %w", err)
		}

	default:
		return tokens, "", nil, ErrUnsupportedProvider
	}

	// Find or create user
	user, err := s.findOrCreateUserByOAuth(ctx, userInfo)
	if err != nil {
		return tokens, "", nil, fmt.Errorf("failed to process oauth user: %w", err)
	}

	// Issue refresh session in Redis
	refreshToken, err := s.sessionMgr.CreateSession(ctx, user.ID)
	if err != nil {
		return tokens, "", nil, fmt.Errorf("failed to create refresh session: %w", err)
	}

	// Issue access JWT
	accessToken, err := s.jwtMgr.GenerateAccessToken(user.ID, user.Role)
	if err != nil {
		return tokens, "", nil, fmt.Errorf("failed to generate access token: %w", err)
	}

	tokens = TokenPair{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	}

	userDTO, err = s.buildUserDTO(ctx, user, nil)
	if err != nil {
		return tokens, "", nil, fmt.Errorf("failed to build user DTO: %w", err)
	}

	redirectURL = statePayload.RedirectTo
	if redirectURL == "" {
		redirectURL = "/"
	}

	return tokens, redirectURL, userDTO, nil
}

func (s *Service) findOrCreateUserByOAuth(ctx context.Context, info *OAuthUserInfo) (*User, error) {
	var user *User
	var err error

	switch info.Provider {
	case "yandex":
		user, err = s.userRepo.GetByYandexID(ctx, info.ProviderID)
	case "vk":
		user, err = s.userRepo.GetByVKID(ctx, info.ProviderID)
	}

	if err != nil && !errors.Is(err, pgx.ErrNoRows) && !errors.Is(err, ErrUserNotFound) {
		return nil, fmt.Errorf("database query error: %w", err)
	}

	// Check bootstrap superuser override
	isBootstrapSuperuser := info.IsSuperuser
	if info.Provider == "yandex" && s.cfg.BootstrapSuperuserYandexID != "" && s.cfg.BootstrapSuperuserYandexID == info.ProviderID {
		isBootstrapSuperuser = true
	}
	if info.Provider == "vk" && s.cfg.BootstrapSuperuserVKID != "" && s.cfg.BootstrapSuperuserVKID == info.ProviderID {
		isBootstrapSuperuser = true
	}

	if user != nil {
		// Existing user: check if superuser promotion is warranted
		if isBootstrapSuperuser && user.Role != "superuser" {
			user.Role = "superuser"
			_ = s.userRepo.Update(ctx, user)
		}
		return user, nil
	}

	// New user creation
	role := "student"
	if isBootstrapSuperuser {
		role = "superuser"
	}

	randomHex := make([]byte, 4)
	_, _ = rand.Read(randomHex)
	uniqueUsername := fmt.Sprintf("user_%s", hex.EncodeToString(randomHex))

	newUser := &User{
		ID:          uuid.NewString(),
		Username:    uniqueUsername,
		FirstName:   info.FirstName,
		LastName:    info.LastName,
		IsOnboarded: false,
		Role:        role,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}

	if info.Provider == "yandex" {
		newUser.YandexID = &info.ProviderID
	} else if info.Provider == "vk" {
		newUser.VKID = &info.ProviderID
	}

	if err := s.userRepo.Create(ctx, newUser); err != nil {
		return nil, fmt.Errorf("failed to create new user: %w", err)
	}

	return newUser, nil
}

// DevLogin creates or logs in a mock user with the requested role in development mode.
func (s *Service) DevLogin(ctx context.Context, role, username string) (TokenPair, *UserDTO, error) {
	if !s.cfg.IsDevMode {
		return TokenPair{}, nil, ErrDevLoginDisabled
	}

	if role == "" {
		role = "student"
	}
	role = strings.ToLower(strings.TrimSpace(role))
	if role != "student" && role != "teacher" && role != "superuser" {
		return TokenPair{}, nil, errors.New("invalid role for dev login: must be student, teacher, or superuser")
	}

	if username == "" {
		username = fmt.Sprintf("dev_%s", role)
	}

	user, err := s.userRepo.GetByUsername(ctx, username)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) && !errors.Is(err, ErrUserNotFound) {
		return TokenPair{}, nil, fmt.Errorf("failed to find dev user: %w", err)
	}

	if user == nil {
		user = &User{
			ID:          uuid.NewString(),
			Username:    username,
			FirstName:   "Dev",
			LastName:    strings.Title(role),
			IsOnboarded: true,
			Role:        role,
			CreatedAt:   time.Now().UTC(),
			UpdatedAt:   time.Now().UTC(),
		}
		if err := s.userRepo.Create(ctx, user); err != nil {
			return TokenPair{}, nil, fmt.Errorf("failed to insert dev user: %w", err)
		}
	} else if user.Role != role {
		user.Role = role
		_ = s.userRepo.Update(ctx, user)
	}

	refreshToken, err := s.sessionMgr.CreateSession(ctx, user.ID)
	if err != nil {
		return TokenPair{}, nil, fmt.Errorf("failed to create dev refresh session: %w", err)
	}

	accessToken, err := s.jwtMgr.GenerateAccessToken(user.ID, user.Role)
	if err != nil {
		return TokenPair{}, nil, fmt.Errorf("failed to generate dev access token: %w", err)
	}

	tokens := TokenPair{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	}

	dto, err := s.buildUserDTO(ctx, user, nil)
	if err != nil {
		return tokens, nil, err
	}

	return tokens, dto, nil
}

// RefreshToken validates and rotates the refresh token session in Redis, returning fresh tokens.
func (s *Service) RefreshToken(ctx context.Context, refreshToken string) (TokenPair, error) {
	if refreshToken == "" {
		return TokenPair{}, ErrInvalidSession
	}

	newRefreshToken, userID, err := s.sessionMgr.RotateSession(ctx, refreshToken)
	if err != nil {
		return TokenPair{}, err
	}

	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return TokenPair{}, fmt.Errorf("failed to load user during refresh: %w", err)
	}

	newAccessToken, err := s.jwtMgr.GenerateAccessToken(user.ID, user.Role)
	if err != nil {
		return TokenPair{}, fmt.Errorf("failed to generate new access token: %w", err)
	}

	return TokenPair{
		AccessToken:  newAccessToken,
		RefreshToken: newRefreshToken,
	}, nil
}

// Logout revokes the session from Redis.
func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	if refreshToken == "" {
		return nil
	}
	return s.sessionMgr.RevokeSession(ctx, refreshToken)
}

// GetMe retrieves current user information for /auth/me.
// If userID is empty, returns an unauthenticated response without error.
// If impersonatorID is provided, it is reflected in the UserDTO.
func (s *Service) GetMe(ctx context.Context, userID string, impersonatorID *string) (*MeResponse, error) {
	if userID == "" {
		return &MeResponse{Authenticated: false, User: nil}, nil
	}

	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, ErrUserNotFound) {
			return &MeResponse{Authenticated: false, User: nil}, nil
		}
		return nil, fmt.Errorf("failed to fetch user: %w", err)
	}

	dto, err := s.buildUserDTO(ctx, user, impersonatorID)
	if err != nil {
		return nil, err
	}

	return &MeResponse{
		Authenticated: true,
		User:          dto,
	}, nil
}

func (s *Service) buildUserDTO(ctx context.Context, user *User, impersonatorID *string) (*UserDTO, error) {
	linked := make([]string, 0, 2)
	if user.YandexID != nil && *user.YandexID != "" {
		linked = append(linked, "yandex")
	}
	if user.VKID != nil && *user.VKID != "" {
		linked = append(linked, "vk")
	}

	sub, err := s.userRepo.GetUserSubscription(ctx, user.ID)
	if err != nil {
		// Log or suppress subscription retrieval errors for robust profile view
		sub = nil
	}

	var impID *string
	if impersonatorID != nil && *impersonatorID != "" {
		val := *impersonatorID
		impID = &val
	}

	return &UserDTO{
		ID:              user.ID,
		Username:        user.Username,
		FirstName:       user.FirstName,
		LastName:        user.LastName,
		Role:            user.Role,
		IsOnboarded:     user.IsOnboarded,
		LinkedProviders: linked,
		Subscription:    sub,
		ImpersonatedBy:  impID,
		CreatedAt:       user.CreatedAt,
	}, nil
}

// PGUserRepository implements UserRepository using pgxpool.Pool.
type PGUserRepository struct {
	pool *pgxpool.Pool
}

// NewPGUserRepository creates a new postgres user repository.
func NewPGUserRepository(pool *pgxpool.Pool) *PGUserRepository {
	return &PGUserRepository{pool: pool}
}

func (r *PGUserRepository) GetByID(ctx context.Context, id string) (*User, error) {
	query := `
		SELECT id, username, first_name, last_name, is_onboarded, yandex_id, vk_id, role, created_at, updated_at, deleted_at
		FROM users
		WHERE id = $1 AND deleted_at IS NULL
	`
	var u User
	err := r.pool.QueryRow(ctx, query, id).Scan(
		&u.ID, &u.Username, &u.FirstName, &u.LastName, &u.IsOnboarded,
		&u.YandexID, &u.VKID, &u.Role, &u.CreatedAt, &u.UpdatedAt, &u.DeletedAt,
	)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *PGUserRepository) GetByYandexID(ctx context.Context, yandexID string) (*User, error) {
	query := `
		SELECT id, username, first_name, last_name, is_onboarded, yandex_id, vk_id, role, created_at, updated_at, deleted_at
		FROM users
		WHERE yandex_id = $1 AND deleted_at IS NULL
	`
	var u User
	err := r.pool.QueryRow(ctx, query, yandexID).Scan(
		&u.ID, &u.Username, &u.FirstName, &u.LastName, &u.IsOnboarded,
		&u.YandexID, &u.VKID, &u.Role, &u.CreatedAt, &u.UpdatedAt, &u.DeletedAt,
	)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *PGUserRepository) GetByVKID(ctx context.Context, vkID string) (*User, error) {
	query := `
		SELECT id, username, first_name, last_name, is_onboarded, yandex_id, vk_id, role, created_at, updated_at, deleted_at
		FROM users
		WHERE vk_id = $1 AND deleted_at IS NULL
	`
	var u User
	err := r.pool.QueryRow(ctx, query, vkID).Scan(
		&u.ID, &u.Username, &u.FirstName, &u.LastName, &u.IsOnboarded,
		&u.YandexID, &u.VKID, &u.Role, &u.CreatedAt, &u.UpdatedAt, &u.DeletedAt,
	)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *PGUserRepository) GetByUsername(ctx context.Context, username string) (*User, error) {
	query := `
		SELECT id, username, first_name, last_name, is_onboarded, yandex_id, vk_id, role, created_at, updated_at, deleted_at
		FROM users
		WHERE username = $1 AND deleted_at IS NULL
	`
	var u User
	err := r.pool.QueryRow(ctx, query, username).Scan(
		&u.ID, &u.Username, &u.FirstName, &u.LastName, &u.IsOnboarded,
		&u.YandexID, &u.VKID, &u.Role, &u.CreatedAt, &u.UpdatedAt, &u.DeletedAt,
	)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *PGUserRepository) Create(ctx context.Context, u *User) error {
	query := `
		INSERT INTO users (id, username, first_name, last_name, is_onboarded, yandex_id, vk_id, role, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`
	now := time.Now().UTC()
	if u.CreatedAt.IsZero() {
		u.CreatedAt = now
	}
	if u.UpdatedAt.IsZero() {
		u.UpdatedAt = now
	}

	_, err := r.pool.Exec(ctx, query,
		u.ID, u.Username, u.FirstName, u.LastName, u.IsOnboarded,
		u.YandexID, u.VKID, u.Role, u.CreatedAt, u.UpdatedAt,
	)
	return err
}

func (r *PGUserRepository) Update(ctx context.Context, u *User) error {
	query := `
		UPDATE users
		SET username = $2, first_name = $3, last_name = $4, is_onboarded = $5, role = $6, updated_at = now()
		WHERE id = $1
	`
	_, err := r.pool.Exec(ctx, query, u.ID, u.Username, u.FirstName, u.LastName, u.IsOnboarded, u.Role)
	return err
}

func (r *PGUserRepository) GetUserSubscription(ctx context.Context, userID string) (*UserSubscriptionDTO, error) {
	query := `
		SELECT us.starts_at, us.expires_at, st.id, st.slug, st.name, st.price_rub, st.features
		FROM user_subscriptions us
		JOIN subscription_tiers st ON us.tier_id = st.id
		WHERE us.user_id = $1 AND us.revoked_at IS NULL
		ORDER BY us.expires_at DESC
		LIMIT 1
	`
	var startsAt, expiresAt time.Time
	var tierID, priceRub int
	var slug, name string
	var featuresBytes []byte

	err := r.pool.QueryRow(ctx, query, userID).Scan(
		&startsAt, &expiresAt, &tierID, &slug, &name, &priceRub, &featuresBytes,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	features := make(map[string]any)
	if len(featuresBytes) > 0 {
		_ = json.Unmarshal(featuresBytes, &features)
	}

	now := time.Now().UTC()
	isActive := expiresAt.After(now)
	isReadOnly := !isActive

	return &UserSubscriptionDTO{
		IsActive:   isActive,
		IsReadOnly: isReadOnly,
		Tier: &SubscriptionTierDTO{
			ID:       tierID,
			Slug:     slug,
			Name:     name,
			PriceRub: priceRub,
			Features: features,
		},
		StartsAt:  &startsAt,
		ExpiresAt: &expiresAt,
	}, nil
}
