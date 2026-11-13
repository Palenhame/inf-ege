package rooms

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
)

// StorageClient defines the object storage interface required by the rooms service.
type StorageClient interface {
	PutObject(ctx context.Context, bucket, objectKey string, reader io.Reader, objectSize int64, contentType string) error
	GetObject(ctx context.Context, bucket, objectKey string) (io.ReadCloser, error)
}

// PresenceChecker defines the online status lookup interface.
type PresenceChecker interface {
	IsUserOnline(ctx context.Context, userID uuid.UUID) bool
}

// NoopPresenceChecker always returns false (offline).
type NoopPresenceChecker struct{}

// IsUserOnline returns false.
func (NoopPresenceChecker) IsUserOnline(ctx context.Context, userID uuid.UUID) bool {
	return false
}

// ServiceConfig holds configuration for the rooms service.
type ServiceConfig struct {
	PrivateBucket string
}

// Service orchestrates business logic for rooms, rosters, and free code storage.
type Service struct {
	repo          Repository
	storage       StorageClient
	presence      PresenceChecker
	privateBucket string
}

// NewService creates a new rooms Service.
func NewService(repo Repository, storage StorageClient, presence PresenceChecker, cfg ServiceConfig) *Service {
	bucket := cfg.PrivateBucket
	if bucket == "" {
		bucket = "egeinf-private"
	}
	if presence == nil {
		presence = NoopPresenceChecker{}
	}
	return &Service{
		repo:          repo,
		storage:       storage,
		presence:      presence,
		privateBucket: bucket,
	}
}

// CreateRoom handles creating a room with limits validation and invite code generation.
func (s *Service) CreateRoom(ctx context.Context, creatorID uuid.UUID, role string, req CreateRoomRequest) (*Room, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > 250 {
		return nil, fmt.Errorf("%w: name must be between 1 and 250 characters", ErrInvalidInput)
	}

	maxMembers := req.MaxMembers
	if maxMembers <= 0 {
		maxMembers = 25
	}
	if maxMembers < 1 || maxMembers > 100 {
		return nil, fmt.Errorf("%w: max_members must be between 1 and 100", ErrInvalidInput)
	}

	isSuperuser := role == "superuser"
	if !isSuperuser && role != "teacher" {
		return nil, ErrNotCreator
	}

	if !isSuperuser {
		maxRooms, maxStudentsPerRoom, isActive, err := s.repo.GetTeacherTierLimits(ctx, creatorID)
		if err != nil {
			return nil, fmt.Errorf("failed to check subscription limits: %w", err)
		}
		if !isActive {
			return nil, ErrSubscriptionReadOnly
		}

		activeRooms, err := s.repo.CountActiveTeacherRooms(ctx, creatorID)
		if err != nil {
			return nil, fmt.Errorf("failed to count teacher rooms: %w", err)
		}
		if activeRooms >= maxRooms {
			return nil, fmt.Errorf("%w: reached max rooms limit for subscription tier (%d)", ErrLimitExceeded, maxRooms)
		}

		if maxMembers > maxStudentsPerRoom {
			return nil, fmt.Errorf("%w: max_members (%d) exceeds tier limit (%d)", ErrLimitExceeded, maxMembers, maxStudentsPerRoom)
		}
	}

	var inviteCode string
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		inviteCode, err = GenerateInviteCode()
		if err != nil {
			return nil, fmt.Errorf("failed to generate invite code: %w", err)
		}
		_, err = s.repo.GetRoomByInviteCode(ctx, inviteCode)
		if err != nil && err == ErrRoomNotFound {
			break
		}
		if attempt == 4 {
			return nil, fmt.Errorf("failed to generate unique invite code after 5 attempts")
		}
	}

	now := time.Now().UTC()
	room := &Room{
		ID:         uuid.New(),
		Name:       name,
		CreatorID:  creatorID,
		InviteCode: inviteCode,
		MaxMembers: maxMembers,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	if err := s.repo.CreateRoom(ctx, room); err != nil {
		return nil, err
	}

	return room, nil
}

