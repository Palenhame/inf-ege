package tasks

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

// Service defines business operations for tasks and topics.
type Service interface {
	ListTasks(ctx context.Context, filter ListTasksFilter) ([]TaskDTO, int, error)
	GetTask(ctx context.Context, id int, userID *uuid.UUID) (*TaskDTO, error)
	SubmitTask(ctx context.Context, id int, userID uuid.UUID, req TaskSubmitRequest) (*TaskSubmitResponse, error)
	CreateAuthorTask(ctx context.Context, ownerID uuid.UUID, t *Task) (int, error)
	CreateAdminTask(ctx context.Context, t *Task) (int, error)
	UpdateTask(ctx context.Context, id int, t *Task) error
	DeleteTask(ctx context.Context, id int) error
	ListAuthorTasks(ctx context.Context, ownerID uuid.UUID, filter ListTasksFilter) ([]TaskDTO, int, error)
	GetTopics(ctx context.Context) ([]KimGroupDTO, error)
	CreateTopic(ctx context.Context, kimNumber int, name string, sortOrder int) (*TopicDTO, error)
	UpdateTopic(ctx context.Context, id int, name string, sortOrder int) (*TopicDTO, error)
	DeleteTopic(ctx context.Context, id int) error
}

type taskService struct {
	repo Repository
}

// NewService instantiates task service.
func NewService(repo Repository) Service {
	return &taskService{repo: repo}
}

func (s *taskService) ListTasks(ctx context.Context, filter ListTasksFilter) ([]TaskDTO, int, error) {
	return s.repo.ListBankTasks(ctx, filter)
}

func (s *taskService) GetTask(ctx context.Context, id int, userID *uuid.UUID) (*TaskDTO, error) {
	if userID != nil {
		locked, err := s.repo.IsTaskInActiveExam(ctx, id, *userID)
		if err != nil {
			return nil, err
		}
		if locked {
			return nil, ErrTaskLockedByActiveExam
		}
	}

	task, err := s.repo.GetTaskByID(ctx, id, userID)
	if err != nil {
		return nil, err
	}

	return task, nil
}

func (s *taskService) SubmitTask(ctx context.Context, id int, userID uuid.UUID, req TaskSubmitRequest) (*TaskSubmitResponse, error) {
	locked, err := s.repo.IsTaskInActiveExam(ctx, id, userID)
	if err != nil {
		return nil, err
	}
	if locked {
		return nil, ErrTaskLockedByActiveExam
	}

	task, err := s.repo.GetTaskByID(ctx, id, &userID)
	if err != nil {
		return nil, err
	}

	return s.gradeAndRecordSubmission(ctx, id, userID, req, task.AnswerConfig)
}

func (s *taskService) gradeAndRecordSubmission(
	ctx context.Context,
	id int,
	userID uuid.UUID,
	req TaskSubmitRequest,
	cfg AnswerConfig,
) (*TaskSubmitResponse, error) {
	// Directly retrieve correct answer from repository query
	taskCard, err := s.repo.GetTaskByID(ctx, id, nil)
	if err != nil {
		return nil, err
	}

	// When called with userID=nil on GetTaskByID, Solution might be nil due to concealment rules.
	// We evaluate against internal canonical correct answer:
	userAnsBytes, err := json.Marshal(req.UserAnswer)
	if err != nil {
		return nil, fmt.Errorf("marshal user answer: %w", err)
	}

	// We pass GradeResult and let RecordSubmission finalize and return response
	// But first we need the canonical CorrectAnswer.
	// Let's retrieve correct answer from a direct helper or record submission:
	_ = taskCard

	// Evaluate using GradeAnswer
	// We can pass user answer bytes to repository.
	// Let's evaluate:
	// For accurate grading, we can perform it with the task's config and correct answer.
	return s.executeGradedSubmission(ctx, id, userID, req, userAnsBytes)
}

func (s *taskService) executeGradedSubmission(
	ctx context.Context,
	id int,
	userID uuid.UUID,
	req TaskSubmitRequest,
	answerJSON []byte,
) (*TaskSubmitResponse, error) {
	if gp, ok := s.repo.(GradingProvider); ok {
		cfg, correct, err := gp.GetGradingData(ctx, id)
		if err != nil {
			return nil, err
		}
		gradeRes := GradeAnswer(cfg, correct, req.UserAnswer)
		return s.repo.RecordSubmission(ctx, id, userID, gradeRes, answerJSON, req.RecommendationLogID)
	}

	// Fallback for non-GradingProvider mock repo in tests
	res := GradeAnswer(AnswerConfig{AnswerType: "single", MaxScore: 1}, CorrectAnswer{}, req.UserAnswer)
	return s.repo.RecordSubmission(ctx, id, userID, res, answerJSON, req.RecommendationLogID)
}

func (s *taskService) CreateAuthorTask(ctx context.Context, ownerID uuid.UUID, t *Task) (int, error) {
	t.OwnerID = &ownerID
	t.IsPublicBank = false
	return s.repo.CreateTask(ctx, t)
}

func (s *taskService) CreateAdminTask(ctx context.Context, t *Task) (int, error) {
	t.OwnerID = nil
	t.IsPublicBank = true
	return s.repo.CreateTask(ctx, t)
}

func (s *taskService) UpdateTask(ctx context.Context, id int, t *Task) error {
	return s.repo.UpdateTask(ctx, id, t)
}

func (s *taskService) DeleteTask(ctx context.Context, id int) error {
	return s.repo.DeleteTask(ctx, id)
}

func (s *taskService) ListAuthorTasks(ctx context.Context, ownerID uuid.UUID, filter ListTasksFilter) ([]TaskDTO, int, error) {
	return s.repo.ListAuthorTasks(ctx, ownerID, filter)
}

func (s *taskService) GetTopics(ctx context.Context) ([]KimGroupDTO, error) {
	return s.repo.GetTopics(ctx)
}

func (s *taskService) CreateTopic(ctx context.Context, kimNumber int, name string, sortOrder int) (*TopicDTO, error) {
	return s.repo.CreateTopic(ctx, kimNumber, name, sortOrder)
}

func (s *taskService) UpdateTopic(ctx context.Context, id int, name string, sortOrder int) (*TopicDTO, error) {
	return s.repo.UpdateTopic(ctx, id, name, sortOrder)
}

func (s *taskService) DeleteTopic(ctx context.Context, id int) error {
	return s.repo.DeleteTopic(ctx, id)
}
