package assignments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"backend/internal/domain/tasks"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound         = errors.New("not found")
	ErrSessionFinished  = errors.New("session already finished")
	ErrDeadlinePassed   = errors.New("deadline has passed")
	ErrAttemptsExceeded = errors.New("attempts exceeded")
	ErrForbidden        = errors.New("forbidden")
)

// Repository defines all database operations for assignments and test sessions.
type Repository interface {
	CreateAssignment(ctx context.Context, a *Assignment, taskIDs []int) error
	GetAssignmentByID(ctx context.Context, id uuid.UUID) (*Assignment, error)
	UpdateAssignment(ctx context.Context, id uuid.UUID, title *string, settings *AssignmentSettings, resultsReleased *bool) (*Assignment, error)
	DeleteAssignment(ctx context.Context, id uuid.UUID) error

	CreateSession(ctx context.Context, s *AssignmentSession) error
	GetSessionByID(ctx context.Context, id uuid.UUID) (*AssignmentSession, error)
	GetSessionByAssignmentAndUser(ctx context.Context, assignmentID, userID uuid.UUID) (*AssignmentSession, error)
	UpdateSessionCounts(ctx context.Context, sessionID uuid.UUID, blurDelta, pasteDelta int) error
	FinishSession(ctx context.Context, sessionID uuid.UUID, primaryScore, secondaryScore int16) error
	ExpireSession(ctx context.Context, sessionID uuid.UUID, primaryScore, secondaryScore int16) error

	GetExamSecondaryScore(ctx context.Context, primaryScore int16) (int16, error)
	GetExamScale(ctx context.Context) ([]ExamScaleEntry, error)
	UpdateExamScale(ctx context.Context, entries []ExamScaleEntry) error
	GetTaskByID(ctx context.Context, taskID int) (*tasks.Task, error)
	GetAssignmentTasksWithDetails(ctx context.Context, assignmentID uuid.UUID) ([]SessionTaskDetailRow, error)
	GetUserAssignmentTask(ctx context.Context, sessionID uuid.UUID, taskID int) (*UserAssignmentTask, error)
	UpsertUserAssignmentTask(ctx context.Context, uat *UserAssignmentTask) error
	ListUserAssignmentTasks(ctx context.Context, sessionID uuid.UUID) ([]UserAssignmentTask, error)
	SelectSelfExamTasks(ctx context.Context, userID uuid.UUID) ([]int, error)
	LogTaskEvent(ctx context.Context, sessionID, assignmentID, userID uuid.UUID, taskID *int, event string, payload any) error

	CheckRoomMembership(ctx context.Context, roomID, userID uuid.UUID) (bool, error)
	CheckRoomCreator(ctx context.Context, roomID, userID uuid.UUID) (bool, error)
}

// SessionTaskDetailRow represents a joined row of assignment task + task details.
type SessionTaskDetailRow struct {
	Position      int16
	TaskID        int
	KimNumbers    []int16
	TopicID       int
	TopicKim      *int
	TopicName     *string
	Difficulty    int16
	Source        *string
	TextMD        string
	Images        []string
	Files         []byte
	AnswerConfig  []byte
	CorrectAnswer []byte
	SolutionMD    *string
	SolutionCode  *string
}

// PostgresRepository implements Repository using pgxpool.
type PostgresRepository struct {
	pool *pgxpool.Pool
}

// NewRepository creates a new PostgresRepository instance.
func NewRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

// CreateAssignment persists assignment header and associates tasks in transaction.
func (r *PostgresRepository) CreateAssignment(ctx context.Context, a *Assignment, taskIDs []int) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	settingsJSON, err := json.Marshal(a.Settings)
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}

	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}
	now := time.Now().UTC()
	a.CreatedAt = now
	a.UpdatedAt = now

	const insertAssignmentSQL = `
		INSERT INTO assignments (id, room_id, creator_id, title, mode, settings, results_released, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`

	_, err = tx.Exec(ctx, insertAssignmentSQL,
		a.ID, a.RoomID, a.CreatorID, a.Title, a.Mode, settingsJSON, a.ResultsReleased, a.CreatedAt, a.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert assignment: %w", err)
	}

	const insertTaskSQL = `
		INSERT INTO assignment_tasks (assignment_id, task_id, position)
		VALUES ($1, $2, $3)`

	const insertStatSQL = `
		INSERT INTO assignment_task_stats (assignment_id, task_id)
		VALUES ($1, $2)
		ON CONFLICT (assignment_id, task_id) DO NOTHING`

	for idx, taskID := range taskIDs {
		position := int16(idx + 1)
		if _, err := tx.Exec(ctx, insertTaskSQL, a.ID, taskID, position); err != nil {
			return fmt.Errorf("insert assignment_task: %w", err)
		}
		if _, err := tx.Exec(ctx, insertStatSQL, a.ID, taskID); err != nil {
			return fmt.Errorf("insert assignment_task_stats: %w", err)
		}
	}

	a.TasksCount = len(taskIDs)
	return tx.Commit(ctx)
}

