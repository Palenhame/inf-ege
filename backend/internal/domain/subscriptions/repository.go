package subscriptions

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrTierNotFound           = errors.New("subscription tier not found")
	ErrTierSlugTaken          = errors.New("subscription tier slug already taken")
	ErrInviteNotFound         = errors.New("subscription invite not found")
	ErrInviteExpired          = errors.New("subscription invite has expired")
	ErrInviteExhausted        = errors.New("subscription invite has exceeded max uses")
	ErrInviteAlreadyClaimed    = errors.New("subscription invite has already been claimed by this user")
	ErrNoActiveSubscription   = errors.New("no active subscription found")
)

// DBTX matches minimal query interface.
type DBTX interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Pool defines database pool supporting transactions.
type Pool interface {
	DBTX
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Repository handles persistence of tiers, user subscriptions, and invites.
type Repository interface {
	// Tiers
	GetTierByID(ctx context.Context, id int) (*SubscriptionTier, error)
	GetTierBySlug(ctx context.Context, slug string) (*SubscriptionTier, error)
	ListPublicTiers(ctx context.Context) ([]*SubscriptionTier, error)
	ListAllTiers(ctx context.Context) ([]*SubscriptionTier, error)
	CreateTier(ctx context.Context, tier *SubscriptionTier) error
	UpdateTier(ctx context.Context, tier *SubscriptionTier) error

	// User Subscriptions
	GetActiveSubscription(ctx context.Context, userID uuid.UUID) (*UserSubscription, error)
	GetLatestSubscription(ctx context.Context, userID uuid.UUID) (*UserSubscription, error)
	CreateUserSubscription(ctx context.Context, sub *UserSubscription) error
	ExtendUserSubscription(ctx context.Context, userID uuid.UUID, tierID int, durationDays int, grantedBy *uuid.UUID, note *string) (*UserSubscription, error)

	// Invites
	GetInviteByCode(ctx context.Context, code string) (*SubscriptionInvite, error)
	GetInviteByID(ctx context.Context, id uuid.UUID) (*SubscriptionInvite, error)
	ListInvites(ctx context.Context, limit, offset int) ([]*SubscriptionInvite, int, error)
	CreateInvite(ctx context.Context, invite *SubscriptionInvite) error
	DeleteInvite(ctx context.Context, id uuid.UUID) error
	ClaimInviteTx(ctx context.Context, code string, userID uuid.UUID) (*UserSubscription, error)
}

type pgRepository struct {
	pool Pool
}

// NewRepository creates a new subscriptions repository.
func NewRepository(pool Pool) Repository {
	return &pgRepository{pool: pool}
}

// scanTier scans a single tier row.
func scanTier(row pgx.Row) (*SubscriptionTier, error) {
	var t SubscriptionTier
	err := row.Scan(
		&t.ID,
		&t.Slug,
		&t.Name,
		&t.Description,
		&t.PriceRub,
		&t.IsPublic,
		&t.SortOrder,
		&t.Features,
		&t.CreatedAt,
		&t.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTierNotFound
		}
		return nil, fmt.Errorf("scan tier: %w", err)
	}
	return &t, nil
}

const selectTierFields = `
	id,
	slug,
	name,
	description,
	price_rub,
	is_public,
	sort_order,
	features,
	created_at,
	updated_at
`

func (r *pgRepository) GetTierByID(ctx context.Context, id int) (*SubscriptionTier, error) {
	query := fmt.Sprintf(`SELECT %s FROM subscription_tiers WHERE id = $1`, selectTierFields)
	row := r.pool.QueryRow(ctx, query, id)
	return scanTier(row)
}

func (r *pgRepository) GetTierBySlug(ctx context.Context, slug string) (*SubscriptionTier, error) {
	query := fmt.Sprintf(`SELECT %s FROM subscription_tiers WHERE slug = $1`, selectTierFields)
	row := r.pool.QueryRow(ctx, query, slug)
	return scanTier(row)
}

