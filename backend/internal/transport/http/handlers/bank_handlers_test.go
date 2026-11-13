package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"backend/internal/domain/tasks"
	"backend/internal/transport/http/handlers"
	"backend/internal/transport/http/middleware"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type mockTasksService struct {
	tasks           map[int]tasks.TaskDTO
	topics          []tasks.KimGroupDTO
	submitResponse  *tasks.TaskSubmitResponse
	errToReturn     error
}

func (m *mockTasksService) ListTasks(ctx context.Context, filter tasks.ListTasksFilter) ([]tasks.TaskDTO, int, error) {
	if m.errToReturn != nil {
		return nil, 0, m.errToReturn
	}
	var res []tasks.TaskDTO
	for _, t := range m.tasks {
		res = append(res, t)
	}
	return res, len(res), nil
}

func (m *mockTasksService) GetTask(ctx context.Context, id int, userID *uuid.UUID) (*tasks.TaskDTO, error) {
	if m.errToReturn != nil {
		return nil, m.errToReturn
	}
	t, ok := m.tasks[id]
	if !ok {
		return nil, tasks.ErrTaskNotFound
	}
	return &t, nil
}

func (m *mockTasksService) SubmitTask(ctx context.Context, id int, userID uuid.UUID, req tasks.TaskSubmitRequest) (*tasks.TaskSubmitResponse, error) {
	if m.errToReturn != nil {
		return nil, m.errToReturn
	}
	return m.submitResponse, nil
}

func (m *mockTasksService) CreateAuthorTask(ctx context.Context, ownerID uuid.UUID, t *tasks.Task) (int, error) {
	return 100, nil
}
func (m *mockTasksService) CreateAdminTask(ctx context.Context, t *tasks.Task) (int, error) {
	return 101, nil
}
func (m *mockTasksService) UpdateTask(ctx context.Context, id int, t *tasks.Task) error {
	return nil
}
func (m *mockTasksService) DeleteTask(ctx context.Context, id int) error {
	return nil
}
func (m *mockTasksService) ListAuthorTasks(ctx context.Context, ownerID uuid.UUID, filter tasks.ListTasksFilter) ([]tasks.TaskDTO, int, error) {
	return nil, 0, nil
}
func (m *mockTasksService) GetTopics(ctx context.Context) ([]tasks.KimGroupDTO, error) {
	return m.topics, nil
}
func (m *mockTasksService) CreateTopic(ctx context.Context, kimNumber int, name string, sortOrder int) (*tasks.TopicDTO, error) {
	return &tasks.TopicDTO{ID: 1, KimNumber: kimNumber, Name: name, SortOrder: sortOrder}, nil
}
func (m *mockTasksService) UpdateTopic(ctx context.Context, id int, name string, sortOrder int) (*tasks.TopicDTO, error) {
	return &tasks.TopicDTO{ID: id, Name: name, SortOrder: sortOrder}, nil
}
func (m *mockTasksService) DeleteTopic(ctx context.Context, id int) error {
	return nil
}

func TestBankHandler_GetTopics(t *testing.T) {
	mockSvc := &mockTasksService{
		topics: []tasks.KimGroupDTO{
			{
				KimNumber: 24,
				Topics: []tasks.TopicDTO{
					{ID: 2401, KimNumber: 24, Name: "Строки"},
				},
			},
		},
	}

	handler := handlers.NewBankHandler(mockSvc, nil, nil, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/topics", nil)
	rec := httptest.NewRecorder()

	handler.GetTopics(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var body map[string][]tasks.KimGroupDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse json response: %v", err)
	}

	if len(body["items"]) != 1 || body["items"][0].KimNumber != 24 {
		t.Errorf("unexpected topics response: %+v", body)
	}
}

func TestBankHandler_GetBankTask_LockedByActiveExam(t *testing.T) {
	mockSvc := &mockTasksService{
		errToReturn: tasks.ErrTaskLockedByActiveExam,
	}

	handler := handlers.NewBankHandler(mockSvc, nil, nil, "")

	r := chi.NewRouter()
	r.Get("/api/v1/bank/tasks/{id}", handler.GetBankTask)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/bank/tasks/42", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", rec.Code)
	}

	if !strings.Contains(rec.Body.String(), "TASK_LOCKED_BY_ACTIVE_EXAM") {
		t.Errorf("expected TASK_LOCKED_BY_ACTIVE_EXAM in error body, got: %s", rec.Body.String())
	}
}

func TestBankHandler_SubmitBankTask(t *testing.T) {
	mockSvc := &mockTasksService{
		submitResponse: &tasks.TaskSubmitResponse{
			IsCorrect:       true,
			EarnedScore:     1,
			MaxScore:        1,
			TotalAttempts:   1,
			CanViewSolution: true,
		},
	}

	handler := handlers.NewBankHandler(mockSvc, nil, nil, "")

	r := chi.NewRouter()
	r.Post("/api/v1/bank/tasks/{id}/submit", handler.SubmitBankTask)

	userID := uuid.New()
	reqBody := `{"user_answer":{"answer_type":"single","value":"42"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/bank/tasks/101/submit", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	// Inject authenticated user into context
	ctx := middleware.ContextWithUser(req.Context(), middleware.UserContext{
		UserID:          userID,
		IsAuthenticated: true,
		Role:            "student",
	})
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	var resp tasks.TaskSubmitResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode submit response: %v", err)
	}

	if !resp.IsCorrect || resp.EarnedScore != 1 {
		t.Errorf("unexpected submit response: %+v", resp)
	}
}
