package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"backend/internal/domain/rooms"
	"backend/internal/transport/http/middleware"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type testRoomRepo struct {
	mu           sync.Mutex
	rooms        map[uuid.UUID]*rooms.Room
	codeToID     map[string]uuid.UUID
	userRooms    map[string]*rooms.UserRoom
	userNames    map[uuid.UUID]string
	userProfiles map[uuid.UUID]rooms.RoomMember
	assignments  map[uuid.UUID][]rooms.RoomAssignment
}

func newTestRoomRepo() *testRoomRepo {
	return &testRoomRepo{
		rooms:        make(map[uuid.UUID]*rooms.Room),
		codeToID:     make(map[string]uuid.UUID),
		userRooms:    make(map[string]*rooms.UserRoom),
		userNames:    make(map[uuid.UUID]string),
		userProfiles: make(map[uuid.UUID]rooms.RoomMember),
		assignments:  make(map[uuid.UUID][]rooms.RoomAssignment),
	}
}

func (r *testRoomRepo) CreateRoom(ctx context.Context, room *rooms.Room) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rooms[room.ID] = room
	r.codeToID[strings.ToUpper(room.InviteCode)] = room.ID
	return nil
}

func (r *testRoomRepo) GetRoomByID(ctx context.Context, id uuid.UUID) (*rooms.Room, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	room, ok := r.rooms[id]
	if !ok || room.DeletedAt != nil {
		return nil, rooms.ErrRoomNotFound
	}
	cp := *room
	return &cp, nil
}

func (r *testRoomRepo) GetRoomByInviteCode(ctx context.Context, code string) (*rooms.Room, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.codeToID[strings.ToUpper(strings.TrimSpace(code))]
	if !ok {
		return nil, rooms.ErrRoomNotFound
	}
	room, ok := r.rooms[id]
	if !ok || room.DeletedAt != nil {
		return nil, rooms.ErrRoomNotFound
	}
	cp := *room
	return &cp, nil
}

func (r *testRoomRepo) ListTeacherRooms(ctx context.Context, teacherID uuid.UUID) ([]rooms.RoomListItem, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var list []rooms.RoomListItem
	for _, rm := range r.rooms {
		if rm.CreatorID == teacherID && rm.DeletedAt == nil {
			list = append(list, rooms.RoomListItem{
				ID:          rm.ID,
				Name:        rm.Name,
				CreatorID:   rm.CreatorID,
				CreatorName: r.userNames[rm.CreatorID],
				InviteCode:  rm.InviteCode,
				MaxMembers:  rm.MaxMembers,
				CreatedAt:   rm.CreatedAt,
			})
		}
	}
	return list, nil
}

func (r *testRoomRepo) ListStudentRooms(ctx context.Context, studentID uuid.UUID) ([]rooms.RoomListItem, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var list []rooms.RoomListItem
	for _, ur := range r.userRooms {
		if ur.UserID == studentID {
			rm, ok := r.rooms[ur.RoomID]
			if ok && rm.DeletedAt == nil {
				list = append(list, rooms.RoomListItem{
					ID:          rm.ID,
					Name:        rm.Name,
					CreatorID:   rm.CreatorID,
					CreatorName: r.userNames[rm.CreatorID],
					InviteCode:  rm.InviteCode,
					MaxMembers:  rm.MaxMembers,
					CreatedAt:   rm.CreatedAt,
				})
			}
		}
	}
	return list, nil
}

func (r *testRoomRepo) UpdateRoom(ctx context.Context, room *rooms.Room) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.rooms[room.ID]
	if !ok || existing.DeletedAt != nil {
		return rooms.ErrRoomNotFound
	}
	delete(r.codeToID, strings.ToUpper(existing.InviteCode))
	existing.Name = room.Name
	existing.MaxMembers = room.MaxMembers
	existing.InviteCode = room.InviteCode
	existing.UpdatedAt = time.Now().UTC()
	r.codeToID[strings.ToUpper(room.InviteCode)] = room.ID
	return nil
}

func (r *testRoomRepo) SoftDeleteRoom(ctx context.Context, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.rooms[id]
	if !ok || existing.DeletedAt != nil {
		return rooms.ErrRoomNotFound
	}
	now := time.Now().UTC()
	existing.DeletedAt = &now
	delete(r.codeToID, strings.ToUpper(existing.InviteCode))
	return nil
}

