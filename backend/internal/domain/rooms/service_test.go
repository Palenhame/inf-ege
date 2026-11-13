package rooms

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type mockStorage struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func newMockStorage() *mockStorage {
	return &mockStorage{
		objects: make(map[string][]byte),
	}
}

func (m *mockStorage) PutObject(ctx context.Context, bucket, objectKey string, reader io.Reader, objectSize int64, contentType string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	m.objects[bucket+"/"+objectKey] = data
	return nil
}

func (m *mockStorage) GetObject(ctx context.Context, bucket, objectKey string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.objects[bucket+"/"+objectKey]
	if !ok {
		return nil, ErrRoomNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

type mockPresence struct {
	online map[uuid.UUID]bool
}

func (p *mockPresence) IsUserOnline(ctx context.Context, userID uuid.UUID) bool {
	return p.online[userID]
}

type mockRepo struct {
	mu           sync.Mutex
	rooms        map[uuid.UUID]*Room
	codeToRoomID map[string]uuid.UUID
	userRooms    map[string]*UserRoom // key: roomID:userID
	userNames    map[uuid.UUID]string
	userProfiles map[uuid.UUID]RoomMember
	assignments  map[uuid.UUID][]RoomAssignment
	tierLimits   map[uuid.UUID]tierLimitsData
}

type tierLimitsData struct {
	maxRooms           int
	maxStudentsPerRoom int
	isActive           bool
}

func newMockRepo() *mockRepo {
	return &mockRepo{
		rooms:        make(map[uuid.UUID]*Room),
		codeToRoomID: make(map[string]uuid.UUID),
		userRooms:    make(map[string]*UserRoom),
		userNames:    make(map[uuid.UUID]string),
		userProfiles: make(map[uuid.UUID]RoomMember),
		assignments:  make(map[uuid.UUID][]RoomAssignment),
		tierLimits:   make(map[uuid.UUID]tierLimitsData),
	}
}

func (m *mockRepo) CreateRoom(ctx context.Context, room *Room) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rooms[room.ID] = room
	m.codeToRoomID[strings.ToUpper(room.InviteCode)] = room.ID
	return nil
}

func (m *mockRepo) GetRoomByID(ctx context.Context, id uuid.UUID) (*Room, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rooms[id]
	if !ok || r.DeletedAt != nil {
		return nil, ErrRoomNotFound
	}
	// Return copy
	copyR := *r
	return &copyR, nil
}

func (m *mockRepo) GetRoomByInviteCode(ctx context.Context, code string) (*Room, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.codeToRoomID[strings.ToUpper(strings.TrimSpace(code))]
	if !ok {
		return nil, ErrRoomNotFound
	}
	r, ok := m.rooms[id]
	if !ok || r.DeletedAt != nil {
		return nil, ErrRoomNotFound
	}
	copyR := *r
	return &copyR, nil
}

func (m *mockRepo) ListTeacherRooms(ctx context.Context, teacherID uuid.UUID) ([]RoomListItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var items []RoomListItem
	for _, r := range m.rooms {
		if r.CreatorID == teacherID && r.DeletedAt == nil {
			count := 0
			for _, ur := range m.userRooms {
				if ur.RoomID == r.ID {
					count++
				}
			}
			items = append(items, RoomListItem{
				ID:           r.ID,
				Name:         r.Name,
				CreatorID:    r.CreatorID,
				CreatorName:  m.userNames[r.CreatorID],
				InviteCode:   r.InviteCode,
				MaxMembers:   r.MaxMembers,
				MembersCount: count,
				CreatedAt:    r.CreatedAt,
			})
		}
	}
	return items, nil
}

func (m *mockRepo) ListStudentRooms(ctx context.Context, studentID uuid.UUID) ([]RoomListItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var items []RoomListItem
	for _, ur := range m.userRooms {
		if ur.UserID == studentID {
			r, ok := m.rooms[ur.RoomID]
			if ok && r.DeletedAt == nil {
				count := 0
				for _, otherUr := range m.userRooms {
					if otherUr.RoomID == r.ID {
						count++
					}
				}
				items = append(items, RoomListItem{
					ID:           r.ID,
					Name:         r.Name,
					CreatorID:    r.CreatorID,
					CreatorName:  m.userNames[r.CreatorID],
					InviteCode:   r.InviteCode,
					MaxMembers:   r.MaxMembers,
					MembersCount: count,
					CreatedAt:    r.CreatedAt,
				})
			}
		}
	}
	return items, nil
}

func (m *mockRepo) UpdateRoom(ctx context.Context, room *Room) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rooms[room.ID]
	if !ok || r.DeletedAt != nil {
		return ErrRoomNotFound
	}
	delete(m.codeToRoomID, strings.ToUpper(r.InviteCode))
	r.Name = room.Name
	r.MaxMembers = room.MaxMembers
	r.InviteCode = room.InviteCode
	r.UpdatedAt = time.Now().UTC()
	m.codeToRoomID[strings.ToUpper(room.InviteCode)] = room.ID
	return nil
}