// GetAssignmentByID retrieves an assignment by UUID if not soft deleted.
func (r *PostgresRepository) GetAssignmentByID(ctx context.Context, id uuid.UUID) (*Assignment, error) {
	const query = `
		SELECT a.id, a.room_id, a.creator_id, a.title, a.mode, a.settings, a.results_released,
		       a.created_at, a.updated_at, a.deleted_at,
		       (SELECT COUNT(*) FROM assignment_tasks WHERE assignment_id = a.id) as tasks_count
		FROM assignments a
		WHERE a.id = $1 AND a.deleted_at IS NULL`

	var a Assignment
	var settingsJSON []byte

	err := r.pool.QueryRow(ctx, query, id).Scan(
		&a.ID, &a.RoomID, &a.CreatorID, &a.Title, &a.Mode, &settingsJSON,
		&a.ResultsReleased, &a.CreatedAt, &a.UpdatedAt, &a.DeletedAt, &a.TasksCount,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("query assignment: %w", err)
	}

	if len(settingsJSON) > 0 {
		if err := json.Unmarshal(settingsJSON, &a.Settings); err != nil {
			return nil, fmt.Errorf("unmarshal settings: %w", err)
		}
	}

	return &a, nil
}

// UpdateAssignment modifies title, settings, or results_released flag.
func (r *PostgresRepository) UpdateAssignment(ctx context.Context, id uuid.UUID, title *string, settings *AssignmentSettings, resultsReleased *bool) (*Assignment, error) {
	current, err := r.GetAssignmentByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if title != nil {
		current.Title = *title
	}
	if settings != nil {
		current.Settings = *settings
	}
	if resultsReleased != nil {
		current.ResultsReleased = *resultsReleased
	}
	current.UpdatedAt = time.Now().UTC()

	settingsJSON, err := json.Marshal(current.Settings)
	if err != nil {
		return nil, fmt.Errorf("marshal settings: %w", err)
	}

	const updateSQL = `
		UPDATE assignments
		SET title = $1, settings = $2, results_released = $3, updated_at = $4
		WHERE id = $5 AND deleted_at IS NULL`

	_, err = r.pool.Exec(ctx, updateSQL, current.Title, settingsJSON, current.ResultsReleased, current.UpdatedAt, id)
	if err != nil {
		return nil, fmt.Errorf("update assignment: %w", err)
	}

	return current, nil
}

