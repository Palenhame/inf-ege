package recommendations

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository handles database operations for student dashboard and recommendations.
type Repository interface {
	GetActiveAssignments(ctx context.Context, userID uuid.UUID) ([]ActiveAssignmentDTO, error)
	GetKimMatrix(ctx context.Context, userID uuid.UUID) ([]KimMatrixCellDTO, error)
	GetWeakTopics(ctx context.Context, userID uuid.UUID, limit int) ([]WeakTopicDTO, error)
	GetUserOverallSuccessRate(ctx context.Context, userID uuid.UUID) (float64, int, error)
	FindWeakTopicTask(ctx context.Context, userID uuid.UUID) (*TaskPreviewDTO, error)
	FindAdaptiveDifficultyTask(ctx context.Context, userID uuid.UUID, targetDifficulty int, excludeTaskID int) (*TaskPreviewDTO, error)
	LogRecommendation(ctx context.Context, userID uuid.UUID, taskID int, reason string) (int64, error)
}

type pgRepository struct {
	pool *pgxpool.Pool
}

// NewRepository creates a new postgres repository for recommendations.
func NewRepository(pool *pgxpool.Pool) Repository {
	return &pgRepository{pool: pool}
}

// GetActiveAssignments fetches all currently in_progress sessions and assignment metadata.
func (r *pgRepository) GetActiveAssignments(ctx context.Context, userID uuid.UUID) ([]ActiveAssignmentDTO, error) {
	query := `
		SELECT
			s.id, s.status, s.started_at, s.expires_at,
			a.id, a.room_id, COALESCE(r.name, 'Самостоятельный пробник') AS room_name,
			a.title, a.mode, a.settings->>'deadline_at' AS deadline_at
		FROM assignment_sessions s
		JOIN assignments a ON a.id = s.assignment_id AND a.deleted_at IS NULL
		LEFT JOIN rooms r ON r.id = a.room_id AND r.deleted_at IS NULL
		WHERE s.user_id = $1 AND s.status = 'in_progress'
		ORDER BY s.started_at DESC
	`
	rows, err := r.pool.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("active assignments query: %w", err)
	}
	defer rows.Close()

	var result []ActiveAssignmentDTO
	for rows.Next() {
		var (
			sessionID      uuid.UUID
			sessionStatus  string
			startedAt      time.Time
			expiresAt      *time.Time
			assignmentID   uuid.UUID
			roomID         *uuid.UUID
			roomName       string
			title          string
			mode           string
			deadlineString *string
		)

		err := rows.Scan(
			&sessionID, &sessionStatus, &startedAt, &expiresAt,
			&assignmentID, &roomID, &roomName,
			&title, &mode, &deadlineString,
		)
		if err != nil {
			return nil, fmt.Errorf("scan active assignment row: %w", err)
		}

		var expiresStr *string
		if expiresAt != nil {
			s := expiresAt.UTC().Format(time.RFC3339)
			expiresStr = &s
		}

		var roomIDStr *string
		if roomID != nil {
			s := roomID.String()
			roomIDStr = &s
		}

		result = append(result, ActiveAssignmentDTO{
			ID:         assignmentID.String(),
			RoomID:     roomIDStr,
			RoomName:   roomName,
			Title:      title,
			Mode:       mode,
			DeadlineAt: deadlineString,
			Session: ActiveSessionDTO{
				ID:        sessionID.String(),
				Status:    sessionStatus,
				StartedAt: startedAt.UTC().Format(time.RFC3339),
				ExpiresAt: expiresStr,
			},
		})
	}

	if result == nil {
		result = []ActiveAssignmentDTO{}
	}
	return result, nil
}

// GetKimMatrix retrieves mastery stats across all 27 KIM numbers.
func (r *pgRepository) GetKimMatrix(ctx context.Context, userID uuid.UUID) ([]KimMatrixCellDTO, error) {
	query := `
		SELECT
			k.kim_number,
			COALESCE(SUM(uts.solved), 0)::int AS solved,
			COALESCE(SUM(uts.attempted), 0)::int AS attempted
		FROM generate_series(1, 27) AS k(kim_number)
		LEFT JOIN topics tp ON tp.kim_number = k.kim_number AND tp.deleted_at IS NULL
		LEFT JOIN user_topic_stats uts ON uts.topic_id = tp.id AND uts.user_id = $1
		GROUP BY k.kim_number
		ORDER BY k.kim_number ASC
	`
	rows, err := r.pool.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("kim matrix query: %w", err)
	}
	defer rows.Close()

	var result []KimMatrixCellDTO
	for rows.Next() {
		var kim int
		var solved int
		var attempted int

		if err := rows.Scan(&kim, &solved, &attempted); err != nil {
			return nil, fmt.Errorf("scan kim matrix: %w", err)
		}

		rate := 0.0
		if attempted > 0 {
			rate = math.Round((float64(solved)/float64(attempted))*1000) / 10
		}

		result = append(result, KimMatrixCellDTO{
			KimNumber:   kim,
			Solved:      solved,
			Attempted:   attempted,
			SuccessRate: rate,
		})
	}

	if result == nil {
		result = []KimMatrixCellDTO{}
	}
	return result, nil
}