func (m *mockRepo) SoftDeleteRoom(ctx context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rooms[id]
	if !ok || r.DeletedAt != nil {
		return ErrRoomNotFound
	}
	now := time.Now().UTC()
	r.DeletedAt = &now
	r.UpdatedAt = now
	delete(m.codeToRoomID, strings.ToUpper(r.InviteCode))
	return nil
}

func (m *mockRepo) GetRoomMembers(ctx context.Context, roomID uuid.UUID) ([]RoomMember, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var members []RoomMember
	for _, ur := range m.userRooms {
		if ur.RoomID == roomID {
			p, ok := m.userProfiles[ur.UserID]
			if ok {
				p.JoinedAt = ur.JoinedAt
				members = append(members, p)
			} else {
				members = append(members, RoomMember{
					UserID:   ur.UserID,
					JoinedAt: ur.JoinedAt,
				})
			}
		}
	}
	return members, nil
}

func (m *mockRepo) GetRoomAssignments(ctx context.Context, roomID uuid.UUID) ([]RoomAssignment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.assignments[roomID], nil
}

func (m *mockRepo) CountRoomMembers(ctx context.Context, roomID uuid.UUID) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, ur := range m.userRooms {
		if ur.RoomID == roomID {
			count++
		}
	}
	return count, nil
}

func (m *mockRepo) CountActiveTeacherRooms(ctx context.Context, teacherID uuid.UUID) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, r := range m.rooms {
		if r.CreatorID == teacherID && r.DeletedAt == nil {
			count++
		}
	}
	return count, nil
}

func (m *mockRepo) IsRoomMember(ctx context.Context, roomID, userID uuid.UUID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := roomID.String() + ":" + userID.String()
	_, ok := m.userRooms[key]
	return ok, nil
}

func (m *mockRepo) AddRoomMember(ctx context.Context, roomID, userID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := roomID.String() + ":" + userID.String()
	if _, ok := m.userRooms[key]; ok {
		return ErrAlreadyMember
	}
	now := time.Now().UTC()
	m.userRooms[key] = &UserRoom{
		UserID:    userID,
		RoomID:    roomID,
		JoinedAt:  now,
		UpdatedAt: now,
	}
	return nil
}

func (m *mockRepo) RemoveRoomMember(ctx context.Context, roomID, userID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := roomID.String() + ":" + userID.String()
	if _, ok := m.userRooms[key]; !ok {
		return ErrNotMember
	}
	delete(m.userRooms, key)
	return nil
}

func (m *mockRepo) UpdateFreeCodeS3Key(ctx context.Context, roomID, userID uuid.UUID, s3Key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := roomID.String() + ":" + userID.String()
	ur, ok := m.userRooms[key]
	if !ok {
		return ErrNotMember
	}
	ur.FreeCodeS3Key = &s3Key
	ur.UpdatedAt = time.Now().UTC()
	return nil
}

func (m *mockRepo) GetTeacherTierLimits(ctx context.Context, teacherID uuid.UUID) (maxRooms int, maxStudentsPerRoom int, isActive bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tl, ok := m.tierLimits[teacherID]
	if !ok {
		return 15, 40, true, nil
	}
	return tl.maxRooms, tl.maxStudentsPerRoom, tl.isActive, nil
}

func (m *mockRepo) GetUserName(ctx context.Context, userID uuid.UUID) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.userNames[userID], nil
}

// Tests

