package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"backend/internal/domain/recommendations"
	"backend/internal/transport/http/handlers"
	"backend/internal/transport/http/middleware"
	"github.com/google/uuid"
)

type mockRecService struct {
	dashboard *recommendations.StudentDashboardDTO
	err       error
}

func (m *mockRecService) GetStudentDashboard(ctx context.Context, userID uuid.UUID) (*recommendations.StudentDashboardDTO, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.dashboard, nil
}

func TestDashboardHandler_GetStudentDashboard_Unauthenticated(t *testing.T) {
	mockSvc := &mockRecService{}
	handler := handlers.NewDashboardHandler(mockSvc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/student", nil)
	rec := httptest.NewRecorder()

	handler.GetStudentDashboard(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401 for unauthenticated request, got %d", rec.Code)
	}
}

func TestDashboardHandler_GetStudentDashboard_Success(t *testing.T) {
	mockSvc := &mockRecService{
		dashboard: &recommendations.StudentDashboardDTO{
			ActiveAssignments: []recommendations.ActiveAssignmentDTO{},
			KimMatrix:         []recommendations.KimMatrixCellDTO{{KimNumber: 1, Solved: 10, Attempted: 12, SuccessRate: 83.3}},
			WeakTopics:        []recommendations.WeakTopicDTO{},
			RecommendedTasks:  []recommendations.RecommendedTaskDTO{},
		},
	}
	handler := handlers.NewDashboardHandler(mockSvc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/student", nil)
	ctx := middleware.ContextWithUser(req.Context(), middleware.UserContext{
		UserID:          uuid.New(),
		IsAuthenticated: true,
		Role:            "student",
	})
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	handler.GetStudentDashboard(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var d recommendations.StudentDashboardDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatalf("failed to decode dashboard json: %v", err)
	}

	if len(d.KimMatrix) != 1 || d.KimMatrix[0].KimNumber != 1 {
		t.Errorf("unexpected dashboard response: %+v", d)
	}
}