func (r *pgRepository) ListPublicTiers(ctx context.Context) ([]*SubscriptionTier, error) {
	query := fmt.Sprintf(`
		SELECT %s
		FROM subscription_tiers
		WHERE is_public = TRUE
		ORDER BY sort_order ASC, id ASC
	`, selectTierFields)

	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list public tiers: %w", err)
	}
	defer rows.Close()

	tiers := make([]*SubscriptionTier, 0)
	for rows.Next() {
		var t SubscriptionTier
		if err := rows.Scan(
			&t.ID,
			&t.Slug,
			&t.Name,
			&t.Description,
			&t.PriceRub,
			&t.IsPublic,
			&t.SortOrder,
			&t.Features,
			&t.CreatedAt,
			&t.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan public tier: %w", err)
		}
		tiers = append(tiers, &t)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate public tiers: %w", err)
	}

	return tiers, nil
}

func (r *pgRepository) ListAllTiers(ctx context.Context) ([]*SubscriptionTier, error) {
	query := fmt.Sprintf(`
		SELECT %s
		FROM subscription_tiers
		ORDER BY sort_order ASC, id ASC
	`, selectTierFields)

	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list all tiers: %w", err)
	}
	defer rows.Close()

	tiers := make([]*SubscriptionTier, 0)
	for rows.Next() {
		var t SubscriptionTier
		if err := rows.Scan(
			&t.ID,
			&t.Slug,
			&t.Name,
			&t.Description,
			&t.PriceRub,
			&t.IsPublic,
			&t.SortOrder,
			&t.Features,
			&t.CreatedAt,
			&t.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan tier: %w", err)
		}
		tiers = append(tiers, &t)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate all tiers: %w", err)
	}

	return tiers, nil
}

func (r *pgRepository) CreateTier(ctx context.Context, tier *SubscriptionTier) error {
	query := `
		INSERT INTO subscription_tiers (
			slug,
			name,
			description,
			price_rub,
			is_public,
			sort_order,
			features,
			created_at,
			updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, now(), now()
		)
		RETURNING id, created_at, updated_at
	`
	err := r.pool.QueryRow(
		ctx,
		query,
		tier.Slug,
		tier.Name,
		tier.Description,
		tier.PriceRub,
		tier.IsPublic,
		tier.SortOrder,
		tier.Features,
	).Scan(&tier.ID, &tier.CreatedAt, &tier.UpdatedAt)

	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrTierSlugTaken
		}
		return fmt.Errorf("create tier: %w", err)
	}
	return nil
}

func (r *pgRepository) UpdateTier(ctx context.Context, tier *SubscriptionTier) error {
	query := `
		UPDATE subscription_tiers
		SET slug = $2,
		    name = $3,
		    description = $4,
		    price_rub = $5,
		    is_public = $6,
		    sort_order = $7,
		    features = $8,
		    updated_at = now()
		WHERE id = $1
		RETURNING updated_at
	`
	err := r.pool.QueryRow(
		ctx,
		query,
		tier.ID,
		tier.Slug,
		tier.Name,
		tier.Description,
		tier.PriceRub,
		tier.IsPublic,
		tier.SortOrder,
		tier.Features,
	).Scan(&tier.UpdatedAt)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTierNotFound
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrTierSlugTaken
		}
		return fmt.Errorf("update tier: %w", err)
	}
	return nil
}

// scanUserSubscriptionWithTier scans user_subscription joined with subscription_tiers.
func scanUserSubscriptionWithTier(row pgx.Row) (*UserSubscription, error) {
	var us UserSubscription
	var t SubscriptionTier
	err := row.Scan(
		&us.ID,
		&us.UserID,
		&us.TierID,
		&us.StartsAt,
		&us.ExpiresAt,
		&us.GrantedBy,
		&us.Note,
		&us.RevokedAt,
		&us.CreatedAt,
		&t.ID,
		&t.Slug,
		&t.Name,
		&t.Description,
		&t.PriceRub,
		&t.IsPublic,
		&t.SortOrder,
		&t.Features,
		&t.CreatedAt,
		&t.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNoActiveSubscription
		}
		return nil, fmt.Errorf("scan user subscription: %w", err)
	}
	us.Tier = &t
	return &us, nil
}

const selectUserSubWithTierFields = `
	us.id,
	us.user_id,
	us.tier_id,
	us.starts_at,
	us.expires_at,
	us.granted_by,
	us.note,
	us.revoked_at,
	us.created_at,
	t.id,
	t.slug,
	t.name,
	t.description,
	t.price_rub,
	t.is_public,
	t.sort_order,
	t.features,
	t.created_at,
	t.updated_at
`

