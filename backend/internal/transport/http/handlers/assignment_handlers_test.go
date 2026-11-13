package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"backend/internal/domain/assignments"
	"backend/internal/transport/http/middleware"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type mockAssignmentSvc struct {
	createFunc    func(ctx context.Context, creatorID, roomID uuid.UUID, req assignments.CreateAssignmentRequest) (*assignments.AssignmentDTO, error)
	updateFunc    func(ctx context.Context, callerID, assignmentID uuid.UUID, req assignments.UpdateAssignmentRequest) (*assignments.AssignmentDTO, error)
	deleteFunc    func(ctx context.Context, callerID, assignmentID uuid.UUID) error
	selfStartFunc func(ctx context.Context, userID uuid.UUID, req assignments.SelfStartRequest) (*assignments.SelfStartResponse, error)
	startFunc     func(ctx context.Context, userID, assignmentID uuid.UUID) (*assignments.StartSessionResponse, error)
	getFunc       func(ctx context.Context, callerID, sessionID uuid.UUID) (*assignments.SessionDetailDTO, error)
	answerFunc      func(ctx context.Context, userID, sessionID uuid.UUID, taskID int, req assignments.AnswerTaskRequest) (*assignments.AnswerTaskResponse, error)
	finishFunc      func(ctx context.Context, userID, sessionID uuid.UUID) (*assignments.SessionFinishDTO, error)
	getScaleFunc    func(ctx context.Context) ([]assignments.ExamScaleEntry, error)
	updateScaleFunc func(ctx context.Context, entries []assignments.ExamScaleEntry) error
}

func (m *mockAssignmentSvc) CreateAssignment(ctx context.Context, creatorID, roomID uuid.UUID, req assignments.CreateAssignmentRequest) (*assignments.AssignmentDTO, error) {
	if m.createFunc != nil {
		return m.createFunc(ctx, creatorID, roomID, req)
	}
	return &assignments.AssignmentDTO{ID: uuid.New(), Title: req.Title, Mode: req.Mode}, nil
}

func (m *mockAssignmentSvc) UpdateAssignment(ctx context.Context, callerID, assignmentID uuid.UUID, req assignments.UpdateAssignmentRequest) (*assignments.AssignmentDTO, error) {
	if m.updateFunc != nil {
		return m.updateFunc(ctx, callerID, assignmentID, req)
	}
	return &assignments.AssignmentDTO{ID: assignmentID}, nil
}

func (m *mockAssignmentSvc) DeleteAssignment(ctx context.Context, callerID, assignmentID uuid.UUID) error {
	if m.deleteFunc != nil {
		return m.deleteFunc(ctx, callerID, assignmentID)
	}
	return nil
}

func (m *mockAssignmentSvc) SelfStartExam(ctx context.Context, userID uuid.UUID, req assignments.SelfStartRequest) (*assignments.SelfStartResponse, error) {
	if m.selfStartFunc != nil {
		return m.selfStartFunc(ctx, userID, req)
	}
	return &assignments.SelfStartResponse{SessionID: uuid.New(), AssignmentID: uuid.New(), ExpiresAt: time.Now().Format(time.RFC3339)}, nil
}

func (m *mockAssignmentSvc) StartSession(ctx context.Context, userID, assignmentID uuid.UUID) (*assignments.StartSessionResponse, error) {
	if m.startFunc != nil {
		return m.startFunc(ctx, userID, assignmentID)
	}
	return &assignments.StartSessionResponse{SessionID: uuid.New(), AssignmentID: assignmentID, Status: assignments.SessionInProgress}, nil
}

func (m *mockAssignmentSvc) GetSession(ctx context.Context, callerID, sessionID uuid.UUID) (*assignments.SessionDetailDTO, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx, callerID, sessionID)
	}
	return &assignments.SessionDetailDTO{ID: sessionID, Status: assignments.SessionInProgress}, nil
}