func TestCreateRoom(t *testing.T) {
	ctx := context.Background()
	repo := newMockRepo()
	storage := newMockStorage()
	svc := NewService(repo, storage, nil, ServiceConfig{})

	teacherID := uuid.New()
	studentID := uuid.New()

	// 1. Success: Teacher creates room
	req := CreateRoomRequest{
		Name:       "11-А Информатика",
		MaxMembers: 30,
	}
	room, err := svc.CreateRoom(ctx, teacherID, "teacher", req)
	if err != nil {
		t.Fatalf("failed to create room: %v", err)
	}
	if room.Name != "11-А Информатика" {
		t.Errorf("expected room name %s, got %s", req.Name, room.Name)
	}
	if room.MaxMembers != 30 {
		t.Errorf("expected max members 30, got %d", room.MaxMembers)
	}
	if len(room.InviteCode) != 8 {
		t.Errorf("expected 8-char invite code, got %s", room.InviteCode)
	}

	// 2. Student cannot create room
	_, err = svc.CreateRoom(ctx, studentID, "student", req)
	if err != ErrNotCreator {
		t.Errorf("expected ErrNotCreator for student, got %v", err)
	}

	// 3. Validation errors
	_, err = svc.CreateRoom(ctx, teacherID, "teacher", CreateRoomRequest{Name: ""})
	if err == nil || !strings.Contains(err.Error(), "name must be") {
		t.Errorf("expected name validation error, got %v", err)
	}
	_, err = svc.CreateRoom(ctx, teacherID, "teacher", CreateRoomRequest{Name: "Test", MaxMembers: 105})
	if err == nil || !strings.Contains(err.Error(), "max_members must be") {
		t.Errorf("expected max_members validation error, got %v", err)
	}

	// 4. Subscription expired
	repo.tierLimits[teacherID] = tierLimitsData{maxRooms: 10, maxStudentsPerRoom: 40, isActive: false}
	_, err = svc.CreateRoom(ctx, teacherID, "teacher", req)
	if err != ErrSubscriptionReadOnly {
		t.Errorf("expected ErrSubscriptionReadOnly, got %v", err)
	}

	// 5. Quota exceeded: max rooms reached
	repo.tierLimits[teacherID] = tierLimitsData{maxRooms: 1, maxStudentsPerRoom: 40, isActive: true}
	_, err = svc.CreateRoom(ctx, teacherID, "teacher", CreateRoomRequest{Name: "Second Room", MaxMembers: 20})
	if err == nil || !strings.Contains(err.Error(), "reached max rooms limit") {
		t.Errorf("expected max rooms limit error, got %v", err)
	}

	// 6. Max members exceeds tier limit
	repo.tierLimits[teacherID] = tierLimitsData{maxRooms: 10, maxStudentsPerRoom: 25, isActive: true}
	_, err = svc.CreateRoom(ctx, teacherID, "teacher", CreateRoomRequest{Name: "Big Room", MaxMembers: 30})
	if err == nil || !strings.Contains(err.Error(), "exceeds tier limit") {
		t.Errorf("expected tier limit error, got %v", err)
	}
}

func TestListRooms(t *testing.T) {
	ctx := context.Background()
	repo := newMockRepo()
	svc := NewService(repo, nil, nil, ServiceConfig{})

	teacherID := uuid.New()
	studentID := uuid.New()
	repo.userNames[teacherID] = "Петр Сидоров"

	// Create teacher room
	room, err := svc.CreateRoom(ctx, teacherID, "teacher", CreateRoomRequest{Name: "Room 1", MaxMembers: 25})
	if err != nil {
		t.Fatalf("failed to create room: %v", err)
	}

	// Add student
	if err := repo.AddRoomMember(ctx, room.ID, studentID); err != nil {
		t.Fatalf("failed to add member: %v", err)
	}

	// Teacher view
	teacherRooms, err := svc.ListRooms(ctx, teacherID, "teacher")
	if err != nil {
		t.Fatalf("failed to list teacher rooms: %v", err)
	}
	if len(teacherRooms) != 1 {
		t.Fatalf("expected 1 teacher room, got %d", len(teacherRooms))
	}
	if teacherRooms[0].MembersCount != 1 {
		t.Errorf("expected 1 member, got %d", teacherRooms[0].MembersCount)
	}

	// Student view
	studentRooms, err := svc.ListRooms(ctx, studentID, "student")
	if err != nil {
		t.Fatalf("failed to list student rooms: %v", err)
	}
	if len(studentRooms) != 1 {
		t.Fatalf("expected 1 student room, got %d", len(studentRooms))
	}

	// Invalid view mode
	_, err = svc.ListRooms(ctx, teacherID, "invalid")
	if err == nil {
		t.Errorf("expected error for invalid view mode")
	}
}