func (r *pgRepository) GetActiveSubscription(ctx context.Context, userID uuid.UUID) (*UserSubscription, error) {
	query := fmt.Sprintf(`
		SELECT %s
		FROM user_subscriptions us
		JOIN subscription_tiers t ON t.id = us.tier_id
		WHERE us.user_id = $1
		  AND us.revoked_at IS NULL
		  AND us.expires_at > now()
		ORDER BY us.expires_at DESC
		LIMIT 1
	`, selectUserSubWithTierFields)

	row := r.pool.QueryRow(ctx, query, userID)
	return scanUserSubscriptionWithTier(row)
}

func (r *pgRepository) GetLatestSubscription(ctx context.Context, userID uuid.UUID) (*UserSubscription, error) {
	query := fmt.Sprintf(`
		SELECT %s
		FROM user_subscriptions us
		JOIN subscription_tiers t ON t.id = us.tier_id
		WHERE us.user_id = $1
		ORDER BY us.expires_at DESC
		LIMIT 1
	`, selectUserSubWithTierFields)

	row := r.pool.QueryRow(ctx, query, userID)
	return scanUserSubscriptionWithTier(row)
}

func (r *pgRepository) CreateUserSubscription(ctx context.Context, sub *UserSubscription) error {
	query := `
		INSERT INTO user_subscriptions (
			id,
			user_id,
			tier_id,
			starts_at,
			expires_at,
			granted_by,
			note,
			created_at
		) VALUES (
			COALESCE(NULLIF($1, '00000000-0000-0000-0000-000000000000'::uuid), gen_random_uuid()),
			$2, $3, $4, $5, $6, $7, now()
		)
		RETURNING id, created_at
	`
	return r.pool.QueryRow(
		ctx,
		query,
		sub.ID,
		sub.UserID,
		sub.TierID,
		sub.StartsAt,
		sub.ExpiresAt,
		sub.GrantedBy,
		sub.Note,
	).Scan(&sub.ID, &sub.CreatedAt)
}

func (r *pgRepository) ExtendUserSubscription(ctx context.Context, userID uuid.UUID, tierID int, durationDays int, grantedBy *uuid.UUID, note *string) (*UserSubscription, error) {
	activeSub, err := r.GetActiveSubscription(ctx, userID)
	now := time.Now()
	var startsAt, expiresAt time.Time

	if err == nil && activeSub != nil {
		startsAt = activeSub.StartsAt
		if activeSub.ExpiresAt.After(now) {
			expiresAt = activeSub.ExpiresAt.AddDate(0, 0, durationDays)
		} else {
			expiresAt = now.AddDate(0, 0, durationDays)
		}
	} else {
		startsAt = now
		expiresAt = now.AddDate(0, 0, durationDays)
	}

	newSub := &UserSubscription{
		ID:        uuid.New(),
		UserID:    userID,
		TierID:    tierID,
		StartsAt:  startsAt,
		ExpiresAt: expiresAt,
		GrantedBy: grantedBy,
		Note:      note,
	}

	if err := r.CreateUserSubscription(ctx, newSub); err != nil {
		return nil, err
	}

	// Fetch tier details
	tier, err := r.GetTierByID(ctx, tierID)
	if err == nil {
		newSub.Tier = tier
	}

	return newSub, nil
}

func scanInvite(row pgx.Row) (*SubscriptionInvite, error) {
	var inv SubscriptionInvite
	err := row.Scan(
		&inv.ID,
		&inv.Code,
		&inv.TierID,
		&inv.TierName,
		&inv.DurationDays,
		&inv.MaxUses,
		&inv.UsedCount,
		&inv.ExpiresAt,
		&inv.Note,
		&inv.CreatedBy,
		&inv.CreatedAt,
		&inv.DeletedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrInviteNotFound
		}
		return nil, fmt.Errorf("scan invite: %w", err)
	}
	return &inv, nil
}

const selectInviteFields = `
	inv.id,
	inv.code,
	inv.tier_id,
	COALESCE(t.name, ''),
	inv.duration_days,
	inv.max_uses,
	inv.used_count,
	inv.expires_at,
	inv.note,
	inv.created_by,
	inv.created_at,
	inv.deleted_at
`

