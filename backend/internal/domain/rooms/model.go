package rooms

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Common domain errors for rooms.
var (
	ErrRoomNotFound         = errors.New("room not found")
	ErrAlreadyMember        = errors.New("user is already a member of this room")
	ErrNotMember            = errors.New("user is not a member of this room")
	ErrNotCreator           = errors.New("user is not the creator of this room")
	ErrRoomFull             = errors.New("room capacity reached")
	ErrLimitExceeded        = errors.New("resource quota limit exceeded")
	ErrSubscriptionReadOnly = errors.New("subscription expired: room is read-only")
	ErrSubscriptionRequired = errors.New("active subscription required")
	ErrInvalidInput         = errors.New("invalid input parameters")
)

// Room represents the database record in `rooms`.
type Room struct {
	ID         uuid.UUID  `json:"id"`
	Name       string     `json:"name"`
	CreatorID  uuid.UUID  `json:"creator_id"`
	InviteCode string     `json:"invite_code"`
	MaxMembers int        `json:"max_members"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	DeletedAt  *time.Time `json:"deleted_at,omitempty"`
}

// UserRoom represents the membership link in `user_rooms`.
type UserRoom struct {
	UserID        uuid.UUID `json:"user_id"`
	RoomID        uuid.UUID `json:"room_id"`
	FreeCodeS3Key *string   `json:"free_code_s3_key,omitempty"`
	JoinedAt      time.Time `json:"joined_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// RoomMember represents a student member in the room roster.
type RoomMember struct {
	UserID    uuid.UUID `json:"user_id"`
	Username  string    `json:"username"`
	FirstName string    `json:"first_name"`
	LastName  string    `json:"last_name"`
	JoinedAt  time.Time `json:"joined_at"`
	IsOnline  bool      `json:"is_online"`
}

// RoomAssignment represents an assignment linked to the room.
type RoomAssignment struct {
	ID              uuid.UUID `json:"id"`
	Title           string    `json:"title"`
	Mode            string    `json:"mode"`
	Settings        any       `json:"settings"`
	ResultsReleased bool      `json:"results_released"`
	CreatedAt       time.Time `json:"created_at"`
}

// RoomListItem represents an item in GET /api/v1/rooms response.
type RoomListItem struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	CreatorID    uuid.UUID `json:"creator_id"`
	CreatorName  string    `json:"creator_name"`
	InviteCode   string    `json:"invite_code"`
	MaxMembers   int       `json:"max_members"`
	MembersCount int       `json:"members_count"`
	IsReadOnly   bool      `json:"is_read_only"`
	CreatedAt    time.Time `json:"created_at"`
}

// RoomDetail represents the full details of a room for GET /api/v1/rooms/{id}.
type RoomDetail struct {
	ID          uuid.UUID        `json:"id"`
	Name        string           `json:"name"`
	CreatorID   uuid.UUID        `json:"creator_id"`
	InviteCode  string           `json:"invite_code"`
	MaxMembers  int              `json:"max_members"`
	IsReadOnly  bool             `json:"is_read_only"`
	Members     []RoomMember     `json:"members"`
	Assignments []RoomAssignment `json:"assignments"`
}

// RoomPreview represents preview data for GET /api/v1/rooms/join/{invite_code}.
type RoomPreview struct {
	RoomID       uuid.UUID `json:"room_id"`
	RoomName     string    `json:"room_name"`
	TeacherName  string    `json:"teacher_name"`
	MembersCount int       `json:"members_count"`
	MaxMembers   int       `json:"max_members"`
}

// CreateRoomRequest is the input payload for POST /api/v1/rooms.
type CreateRoomRequest struct {
	Name       string `json:"name"`
	MaxMembers int    `json:"max_members"`
}

// UpdateRoomRequest is the input payload for PATCH /api/v1/rooms/{id}.
type UpdateRoomRequest struct {
	Name                 *string `json:"name,omitempty"`
	MaxMembers           *int    `json:"max_members,omitempty"`
	RegenerateInviteCode bool    `json:"regenerate_invite_code"`
}

// JoinRoomResponse is the output payload for POST /api/v1/rooms/join/{invite_code}.
type JoinRoomResponse struct {
	OK     bool      `json:"ok"`
	RoomID uuid.UUID `json:"room_id"`
}

// SaveFreeCodeRequest is the input payload for PUT /api/v1/rooms/{id}/free-code.
type SaveFreeCodeRequest struct {
	Code string `json:"code"`
}

// SaveFreeCodeResponse is the output payload for PUT /api/v1/rooms/{id}/free-code.
type SaveFreeCodeResponse struct {
	OK    bool   `json:"ok"`
	S3Key string `json:"s3_key"`
}