// GetWeakTopics fetches top weak topics sorted by weakness score DESC.
func (r *pgRepository) GetWeakTopics(ctx context.Context, userID uuid.UUID, limit int) ([]WeakTopicDTO, error) {
	if limit <= 0 {
		limit = 5
	}
	query := `
		SELECT
			uts.topic_id, tp.kim_number, tp.name,
			uts.score, uts.solved, uts.attempted, uts.success_rate
		FROM user_topic_stats uts
		JOIN topics tp ON tp.id = uts.topic_id AND tp.deleted_at IS NULL
		WHERE uts.user_id = $1 AND uts.attempted > 0
		ORDER BY uts.score DESC, uts.success_rate ASC
		LIMIT $2
	`
	rows, err := r.pool.Query(ctx, query, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("weak topics query: %w", err)
	}
	defer rows.Close()

	var result []WeakTopicDTO
	for rows.Next() {
		var (
			topicID     int
			kimNumber   int16
			name        string
			score       float64
			solved      int
			attempted   int
			successRate float64
		)
		err := rows.Scan(&topicID, &kimNumber, &name, &score, &solved, &attempted, &successRate)
		if err != nil {
			return nil, fmt.Errorf("scan weak topic row: %w", err)
		}

		result = append(result, WeakTopicDTO{
			TopicID:     topicID,
			KimNumber:   int(kimNumber),
			TopicName:   name,
			Score:       score,
			Solved:      solved,
			Attempted:   attempted,
			SuccessRate: successRate,
		})
	}

	if result == nil {
		result = []WeakTopicDTO{}
	}
	return result, nil
}

// GetUserOverallSuccessRate calculates aggregate success rate and total attempts across all tasks.
func (r *pgRepository) GetUserOverallSuccessRate(ctx context.Context, userID uuid.UUID) (float64, int, error) {
	query := `
		SELECT
			COALESCE(SUM(solved), 0)::int,
			COALESCE(SUM(attempted), 0)::int
		FROM user_topic_stats
		WHERE user_id = $1
	`
	var solved, attempted int
	err := r.pool.QueryRow(ctx, query, userID).Scan(&solved, &attempted)
	if err != nil {
		return 0, 0, fmt.Errorf("user overall stats query: %w", err)
	}
	if attempted == 0 {
		return 0, 0, nil
	}
	return (float64(solved) / float64(attempted)) * 100.0, attempted, nil
}

// FindWeakTopicTask locates an unsolved task in the student's weakest topics.
func (r *pgRepository) FindWeakTopicTask(ctx context.Context, userID uuid.UUID) (*TaskPreviewDTO, error) {
	query := `
		SELECT t.id, t.kim_numbers, t.difficulty, t.text_md
		FROM tasks t
		WHERE t.deleted_at IS NULL
		  AND t.is_public_bank = TRUE
		  AND t.topic_id IN (
		      SELECT uts.topic_id
		      FROM user_topic_stats uts
		      WHERE uts.user_id = $1 AND uts.attempted > 0
		      ORDER BY uts.score DESC, uts.success_rate ASC
		      LIMIT 5
		  )
		  AND NOT EXISTS (
		      SELECT 1 FROM user_tasks ut
		      WHERE ut.task_id = t.id AND ut.user_id = $1 AND ut.is_correct = TRUE
		  )
		  AND NOT EXISTS (
		      SELECT 1
		      FROM assignment_sessions s
		      JOIN assignments a ON a.id = s.assignment_id
		      JOIN assignment_tasks at ON at.assignment_id = a.id
		      WHERE s.user_id = $1
		        AND s.status = 'in_progress'
		        AND a.mode IN ('mock_exam', 'proctored_test')
		        AND at.task_id = t.id
		  )
		ORDER BY random()
		LIMIT 1
	`
	var (
		id         int
		kim16      []int16
		difficulty int16
		textMD     string
	)
	err := r.pool.QueryRow(ctx, query, userID).Scan(&id, &kim16, &difficulty, &textMD)
	if err == nil {
		var kimInts []int
		for _, k := range kim16 {
			kimInts = append(kimInts, int(k))
		}
		return &TaskPreviewDTO{
			ID:         id,
			KimNumbers: kimInts,
			Difficulty: int(difficulty),
			TextMD:     textMD,
		}, nil
	}

	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("find weak topic task: %w", err)
	}

	// Fallback to any unsolved bank task
	fallbackQuery := `
		SELECT t.id, t.kim_numbers, t.difficulty, t.text_md
		FROM tasks t
		WHERE t.deleted_at IS NULL
		  AND t.is_public_bank = TRUE
		  AND NOT EXISTS (
		      SELECT 1 FROM user_tasks ut
		      WHERE ut.task_id = t.id AND ut.user_id = $1 AND ut.is_correct = TRUE
		  )
		  AND NOT EXISTS (
		      SELECT 1
		      FROM assignment_sessions s
		      JOIN assignments a ON a.id = s.assignment_id
		      JOIN assignment_tasks at ON at.assignment_id = a.id
		      WHERE s.user_id = $1
		        AND s.status = 'in_progress'
		        AND a.mode IN ('mock_exam', 'proctored_test')
		        AND at.task_id = t.id
		  )
		ORDER BY t.difficulty ASC, random()
		LIMIT 1
	`
	err = r.pool.QueryRow(ctx, fallbackQuery, userID).Scan(&id, &kim16, &difficulty, &textMD)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil // No task available
		}
		return nil, fmt.Errorf("fallback task query: %w", err)
	}

	var kimInts []int
	for _, k := range kim16 {
		kimInts = append(kimInts, int(k))
	}
	return &TaskPreviewDTO{
		ID:         id,
		KimNumbers: kimInts,
		Difficulty: int(difficulty),
		TextMD:     textMD,
	}, nil
}