func (r *pgRepository) GetInviteByCode(ctx context.Context, code string) (*SubscriptionInvite, error) {
	query := fmt.Sprintf(`
		SELECT %s
		FROM subscription_invites inv
		LEFT JOIN subscription_tiers t ON t.id = inv.tier_id
		WHERE inv.code = $1 AND inv.deleted_at IS NULL
	`, selectInviteFields)

	row := r.pool.QueryRow(ctx, query, code)
	return scanInvite(row)
}

func (r *pgRepository) GetInviteByID(ctx context.Context, id uuid.UUID) (*SubscriptionInvite, error) {
	query := fmt.Sprintf(`
		SELECT %s
		FROM subscription_invites inv
		LEFT JOIN subscription_tiers t ON t.id = inv.tier_id
		WHERE inv.id = $1 AND inv.deleted_at IS NULL
	`, selectInviteFields)

	row := r.pool.QueryRow(ctx, query, id)
	return scanInvite(row)
}

func (r *pgRepository) ListInvites(ctx context.Context, limit, offset int) ([]*SubscriptionInvite, int, error) {
	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	countQuery := `SELECT COUNT(*) FROM subscription_invites WHERE deleted_at IS NULL`
	var total int
	if err := r.pool.QueryRow(ctx, countQuery).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count invites: %w", err)
	}

	selectSQL := fmt.Sprintf(`
		SELECT %s
		FROM subscription_invites inv
		LEFT JOIN subscription_tiers t ON t.id = inv.tier_id
		WHERE inv.deleted_at IS NULL
		ORDER BY inv.created_at DESC
		LIMIT $1 OFFSET $2
	`, selectInviteFields)

	rows, err := r.pool.Query(ctx, selectSQL, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list invites query: %w", err)
	}
	defer rows.Close()

	invites := make([]*SubscriptionInvite, 0, limit)
	for rows.Next() {
		var inv SubscriptionInvite
		if err := rows.Scan(
			&inv.ID,
			&inv.Code,
			&inv.TierID,
			&inv.TierName,
			&inv.DurationDays,
			&inv.MaxUses,
			&inv.UsedCount,
			&inv.ExpiresAt,
			&inv.Note,
			&inv.CreatedBy,
			&inv.CreatedAt,
			&inv.DeletedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("scan invite item: %w", err)
		}
		invites = append(invites, &inv)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate invites: %w", err)
	}

	return invites, total, nil
}

func (r *pgRepository) CreateInvite(ctx context.Context, invite *SubscriptionInvite) error {
	query := `
		INSERT INTO subscription_invites (
			id,
			code,
			tier_id,
			duration_days,
			max_uses,
			used_count,
			expires_at,
			note,
			created_by,
			created_at
		) VALUES (
			COALESCE(NULLIF($1, '00000000-0000-0000-0000-000000000000'::uuid), gen_random_uuid()),
			$2, $3, $4, $5, 0, $6, $7, $8, now()
		)
		RETURNING id, created_at
	`
	return r.pool.QueryRow(
		ctx,
		query,
		invite.ID,
		invite.Code,
		invite.TierID,
		invite.DurationDays,
		invite.MaxUses,
		invite.ExpiresAt,
		invite.Note,
		invite.CreatedBy,
	).Scan(&invite.ID, &invite.CreatedAt)
}

func (r *pgRepository) DeleteInvite(ctx context.Context, id uuid.UUID) error {
	query := `UPDATE subscription_invites SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL`
	tag, err := r.pool.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("delete invite: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrInviteNotFound
	}
	return nil
}