// ListRooms lists rooms based on viewMode ("teacher" or "student").
func (s *Service) ListRooms(ctx context.Context, userID uuid.UUID, viewMode string) ([]RoomListItem, error) {
	switch strings.ToLower(strings.TrimSpace(viewMode)) {
	case "teacher":
		items, err := s.repo.ListTeacherRooms(ctx, userID)
		if err != nil {
			return nil, err
		}
		_, _, isActive, _ := s.repo.GetTeacherTierLimits(ctx, userID)
		for i := range items {
			items[i].IsReadOnly = !isActive
		}
		return items, nil

	case "student":
		items, err := s.repo.ListStudentRooms(ctx, userID)
		if err != nil {
			return nil, err
		}
		for i := range items {
			_, _, isActive, _ := s.repo.GetTeacherTierLimits(ctx, items[i].CreatorID)
			items[i].IsReadOnly = !isActive
		}
		return items, nil

	default:
		return nil, fmt.Errorf("%w: view_mode must be 'teacher' or 'student'", ErrInvalidInput)
	}
}

// GetRoomDetail retrieves complete room metadata, members roster, and assignments.
func (s *Service) GetRoomDetail(ctx context.Context, userID uuid.UUID, isSuperuser bool, roomID uuid.UUID) (*RoomDetail, error) {
	room, err := s.repo.GetRoomByID(ctx, roomID)
	if err != nil {
		return nil, err
	}

	if !isSuperuser && userID != room.CreatorID {
		isMember, err := s.repo.IsRoomMember(ctx, roomID, userID)
		if err != nil {
			return nil, fmt.Errorf("failed to verify membership: %w", err)
		}
		if !isMember {
			return nil, ErrNotMember
		}
	}

	members, err := s.repo.GetRoomMembers(ctx, roomID)
	if err != nil {
		return nil, fmt.Errorf("failed to load room members: %w", err)
	}

	for i := range members {
		members[i].IsOnline = s.presence.IsUserOnline(ctx, members[i].UserID)
	}

	assignments, err := s.repo.GetRoomAssignments(ctx, roomID)
	if err != nil {
		return nil, fmt.Errorf("failed to load room assignments: %w", err)
	}

	_, _, isActive, _ := s.repo.GetTeacherTierLimits(ctx, room.CreatorID)

	return &RoomDetail{
		ID:          room.ID,
		Name:        room.Name,
		CreatorID:   room.CreatorID,
		InviteCode:  room.InviteCode,
		MaxMembers:  room.MaxMembers,
		IsReadOnly:  !isActive,
		Members:     members,
		Assignments: assignments,
	}, nil
}

// UpdateRoom modifies room name, capacity, or regenerates invite code.
func (s *Service) UpdateRoom(ctx context.Context, userID uuid.UUID, isSuperuser bool, roomID uuid.UUID, req UpdateRoomRequest) (*Room, error) {
	room, err := s.repo.GetRoomByID(ctx, roomID)
	if err != nil {
		return nil, err
	}

	if !isSuperuser && userID != room.CreatorID {
		return nil, ErrNotCreator
	}

	if !isSuperuser {
		_, maxStudentsPerRoom, isActive, err := s.repo.GetTeacherTierLimits(ctx, room.CreatorID)
		if err != nil {
			return nil, fmt.Errorf("failed to check subscription limits: %w", err)
		}
		if !isActive {
			return nil, ErrSubscriptionReadOnly
		}

		if req.MaxMembers != nil && *req.MaxMembers > maxStudentsPerRoom {
			return nil, fmt.Errorf("%w: max_members exceeds tier limit (%d)", ErrLimitExceeded, maxStudentsPerRoom)
		}
	}

	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" || len(name) > 250 {
			return nil, fmt.Errorf("%w: name must be between 1 and 250 characters", ErrInvalidInput)
		}
		room.Name = name
	}

	if req.MaxMembers != nil {
		max := *req.MaxMembers
		if max < 1 || max > 100 {
			return nil, fmt.Errorf("%w: max_members must be between 1 and 100", ErrInvalidInput)
		}
		room.MaxMembers = max
	}

	if req.RegenerateInviteCode {
		newCode, err := GenerateInviteCode()
		if err != nil {
			return nil, fmt.Errorf("failed to generate new invite code: %w", err)
		}
		room.InviteCode = newCode
	}

	if err := s.repo.UpdateRoom(ctx, room); err != nil {
		return nil, err
	}

	return room, nil
}

// DeleteRoom performs soft deletion of a room.
func (s *Service) DeleteRoom(ctx context.Context, userID uuid.UUID, isSuperuser bool, roomID uuid.UUID) error {
	room, err := s.repo.GetRoomByID(ctx, roomID)
	if err != nil {
		return err
	}

	if !isSuperuser && userID != room.CreatorID {
		return ErrNotCreator
	}

	return s.repo.SoftDeleteRoom(ctx, roomID)
}