func (m *mockAssignmentSvc) AnswerTask(ctx context.Context, userID, sessionID uuid.UUID, taskID int, req assignments.AnswerTaskRequest) (*assignments.AnswerTaskResponse, error) {
	if m.answerFunc != nil {
		return m.answerFunc(ctx, userID, sessionID, taskID, req)
	}
	return &assignments.AnswerTaskResponse{MaxScore: 1, AttemptsUsed: 1, RevealPolicy: assignments.RevealAfterSubmit}, nil
}

func (m *mockAssignmentSvc) FinishSession(ctx context.Context, userID, sessionID uuid.UUID) (*assignments.SessionFinishDTO, error) {
	if m.finishFunc != nil {
		return m.finishFunc(ctx, userID, sessionID)
	}
	return &assignments.SessionFinishDTO{SessionID: sessionID, Status: assignments.SessionFinished, PrimaryScore: 26, SecondaryScore: 90}, nil
}

func (m *mockAssignmentSvc) GetExamScale(ctx context.Context) ([]assignments.ExamScaleEntry, error) {
	if m.getScaleFunc != nil {
		return m.getScaleFunc(ctx)
	}
	return []assignments.ExamScaleEntry{{PrimaryScore: 0, SecondaryScore: 0}, {PrimaryScore: 29, SecondaryScore: 100}}, nil
}

func (m *mockAssignmentSvc) UpdateExamScale(ctx context.Context, entries []assignments.ExamScaleEntry) error {
	if m.updateScaleFunc != nil {
		return m.updateScaleFunc(ctx, entries)
	}
	return nil
}

func authReq(r *http.Request, userID uuid.UUID, role string) *http.Request {
	ctx := middleware.ContextWithUser(r.Context(), middleware.UserContext{
		UserID:          userID,
		Role:            role,
		IsSuperuser:     role == "superuser",
		IsAuthenticated: true,
	})
	return r.WithContext(ctx)
}

func setupAssignmentRouter(svc assignments.Service) *chi.Mux {
	r := chi.NewRouter()
	h := NewAssignmentHandler(svc)
	h.RegisterRoutes(r)
	return r
}

func TestAssignmentHandlers_Create(t *testing.T) {
	svc := &mockAssignmentSvc{}
	router := setupAssignmentRouter(svc)
	roomID := uuid.New()
	userID := uuid.New()

	body, _ := json.Marshal(assignments.CreateAssignmentRequest{
		Title:   "ДЗ 1",
		Mode:    assignments.ModeHomework,
		TaskIDs: []int{1042},
	})

	// 1. Unauthenticated -> 401
	req := httptest.NewRequest(http.MethodPost, "/rooms/"+roomID.String()+"/assignments", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}

	// 2. Authenticated -> 201 Created
	req = httptest.NewRequest(http.MethodPost, "/rooms/"+roomID.String()+"/assignments", bytes.NewReader(body))
	req = authReq(req, userID, "teacher")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d", rec.Code)
	}
}

func TestAssignmentHandlers_SelfStart(t *testing.T) {
	svc := &mockAssignmentSvc{}
	router := setupAssignmentRouter(svc)
	userID := uuid.New()

	req := httptest.NewRequest(http.MethodPost, "/exams/self-start", bytes.NewReader([]byte("{}")))
	req = authReq(req, userID, "student")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d", rec.Code)
	}
}

func TestAssignmentHandlers_StartAndGetSession(t *testing.T) {
	svc := &mockAssignmentSvc{}
	router := setupAssignmentRouter(svc)
	assignmentID := uuid.New()
	userID := uuid.New()

	// Start
	req := httptest.NewRequest(http.MethodPost, "/assignments/"+assignmentID.String()+"/start", nil)
	req = authReq(req, userID, "student")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	// Get session
	sessionID := uuid.New()
	req = httptest.NewRequest(http.MethodGet, "/sessions/"+sessionID.String(), nil)
	req = authReq(req, userID, "student")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestAssignmentHandlers_AnswerAndFinish(t *testing.T) {
	svc := &mockAssignmentSvc{}
	router := setupAssignmentRouter(svc)
	sessionID := uuid.New()
	userID := uuid.New()

	// Answer task
	ansBody, _ := json.Marshal(assignments.AnswerTaskRequest{
		UserAnswer: json.RawMessage(`{"answer_type":"single","value":"1024"}`),
	})
	req := httptest.NewRequest(http.MethodPost, "/sessions/"+sessionID.String()+"/tasks/1042/answer", bytes.NewReader(ansBody))
	req = authReq(req, userID, "student")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 on answer, got %d", rec.Code)
	}

	// Finish session
	req = httptest.NewRequest(http.MethodPost, "/sessions/"+sessionID.String()+"/finish", nil)
	req = authReq(req, userID, "student")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 on finish, got %d", rec.Code)
	}
}