func TestGetRoomDetailAndPresence(t *testing.T) {
	ctx := context.Background()
	repo := newMockRepo()
	teacherID := uuid.New()
	studentID := uuid.New()
	outsiderID := uuid.New()

	presence := &mockPresence{
		online: map[uuid.UUID]bool{studentID: true},
	}
	svc := NewService(repo, nil, presence, ServiceConfig{})

	room, err := svc.CreateRoom(ctx, teacherID, "teacher", CreateRoomRequest{Name: "Class 10", MaxMembers: 20})
	if err != nil {
		t.Fatalf("failed to create room: %v", err)
	}

	repo.userProfiles[studentID] = RoomMember{
		UserID:    studentID,
		Username:  "student1",
		FirstName: "Иван",
		LastName:  "Иванов",
	}
	_ = repo.AddRoomMember(ctx, room.ID, studentID)

	repo.assignments[room.ID] = []RoomAssignment{
		{ID: uuid.New(), Title: "HW 1", Mode: "homework"},
	}

	// Teacher detail
	detail, err := svc.GetRoomDetail(ctx, teacherID, false, room.ID)
	if err != nil {
		t.Fatalf("failed to get room detail for teacher: %v", err)
	}
	if len(detail.Members) != 1 {
		t.Fatalf("expected 1 member, got %d", len(detail.Members))
	}
	if !detail.Members[0].IsOnline {
		t.Errorf("expected member to be online")
	}
	if len(detail.Assignments) != 1 {
		t.Fatalf("expected 1 assignment, got %d", len(detail.Assignments))
	}

	// Joined student detail
	studentDetail, err := svc.GetRoomDetail(ctx, studentID, false, room.ID)
	if err != nil {
		t.Fatalf("failed to get room detail for student: %v", err)
	}
	if studentDetail.ID != room.ID {
		t.Errorf("mismatched room id")
	}

	// Outsider detail
	_, err = svc.GetRoomDetail(ctx, outsiderID, false, room.ID)
	if err != ErrNotMember {
		t.Errorf("expected ErrNotMember for outsider, got %v", err)
	}

	// Superuser detail
	suDetail, err := svc.GetRoomDetail(ctx, outsiderID, true, room.ID)
	if err != nil {
		t.Fatalf("superuser should access room detail: %v", err)
	}
	if suDetail.ID != room.ID {
		t.Errorf("mismatched su room id")
	}
}

func TestUpdateAndDeleteRoom(t *testing.T) {
	ctx := context.Background()
	repo := newMockRepo()
	svc := NewService(repo, nil, nil, ServiceConfig{})

	teacherID := uuid.New()
	otherTeacherID := uuid.New()

	room, err := svc.CreateRoom(ctx, teacherID, "teacher", CreateRoomRequest{Name: "Old Name", MaxMembers: 25})
	if err != nil {
		t.Fatalf("failed to create room: %v", err)
	}
	oldCode := room.InviteCode

	// Unauthorized update
	newName := "New Name"
	_, err = svc.UpdateRoom(ctx, otherTeacherID, false, room.ID, UpdateRoomRequest{Name: &newName})
	if err != ErrNotCreator {
		t.Errorf("expected ErrNotCreator, got %v", err)
	}

	// Successful update with code regeneration
	newMax := 35
	updated, err := svc.UpdateRoom(ctx, teacherID, false, room.ID, UpdateRoomRequest{
		Name:                 &newName,
		MaxMembers:           &newMax,
		RegenerateInviteCode: true,
	})
	if err != nil {
		t.Fatalf("failed to update room: %v", err)
	}
	if updated.Name != "New Name" {
		t.Errorf("expected name New Name, got %s", updated.Name)
	}
	if updated.MaxMembers != 35 {
		t.Errorf("expected max members 35, got %d", updated.MaxMembers)
	}
	if updated.InviteCode == oldCode {
		t.Errorf("invite code should have changed from %s", oldCode)
	}

	// Soft delete
	err = svc.DeleteRoom(ctx, teacherID, false, room.ID)
	if err != nil {
		t.Fatalf("failed to delete room: %v", err)
	}

	// Detail on deleted room should fail
	_, err = svc.GetRoomDetail(ctx, teacherID, false, room.ID)
	if err != ErrRoomNotFound {
		t.Errorf("expected ErrRoomNotFound for deleted room, got %v", err)
	}
}