// ClaimInviteTx activates an invite code atomically within a database transaction.
func (r *pgRepository) ClaimInviteTx(ctx context.Context, code string, userID uuid.UUID) (*UserSubscription, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return nil, ErrInviteNotFound
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin claim tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// 1. Lock invite row
	var inv SubscriptionInvite
	lockQuery := `
		SELECT id, code, tier_id, duration_days, max_uses, used_count, expires_at, deleted_at
		FROM subscription_invites
		WHERE code = $1 AND deleted_at IS NULL
		FOR UPDATE
	`
	err = tx.QueryRow(ctx, lockQuery, code).Scan(
		&inv.ID,
		&inv.Code,
		&inv.TierID,
		&inv.DurationDays,
		&inv.MaxUses,
		&inv.UsedCount,
		&inv.ExpiresAt,
		&inv.DeletedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrInviteNotFound
		}
		return nil, fmt.Errorf("lock invite row: %w", err)
	}

	// 2. Validate expiration
	now := time.Now()
	if inv.ExpiresAt != nil && inv.ExpiresAt.Before(now) {
		return nil, ErrInviteExpired
	}

	// 3. Check if user already claimed this invite
	var exists bool
	checkQuery := `
		SELECT EXISTS(
			SELECT 1 FROM subscription_invite_uses
			WHERE invite_id = $1 AND user_id = $2
		)
	`
	if err := tx.QueryRow(ctx, checkQuery, inv.ID, userID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("check invite use: %w", err)
	}
	if exists {
		return nil, ErrInviteAlreadyClaimed
	}

	// 4. Validate uses count
	if inv.UsedCount >= inv.MaxUses {
		return nil, ErrInviteExhausted
	}

	// 5. Record use in subscription_invite_uses
	insertUseQuery := `
		INSERT INTO subscription_invite_uses (invite_id, user_id, used_at)
		VALUES ($1, $2, now())
	`
	if _, err := tx.Exec(ctx, insertUseQuery, inv.ID, userID); err != nil {
		return nil, fmt.Errorf("record invite use: %w", err)
	}

	// 6. Increment used_count on invite
	updateInviteQuery := `
		UPDATE subscription_invites
		SET used_count = used_count + 1
		WHERE id = $1
	`
	if _, err := tx.Exec(ctx, updateInviteQuery, inv.ID); err != nil {
		return nil, fmt.Errorf("increment invite used count: %w", err)
	}

	// 7. Check current active subscription for user to extend or start fresh
	var currentExpiresAt time.Time
	checkActiveQuery := `
		SELECT expires_at
		FROM user_subscriptions
		WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > now()
		ORDER BY expires_at DESC
		LIMIT 1
	`
	err = tx.QueryRow(ctx, checkActiveQuery, userID).Scan(&currentExpiresAt)
	var startsAt, expiresAt time.Time
	if err == nil && currentExpiresAt.After(now) {
		startsAt = now
		expiresAt = currentExpiresAt.AddDate(0, 0, inv.DurationDays)
	} else {
		startsAt = now
		expiresAt = now.AddDate(0, 0, inv.DurationDays)
	}

	// 8. Create user_subscriptions row
	subID := uuid.New()
	note := fmt.Sprintf("Активация инвайт-кода %s", inv.Code)
	insertSubQuery := `
		INSERT INTO user_subscriptions (
			id,
			user_id,
			tier_id,
			starts_at,
			expires_at,
			note,
			created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, now()
		)
		RETURNING created_at
	`
	var subCreatedAt time.Time
	err = tx.QueryRow(
		ctx,
		insertSubQuery,
		subID,
		userID,
		inv.TierID,
		startsAt,
		expiresAt,
		note,
	).Scan(&subCreatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert user subscription: %w", err)
	}

	// 9. Promote user role to teacher if currently student
	promoteUserQuery := `
		UPDATE users
		SET role = 'teacher',
		    updated_at = now()
		WHERE id = $1 AND role = 'student'
	`
	if _, err := tx.Exec(ctx, promoteUserQuery, userID); err != nil {
		return nil, fmt.Errorf("promote user role: %w", err)
	}

	// 10. Fetch tier details for returned object
	tierQuery := fmt.Sprintf(`SELECT %s FROM subscription_tiers WHERE id = $1`, selectTierFields)
	tierRow := tx.QueryRow(ctx, tierQuery, inv.TierID)
	tier, err := scanTier(tierRow)
	if err != nil {
		return nil, fmt.Errorf("fetch tier for subscription: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit claim tx: %w", err)
	}

	return &UserSubscription{
		ID:        subID,
		UserID:    userID,
		TierID:    inv.TierID,
		StartsAt:  startsAt,
		ExpiresAt: expiresAt,
		Note:      &note,
		CreatedAt: subCreatedAt,
		Tier:      tier,
	}, nil
}