// FindAdaptiveDifficultyTask finds a task matched to the student's skill level.
func (r *pgRepository) FindAdaptiveDifficultyTask(ctx context.Context, userID uuid.UUID, targetDifficulty int, excludeTaskID int) (*TaskPreviewDTO, error) {
	query := `
		SELECT t.id, t.kim_numbers, t.difficulty, t.text_md
		FROM tasks t
		WHERE t.deleted_at IS NULL
		  AND t.is_public_bank = TRUE
		  AND t.difficulty = $2
		  AND ($3 = 0 OR t.id != $3)
		  AND NOT EXISTS (
		      SELECT 1 FROM user_tasks ut
		      WHERE ut.task_id = t.id AND ut.user_id = $1 AND ut.is_correct = TRUE
		  )
		  AND NOT EXISTS (
		      SELECT 1
		      FROM assignment_sessions s
		      JOIN assignments a ON a.id = s.assignment_id
		      JOIN assignment_tasks at ON at.assignment_id = a.id
		      WHERE s.user_id = $1
		        AND s.status = 'in_progress'
		        AND a.mode IN ('mock_exam', 'proctored_test')
		        AND at.task_id = t.id
		  )
		ORDER BY random()
		LIMIT 1
	`
	var (
		id         int
		kim16      []int16
		difficulty int16
		textMD     string
	)
	err := r.pool.QueryRow(ctx, query, userID, int16(targetDifficulty), excludeTaskID).Scan(&id, &kim16, &difficulty, &textMD)
	if err == nil {
		var kimInts []int
		for _, k := range kim16 {
			kimInts = append(kimInts, int(k))
		}
		return &TaskPreviewDTO{
			ID:         id,
			KimNumbers: kimInts,
			Difficulty: int(difficulty),
			TextMD:     textMD,
		}, nil
	}

	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("find adaptive task: %w", err)
	}

	// Fallback to any difficulty
	fallbackQuery := `
		SELECT t.id, t.kim_numbers, t.difficulty, t.text_md
		FROM tasks t
		WHERE t.deleted_at IS NULL
		  AND t.is_public_bank = TRUE
		  AND ($2 = 0 OR t.id != $2)
		  AND NOT EXISTS (
		      SELECT 1 FROM user_tasks ut
		      WHERE ut.task_id = t.id AND ut.user_id = $1 AND ut.is_correct = TRUE
		  )
		  AND NOT EXISTS (
		      SELECT 1
		      FROM assignment_sessions s
		      JOIN assignments a ON a.id = s.assignment_id
		      JOIN assignment_tasks at ON at.assignment_id = a.id
		      WHERE s.user_id = $1
		        AND s.status = 'in_progress'
		        AND a.mode IN ('mock_exam', 'proctored_test')
		        AND at.task_id = t.id
		  )
		ORDER BY random()
		LIMIT 1
	`
	err = r.pool.QueryRow(ctx, fallbackQuery, userID, excludeTaskID).Scan(&id, &kim16, &difficulty, &textMD)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("fallback adaptive task: %w", err)
	}

	var kimInts []int
	for _, k := range kim16 {
		kimInts = append(kimInts, int(k))
	}
	return &TaskPreviewDTO{
		ID:         id,
		KimNumbers: kimInts,
		Difficulty: int(difficulty),
		TextMD:     textMD,
	}, nil
}

// LogRecommendation inserts an entry into task_recommendation_logs.
func (r *pgRepository) LogRecommendation(ctx context.Context, userID uuid.UUID, taskID int, reason string) (int64, error) {
	query := `
		INSERT INTO task_recommendation_logs (user_id, task_id, reason, created_at)
		VALUES ($1, $2, $3::recommendation_reason, now())
		RETURNING id
	`
	var logID int64
	err := r.pool.QueryRow(ctx, query, userID, taskID, reason).Scan(&logID)
	if err != nil {
		return 0, fmt.Errorf("log recommendation: %w", err)
	}
	return logID, nil
}
