package rooms

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DBExecutor represents common execution capabilities shared by *pgxpool.Pool and pgx.Tx.
type DBExecutor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Repository defines data access operations for rooms and user_rooms.
type Repository interface {
	CreateRoom(ctx context.Context, room *Room) error
	GetRoomByID(ctx context.Context, id uuid.UUID) (*Room, error)
	GetRoomByInviteCode(ctx context.Context, code string) (*Room, error)
	ListTeacherRooms(ctx context.Context, teacherID uuid.UUID) ([]RoomListItem, error)
	ListStudentRooms(ctx context.Context, studentID uuid.UUID) ([]RoomListItem, error)
	UpdateRoom(ctx context.Context, room *Room) error
	SoftDeleteRoom(ctx context.Context, id uuid.UUID) error
	GetRoomMembers(ctx context.Context, roomID uuid.UUID) ([]RoomMember, error)
	GetRoomAssignments(ctx context.Context, roomID uuid.UUID) ([]RoomAssignment, error)
	CountRoomMembers(ctx context.Context, roomID uuid.UUID) (int, error)
	CountActiveTeacherRooms(ctx context.Context, teacherID uuid.UUID) (int, error)
	IsRoomMember(ctx context.Context, roomID, userID uuid.UUID) (bool, error)
	AddRoomMember(ctx context.Context, roomID, userID uuid.UUID) error
	RemoveRoomMember(ctx context.Context, roomID, userID uuid.UUID) error
	UpdateFreeCodeS3Key(ctx context.Context, roomID, userID uuid.UUID, s3Key string) error
	GetTeacherTierLimits(ctx context.Context, teacherID uuid.UUID) (maxRooms int, maxStudentsPerRoom int, isActive bool, err error)
	GetUserName(ctx context.Context, userID uuid.UUID) (string, error)
}

// PostgresRepository implements Repository using raw SQL via jackc/pgx/v5.
type PostgresRepository struct {
	db DBExecutor
}

// NewPostgresRepository creates a new PostgresRepository.
func NewPostgresRepository(db DBExecutor) *PostgresRepository {
	return &PostgresRepository{db: db}
}

