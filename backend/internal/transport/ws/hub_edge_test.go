package ws

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type mockRedisCacher struct {
	data map[string]string
}

func newMockRedisCacher() *mockRedisCacher {
	return &mockRedisCacher{
		data: make(map[string]string),
	}
}

func (m *mockRedisCacher) Get(ctx context.Context, key string) *redis.StringCmd {
	cmd := redis.NewStringCmd(ctx)
	if val, ok := m.data[key]; ok {
		cmd.SetVal(val)
	} else {
		cmd.SetErr(redis.Nil)
	}
	return cmd
}

func (m *mockRedisCacher) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd {
	cmd := redis.NewStatusCmd(ctx)
	switch v := value.(type) {
	case string:
		m.data[key] = v
	case []byte:
		m.data[key] = string(v)
	}
	cmd.SetVal("OK")
	return cmd
}

type denyingAuthorizer struct{}

func (d *denyingAuthorizer) CanWatchAssignment(ctx context.Context, teacherID uuid.UUID, assignmentID uuid.UUID) (bool, error) {
	return false, errors.New("unauthorized room")
}

func (d *denyingAuthorizer) CanWatchStudentLive(ctx context.Context, teacherID uuid.UUID, sessionID uuid.UUID) (bool, error) {
	return false, errors.New("unauthorized student")
}

func TestHub_BroadcastCellPatch(t *testing.T) {
	hub := NewHub(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	teacherConn := newMockWSConn()
	teacher := NewClient(uuid.New(), "teacher", teacherConn, hub)
	hub.Register(teacher)
	go teacher.WritePump(ctx)

	assignmentID := uuid.New()
	sessionID := uuid.New()
	userID := uuid.New()

	// Teacher watches assignment
	watchPayload, _ := json.Marshal(TeacherWatchAssignmentPayload{AssignmentID: assignmentID})
	hub.HandleMessage(teacher, Envelope{
		Type: TypeTeacherWatchAssignment,
		Data: watchPayload,
	})

	time.Sleep(20 * time.Millisecond)

	// Broadcast cell patch
	cellPatch := &MatrixCellPatchPayload{
		AssignmentID:        assignmentID,
		UserID:              userID,
		SessionID:           sessionID,
		TaskID:              1042,
		IsCorrect:           true,
		EarnedScore:         1,
		Attempts:            1,
		TotalPrimaryScore:   1,
		TotalSecondaryScore: 7,
	}
	hub.BroadcastCellPatch(assignmentID, cellPatch)

	time.Sleep(30 * time.Millisecond)

	teacherConn.mu.Lock()
	foundCell := false
	for _, raw := range teacherConn.writtenMsgs {
		var env Envelope
		if err := json.Unmarshal(raw, &env); err == nil && env.Type == TypeMatrixCellPatch {
			var p MatrixCellPatchPayload
			if err := json.Unmarshal(env.Data, &p); err == nil && p.EarnedScore == 1 {
				foundCell = true
				break
			}
		}
	}
	teacherConn.mu.Unlock()

	if !foundCell {
		t.Error("expected teacher to receive matrix.cell_patch")
	}
}

func TestHub_AuthorizationDenials(t *testing.T) {
	authorizer := &denyingAuthorizer{}
	hub := NewHub(nil, nil, authorizer)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	teacherConn := newMockWSConn()
	teacher := NewClient(uuid.New(), "teacher", teacherConn, hub)
	hub.Register(teacher)
	go teacher.WritePump(ctx)

	studentConn := newMockWSConn()
	student := NewClient(uuid.New(), "student", studentConn, hub)
	hub.Register(student)
	go student.WritePump(ctx)

	// Student tries to watch assignment (role check failure)
	watchPayload, _ := json.Marshal(TeacherWatchAssignmentPayload{AssignmentID: uuid.New()})
	hub.HandleMessage(student, Envelope{
		Type: TypeTeacherWatchAssignment,
		Data: watchPayload,
	})

	// Teacher denied by authorizer
	hub.HandleMessage(teacher, Envelope{
		Type: TypeTeacherWatchAssignment,
		Data: watchPayload,
	})

	// Student tries to watch student live
	watchLivePayload, _ := json.Marshal(TeacherWatchStudentLivePayload{SessionID: uuid.New()})
	hub.HandleMessage(student, Envelope{
		Type: TypeTeacherWatchStudentLive,
		Data: watchLivePayload,
	})

	// Teacher denied live stream by authorizer
	hub.HandleMessage(teacher, Envelope{
		Type: TypeTeacherWatchStudentLive,
		Data: watchLivePayload,
	})

	time.Sleep(40 * time.Millisecond)

	teacherConn.mu.Lock()
	forbiddenCount := 0
	for _, raw := range teacherConn.writtenMsgs {
		var env Envelope
		if err := json.Unmarshal(raw, &env); err == nil && env.Type == TypeServerError {
			forbiddenCount++
		}
	}
	teacherConn.mu.Unlock()

	if forbiddenCount < 2 {
		t.Errorf("expected at least 2 FORBIDDEN errors sent to teacher, got %d", forbiddenCount)
	}
}

func TestHub_RedisFrameCaching(t *testing.T) {
	redisMock := newMockRedisCacher()
	hub := NewHub(redisMock, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	teacherConn := newMockWSConn()
	teacher := NewClient(uuid.New(), "teacher", teacherConn, hub)
	hub.Register(teacher)
	go teacher.WritePump(ctx)

	sessionID := uuid.New()

	// Pre-populate Redis cache with a frame
	cachedFrame := TeacherStudentLiveFramePayload{
		SessionID: sessionID,
		TaskID:    15,
		Code:      "# cached in redis",
		FrameTS:   time.Now().UTC(),
	}
	cachedJSON, _ := json.Marshal(cachedFrame)
	redisMock.data["live:frame:"+sessionID.String()] = string(cachedJSON)

	// Teacher watches student live
	watchPayload, _ := json.Marshal(TeacherWatchStudentLivePayload{SessionID: sessionID})
	hub.HandleMessage(teacher, Envelope{
		Type: TypeTeacherWatchStudentLive,
		Data: watchPayload,
	})

	time.Sleep(30 * time.Millisecond)

	teacherConn.mu.Lock()
	foundRedisFrame := false
	for _, raw := range teacherConn.writtenMsgs {
		var env Envelope
		if err := json.Unmarshal(raw, &env); err == nil && env.Type == TypeTeacherStudentLiveFrame {
			var f TeacherStudentLiveFramePayload
			if err := json.Unmarshal(env.Data, &f); err == nil && f.Code == "# cached in redis" {
				foundRedisFrame = true
				break
			}
		}
	}
	teacherConn.mu.Unlock()

	if !foundRedisFrame {
		t.Error("expected teacher to receive frame fetched from Redis cache")
	}
}
