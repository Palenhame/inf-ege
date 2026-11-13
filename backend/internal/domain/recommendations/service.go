package recommendations

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// Service coordinates dashboard and recommendation analytics.
type Service interface {
	GetStudentDashboard(ctx context.Context, userID uuid.UUID) (*StudentDashboardDTO, error)
}

type recommendationService struct {
	repo Repository
}

// NewService instantiates recommendation service.
func NewService(repo Repository) Service {
	return &recommendationService{repo: repo}
}

func (s *recommendationService) GetStudentDashboard(ctx context.Context, userID uuid.UUID) (*StudentDashboardDTO, error) {
	// 1. Active assignments / deadlines
	activeAssignments, err := s.repo.GetActiveAssignments(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("dashboard active assignments: %w", err)
	}

	// 2. KIM matrix 1..27
	kimMatrix, err := s.repo.GetKimMatrix(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("dashboard kim matrix: %w", err)
	}

	// 3. Weak topics
	weakTopics, err := s.repo.GetWeakTopics(ctx, userID, 5)
	if err != nil {
		return nil, fmt.Errorf("dashboard weak topics: %w", err)
	}

	// 4. Recommendation generation
	var recommendedTasks []RecommendedTaskDTO

	// a) Weak topic recommendation
	weakTask, err := s.repo.FindWeakTopicTask(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("dashboard find weak task: %w", err)
	}

	weakTaskID := 0
	if weakTask != nil {
		weakTaskID = weakTask.ID
		logID, err := s.repo.LogRecommendation(ctx, userID, weakTask.ID, "weak_topic")
		if err == nil {
			recommendedTasks = append(recommendedTasks, RecommendedTaskDTO{
				RecommendationLogID: logID,
				Reason:              "weak_topic",
				Task:                *weakTask,
			})
		}
	}

	// b) Adaptive difficulty recommendation
	rate, attempts, err := s.repo.GetUserOverallSuccessRate(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("dashboard overall success rate: %w", err)
	}

	targetDiff := 1
	if attempts > 0 {
		if rate >= 80 {
			targetDiff = 3
		} else if rate >= 50 {
			targetDiff = 2
		} else {
			targetDiff = 1
		}
	}

	adaptiveTask, err := s.repo.FindAdaptiveDifficultyTask(ctx, userID, targetDiff, weakTaskID)
	if err != nil {
		return nil, fmt.Errorf("dashboard find adaptive task: %w", err)
	}

	if adaptiveTask != nil {
		logID, err := s.repo.LogRecommendation(ctx, userID, adaptiveTask.ID, "adaptive_difficulty")
		if err == nil {
			recommendedTasks = append(recommendedTasks, RecommendedTaskDTO{
				RecommendationLogID: logID,
				Reason:              "adaptive_difficulty",
				Task:                *adaptiveTask,
			})
		}
	}

	if recommendedTasks == nil {
		recommendedTasks = []RecommendedTaskDTO{}
	}

	return &StudentDashboardDTO{
		ActiveAssignments: activeAssignments,
		KimMatrix:         kimMatrix,
		WeakTopics:        weakTopics,
		RecommendedTasks:  recommendedTasks,
	}, nil
}