// DeleteAssignment soft-deletes an assignment by setting deleted_at.
func (r *PostgresRepository) DeleteAssignment(ctx context.Context, id uuid.UUID) error {
	const query = `UPDATE assignments SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL`
	tag, err := r.pool.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("delete assignment: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CreateSession inserts a new assignment_sessions record.
func (r *PostgresRepository) CreateSession(ctx context.Context, s *AssignmentSession) error {
	if s.ID == uuid.Nil {
		s.ID = uuid.New()
	}
	if s.StartedAt.IsZero() {
		s.StartedAt = time.Now().UTC()
	}

	const query = `
		INSERT INTO assignment_sessions (id, assignment_id, user_id, status, started_at, expires_at, primary_score, blur_count, paste_count)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`

	_, err := r.pool.Exec(ctx, query,
		s.ID, s.AssignmentID, s.UserID, s.Status, s.StartedAt, s.ExpiresAt, s.PrimaryScore, s.BlurCount, s.PasteCount,
	)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// GetSessionByID finds a session by its UUID.
func (r *PostgresRepository) GetSessionByID(ctx context.Context, id uuid.UUID) (*AssignmentSession, error) {
	const query = `
		SELECT id, assignment_id, user_id, status, started_at, expires_at, finished_at,
		       primary_score, secondary_score, blur_count, paste_count
		FROM assignment_sessions
		WHERE id = $1`

	var s AssignmentSession
	err := r.pool.QueryRow(ctx, query, id).Scan(
		&s.ID, &s.AssignmentID, &s.UserID, &s.Status, &s.StartedAt, &s.ExpiresAt, &s.FinishedAt,
		&s.PrimaryScore, &s.SecondaryScore, &s.BlurCount, &s.PasteCount,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("query session: %w", err)
	}
	return &s, nil
}

// GetSessionByAssignmentAndUser finds a student's session for an assignment.
func (r *PostgresRepository) GetSessionByAssignmentAndUser(ctx context.Context, assignmentID, userID uuid.UUID) (*AssignmentSession, error) {
	const query = `
		SELECT id, assignment_id, user_id, status, started_at, expires_at, finished_at,
		       primary_score, secondary_score, blur_count, paste_count
		FROM assignment_sessions
		WHERE assignment_id = $1 AND user_id = $2`

	var s AssignmentSession
	err := r.pool.QueryRow(ctx, query, assignmentID, userID).Scan(
		&s.ID, &s.AssignmentID, &s.UserID, &s.Status, &s.StartedAt, &s.ExpiresAt, &s.FinishedAt,
		&s.PrimaryScore, &s.SecondaryScore, &s.BlurCount, &s.PasteCount,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("query session by user: %w", err)
	}
	return &s, nil
}

// UpdateSessionCounts increments blur and paste counters.
func (r *PostgresRepository) UpdateSessionCounts(ctx context.Context, sessionID uuid.UUID, blurDelta, pasteDelta int) error {
	const query = `
		UPDATE assignment_sessions
		SET blur_count = blur_count + $1, paste_count = paste_count + $2
		WHERE id = $3`

	_, err := r.pool.Exec(ctx, query, blurDelta, pasteDelta, sessionID)
	return err
}

// FinishSession marks session as finished and persists calculated scores.
func (r *PostgresRepository) FinishSession(ctx context.Context, sessionID uuid.UUID, primaryScore, secondaryScore int16) error {
	const query = `
		UPDATE assignment_sessions
		SET status = 'finished', finished_at = now(), primary_score = $1, secondary_score = $2
		WHERE id = $3`

	tag, err := r.pool.Exec(ctx, query, primaryScore, secondaryScore, sessionID)
	if err != nil {
		return fmt.Errorf("finish session: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ExpireSession marks session as expired and persists calculated scores.
func (r *PostgresRepository) ExpireSession(ctx context.Context, sessionID uuid.UUID, primaryScore, secondaryScore int16) error {
	const query = `
		UPDATE assignment_sessions
		SET status = 'expired', finished_at = now(), primary_score = $1, secondary_score = $2
		WHERE id = $3`

	tag, err := r.pool.Exec(ctx, query, primaryScore, secondaryScore, sessionID)
	if err != nil {
		return fmt.Errorf("expire session: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Official default KEGE scale for 2025/2026: primary points (0..29) -> secondary (0..100).
var defaultKegeScale = map[int16]int16{
	0: 0, 1: 7, 2: 14, 3: 20, 4: 27, 5: 34, 6: 40, 7: 43, 8: 46, 9: 48,
	10: 51, 11: 54, 12: 56, 13: 59, 14: 62, 15: 64, 16: 67, 17: 70, 18: 72, 19: 75,
	20: 78, 21: 80, 22: 83, 23: 85, 24: 88, 25: 90, 26: 93, 27: 95, 28: 98, 29: 100,
}

// GetExamSecondaryScore translates primary score (0..29) using exam_score_scale table.
func (r *PostgresRepository) GetExamSecondaryScore(ctx context.Context, primaryScore int16) (int16, error) {
	if primaryScore <= 0 {
		return 0, nil
	}
	if primaryScore >= 29 {
		return 100, nil
	}

	const query = `SELECT secondary_score FROM exam_score_scale WHERE primary_score = $1`
	var secondary int16
	err := r.pool.QueryRow(ctx, query, primaryScore).Scan(&secondary)
	if err != nil {
		// Fallback to default canonical KEGE table if database table not yet populated
		if val, ok := defaultKegeScale[primaryScore]; ok {
			return val, nil
		}
		return int16((int(primaryScore) * 100) / 29), nil
	}
	return secondary, nil
}

// GetExamScale returns the KEGE primary-to-secondary score scale ordered by primary_score.
// Falls back to the default canonical scale if exam_score_scale is empty.
func (r *PostgresRepository) GetExamScale(ctx context.Context) ([]ExamScaleEntry, error) {
	const query = `
		SELECT primary_score, secondary_score
		FROM exam_score_scale
		ORDER BY primary_score ASC`

	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query exam_score_scale: %w", err)
	}
	defer rows.Close()

	var entries []ExamScaleEntry
	for rows.Next() {
		var e ExamScaleEntry
		if err := rows.Scan(&e.PrimaryScore, &e.SecondaryScore); err != nil {
			return nil, fmt.Errorf("scan exam_score_scale: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate exam_score_scale: %w", err)
	}

	if len(entries) == 0 {
		return defaultKegeScaleEntries(), nil
	}
	return entries, nil
}

func defaultKegeScaleEntries() []ExamScaleEntry {
	entries := make([]ExamScaleEntry, 0, 30)
	for p := int16(0); p <= 29; p++ {
		entries = append(entries, ExamScaleEntry{
			PrimaryScore:   p,
			SecondaryScore: defaultKegeScale[p],
		})
	}
	return entries
}

// UpdateExamScale upserts entries into exam_score_scale within a transaction.
func (r *PostgresRepository) UpdateExamScale(ctx context.Context, entries []ExamScaleEntry) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const upsertSQL = `
		INSERT INTO exam_score_scale (primary_score, secondary_score)
		VALUES ($1, $2)
		ON CONFLICT (primary_score) DO UPDATE SET secondary_score = EXCLUDED.secondary_score`

	for _, e := range entries {
		if _, err := tx.Exec(ctx, upsertSQL, e.PrimaryScore, e.SecondaryScore); err != nil {
			return fmt.Errorf("upsert exam_score_scale (%d): %w", e.PrimaryScore, err)
		}
	}

	return tx.Commit(ctx)
}

// GetTaskByID reads full task entity by ID.
func (r *PostgresRepository) GetTaskByID(ctx context.Context, taskID int) (*tasks.Task, error) {
	const query = `
		SELECT id, owner_id, is_public_bank, kim_numbers, topic_id, difficulty,
		       source, text_md, images, files, answer_config, correct_answer,
		       solution_md, solution_code
		FROM tasks
		WHERE id = $1 AND deleted_at IS NULL`

	var t tasks.Task
	var answerConfigJSON []byte
	var correctAnswerJSON []byte
	var filesJSON []byte
	var kimSmallints []int16

	err := r.pool.QueryRow(ctx, query, taskID).Scan(
		&t.ID, &t.OwnerID, &t.IsPublicBank, &kimSmallints, &t.TopicID, &t.Difficulty,
		&t.Source, &t.TextMD, &t.Images, &filesJSON, &answerConfigJSON, &correctAnswerJSON,
		&t.SolutionMD, &t.SolutionCode,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("query task: %w", err)
	}

	for _, k := range kimSmallints {
		t.KimNumbers = append(t.KimNumbers, int(k))
	}

	if len(answerConfigJSON) > 0 {
		_ = json.Unmarshal(answerConfigJSON, &t.AnswerConfig)
	}
	if len(correctAnswerJSON) > 0 {
		_ = json.Unmarshal(correctAnswerJSON, &t.CorrectAnswer)
	}
	if len(filesJSON) > 0 {
		_ = json.Unmarshal(filesJSON, &t.Files)
	}

	return &t, nil
}

// GetAssignmentTasksWithDetails fetches tasks associated with an assignment ordered by position.
func (r *PostgresRepository) GetAssignmentTasksWithDetails(ctx context.Context, assignmentID uuid.UUID) ([]SessionTaskDetailRow, error) {
	const query = `
		SELECT at.position, t.id, t.kim_numbers, t.topic_id, top.kim_number, top.name,
		       t.difficulty, t.source, t.text_md, t.images, t.files, t.answer_config,
		       t.correct_answer, t.solution_md, t.solution_code
		FROM assignment_tasks at
		JOIN tasks t ON t.id = at.task_id
		LEFT JOIN topics top ON top.id = t.topic_id
		WHERE at.assignment_id = $1 AND t.deleted_at IS NULL
		ORDER BY at.position ASC`

	rows, err := r.pool.Query(ctx, query, assignmentID)
	if err != nil {
		return nil, fmt.Errorf("query assignment tasks: %w", err)
	}
	defer rows.Close()

	var result []SessionTaskDetailRow
	for rows.Next() {
		var row SessionTaskDetailRow
		err := rows.Scan(
			&row.Position, &row.TaskID, &row.KimNumbers, &row.TopicID, &row.TopicKim, &row.TopicName,
			&row.Difficulty, &row.Source, &row.TextMD, &row.Images, &row.Files, &row.AnswerConfig,
			&row.CorrectAnswer, &row.SolutionMD, &row.SolutionCode,
		)
		if err != nil {
			return nil, fmt.Errorf("scan task detail: %w", err)
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// GetUserAssignmentTask reads student's current submission state for a task.
func (r *PostgresRepository) GetUserAssignmentTask(ctx context.Context, sessionID uuid.UUID, taskID int) (*UserAssignmentTask, error) {
	const query = `
		SELECT session_id, assignment_id, user_id, task_id, topic_id, is_correct,
		       earned_score, attempts_used, user_answer, time_spent_sec, code_s3_key,
		       paste_ranges, student_comment, updated_at, created_at
		FROM user_assignment_tasks
		WHERE session_id = $1 AND task_id = $2`

	var uat UserAssignmentTask
	err := r.pool.QueryRow(ctx, query, sessionID, taskID).Scan(
		&uat.SessionID, &uat.AssignmentID, &uat.UserID, &uat.TaskID, &uat.TopicID, &uat.IsCorrect,
		&uat.EarnedScore, &uat.AttemptsUsed, &uat.UserAnswer, &uat.TimeSpentSec, &uat.CodeS3Key,
		&uat.PasteRanges, &uat.StudentComment, &uat.UpdatedAt, &uat.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("query user_assignment_task: %w", err)
	}
	return &uat, nil
}

// UpsertUserAssignmentTask saves answer, score, code S3 key, and paste ranges.
func (r *PostgresRepository) UpsertUserAssignmentTask(ctx context.Context, uat *UserAssignmentTask) error {
	const query = `
		INSERT INTO user_assignment_tasks (
			session_id, assignment_id, user_id, task_id, topic_id, is_correct,
			earned_score, attempts_used, user_answer, time_spent_sec, code_s3_key,
			paste_ranges, student_comment, updated_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, now(), now())
		ON CONFLICT (session_id, task_id) DO UPDATE SET
			is_correct = EXCLUDED.is_correct,
			earned_score = EXCLUDED.earned_score,
			attempts_used = EXCLUDED.attempts_used,
			user_answer = EXCLUDED.user_answer,
			time_spent_sec = EXCLUDED.time_spent_sec,
			code_s3_key = COALESCE(EXCLUDED.code_s3_key, user_assignment_tasks.code_s3_key),
			paste_ranges = EXCLUDED.paste_ranges,
			student_comment = COALESCE(EXCLUDED.student_comment, user_assignment_tasks.student_comment),
			updated_at = now()`

	_, err := r.pool.Exec(ctx, query,
		uat.SessionID, uat.AssignmentID, uat.UserID, uat.TaskID, uat.TopicID, uat.IsCorrect,
		uat.EarnedScore, uat.AttemptsUsed, uat.UserAnswer, uat.TimeSpentSec, uat.CodeS3Key,
		uat.PasteRanges, uat.StudentComment,
	)
	if err != nil {
		return fmt.Errorf("upsert user_assignment_task: %w", err)
	}
	return nil
}

// ListUserAssignmentTasks returns all task submissions in a session.
func (r *PostgresRepository) ListUserAssignmentTasks(ctx context.Context, sessionID uuid.UUID) ([]UserAssignmentTask, error) {
	const query = `
		SELECT session_id, assignment_id, user_id, task_id, topic_id, is_correct,
		       earned_score, attempts_used, user_answer, time_spent_sec, code_s3_key,
		       paste_ranges, student_comment, updated_at, created_at
		FROM user_assignment_tasks
		WHERE session_id = $1
		ORDER BY task_id ASC`

	rows, err := r.pool.Query(ctx, query, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list user_assignment_tasks: %w", err)
	}
	defer rows.Close()

	var list []UserAssignmentTask
	for rows.Next() {
		var uat UserAssignmentTask
		err := rows.Scan(
			&uat.SessionID, &uat.AssignmentID, &uat.UserID, &uat.TaskID, &uat.TopicID, &uat.IsCorrect,
			&uat.EarnedScore, &uat.AttemptsUsed, &uat.UserAnswer, &uat.TimeSpentSec, &uat.CodeS3Key,
			&uat.PasteRanges, &uat.StudentComment, &uat.UpdatedAt, &uat.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan user_assignment_task: %w", err)
		}
		list = append(list, uat)
	}
	return list, rows.Err()
}

// SelectSelfExamTasks selects 25 task cards covering KIM 1..27 (with triplet 19..21 as 1 card).
func (r *PostgresRepository) SelectSelfExamTasks(ctx context.Context, userID uuid.UUID) ([]int, error) {
	var selectedTaskIDs []int

	// For KIM 1..18, 22..27 (24 single cards)
	singleKIMs := []int{
		1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18,
		// triplet 19_21 goes between 18 and 22
		22, 23, 24, 25, 26, 27,
	}

	const singleCardQuery = `
		SELECT t.id
		FROM tasks t
		WHERE t.kim_numbers @> ARRAY[$1::smallint]
		  AND cardinality(t.kim_numbers) = 1
		  AND t.is_public_bank = TRUE
		  AND t.deleted_at IS NULL
		ORDER BY (
			SELECT CASE WHEN ut.is_correct THEN 1 ELSE 0 END
			FROM user_tasks ut
			WHERE ut.user_id = $2 AND ut.task_id = t.id
		) ASC, RANDOM()
		LIMIT 1`

	const tripletCardQuery = `
		SELECT t.id
		FROM tasks t
		WHERE t.kim_numbers @> ARRAY[19::smallint, 20::smallint, 21::smallint]
		  AND cardinality(t.kim_numbers) = 3
		  AND t.is_public_bank = TRUE
		  AND t.deleted_at IS NULL
		ORDER BY (
			SELECT CASE WHEN ut.is_correct THEN 1 ELSE 0 END
			FROM user_tasks ut
			WHERE ut.user_id = $1 AND ut.task_id = t.id
		) ASC, RANDOM()
		LIMIT 1`

	for _, kim := range singleKIMs[:18] { // 1..18
		var taskID int
		err := r.pool.QueryRow(ctx, singleCardQuery, kim, userID).Scan(&taskID)
		if err != nil {
			return nil, fmt.Errorf("failed to select task for KIM %d: %w", kim, err)
		}
		selectedTaskIDs = append(selectedTaskIDs, taskID)
	}

	// 19..21 triplet card
	var tripletTaskID int
	err := r.pool.QueryRow(ctx, tripletCardQuery, userID).Scan(&tripletTaskID)
	if err != nil {
		// Fallback: try finding task with kim 19
		err = r.pool.QueryRow(ctx, `SELECT id FROM tasks WHERE kim_numbers @> ARRAY[19::smallint] AND deleted_at IS NULL LIMIT 1`).Scan(&tripletTaskID)
		if err != nil {
			return nil, fmt.Errorf("failed to select triplet task for KIM 19-21: %w", err)
		}
	}
	selectedTaskIDs = append(selectedTaskIDs, tripletTaskID)

	// 22..27 (6 cards)
	for _, kim := range singleKIMs[18:] {
		var taskID int
		err := r.pool.QueryRow(ctx, singleCardQuery, kim, userID).Scan(&taskID)
		if err != nil {
			return nil, fmt.Errorf("failed to select task for KIM %d: %w", kim, err)
		}
		selectedTaskIDs = append(selectedTaskIDs, taskID)
	}

	return selectedTaskIDs, nil
}

// LogTaskEvent records an audit / proctoring event into partitioned user_task_events table.
func (r *PostgresRepository) LogTaskEvent(ctx context.Context, sessionID, assignmentID, userID uuid.UUID, taskID *int, event string, payload any) error {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		payloadJSON = []byte("{}")
	}

	const query = `
		INSERT INTO user_task_events (session_id, assignment_id, user_id, task_id, event, payload, occurred_at)
		VALUES ($1, $2, $3, $4, $5, $6, now())`

	_, err = r.pool.Exec(ctx, query, sessionID, assignmentID, userID, taskID, event, payloadJSON)
	return err
}

// CheckRoomMembership tests whether a student is enrolled in a room.
func (r *PostgresRepository) CheckRoomMembership(ctx context.Context, roomID, userID uuid.UUID) (bool, error) {
	const query = `SELECT EXISTS(SELECT 1 FROM user_rooms WHERE room_id = $1 AND user_id = $2)`
	var exists bool
	err := r.pool.QueryRow(ctx, query, roomID, userID).Scan(&exists)
	return exists, err
}

// CheckRoomCreator tests whether a user is the creator of a room.
func (r *PostgresRepository) CheckRoomCreator(ctx context.Context, roomID, userID uuid.UUID) (bool, error) {
	const query = `SELECT EXISTS(SELECT 1 FROM rooms WHERE id = $1 AND creator_id = $2 AND deleted_at IS NULL)`
	var exists bool
	err := r.pool.QueryRow(ctx, query, roomID, userID).Scan(&exists)
	return exists, err
}