// CreateRoom inserts a new room into the rooms table.
func (r *PostgresRepository) CreateRoom(ctx context.Context, room *Room) error {
	query := `
		INSERT INTO rooms (id, name, creator_id, invite_code, max_members, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	_, err := r.db.Exec(ctx, query,
		room.ID,
		room.Name,
		room.CreatorID,
		room.InviteCode,
		room.MaxMembers,
		room.CreatedAt,
		room.UpdatedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique violation
			return fmt.Errorf("room code or id collision: %w", err)
		}
		return fmt.Errorf("failed to insert room: %w", err)
	}
	return nil
}

// GetRoomByID retrieves an active (non-deleted) room by ID.
func (r *PostgresRepository) GetRoomByID(ctx context.Context, id uuid.UUID) (*Room, error) {
	query := `
		SELECT id, name, creator_id, invite_code, max_members, created_at, updated_at, deleted_at
		FROM rooms
		WHERE id = $1 AND deleted_at IS NULL
	`
	room := &Room{}
	err := r.db.QueryRow(ctx, query, id).Scan(
		&room.ID,
		&room.Name,
		&room.CreatorID,
		&room.InviteCode,
		&room.MaxMembers,
		&room.CreatedAt,
		&room.UpdatedAt,
		&room.DeletedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRoomNotFound
		}
		return nil, fmt.Errorf("failed to query room by id: %w", err)
	}
	return room, nil
}

// GetRoomByInviteCode retrieves an active room by its 8-character invite code.
func (r *PostgresRepository) GetRoomByInviteCode(ctx context.Context, code string) (*Room, error) {
	query := `
		SELECT id, name, creator_id, invite_code, max_members, created_at, updated_at, deleted_at
		FROM rooms
		WHERE UPPER(invite_code) = UPPER($1) AND deleted_at IS NULL
	`
	room := &Room{}
	err := r.db.QueryRow(ctx, query, strings.TrimSpace(code)).Scan(
		&room.ID,
		&room.Name,
		&room.CreatorID,
		&room.InviteCode,
		&room.MaxMembers,
		&room.CreatedAt,
		&room.UpdatedAt,
		&room.DeletedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRoomNotFound
		}
		return nil, fmt.Errorf("failed to query room by invite code: %w", err)
	}
	return room, nil
}

// ListTeacherRooms returns all active rooms created by the given teacher.
func (r *PostgresRepository) ListTeacherRooms(ctx context.Context, teacherID uuid.UUID) ([]RoomListItem, error) {
	query := `
		SELECT 
			r.id,
			r.name,
			r.creator_id,
			COALESCE(NULLIF(TRIM(u.first_name || ' ' || u.last_name), ''), u.username, '') AS creator_name,
			r.invite_code,
			r.max_members,
			COUNT(ur.user_id) AS members_count,
			r.created_at
		FROM rooms r
		LEFT JOIN users u ON u.id = r.creator_id
		LEFT JOIN user_rooms ur ON ur.room_id = r.id
		WHERE r.creator_id = $1 AND r.deleted_at IS NULL
		GROUP BY r.id, r.name, r.creator_id, u.first_name, u.last_name, u.username, r.invite_code, r.max_members, r.created_at
		ORDER BY r.updated_at DESC
	`
	rows, err := r.db.Query(ctx, query, teacherID)
	if err != nil {
		return nil, fmt.Errorf("failed to list teacher rooms: %w", err)
	}
	defer rows.Close()

	items := make([]RoomListItem, 0)
	for rows.Next() {
		var item RoomListItem
		err := rows.Scan(
			&item.ID,
			&item.Name,
			&item.CreatorID,
			&item.CreatorName,
			&item.InviteCode,
			&item.MaxMembers,
			&item.MembersCount,
			&item.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan room list item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error listing teacher rooms: %w", err)
	}
	return items, nil
}

// ListStudentRooms returns all active rooms joined by the student.
func (r *PostgresRepository) ListStudentRooms(ctx context.Context, studentID uuid.UUID) ([]RoomListItem, error) {
	query := `
		SELECT 
			r.id,
			r.name,
			r.creator_id,
			COALESCE(NULLIF(TRIM(u.first_name || ' ' || u.last_name), ''), u.username, '') AS creator_name,
			r.invite_code,
			r.max_members,
			(SELECT COUNT(*) FROM user_rooms ur2 WHERE ur2.room_id = r.id) AS members_count,
			r.created_at
		FROM user_rooms ur
		JOIN rooms r ON r.id = ur.room_id
		LEFT JOIN users u ON u.id = r.creator_id
		WHERE ur.user_id = $1 AND r.deleted_at IS NULL
		ORDER BY ur.joined_at DESC
	`
	rows, err := r.db.Query(ctx, query, studentID)
	if err != nil {
		return nil, fmt.Errorf("failed to list student rooms: %w", err)
	}
	defer rows.Close()

	items := make([]RoomListItem, 0)
	for rows.Next() {
		var item RoomListItem
		err := rows.Scan(
			&item.ID,
			&item.Name,
			&item.CreatorID,
			&item.CreatorName,
			&item.InviteCode,
			&item.MaxMembers,
			&item.MembersCount,
			&item.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan student room list item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error listing student rooms: %w", err)
	}
	return items, nil
}

// UpdateRoom updates room fields.
func (r *PostgresRepository) UpdateRoom(ctx context.Context, room *Room) error {
	query := `
		UPDATE rooms
		SET name = $2, max_members = $3, invite_code = $4, updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL
	`
	tag, err := r.db.Exec(ctx, query, room.ID, room.Name, room.MaxMembers, room.InviteCode)
	if err != nil {
		return fmt.Errorf("failed to update room: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrRoomNotFound
	}
	return nil
}

// SoftDeleteRoom marks a room as deleted.
func (r *PostgresRepository) SoftDeleteRoom(ctx context.Context, id uuid.UUID) error {
	query := `
		UPDATE rooms
		SET deleted_at = now(), updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL
	`
	tag, err := r.db.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to soft-delete room: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrRoomNotFound
	}
	return nil
}

// GetRoomMembers retrieves members of a room with user profile information.
func (r *PostgresRepository) GetRoomMembers(ctx context.Context, roomID uuid.UUID) ([]RoomMember, error) {
	query := `
		SELECT 
			u.id,
			u.username,
			u.first_name,
			u.last_name,
			ur.joined_at
		FROM user_rooms ur
		JOIN users u ON u.id = ur.user_id
		WHERE ur.room_id = $1 AND u.deleted_at IS NULL
		ORDER BY ur.joined_at ASC
	`
	rows, err := r.db.Query(ctx, query, roomID)
	if err != nil {
		return nil, fmt.Errorf("failed to get room members: %w", err)
	}
	defer rows.Close()

	members := make([]RoomMember, 0)
	for rows.Next() {
		var m RoomMember
		err := rows.Scan(
			&m.UserID,
			&m.Username,
			&m.FirstName,
			&m.LastName,
			&m.JoinedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan room member: %w", err)
		}
		members = append(members, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error getting room members: %w", err)
	}
	return members, nil
}

// GetRoomAssignments retrieves assignments belonging to a room.
func (r *PostgresRepository) GetRoomAssignments(ctx context.Context, roomID uuid.UUID) ([]RoomAssignment, error) {
	query := `
		SELECT id, title, mode, settings, results_released, created_at
		FROM assignments
		WHERE room_id = $1 AND deleted_at IS NULL
		ORDER BY created_at DESC
	`
	rows, err := r.db.Query(ctx, query, roomID)
	if err != nil {
		return nil, fmt.Errorf("failed to query room assignments: %w", err)
	}
	defer rows.Close()

	assignments := make([]RoomAssignment, 0)
	for rows.Next() {
		var a RoomAssignment
		var rawSettings []byte
		err := rows.Scan(
			&a.ID,
			&a.Title,
			&a.Mode,
			&rawSettings,
			&a.ResultsReleased,
			&a.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan room assignment: %w", err)
		}
		if len(rawSettings) > 0 {
			var parsed any
			if jsonErr := json.Unmarshal(rawSettings, &parsed); jsonErr == nil {
				a.Settings = parsed
			} else {
				a.Settings = map[string]any{}
			}
		} else {
			a.Settings = map[string]any{}
		}
		assignments = append(assignments, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error getting room assignments: %w", err)
	}
	return assignments, nil
}

// CountRoomMembers counts the current members in user_rooms.
func (r *PostgresRepository) CountRoomMembers(ctx context.Context, roomID uuid.UUID) (int, error) {
	query := `SELECT COUNT(*) FROM user_rooms WHERE room_id = $1`
	var count int
	err := r.db.QueryRow(ctx, query, roomID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count room members: %w", err)
	}
	return count, nil
}

// CountActiveTeacherRooms counts active non-deleted rooms created by a teacher.
func (r *PostgresRepository) CountActiveTeacherRooms(ctx context.Context, teacherID uuid.UUID) (int, error) {
	query := `SELECT COUNT(*) FROM rooms WHERE creator_id = $1 AND deleted_at IS NULL`
	var count int
	err := r.db.QueryRow(ctx, query, teacherID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count active teacher rooms: %w", err)
	}
	return count, nil
}

// IsRoomMember checks if a user is currently a member of a room.
func (r *PostgresRepository) IsRoomMember(ctx context.Context, roomID, userID uuid.UUID) (bool, error) {
	query := `SELECT EXISTS(SELECT 1 FROM user_rooms WHERE room_id = $1 AND user_id = $2)`
	var exists bool
	err := r.db.QueryRow(ctx, query, roomID, userID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check room membership: %w", err)
	}
	return exists, nil
}

// AddRoomMember inserts a user into user_rooms.
func (r *PostgresRepository) AddRoomMember(ctx context.Context, roomID, userID uuid.UUID) error {
	query := `
		INSERT INTO user_rooms (user_id, room_id, joined_at, updated_at)
		VALUES ($1, $2, now(), now())
		ON CONFLICT (user_id, room_id) DO NOTHING
	`
	tag, err := r.db.Exec(ctx, query, userID, roomID)
	if err != nil {
		return fmt.Errorf("failed to add room member: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrAlreadyMember
	}
	return nil
}

// RemoveRoomMember deletes a record from user_rooms.
func (r *PostgresRepository) RemoveRoomMember(ctx context.Context, roomID, userID uuid.UUID) error {
	query := `DELETE FROM user_rooms WHERE room_id = $1 AND user_id = $2`
	tag, err := r.db.Exec(ctx, query, roomID, userID)
	if err != nil {
		return fmt.Errorf("failed to remove room member: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotMember
	}
	return nil
}

// UpdateFreeCodeS3Key updates free_code_s3_key in user_rooms.
func (r *PostgresRepository) UpdateFreeCodeS3Key(ctx context.Context, roomID, userID uuid.UUID, s3Key string) error {
	query := `
		UPDATE user_rooms
		SET free_code_s3_key = $3, updated_at = now()
		WHERE room_id = $1 AND user_id = $2
	`
	tag, err := r.db.Exec(ctx, query, roomID, userID, s3Key)
	if err != nil {
		return fmt.Errorf("failed to update free code s3 key: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotMember
	}
	return nil
}

// GetTeacherTierLimits queries active teacher subscription limits.
func (r *PostgresRepository) GetTeacherTierLimits(ctx context.Context, teacherID uuid.UUID) (maxRooms int, maxStudentsPerRoom int, isActive bool, err error) {
	query := `
		SELECT 
			COALESCE(st.features->>'max_rooms', '15'),
			COALESCE(st.features->>'max_students_per_room', '40'),
			(us.expires_at > now() AND us.revoked_at IS NULL)
		FROM user_subscriptions us
		JOIN subscription_tiers st ON st.id = us.tier_id
		WHERE us.user_id = $1 AND us.revoked_at IS NULL
		ORDER BY us.expires_at DESC
		LIMIT 1
	`
	var maxRoomsStr, maxStudentsStr string
	err = r.db.QueryRow(ctx, query, teacherID).Scan(&maxRoomsStr, &maxStudentsStr, &isActive)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
			// No subscription record: default limits
			return 1, 25, false, nil
		}
		return 0, 0, false, fmt.Errorf("failed to query teacher subscription tier: %w", err)
	}

	mr, err := strconv.Atoi(maxRoomsStr)
	if err != nil || mr <= 0 {
		mr = 15
	}
	ms, err := strconv.Atoi(maxStudentsStr)
	if err != nil || ms <= 0 {
		ms = 40
	}
	return mr, ms, isActive, nil
}

// GetUserName returns the user's display name or username.
func (r *PostgresRepository) GetUserName(ctx context.Context, userID uuid.UUID) (string, error) {
	query := `
		SELECT COALESCE(NULLIF(TRIM(first_name || ' ' || last_name), ''), username, '')
		FROM users
		WHERE id = $1
	`
	var name string
	err := r.db.QueryRow(ctx, query, userID).Scan(&name)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("failed to query user name: %w", err)
	}
	return name, nil
}