func TestAssignmentHandlers_ExamAndSessionRoutes(t *testing.T) {
	svc := &mockAssignmentSvc{}
	h := NewAssignmentHandler(svc)
	userID := uuid.New()
	sessionID := uuid.New()

	// ExamRoutes
	examRouter := chi.NewRouter()
	examRouter.Mount("/exams", h.ExamRoutes())

	req := httptest.NewRequest(http.MethodPost, "/exams/self-start", bytes.NewReader([]byte("{}")))
	req = authReq(req, userID, "student")
	rec := httptest.NewRecorder()
	examRouter.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Errorf("expected 201 on ExamRoutes self-start, got %d", rec.Code)
	}

	// SessionRoutes with inspect handler
	inspectCalled := false
	sessRouter := chi.NewRouter()
	sessRouter.Mount("/sessions", h.SessionRoutes(func(w http.ResponseWriter, r *http.Request) {
		inspectCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req = httptest.NewRequest(http.MethodGet, "/sessions/"+sessionID.String()+"/inspect", nil)
	req = authReq(req, userID, "teacher")
	rec = httptest.NewRecorder()
	sessRouter.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !inspectCalled {
		t.Errorf("expected 200 and inspectCalled=true, got %d (called=%v)", rec.Code, inspectCalled)
	}
}

func TestAssignmentHandlers_ExamScale(t *testing.T) {
	svc := &mockAssignmentSvc{}
	h := NewAssignmentHandler(svc)
	r := chi.NewRouter()
	r.Get("/admin/exam-scale", h.GetExamScale)
	r.Put("/admin/exam-scale", h.UpdateExamScale)

	// Non-superuser -> 403 FORBIDDEN
	req := httptest.NewRequest(http.MethodGet, "/admin/exam-scale", nil)
	req = authReq(req, uuid.New(), "teacher")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for teacher GetExamScale, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPut, "/admin/exam-scale", bytes.NewReader([]byte(`{"items":[{"primary_score":1,"secondary_score":7}]}`)))
	req = authReq(req, uuid.New(), "teacher")
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for teacher UpdateExamScale, got %d", rec.Code)
	}

	// Superuser Get -> 200
	req = httptest.NewRequest(http.MethodGet, "/admin/exam-scale", nil)
	req = authReq(req, uuid.New(), "superuser")
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for superuser GetExamScale, got %d", rec.Code)
	}

	// Superuser Put invalid range -> 400
	req = httptest.NewRequest(http.MethodPut, "/admin/exam-scale", bytes.NewReader([]byte(`{"items":[{"primary_score":30,"secondary_score":100}]}`)))
	req = authReq(req, uuid.New(), "superuser")
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for primary_score=30, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPut, "/admin/exam-scale", bytes.NewReader([]byte(`{"items":[{"primary_score":10,"secondary_score":101}]}`)))
	req = authReq(req, uuid.New(), "superuser")
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for secondary_score=101, got %d", rec.Code)
	}

	// Superuser Put valid -> 200
	req = httptest.NewRequest(http.MethodPut, "/admin/exam-scale", bytes.NewReader([]byte(`{"items":[{"primary_score":1,"secondary_score":7}]}`)))
	req = authReq(req, uuid.New(), "superuser")
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for valid UpdateExamScale, got %d: %s", rec.Code, rec.Body.String())
	}
}