func TestJoinRoomAndLeave(t *testing.T) {
	ctx := context.Background()
	repo := newMockRepo()
	svc := NewService(repo, nil, nil, ServiceConfig{})

	teacherID := uuid.New()
	student1 := uuid.New()
	student2 := uuid.New()
	repo.userNames[teacherID] = "Учитель Тест"

	room, err := svc.CreateRoom(ctx, teacherID, "teacher", CreateRoomRequest{Name: "Small Room", MaxMembers: 1})
	if err != nil {
		t.Fatalf("failed to create room: %v", err)
	}

	// Preview
	preview, err := svc.PreviewJoin(ctx, room.InviteCode)
	if err != nil {
		t.Fatalf("failed to preview join: %v", err)
	}
	if preview.RoomName != "Small Room" || preview.TeacherName != "Учитель Тест" {
		t.Errorf("unexpected preview data: %+v", preview)
	}

	// Student 1 joins
	joinedID, err := svc.JoinRoom(ctx, student1, room.InviteCode)
	if err != nil {
		t.Fatalf("student 1 failed to join: %v", err)
	}
	if joinedID != room.ID {
		t.Errorf("mismatched joined room ID")
	}

	// Duplicate join
	_, err = svc.JoinRoom(ctx, student1, room.InviteCode)
	if err != ErrAlreadyMember {
		t.Errorf("expected ErrAlreadyMember, got %v", err)
	}

	// Student 2 joins but room full (capacity 1 reached)
	_, err = svc.JoinRoom(ctx, student2, room.InviteCode)
	if err == nil || !strings.Contains(err.Error(), "capacity limit") {
		t.Errorf("expected room full error, got %v", err)
	}

	// Student 1 leaves room
	err = svc.RemoveMember(ctx, student1, false, room.ID, student1)
	if err != nil {
		t.Fatalf("failed to self-remove member: %v", err)
	}

	// Now student 2 can join
	_, err = svc.JoinRoom(ctx, student2, room.InviteCode)
	if err != nil {
		t.Fatalf("student 2 should join after room space freed: %v", err)
	}

	// Teacher removes student 2
	err = svc.RemoveMember(ctx, teacherID, false, room.ID, student2)
	if err != nil {
		t.Fatalf("teacher failed to remove student: %v", err)
	}
}

func TestSaveFreeCode(t *testing.T) {
	ctx := context.Background()
	repo := newMockRepo()
	storage := newMockStorage()
	svc := NewService(repo, storage, nil, ServiceConfig{PrivateBucket: "egeinf-private"})

	teacherID := uuid.New()
	studentID := uuid.New()
	outsiderID := uuid.New()

	room, err := svc.CreateRoom(ctx, teacherID, "teacher", CreateRoomRequest{Name: "Python Room", MaxMembers: 20})
	if err != nil {
		t.Fatalf("failed to create room: %v", err)
	}
	_, _ = svc.JoinRoom(ctx, studentID, room.InviteCode)

	// Outsider cannot save free code
	_, err = svc.SaveFreeCode(ctx, outsiderID, room.ID, "print('hello')")
	if err != ErrNotMember {
		t.Errorf("expected ErrNotMember for outsider, got %v", err)
	}

	// Member saves code
	code := "with open('24.txt') as f:\n    print(len(f.read()))"
	s3Key, err := svc.SaveFreeCode(ctx, studentID, room.ID, code)
	if err != nil {
		t.Fatalf("failed to save free code: %v", err)
	}

	expectedKey := "rooms/" + room.ID.String() + "/free_code/" + studentID.String() + ".py"
	if s3Key != expectedKey {
		t.Errorf("expected s3 key %s, got %s", expectedKey, s3Key)
	}

	// Verify in mock storage
	storedData, ok := storage.objects["egeinf-private/"+s3Key]
	if !ok {
		t.Fatalf("object not found in storage at key egeinf-private/%s", s3Key)
	}
	if string(storedData) != code {
		t.Errorf("stored data mismatch: expected %s, got %s", code, string(storedData))
	}
}
