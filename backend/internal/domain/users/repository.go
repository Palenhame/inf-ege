package users

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrNotFound      = errors.New("user not found")
	ErrUsernameTaken = errors.New("username is already taken")
)

// DBTX is the minimal database execution interface satisfied by *pgxpool.Pool and pgx.Tx.
type DBTX interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Repository defines operations on the users storage table.
type Repository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*User, error)
	GetByUsername(ctx context.Context, username string) (*User, error)
	GetByYandexID(ctx context.Context, yandexID string) (*User, error)
	GetByVKID(ctx context.Context, vkID string) (*User, error)
	Create(ctx context.Context, user *User) error
	UpdateOnboarding(ctx context.Context, id uuid.UUID, firstName, lastName, username string) (*User, error)
	UpdateProfile(ctx context.Context, id uuid.UUID, firstName, lastName, username *string) (*User, error)
	UpdateRole(ctx context.Context, id uuid.UUID, role UserRole) (*User, error)
	List(ctx context.Context, q, role string, limit, offset int) ([]*User, int, error)
}

type pgRepository struct {
	db DBTX
}

// NewRepository creates a new PostgreSQL repository for users.
func NewRepository(db DBTX) Repository {
	return &pgRepository{db: db}
}

// scanUser scans a single row into a User entity.
func scanUser(row pgx.Row) (*User, error) {
	var u User
	err := row.Scan(
		&u.ID,
		&u.Username,
		&u.FirstName,
		&u.LastName,
		&u.IsOnboarded,
		&u.YandexID,
		&u.VKID,
		&u.Role,
		&u.CreatedAt,
		&u.UpdatedAt,
		&u.DeletedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan user: %w", err)
	}
	return &u, nil
}

const selectUserFields = `
	id,
	username,
	first_name,
	last_name,
	is_onboarded,
	yandex_id,
	vk_id,
	role,
	created_at,
	updated_at,
	deleted_at
`

func (r *pgRepository) GetByID(ctx context.Context, id uuid.UUID) (*User, error) {
	query := fmt.Sprintf(`SELECT %s FROM users WHERE id = $1 AND deleted_at IS NULL`, selectUserFields)
	row := r.db.QueryRow(ctx, query, id)
	return scanUser(row)
}

func (r *pgRepository) GetByUsername(ctx context.Context, username string) (*User, error) {
	query := fmt.Sprintf(`SELECT %s FROM users WHERE username = $1 AND deleted_at IS NULL`, selectUserFields)
	row := r.db.QueryRow(ctx, query, username)
	return scanUser(row)
}

func (r *pgRepository) GetByYandexID(ctx context.Context, yandexID string) (*User, error) {
	query := fmt.Sprintf(`SELECT %s FROM users WHERE yandex_id = $1 AND deleted_at IS NULL`, selectUserFields)
	row := r.db.QueryRow(ctx, query, yandexID)
	return scanUser(row)
}

func (r *pgRepository) GetByVKID(ctx context.Context, vkID string) (*User, error) {
	query := fmt.Sprintf(`SELECT %s FROM users WHERE vk_id = $1 AND deleted_at IS NULL`, selectUserFields)
	row := r.db.QueryRow(ctx, query, vkID)
	return scanUser(row)
}

func (r *pgRepository) Create(ctx context.Context, user *User) error {
	query := `
		INSERT INTO users (
			id,
			username,
			first_name,
			last_name,
			is_onboarded,
			yandex_id,
			vk_id,
			role,
			created_at,
			updated_at
		) VALUES (
			COALESCE(NULLIF($1, '00000000-0000-0000-0000-000000000000'::uuid), gen_random_uuid()),
			$2, $3, $4, $5, $6, $7, $8, now(), now()
		)
		RETURNING id, created_at, updated_at
	`
	err := r.db.QueryRow(
		ctx,
		query,
		user.ID,
		user.Username,
		user.FirstName,
		user.LastName,
		user.IsOnboarded,
		user.YandexID,
		user.VKID,
		user.Role,
	).Scan(&user.ID, &user.CreatedAt, &user.UpdatedAt)

	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrUsernameTaken
		}
		return fmt.Errorf("create user: %w", err)
	}

	return nil
}

func (r *pgRepository) UpdateOnboarding(ctx context.Context, id uuid.UUID, firstName, lastName, username string) (*User, error) {
	query := fmt.Sprintf(`
		UPDATE users
		SET first_name = $2,
		    last_name = $3,
		    username = $4,
		    is_onboarded = TRUE,
		    updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING %s
	`, selectUserFields)

	row := r.db.QueryRow(ctx, query, id, firstName, lastName, username)
	u, err := scanUser(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrUsernameTaken
		}
		return nil, err
	}
	return u, nil
}

func (r *pgRepository) UpdateProfile(ctx context.Context, id uuid.UUID, firstName, lastName, username *string) (*User, error) {
	query := fmt.Sprintf(`
		UPDATE users
		SET first_name = COALESCE($2, first_name),
		    last_name = COALESCE($3, last_name),
		    username = COALESCE($4, username),
		    updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING %s
	`, selectUserFields)

	row := r.db.QueryRow(ctx, query, id, firstName, lastName, username)
	u, err := scanUser(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrUsernameTaken
		}
		return nil, err
	}
	return u, nil
}

func (r *pgRepository) UpdateRole(ctx context.Context, id uuid.UUID, role UserRole) (*User, error) {
	query := fmt.Sprintf(`
		UPDATE users
		SET role = $2,
		    updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING %s
	`, selectUserFields)

	row := r.db.QueryRow(ctx, query, id, role)
	return scanUser(row)
}

func (r *pgRepository) List(ctx context.Context, q, role string, limit, offset int) ([]*User, int, error) {
	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	whereClauses := []string{"deleted_at IS NULL"}
	args := make([]any, 0, 4)
	argIdx := 1

	if role != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("role = $%d", argIdx))
		args = append(args, role)
		argIdx++
	}

	if q != "" {
		pattern := "%" + strings.ToLower(q) + "%"
		whereClauses = append(whereClauses, fmt.Sprintf(
			"(lower(username) LIKE $%d OR lower(first_name) LIKE $%d OR lower(last_name) LIKE $%d)",
			argIdx, argIdx, argIdx,
		))
		args = append(args, pattern)
		argIdx++
	}

	whereSQL := strings.Join(whereClauses, " AND ")

	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM users WHERE %s", whereSQL)
	var total int
	if err := r.db.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count users: %w", err)
	}

	selectSQL := fmt.Sprintf(`
		SELECT %s
		FROM users
		WHERE %s
		ORDER BY created_at DESC
		LIMIT $%d OFFSET $%d
	`, selectUserFields, whereSQL, argIdx, argIdx+1)

	args = append(args, limit, offset)

	rows, err := r.db.Query(ctx, selectSQL, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list users query: %w", err)
	}
	defer rows.Close()

	usersList := make([]*User, 0, limit)
	for rows.Next() {
		var u User
		if err := rows.Scan(
			&u.ID,
			&u.Username,
			&u.FirstName,
			&u.LastName,
			&u.IsOnboarded,
			&u.YandexID,
			&u.VKID,
			&u.Role,
			&u.CreatedAt,
			&u.UpdatedAt,
			&u.DeletedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("scan user in list: %w", err)
		}
		usersList = append(usersList, &u)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate users: %w", err)
	}

	return usersList, total, nil
}