func (r *testRoomRepo) GetRoomMembers(ctx context.Context, roomID uuid.UUID) ([]rooms.RoomMember, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var members []rooms.RoomMember
	for _, ur := range r.userRooms {
		if ur.RoomID == roomID {
			p, ok := r.userProfiles[ur.UserID]
			if ok {
				p.JoinedAt = ur.JoinedAt
				members = append(members, p)
			} else {
				members = append(members, rooms.RoomMember{
					UserID:   ur.UserID,
					JoinedAt: ur.JoinedAt,
				})
			}
		}
	}
	return members, nil
}

func (r *testRoomRepo) GetRoomAssignments(ctx context.Context, roomID uuid.UUID) ([]rooms.RoomAssignment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.assignments[roomID], nil
}

func (r *testRoomRepo) CountRoomMembers(ctx context.Context, roomID uuid.UUID) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, ur := range r.userRooms {
		if ur.RoomID == roomID {
			count++
		}
	}
	return count, nil
}

func (r *testRoomRepo) CountActiveTeacherRooms(ctx context.Context, teacherID uuid.UUID) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, rm := range r.rooms {
		if rm.CreatorID == teacherID && rm.DeletedAt == nil {
			count++
		}
	}
	return count, nil
}

func (r *testRoomRepo) IsRoomMember(ctx context.Context, roomID, userID uuid.UUID) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := roomID.String() + ":" + userID.String()
	_, ok := r.userRooms[key]
	return ok, nil
}

func (r *testRoomRepo) AddRoomMember(ctx context.Context, roomID, userID uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := roomID.String() + ":" + userID.String()
	if _, ok := r.userRooms[key]; ok {
		return rooms.ErrAlreadyMember
	}
	now := time.Now().UTC()
	r.userRooms[key] = &rooms.UserRoom{
		UserID:    userID,
		RoomID:    roomID,
		JoinedAt:  now,
		UpdatedAt: now,
	}
	return nil
}

func (r *testRoomRepo) RemoveRoomMember(ctx context.Context, roomID, userID uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := roomID.String() + ":" + userID.String()
	if _, ok := r.userRooms[key]; !ok {
		return rooms.ErrNotMember
	}
	delete(r.userRooms, key)
	return nil
}

func (r *testRoomRepo) UpdateFreeCodeS3Key(ctx context.Context, roomID, userID uuid.UUID, s3Key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := roomID.String() + ":" + userID.String()
	ur, ok := r.userRooms[key]
	if !ok {
		return rooms.ErrNotMember
	}
	ur.FreeCodeS3Key = &s3Key
	return nil
}

func (r *testRoomRepo) GetTeacherTierLimits(ctx context.Context, teacherID uuid.UUID) (int, int, bool, error) {
	return 15, 40, true, nil
}

func (r *testRoomRepo) GetUserName(ctx context.Context, userID uuid.UUID) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.userNames[userID], nil
}

type testRoomStorage struct {
	mu    sync.Mutex
	files map[string][]byte
}

func (s *testRoomStorage) PutObject(ctx context.Context, bucket, objectKey string, reader io.Reader, objectSize int64, contentType string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, _ := io.ReadAll(reader)
	s.files[bucket+"/"+objectKey] = data
	return nil
}

func (s *testRoomStorage) GetObject(ctx context.Context, bucket, objectKey string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data := s.files[bucket+"/"+objectKey]
	return io.NopCloser(bytes.NewReader(data)), nil
}

func setupRoomTestRouter() (chi.Router, *testRoomRepo, *testRoomStorage, *rooms.Service) {
	repo := newTestRoomRepo()
	storage := &testRoomStorage{files: make(map[string][]byte)}
	svc := rooms.NewService(repo, storage, nil, rooms.ServiceConfig{})
	handler := NewRoomHandler(svc)

	r := chi.NewRouter()
	handler.RegisterRoutes(r)
	return r, repo, storage, svc
}

func withRoomAuth(r *http.Request, userID uuid.UUID, role string) *http.Request {
	ctx := middleware.ContextWithUser(r.Context(), middleware.UserContext{
		UserID:          userID,
		Role:            role,
		IsSuperuser:     role == "superuser",
		IsAuthenticated: true,
	})
	return r.WithContext(ctx)
}

