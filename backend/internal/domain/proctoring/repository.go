package proctoring

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
	ErrNotFound  = errors.New("not found")
	ErrForbidden = errors.New("forbidden")
)

// AssignmentHeader contains assignment metadata for permission and proctoring checks.
type AssignmentHeader struct {
	ID        uuid.UUID
	RoomID    *uuid.UUID
	CreatorID uuid.UUID
	Title     string
	Mode      string
}

// StudentMatrixRow represents flat scanned student task interaction.
type StudentMatrixRow struct {
	UserID         uuid.UUID
	Username       string
	FirstName      string
	LastName       string
	SessionID      *uuid.UUID
	Status         *string
	BlurCount      *int
	PasteCount     *int
	PrimaryScore   *int16
	SecondaryScore *int16
	TaskID         *int
	Position       *int16
	IsCorrect      *bool
	EarnedScore    *int16
	AttemptsUsed   *int
}

// InspectionSessionRow contains session and student info for inspection dossier.
type InspectionSessionRow struct {
	SessionID      uuid.UUID
	AssignmentID   uuid.UUID
	UserID         uuid.UUID
	UserName       string
	Status         string
	StartedAt      time.Time
	FinishedAt     *time.Time
	PrimaryScore   int16
	SecondaryScore *int16
	BlurCount      int
	PasteCount     int
	CreatorID      uuid.UUID
}

// TaskCodeRow contains code reference for inspection.
type TaskCodeRow struct {
	TaskID         int
	CodeS3Key      *string
	PasteRanges    []byte
	StudentComment *string
	UpdatedAt      time.Time
}

// Repository defines data access for proctoring inspection and matrix snapshots.
type Repository interface {
	GetAssignmentHeader(ctx context.Context, assignmentID uuid.UUID) (*AssignmentHeader, error)
	GetMatrixStudents(ctx context.Context, assignmentID uuid.UUID, roomID *uuid.UUID) ([]StudentMatrixRow, error)
	GetMatrixTaskStats(ctx context.Context, assignmentID uuid.UUID) ([]MatrixTaskStatDTO, error)
	GetInspectionSession(ctx context.Context, sessionID uuid.UUID) (*InspectionSessionRow, error)
	GetTimelineEvents(ctx context.Context, sessionID uuid.UUID) ([]TimelineEventDTO, error)
	GetSessionTaskCodes(ctx context.Context, sessionID uuid.UUID) ([]TaskCodeRow, error)

	CreateTeacherTask(ctx context.Context, teacherID uuid.UUID, req CreateTeacherTaskRequest) (*tasks.TaskDTO, error)
	ListTeacherTasks(ctx context.Context, teacherID uuid.UUID, limit, offset int, kim *int, topicID *int, q string) ([]tasks.TaskDTO, int, error)
}

// PostgresRepository implements Repository using pgxpool.
type PostgresRepository struct {
	pool *pgxpool.Pool
}

// NewRepository creates a new proctoring PostgresRepository.
func NewRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

// GetAssignmentHeader loads assignment header.
func (r *PostgresRepository) GetAssignmentHeader(ctx context.Context, assignmentID uuid.UUID) (*AssignmentHeader, error) {
	const query = `
		SELECT id, room_id, creator_id, title, mode
		FROM assignments
		WHERE id = $1 AND deleted_at IS NULL`

	var h AssignmentHeader
	err := r.pool.QueryRow(ctx, query, assignmentID).Scan(&h.ID, &h.RoomID, &h.CreatorID, &h.Title, &h.Mode)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get assignment header: %w", err)
	}
	return &h, nil
}