// PreviewJoin retrieves summary information for an invite code.
func (s *Service) PreviewJoin(ctx context.Context, inviteCode string) (*RoomPreview, error) {
	code := strings.TrimSpace(inviteCode)
	if code == "" {
		return nil, fmt.Errorf("%w: invite code cannot be empty", ErrInvalidInput)
	}

	room, err := s.repo.GetRoomByInviteCode(ctx, code)
	if err != nil {
		return nil, err
	}

	teacherName, err := s.repo.GetUserName(ctx, room.CreatorID)
	if err != nil {
		return nil, fmt.Errorf("failed to get teacher name: %w", err)
	}

	membersCount, err := s.repo.CountRoomMembers(ctx, room.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to count room members: %w", err)
	}

	return &RoomPreview{
		RoomID:       room.ID,
		RoomName:     room.Name,
		TeacherName:  teacherName,
		MembersCount: membersCount,
		MaxMembers:   room.MaxMembers,
	}, nil
}

// JoinRoom adds a student to the room after validating capacity.
func (s *Service) JoinRoom(ctx context.Context, userID uuid.UUID, inviteCode string) (uuid.UUID, error) {
	code := strings.TrimSpace(inviteCode)
	if code == "" {
		return uuid.Nil, fmt.Errorf("%w: invite code cannot be empty", ErrInvalidInput)
	}

	room, err := s.repo.GetRoomByInviteCode(ctx, code)
	if err != nil {
		return uuid.Nil, err
	}

	isMember, err := s.repo.IsRoomMember(ctx, room.ID, userID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to verify membership: %w", err)
	}
	if isMember {
		return uuid.Nil, ErrAlreadyMember
	}

	membersCount, err := s.repo.CountRoomMembers(ctx, room.ID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to count room members: %w", err)
	}

	if membersCount >= room.MaxMembers {
		return uuid.Nil, fmt.Errorf("%w: room capacity limit (%d) reached", ErrRoomFull, room.MaxMembers)
	}

	_, maxStudentsPerRoom, _, err := s.repo.GetTeacherTierLimits(ctx, room.CreatorID)
	if err == nil && maxStudentsPerRoom > 0 && membersCount >= maxStudentsPerRoom {
		return uuid.Nil, fmt.Errorf("%w: teacher subscription capacity (%d) reached", ErrRoomFull, maxStudentsPerRoom)
	}

	if err := s.repo.AddRoomMember(ctx, room.ID, userID); err != nil {
		return uuid.Nil, err
	}

	return room.ID, nil
}

// RemoveMember removes a student from a room (by teacher, self-leave, or superuser).
func (s *Service) RemoveMember(ctx context.Context, callerID uuid.UUID, isSuperuser bool, roomID, targetUserID uuid.UUID) error {
	room, err := s.repo.GetRoomByID(ctx, roomID)
	if err != nil {
		return err
	}

	canRemove := isSuperuser || callerID == room.CreatorID || callerID == targetUserID
	if !canRemove {
		return ErrNotCreator
	}

	isMember, err := s.repo.IsRoomMember(ctx, roomID, targetUserID)
	if err != nil {
		return fmt.Errorf("failed to verify membership: %w", err)
	}
	if !isMember {
		return ErrNotMember
	}

	return s.repo.RemoveRoomMember(ctx, roomID, targetUserID)
}

// SaveFreeCode saves student's Python code to private S3 and updates user_rooms.
func (s *Service) SaveFreeCode(ctx context.Context, userID uuid.UUID, roomID uuid.UUID, code string) (string, error) {
	room, err := s.repo.GetRoomByID(ctx, roomID)
	if err != nil {
		return "", err
	}

	isMember, err := s.repo.IsRoomMember(ctx, room.ID, userID)
	if err != nil {
		return "", fmt.Errorf("failed to verify membership: %w", err)
	}
	if !isMember {
		return "", ErrNotMember
	}

	s3Key := fmt.Sprintf("rooms/%s/free_code/%s.py", roomID, userID)

	if s.storage != nil {
		codeBytes := []byte(code)
		reader := bytes.NewReader(codeBytes)
		err = s.storage.PutObject(ctx, s.privateBucket, s3Key, reader, int64(len(codeBytes)), "text/x-python; charset=utf-8")
		if err != nil {
			return "", fmt.Errorf("failed to save code in storage: %w", err)
		}
	}

	if err := s.repo.UpdateFreeCodeS3Key(ctx, roomID, userID, s3Key); err != nil {
		return "", fmt.Errorf("failed to update free code s3 key in database: %w", err)
	}

	return s3Key, nil
}
