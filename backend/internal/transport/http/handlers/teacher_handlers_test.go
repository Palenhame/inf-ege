package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"backend/internal/domain/proctoring"
	"backend/internal/domain/tasks"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type mockProctoringSvc struct {
	matrixFunc     func(ctx context.Context, teacherID, assignmentID uuid.UUID) (*proctoring.MatrixSnapshotDTO, error)
	inspectFunc    func(ctx context.Context, teacherID, sessionID uuid.UUID) (*proctoring.SessionInspectionDTO, error)
	createTaskFunc func(ctx context.Context, teacherID uuid.UUID, req proctoring.CreateTeacherTaskRequest) (*tasks.TaskDTO, error)
	listTasksFunc  func(ctx context.Context, teacherID uuid.UUID, limit, offset int, kim *int, topicID *int, q string) ([]tasks.TaskDTO, int, error)
}

func (m *mockProctoringSvc) GetMatrixSnapshot(ctx context.Context, teacherID, assignmentID uuid.UUID) (*proctoring.MatrixSnapshotDTO, error) {
	if m.matrixFunc != nil {
		return m.matrixFunc(ctx, teacherID, assignmentID)
	}
	return &proctoring.MatrixSnapshotDTO{AssignmentID: assignmentID, Title: "Пробник №1"}, nil
}

func (m *mockProctoringSvc) GetSessionDossier(ctx context.Context, teacherID, sessionID uuid.UUID) (*proctoring.SessionInspectionDTO, error) {
	if m.inspectFunc != nil {
		return m.inspectFunc(ctx, teacherID, sessionID)
	}
	return &proctoring.SessionInspectionDTO{SessionID: sessionID, UserName: "Иван Смирнов"}, nil
}

func (m *mockProctoringSvc) CreateTeacherTask(ctx context.Context, teacherID uuid.UUID, req proctoring.CreateTeacherTaskRequest) (*tasks.TaskDTO, error) {
	if m.createTaskFunc != nil {
		return m.createTaskFunc(ctx, teacherID, req)
	}
	return &tasks.TaskDTO{ID: 101, OwnerID: &teacherID, Difficulty: req.Difficulty}, nil
}

func (m *mockProctoringSvc) ListTeacherTasks(ctx context.Context, teacherID uuid.UUID, limit, offset int, kim *int, topicID *int, q string) ([]tasks.TaskDTO, int, error) {
	if m.listTasksFunc != nil {
		return m.listTasksFunc(ctx, teacherID, limit, offset, kim, topicID, q)
	}
	return []tasks.TaskDTO{{ID: 101, OwnerID: &teacherID}}, 1, nil
}

func setupTeacherRouter(svc proctoring.Service) *chi.Mux {
	r := chi.NewRouter()
	h := NewTeacherHandler(svc)
	h.RegisterRoutes(r)
	return r
}

func TestTeacherHandlers_MatrixSnapshot(t *testing.T) {
	svc := &mockProctoringSvc{}
	router := setupTeacherRouter(svc)
	assignmentID := uuid.New()
	teacherID := uuid.New()

	// 1. Unauthenticated -> 401
	req := httptest.NewRequest(http.MethodGet, "/assignments/"+assignmentID.String()+"/matrix", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}

	// 2. Authenticated -> 200 OK
	req = httptest.NewRequest(http.MethodGet, "/assignments/"+assignmentID.String()+"/matrix", nil)
	req = authReq(req, teacherID, "teacher")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestTeacherHandlers_SessionInspect(t *testing.T) {
	svc := &mockProctoringSvc{}
	router := setupTeacherRouter(svc)
	sessionID := uuid.New()
	teacherID := uuid.New()

	req := httptest.NewRequest(http.MethodGet, "/sessions/"+sessionID.String()+"/inspect", nil)
	req = authReq(req, teacherID, "teacher")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestTeacherHandlers_AuthorTasks(t *testing.T) {
	svc := &mockProctoringSvc{}
	router := setupTeacherRouter(svc)
	teacherID := uuid.New()

	taskReq := proctoring.CreateTeacherTaskRequest{
		KimNumbers: []int{24},
		Difficulty: 2,
		TextMD:     "Текст задачи...",
	}
	body, _ := json.Marshal(taskReq)

	// Create author task
	req := httptest.NewRequest(http.MethodPost, "/teacher/tasks", bytes.NewReader(body))
	req = authReq(req, teacherID, "teacher")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Errorf("expected 201 on task create, got %d", rec.Code)
	}

	// List author tasks
	req = httptest.NewRequest(http.MethodGet, "/teacher/tasks", nil)
	req = authReq(req, teacherID, "teacher")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 on task list, got %d", rec.Code)
	}
}