// GetMatrixStudents queries all students in room or assignment sessions.
func (r *PostgresRepository) GetMatrixStudents(ctx context.Context, assignmentID uuid.UUID, roomID *uuid.UUID) ([]StudentMatrixRow, error) {
	var rows pgx.Rows
	var err error

	if roomID != nil && *roomID != uuid.Nil {
		const roomQuery = `
			SELECT u.id, u.username, u.first_name, u.last_name,
			       s.id, s.status, s.blur_count, s.paste_count, s.primary_score, s.secondary_score,
			       uat.task_id, at.position, uat.is_correct, uat.earned_score, uat.attempts_used
			FROM user_rooms ur
			JOIN users u ON u.id = ur.user_id
			LEFT JOIN assignment_sessions s ON s.assignment_id = $1 AND s.user_id = u.id
			LEFT JOIN user_assignment_tasks uat ON uat.session_id = s.id
			LEFT JOIN assignment_tasks at ON at.assignment_id = $1 AND at.task_id = uat.task_id
			WHERE ur.room_id = $2 AND u.deleted_at IS NULL
			ORDER BY u.last_name ASC, u.first_name ASC, at.position ASC`
		rows, err = r.pool.Query(ctx, roomQuery, assignmentID, *roomID)
	} else {
		const standaloneQuery = `
			SELECT u.id, u.username, u.first_name, u.last_name,
			       s.id, s.status, s.blur_count, s.paste_count, s.primary_score, s.secondary_score,
			       uat.task_id, at.position, uat.is_correct, uat.earned_score, uat.attempts_used
			FROM assignment_sessions s
			JOIN users u ON u.id = s.user_id
			LEFT JOIN user_assignment_tasks uat ON uat.session_id = s.id
			LEFT JOIN assignment_tasks at ON at.assignment_id = $1 AND at.task_id = uat.task_id
			WHERE s.assignment_id = $1 AND u.deleted_at IS NULL
			ORDER BY u.last_name ASC, u.first_name ASC, at.position ASC`
		rows, err = r.pool.Query(ctx, standaloneQuery, assignmentID)
	}

	if err != nil {
		return nil, fmt.Errorf("query matrix students: %w", err)
	}
	defer rows.Close()

	var result []StudentMatrixRow
	for rows.Next() {
		var row StudentMatrixRow
		err := rows.Scan(
			&row.UserID, &row.Username, &row.FirstName, &row.LastName,
			&row.SessionID, &row.Status, &row.BlurCount, &row.PasteCount, &row.PrimaryScore, &row.SecondaryScore,
			&row.TaskID, &row.Position, &row.IsCorrect, &row.EarnedScore, &row.AttemptsUsed,
		)
		if err != nil {
			return nil, fmt.Errorf("scan matrix student row: %w", err)
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// GetMatrixTaskStats loads column aggregations for tasks in this assignment.
func (r *PostgresRepository) GetMatrixTaskStats(ctx context.Context, assignmentID uuid.UUID) ([]MatrixTaskStatDTO, error) {
	const query = `
		SELECT at.task_id, at.position,
		       COALESCE(ats.solved_count, 0),
		       COALESCE(ats.attempted_count, 0),
		       COALESCE(ats.avg_attempts, 0.0),
		       COALESCE(ats.avg_time_sec, 0)
		FROM assignment_tasks at
		LEFT JOIN assignment_task_stats ats ON ats.assignment_id = at.assignment_id AND ats.task_id = at.task_id
		WHERE at.assignment_id = $1
		ORDER BY at.position ASC`

	rows, err := r.pool.Query(ctx, query, assignmentID)
	if err != nil {
		return nil, fmt.Errorf("query matrix task stats: %w", err)
	}
	defer rows.Close()

	var stats []MatrixTaskStatDTO
	for rows.Next() {
		var s MatrixTaskStatDTO
		var pos int16
		if err := rows.Scan(&s.TaskID, &pos, &s.SolvedCount, &s.AttemptedCount, &s.AvgAttempts, &s.AvgTimeSec); err != nil {
			return nil, fmt.Errorf("scan matrix task stat: %w", err)
		}
		s.Position = int(pos)
		stats = append(stats, s)
	}
	return stats, rows.Err()
}

// GetInspectionSession retrieves metadata of the session under inspection.
func (r *PostgresRepository) GetInspectionSession(ctx context.Context, sessionID uuid.UUID) (*InspectionSessionRow, error) {
	const query = `
		SELECT s.id, s.assignment_id, s.user_id,
		       TRIM(u.first_name || ' ' || u.last_name),
		       s.status, s.started_at, s.finished_at, s.primary_score, s.secondary_score,
		       s.blur_count, s.paste_count, a.creator_id
		FROM assignment_sessions s
		JOIN users u ON u.id = s.user_id
		JOIN assignments a ON a.id = s.assignment_id
		WHERE s.id = $1`

	var row InspectionSessionRow
	err := r.pool.QueryRow(ctx, query, sessionID).Scan(
		&row.SessionID, &row.AssignmentID, &row.UserID, &row.UserName,
		&row.Status, &row.StartedAt, &row.FinishedAt, &row.PrimaryScore, &row.SecondaryScore,
		&row.BlurCount, &row.PasteCount, &row.CreatorID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("query inspection session: %w", err)
	}
	return &row, nil
}

// GetTimelineEvents queries all audit / telemetric events from user_task_events for session.
func (r *PostgresRepository) GetTimelineEvents(ctx context.Context, sessionID uuid.UUID) ([]TimelineEventDTO, error) {
	const query = `
		SELECT id, task_id, event, payload, occurred_at
		FROM user_task_events
		WHERE session_id = $1
		ORDER BY occurred_at ASC, id ASC`

	rows, err := r.pool.Query(ctx, query, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query timeline events: %w", err)
	}
	defer rows.Close()

	var events []TimelineEventDTO
	for rows.Next() {
		var e TimelineEventDTO
		var rawPayload []byte
		var occAt time.Time
		if err := rows.Scan(&e.ID, &e.TaskID, &e.Event, &rawPayload, &occAt); err != nil {
			return nil, fmt.Errorf("scan timeline event: %w", err)
		}
		e.Payload = rawPayload
		e.OccurredAt = occAt.UTC().Format(time.RFC3339)
		events = append(events, e)
	}
	return events, rows.Err()
}

// GetSessionTaskCodes queries student's saved python code and paste ranges for inspection.
func (r *PostgresRepository) GetSessionTaskCodes(ctx context.Context, sessionID uuid.UUID) ([]TaskCodeRow, error) {
	const query = `
		SELECT task_id, code_s3_key, paste_ranges, student_comment, updated_at
		FROM user_assignment_tasks
		WHERE session_id = $1
		ORDER BY task_id ASC`

	rows, err := r.pool.Query(ctx, query, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query session task codes: %w", err)
	}
	defer rows.Close()

	var list []TaskCodeRow
	for rows.Next() {
		var row TaskCodeRow
		if err := rows.Scan(&row.TaskID, &row.CodeS3Key, &row.PasteRanges, &row.StudentComment, &row.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan task code row: %w", err)
		}
		list = append(list, row)
	}
	return list, rows.Err()
}

// CreateTeacherTask inserts a private author task for teacher.
func (r *PostgresRepository) CreateTeacherTask(ctx context.Context, teacherID uuid.UUID, req CreateTeacherTaskRequest) (*tasks.TaskDTO, error) {
	ansCfgJSON, err := json.Marshal(req.AnswerConfig)
	if err != nil {
		return nil, fmt.Errorf("marshal answer_config: %w", err)
	}
	corrAnsJSON, err := json.Marshal(req.CorrectAnswer)
	if err != nil {
		return nil, fmt.Errorf("marshal correct_answer: %w", err)
	}
	filesJSON, err := json.Marshal(req.Files)
	if err != nil {
		filesJSON = []byte("[]")
	}

	var kimSmallints []int16
	for _, k := range req.KimNumbers {
		kimSmallints = append(kimSmallints, int16(k))
	}

	const insertSQL = `
		INSERT INTO tasks (
			owner_id, is_public_bank, kim_numbers, topic_id, difficulty,
			source, text_md, images, files, answer_config, correct_answer,
			solution_md, solution_code, created_at, updated_at
		) VALUES ($1, FALSE, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, now(), now())
		RETURNING id, created_at`

	var taskID int
	var createdAt time.Time

	err = r.pool.QueryRow(ctx, insertSQL,
		teacherID, kimSmallints, req.TopicID, req.Difficulty,
		req.Source, req.TextMD, req.Images, filesJSON, ansCfgJSON, corrAnsJSON,
		req.SolutionMD, req.SolutionCode,
	).Scan(&taskID, &createdAt)
	if err != nil {
		return nil, fmt.Errorf("insert teacher task: %w", err)
	}

	var topicDTO tasks.TopicDTO
	_ = r.pool.QueryRow(ctx, `SELECT id, kim_number, name, sort_order FROM topics WHERE id = $1`, req.TopicID).Scan(
		&topicDTO.ID, &topicDTO.KimNumber, &topicDTO.Name, &topicDTO.SortOrder,
	)

	return &tasks.TaskDTO{
		ID:           taskID,
		OwnerID:      &teacherID,
		IsPublicBank: false,
		KimNumbers:   req.KimNumbers,
		Topic:        topicDTO,
		Difficulty:   req.Difficulty,
		Source:       &req.Source,
		TextMD:       req.TextMD,
		Images:       req.Images,
		Files:        req.Files,
		AnswerConfig: req.AnswerConfig,
		Solution: &tasks.SolutionDTO{
			CorrectAnswer: &req.CorrectAnswer,
			SolutionMD:    req.SolutionMD,
			SolutionCode:  req.SolutionCode,
		},
		CreatedAt: createdAt.UTC().Format(time.RFC3339),
	}, nil
}

// ListTeacherTasks retrieves tasks owned by the teacher.
func (r *PostgresRepository) ListTeacherTasks(ctx context.Context, teacherID uuid.UUID, limit, offset int, kim *int, topicID *int, q string) ([]tasks.TaskDTO, int, error) {
	if limit <= 0 {
		limit = 20
	}

	whereClause := `WHERE owner_id = $1 AND deleted_at IS NULL`
	args := []any{teacherID}
	argIdx := 2

	if kim != nil {
		whereClause += fmt.Sprintf(` AND kim_numbers @> ARRAY[$%d::smallint]`, argIdx)
		args = append(args, *kim)
		argIdx++
	}
	if topicID != nil {
		whereClause += fmt.Sprintf(` AND topic_id = $%d`, argIdx)
		args = append(args, *topicID)
		argIdx++
	}
	if q != "" {
		whereClause += fmt.Sprintf(` AND lower(text_md) LIKE '%%' || lower($%d) || '%%'`, argIdx)
		args = append(args, q)
		argIdx++
	}

	countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM tasks %s`, whereClause)
	var total int
	if err := r.pool.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count teacher tasks: %w", err)
	}

	dataQuery := fmt.Sprintf(`
		SELECT t.id, t.owner_id, t.is_public_bank, t.kim_numbers, t.topic_id,
		       top.kim_number, top.name, top.sort_order,
		       t.difficulty, t.source, t.text_md, t.images, t.files, t.answer_config,
		       t.correct_answer, t.solution_md, t.solution_code, t.created_at
		FROM tasks t
		LEFT JOIN topics top ON top.id = t.topic_id
		%s
		ORDER BY t.created_at DESC
		LIMIT $%d OFFSET $%d`, whereClause, argIdx, argIdx+1)

	args = append(args, limit, offset)

	rows, err := r.pool.Query(ctx, dataQuery, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("query teacher tasks: %w", err)
	}
	defer rows.Close()

	var items []tasks.TaskDTO
	for rows.Next() {
		var t tasks.TaskDTO
		var kimSmallints []int16
		var topicKim *int
		var topicName *string
		var topicSort *int
		var filesJSON []byte
		var ansCfgJSON []byte
		var corrAnsJSON []byte
		var solMD *string
		var solCode *string
		var createdAt time.Time

		err := rows.Scan(
			&t.ID, &t.OwnerID, &t.IsPublicBank, &kimSmallints, &t.Topic.ID,
			&topicKim, &topicName, &topicSort,
			&t.Difficulty, &t.Source, &t.TextMD, &t.Images, &filesJSON, &ansCfgJSON,
			&corrAnsJSON, &solMD, &solCode, &createdAt,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("scan teacher task: %w", err)
		}

		for _, k := range kimSmallints {
			t.KimNumbers = append(t.KimNumbers, int(k))
		}
		if topicKim != nil {
			t.Topic.KimNumber = *topicKim
		}
		if topicName != nil {
			t.Topic.Name = *topicName
		}
		if topicSort != nil {
			t.Topic.SortOrder = *topicSort
		}
		if len(filesJSON) > 0 {
			_ = json.Unmarshal(filesJSON, &t.Files)
		}
		if len(ansCfgJSON) > 0 {
			_ = json.Unmarshal(ansCfgJSON, &t.AnswerConfig)
		}
		var corr tasks.CorrectAnswer
		if len(corrAnsJSON) > 0 {
			_ = json.Unmarshal(corrAnsJSON, &corr)
		}
		t.Solution = &tasks.SolutionDTO{
			CorrectAnswer: &corr,
			SolutionMD:    solMD,
			SolutionCode:  solCode,
		}
		t.CreatedAt = createdAt.UTC().Format(time.RFC3339)
		items = append(items, t)
	}

	return items, total, rows.Err()
}
