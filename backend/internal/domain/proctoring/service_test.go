package proctoring

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"backend/internal/domain/assignments"
	"backend/internal/domain/tasks"
	"github.com/google/uuid"
)

type mockProctoringRepo struct {
	assignmentHeader  *AssignmentHeader
	students          []StudentMatrixRow
	taskStats         []MatrixTaskStatDTO
	inspectionSession *InspectionSessionRow
	events            []TimelineEventDTO
	codes             []TaskCodeRow
	teacherTasks      []tasks.TaskDTO
}

func newMockProctoringRepo() *mockProctoringRepo {
	return &mockProctoringRepo{}
}

func (m *mockProctoringRepo) GetAssignmentHeader(ctx context.Context, assignmentID uuid.UUID) (*AssignmentHeader, error) {
	if m.assignmentHeader == nil || m.assignmentHeader.ID != assignmentID {
		return nil, ErrNotFound
	}
	return m.assignmentHeader, nil
}

func (m *mockProctoringRepo) GetMatrixStudents(ctx context.Context, assignmentID uuid.UUID, roomID *uuid.UUID) ([]StudentMatrixRow, error) {
	return m.students, nil
}

func (m *mockProctoringRepo) GetMatrixTaskStats(ctx context.Context, assignmentID uuid.UUID) ([]MatrixTaskStatDTO, error) {
	return m.taskStats, nil
}

func (m *mockProctoringRepo) GetInspectionSession(ctx context.Context, sessionID uuid.UUID) (*InspectionSessionRow, error) {
	if m.inspectionSession == nil || m.inspectionSession.SessionID != sessionID {
		return nil, ErrNotFound
	}
	return m.inspectionSession, nil
}

func (m *mockProctoringRepo) GetTimelineEvents(ctx context.Context, sessionID uuid.UUID) ([]TimelineEventDTO, error) {
	return m.events, nil
}

func (m *mockProctoringRepo) GetSessionTaskCodes(ctx context.Context, sessionID uuid.UUID) ([]TaskCodeRow, error) {
	return m.codes, nil
}

func (m *mockProctoringRepo) CreateTeacherTask(ctx context.Context, teacherID uuid.UUID, req CreateTeacherTaskRequest) (*tasks.TaskDTO, error) {
	t := tasks.TaskDTO{
		ID:           len(m.teacherTasks) + 1,
		OwnerID:      &teacherID,
		IsPublicBank: false,
		KimNumbers:   req.KimNumbers,
		Difficulty:   req.Difficulty,
		Source:       &req.Source,
		TextMD:       req.TextMD,
		Images:       req.Images,
		Files:        req.Files,
		AnswerConfig: req.AnswerConfig,
		Solution: &tasks.SolutionDTO{
			CorrectAnswer: &req.CorrectAnswer,
			SolutionMD:    req.SolutionMD,
			SolutionCode:  req.SolutionCode,
		},
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	m.teacherTasks = append(m.teacherTasks, t)
	return &t, nil
}

func (m *mockProctoringRepo) ListTeacherTasks(ctx context.Context, teacherID uuid.UUID, limit, offset int, kim *int, topicID *int, q string) ([]tasks.TaskDTO, int, error) {
	var filtered []tasks.TaskDTO
	for _, t := range m.teacherTasks {
		if t.OwnerID != nil && *t.OwnerID == teacherID {
			filtered = append(filtered, t)
		}
	}
	return filtered, len(filtered), nil
}

func TestProctoring_GetMatrixSnapshot(t *testing.T) {
	repo := newMockProctoringRepo()
	storage := assignments.NewMemoryCodeStorage()
	svc := NewService(repo, storage)
	ctx := context.Background()

	teacherID := uuid.New()
	assignmentID := uuid.New()
	roomID := uuid.New()

	repo.assignmentHeader = &AssignmentHeader{
		ID:        assignmentID,
		RoomID:    &roomID,
		CreatorID: teacherID,
		Title:     "Пробник №1",
		Mode:      "mock_exam",
	}

	studentID := uuid.New()
	sessionID := uuid.New()
	status := "in_progress"
	blur := 2
	paste := 1
	var score int16 = 14
	var taskID = 1042
	var pos int16 = 1
	isCorr := true
	var earned int16 = 1
	attempts := 1

	repo.students = []StudentMatrixRow{
		{
			UserID:         studentID,
			Username:       "ivan_student",
			FirstName:      "Иван",
			LastName:       "Смирнов",
			SessionID:      &sessionID,
			Status:         &status,
			BlurCount:      &blur,
			PasteCount:     &paste,
			PrimaryScore:   &score,
			SecondaryScore: nil,
			TaskID:         &taskID,
			Position:       &pos,
			IsCorrect:      &isCorr,
			EarnedScore:    &earned,
			AttemptsUsed:   &attempts,
		},
	}

	repo.taskStats = []MatrixTaskStatDTO{
		{
			TaskID:         1042,
			Position:       1,
			SolvedCount:    12,
			AttemptedCount: 18,
			AvgAttempts:    1.6,
			AvgTimeSec:     310,
		},
	}

	snapshot, err := svc.GetMatrixSnapshot(ctx, teacherID, assignmentID)
	if err != nil {
		t.Fatalf("GetMatrixSnapshot failed: %v", err)
	}

	if snapshot.Title != "Пробник №1" {
		t.Errorf("expected title 'Пробник №1', got %s", snapshot.Title)
	}
	if len(snapshot.Students) != 1 {
		t.Fatalf("expected 1 student in matrix, got %d", len(snapshot.Students))
	}
	st := snapshot.Students[0]
	if st.Username != "ivan_student" {
		t.Errorf("expected username 'ivan_student', got %s", st.Username)
	}
	if st.BlurCount != 2 || st.PasteCount != 1 {
		t.Errorf("expected blur=2, paste=1, got blur=%d, paste=%d", st.BlurCount, st.PasteCount)
	}
	if len(st.Tasks) != 1 {
		t.Errorf("expected 1 task in student cell map, got %d", len(st.Tasks))
	}
	if len(snapshot.TaskStats) != 1 {
		t.Errorf("expected 1 task stat entry, got %d", len(snapshot.TaskStats))
	}

	// Unauthorized caller check
	otherUser := uuid.New()
	_, err = svc.GetMatrixSnapshot(ctx, otherUser, assignmentID)
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("expected ErrForbidden for non-creator, got %v", err)
	}
}

