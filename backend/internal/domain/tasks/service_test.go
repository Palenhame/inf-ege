package tasks

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

type mockGradingRepo struct {
	task          *TaskDTO
	cfg           AnswerConfig
	correct       CorrectAnswer
	lastGrade     GradeResult
	gradingCalled bool
}

func (m *mockGradingRepo) ListBankTasks(ctx context.Context, filter ListTasksFilter) ([]TaskDTO, int, error) {
	return nil, 0, nil
}
func (m *mockGradingRepo) GetTaskByID(ctx context.Context, id int, userID *uuid.UUID) (*TaskDTO, error) {
	return m.task, nil
}
func (m *mockGradingRepo) IsTaskInActiveExam(ctx context.Context, taskID int, userID uuid.UUID) (bool, error) {
	return false, nil
}
func (m *mockGradingRepo) RecordSubmission(ctx context.Context, taskID int, userID uuid.UUID, res GradeResult, answerJSON []byte, recLogID *int64) (*TaskSubmitResponse, error) {
	m.lastGrade = res
	return &TaskSubmitResponse{
		IsCorrect:   res.IsCorrect,
		EarnedScore: res.Score,
		MaxScore:    res.MaxScore,
	}, nil
}
func (m *mockGradingRepo) CreateTask(ctx context.Context, task *Task) (int, error) { return 0, nil }
func (m *mockGradingRepo) UpdateTask(ctx context.Context, id int, task *Task) error {
	return nil
}
func (m *mockGradingRepo) DeleteTask(ctx context.Context, id int) error { return nil }
func (m *mockGradingRepo) ListAuthorTasks(ctx context.Context, ownerID uuid.UUID, filter ListTasksFilter) ([]TaskDTO, int, error) {
	return nil, 0, nil
}
func (m *mockGradingRepo) GetTopics(ctx context.Context) ([]KimGroupDTO, error) { return nil, nil }
func (m *mockGradingRepo) CreateTopic(ctx context.Context, kimNumber int, name string, sortOrder int) (*TopicDTO, error) {
	return nil, nil
}
func (m *mockGradingRepo) UpdateTopic(ctx context.Context, id int, name string, sortOrder int) (*TopicDTO, error) {
	return nil, nil
}
func (m *mockGradingRepo) DeleteTopic(ctx context.Context, id int) error { return nil }
func (m *mockGradingRepo) GetOrCreateTopic(ctx context.Context, kimNumber int, name string) (int, error) {
	return 0, nil
}

func (m *mockGradingRepo) GetGradingData(ctx context.Context, id int) (AnswerConfig, CorrectAnswer, error) {
	m.gradingCalled = true
	return m.cfg, m.correct, nil
}

func TestSubmitTask_UsesGradingProvider(t *testing.T) {
	repo := &mockGradingRepo{
		task: &TaskDTO{
			ID:           1042,
			AnswerConfig: AnswerConfig{AnswerType: "single", MaxScore: 1, TrimWhitespace: true},
		},
		cfg:     AnswerConfig{AnswerType: "single", MaxScore: 1, TrimWhitespace: true},
		correct: CorrectAnswer{AnswerType: "single", Accepted: []string{"1024"}},
	}
	svc := NewService(repo)

	resp, err := svc.SubmitTask(context.Background(), 1042, uuid.New(), TaskSubmitRequest{
		UserAnswer: UserAnswer{AnswerType: "single", Value: "1024"},
	})
	if err != nil {
		t.Fatalf("SubmitTask failed: %v", err)
	}
	if !repo.gradingCalled {
		t.Fatalf("expected GetGradingData to be called via GradingProvider")
	}
	if !resp.IsCorrect || resp.EarnedScore != 1 {
		t.Fatalf("expected correct answer with score 1, got %+v", resp)
	}
}
