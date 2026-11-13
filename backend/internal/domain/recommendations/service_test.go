package recommendations_test

import (
	"context"
	"testing"

	"backend/internal/domain/recommendations"
	"github.com/google/uuid"
)

type mockRecRepo struct {
	activeAssignments []recommendations.ActiveAssignmentDTO
	kimMatrix         []recommendations.KimMatrixCellDTO
	weakTopics        []recommendations.WeakTopicDTO
	overallRate       float64
	overallAttempts   int
	weakTask          *recommendations.TaskPreviewDTO
	adaptiveTask      *recommendations.TaskPreviewDTO
	loggedReasons     []string
}

func (m *mockRecRepo) GetActiveAssignments(ctx context.Context, userID uuid.UUID) ([]recommendations.ActiveAssignmentDTO, error) {
	return m.activeAssignments, nil
}
func (m *mockRecRepo) GetKimMatrix(ctx context.Context, userID uuid.UUID) ([]recommendations.KimMatrixCellDTO, error) {
	return m.kimMatrix, nil
}
func (m *mockRecRepo) GetWeakTopics(ctx context.Context, userID uuid.UUID, limit int) ([]recommendations.WeakTopicDTO, error) {
	return m.weakTopics, nil
}
func (m *mockRecRepo) GetUserOverallSuccessRate(ctx context.Context, userID uuid.UUID) (float64, int, error) {
	return m.overallRate, m.overallAttempts, nil
}
func (m *mockRecRepo) FindWeakTopicTask(ctx context.Context, userID uuid.UUID) (*recommendations.TaskPreviewDTO, error) {
	return m.weakTask, nil
}
func (m *mockRecRepo) FindAdaptiveDifficultyTask(ctx context.Context, userID uuid.UUID, targetDifficulty int, excludeTaskID int) (*recommendations.TaskPreviewDTO, error) {
	return m.adaptiveTask, nil
}
func (m *mockRecRepo) LogRecommendation(ctx context.Context, userID uuid.UUID, taskID int, reason string) (int64, error) {
	m.loggedReasons = append(m.loggedReasons, reason)
	return int64(len(m.loggedReasons)), nil
}

func TestStudentDashboard(t *testing.T) {
	userID := uuid.New()
	repo := &mockRecRepo{
		activeAssignments: []recommendations.ActiveAssignmentDTO{
			{
				ID:       uuid.New().String(),
				RoomName: "11-A",
				Title:    "Пробник №1",
				Mode:     "mock_exam",
			},
		},
		kimMatrix: []recommendations.KimMatrixCellDTO{
			{KimNumber: 24, Solved: 5, Attempted: 10, SuccessRate: 50.0},
		},
		weakTopics: []recommendations.WeakTopicDTO{
			{TopicID: 2401, KimNumber: 24, TopicName: "Строки", Score: 85.0},
		},
		overallRate:     75.0,
		overallAttempts: 20,
		weakTask: &recommendations.TaskPreviewDTO{
			ID:         101,
			KimNumbers: []int{24},
			Difficulty: 3,
			TextMD:     "Задание 24",
		},
		adaptiveTask: &recommendations.TaskPreviewDTO{
			ID:         102,
			KimNumbers: []int{15},
			Difficulty: 2,
			TextMD:     "Задание 15",
		},
	}

	svc := recommendations.NewService(repo)
	dashboard, err := svc.GetStudentDashboard(context.Background(), userID)
	if err != nil {
		t.Fatalf("unexpected error getting dashboard: %v", err)
	}

	if len(dashboard.ActiveAssignments) != 1 {
		t.Errorf("expected 1 active assignment, got %d", len(dashboard.ActiveAssignments))
	}
	if len(dashboard.KimMatrix) != 1 {
		t.Errorf("expected 1 kim cell, got %d", len(dashboard.KimMatrix))
	}
	if len(dashboard.WeakTopics) != 1 {
		t.Errorf("expected 1 weak topic, got %d", len(dashboard.WeakTopics))
	}
	if len(dashboard.RecommendedTasks) != 2 {
		t.Errorf("expected 2 recommended tasks, got %d", len(dashboard.RecommendedTasks))
	}

	if len(repo.loggedReasons) != 2 {
		t.Errorf("expected 2 logged recommendations, got %d", len(repo.loggedReasons))
	}
	if repo.loggedReasons[0] != "weak_topic" || repo.loggedReasons[1] != "adaptive_difficulty" {
		t.Errorf("unexpected recommendation reasons: %+v", repo.loggedReasons)
	}
}