func TestProctoring_GetSessionDossier(t *testing.T) {
	repo := newMockProctoringRepo()
	storage := assignments.NewMemoryCodeStorage()
	svc := NewService(repo, storage)
	ctx := context.Background()

	teacherID := uuid.New()
	sessionID := uuid.New()
	assignmentID := uuid.New()
	studentID := uuid.New()

	repo.inspectionSession = &InspectionSessionRow{
		SessionID:    sessionID,
		AssignmentID: assignmentID,
		UserID:       studentID,
		UserName:     "Иван Смирнов",
		Status:       "in_progress",
		StartedAt:    time.Now().UTC().Add(-30 * time.Minute),
		PrimaryScore: 14,
		BlurCount:    3,
		PasteCount:   1,
		CreatorID:    teacherID,
	}

	repo.events = []TimelineEventDTO{
		{
			ID:         1,
			Event:      "session_started",
			Payload:    json.RawMessage(`{}`),
			OccurredAt: "2026-11-12T14:00:00Z",
		},
		{
			ID:         2,
			Event:      "insert_in_ide",
			Payload:    json.RawMessage(`{"chars_count": 148}`),
			OccurredAt: "2026-11-12T14:02:10Z",
		},
	}

	codeKey := "sessions/test/tasks/1042.py"
	_, _ = storage.SaveCode(ctx, "test", 1042, "from itertools import product\nprint(1)")
	comment := "Решено быстро"
	repo.codes = []TaskCodeRow{
		{
			TaskID:         1042,
			CodeS3Key:      &codeKey,
			PasteRanges:    []byte("[[0, 65]]"),
			StudentComment: &comment,
			UpdatedAt:      time.Now().UTC(),
		},
	}

	dossier, err := svc.GetSessionDossier(ctx, teacherID, sessionID)
	if err != nil {
		t.Fatalf("GetSessionDossier failed: %v", err)
	}

	if dossier.UserName != "Иван Смирнов" {
		t.Errorf("expected user_name 'Иван Смирнов', got %s", dossier.UserName)
	}
	if len(dossier.Events) != 2 {
		t.Errorf("expected 2 timeline events, got %d", len(dossier.Events))
	}
	if len(dossier.TaskCodes) != 1 {
		t.Fatalf("expected 1 task code, got %d", len(dossier.TaskCodes))
	}
	tc := dossier.TaskCodes[0]
	if tc.Code == "" {
		t.Errorf("expected non-empty code retrieved from storage")
	}

	// Unauthorized caller check
	_, err = svc.GetSessionDossier(ctx, uuid.New(), sessionID)
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("expected ErrForbidden for non-creator, got %v", err)
	}
}

func TestProctoring_TeacherTasks(t *testing.T) {
	repo := newMockProctoringRepo()
	storage := assignments.NewMemoryCodeStorage()
	svc := NewService(repo, storage)
	ctx := context.Background()

	teacherID := uuid.New()
	req := CreateTeacherTaskRequest{
		KimNumbers: []int{24},
		TopicID:    2401,
		Difficulty: 2,
		Source:     "Авторская задача",
		TextMD:     "Условие задачи...",
		AnswerConfig: tasks.AnswerConfig{
			AnswerType: "single",
			MaxScore:   1,
		},
		CorrectAnswer: tasks.CorrectAnswer{
			AnswerType: "single",
			Accepted:   []string{"54"},
		},
	}

	dto, err := svc.CreateTeacherTask(ctx, teacherID, req)
	if err != nil {
		t.Fatalf("CreateTeacherTask failed: %v", err)
	}
	if dto.ID != 1 {
		t.Errorf("expected task ID 1, got %d", dto.ID)
	}
	if dto.IsPublicBank {
		t.Errorf("expected author task to NOT be public bank")
	}

	items, total, err := svc.ListTeacherTasks(ctx, teacherID, 20, 0, nil, nil, "")
	if err != nil {
		t.Fatalf("ListTeacherTasks failed: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Errorf("expected 1 task in teacher list, got total=%d len=%d", total, len(items))
	}
}