func TestRoomHandlers_CreateAndList(t *testing.T) {
	router, repo, _, _ := setupRoomTestRouter()
	teacherID := uuid.New()
	repo.userNames[teacherID] = "Преподаватель"

	// 1. POST /rooms - Unauthorized
	body := `{"name":"11-А","max_members":25}`
	req := httptest.NewRequest(http.MethodPost, "/rooms", strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}

	// 2. POST /rooms - Success with teacher
	req = withRoomAuth(httptest.NewRequest(http.MethodPost, "/rooms", strings.NewReader(body)), teacherID, "teacher")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var created rooms.Room
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("failed to decode created room: %v", err)
	}
	if created.Name != "11-А" {
		t.Errorf("expected name 11-А, got %s", created.Name)
	}

	// 3. GET /rooms?view_mode=teacher
	req = withRoomAuth(httptest.NewRequest(http.MethodGet, "/rooms?view_mode=teacher", nil), teacherID, "teacher")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var listResp struct {
		Items []rooms.RoomListItem `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("failed to decode list: %v", err)
	}
	if len(listResp.Items) != 1 {
		t.Errorf("expected 1 item, got %d", len(listResp.Items))
	}
}

func TestRoomHandlers_GetUpdateDelete(t *testing.T) {
	router, _, _, svc := setupRoomTestRouter()
	teacherID := uuid.New()
	studentID := uuid.New()

	room, err := svc.CreateRoom(context.Background(), teacherID, "teacher", rooms.CreateRoomRequest{
		Name:       "Test Room",
		MaxMembers: 30,
	})
	if err != nil {
		t.Fatalf("failed to create room: %v", err)
	}

	// GET /rooms/{id} - Teacher access
	req := withRoomAuth(httptest.NewRequest(http.MethodGet, "/rooms/"+room.ID.String(), nil), teacherID, "teacher")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// PATCH /rooms/{id} - Update name
	updateBody := `{"name":"Updated Room","max_members":35}`
	req = withRoomAuth(httptest.NewRequest(http.MethodPatch, "/rooms/"+room.ID.String(), strings.NewReader(updateBody)), teacherID, "teacher")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// DELETE /rooms/{id} - Forbidden for student
	req = withRoomAuth(httptest.NewRequest(http.MethodDelete, "/rooms/"+room.ID.String(), nil), studentID, "student")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}

	// DELETE /rooms/{id} - Teacher deletes
	req = withRoomAuth(httptest.NewRequest(http.MethodDelete, "/rooms/"+room.ID.String(), nil), teacherID, "teacher")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// Verify deleted room is not found
	req = withRoomAuth(httptest.NewRequest(http.MethodGet, "/rooms/"+room.ID.String(), nil), teacherID, "teacher")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestRoomHandlers_JoinAndFreeCode(t *testing.T) {
	router, repo, _, svc := setupRoomTestRouter()
	teacherID := uuid.New()
	studentID := uuid.New()
	repo.userNames[teacherID] = "Иван Преподаватель"

	room, err := svc.CreateRoom(context.Background(), teacherID, "teacher", rooms.CreateRoomRequest{
		Name:       "Join Test Room",
		MaxMembers: 10,
	})
	if err != nil {
		t.Fatalf("failed to create room: %v", err)
	}

	// GET /rooms/join/{invite_code} - Public preview
	req := httptest.NewRequest(http.MethodGet, "/rooms/join/"+room.InviteCode, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 preview, got %d: %s", rec.Code, rec.Body.String())
	}

	// POST /rooms/join/{invite_code} - Student joins
	req = withRoomAuth(httptest.NewRequest(http.MethodPost, "/rooms/join/"+room.InviteCode, nil), studentID, "student")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 join, got %d: %s", rec.Code, rec.Body.String())
	}

	// PUT /rooms/{id}/free-code - Student saves code
	codeBody := `{"code":"print('EGE 100 points!')"}`
	req = withRoomAuth(httptest.NewRequest(http.MethodPut, "/rooms/"+room.ID.String()+"/free-code", strings.NewReader(codeBody)), studentID, "student")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 free code, got %d: %s", rec.Code, rec.Body.String())
	}

	// DELETE /rooms/{id}/members/{user_id} - Student leaves
	req = withRoomAuth(httptest.NewRequest(http.MethodDelete, "/rooms/"+room.ID.String()+"/members/"+studentID.String(), nil), studentID, "student")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 leave, got %d", rec.Code)
	}
}
