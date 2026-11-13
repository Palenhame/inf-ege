package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrTaskNotFound           = errors.New("task not found")
	ErrTaskLockedByActiveExam = errors.New("TASK_LOCKED_BY_ACTIVE_EXAM")
	ErrTopicNotFound          = errors.New("topic not found")
	ErrInvalidAnswerPayload   = errors.New("invalid answer payload")
)

// Repository handles database interactions for tasks and topics.
type Repository interface {
	ListBankTasks(ctx context.Context, filter ListTasksFilter) ([]TaskDTO, int, error)
	GetTaskByID(ctx context.Context, id int, userID *uuid.UUID) (*TaskDTO, error)
	IsTaskInActiveExam(ctx context.Context, taskID int, userID uuid.UUID) (bool, error)
	RecordSubmission(ctx context.Context, taskID int, userID uuid.UUID, res GradeResult, answerJSON []byte, recLogID *int64) (*TaskSubmitResponse, error)
	CreateTask(ctx context.Context, task *Task) (int, error)
	UpdateTask(ctx context.Context, id int, task *Task) error
	DeleteTask(ctx context.Context, id int) error
	ListAuthorTasks(ctx context.Context, ownerID uuid.UUID, filter ListTasksFilter) ([]TaskDTO, int, error)
	GetTopics(ctx context.Context) ([]KimGroupDTO, error)
	CreateTopic(ctx context.Context, kimNumber int, name string, sortOrder int) (*TopicDTO, error)
	UpdateTopic(ctx context.Context, id int, name string, sortOrder int) (*TopicDTO, error)
	DeleteTopic(ctx context.Context, id int) error
	GetOrCreateTopic(ctx context.Context, kimNumber int, name string) (int, error)
}

// GradingProvider is an optional interface repositories may implement to supply
// canonical answer configuration and correct answer for grading submissions.
type GradingProvider interface {
	GetGradingData(ctx context.Context, id int) (AnswerConfig, CorrectAnswer, error)
}

type pgRepository struct {
	pool *pgxpool.Pool
}

// NewRepository creates a new postgres-backed repository.
func NewRepository(pool *pgxpool.Pool) Repository {
	return &pgRepository{pool: pool}
}

// GetGradingData fetches answer_config and correct_answer for a task.
func (r *pgRepository) GetGradingData(ctx context.Context, id int) (AnswerConfig, CorrectAnswer, error) {
	var cfgJSON []byte
	var correctJSON []byte

	err := r.pool.QueryRow(ctx, `
		SELECT answer_config, correct_answer
		FROM tasks
		WHERE id = $1 AND deleted_at IS NULL
	`, id).Scan(&cfgJSON, &correctJSON)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AnswerConfig{}, CorrectAnswer{}, ErrTaskNotFound
		}
		return AnswerConfig{}, CorrectAnswer{}, fmt.Errorf("get grading data: %w", err)
	}

	var cfg AnswerConfig
	if len(cfgJSON) > 0 {
		if err := json.Unmarshal(cfgJSON, &cfg); err != nil {
			return AnswerConfig{}, CorrectAnswer{}, fmt.Errorf("unmarshal answer_config: %w", err)
		}
	}

	var correct CorrectAnswer
	if len(correctJSON) > 0 {
		if err := json.Unmarshal(correctJSON, &correct); err != nil {
			return AnswerConfig{}, CorrectAnswer{}, fmt.Errorf("unmarshal correct_answer: %w", err)
		}
	}

	return cfg, correct, nil
}

// IsTaskInActiveExam checks if the task is currently assigned to an in-progress mock exam or proctored test session.
func (r *pgRepository) IsTaskInActiveExam(ctx context.Context, taskID int, userID uuid.UUID) (bool, error) {
	query := `
		SELECT EXISTS (
			SELECT 1
			FROM assignment_sessions s
			JOIN assignments a ON a.id = s.assignment_id
			JOIN assignment_tasks at ON at.assignment_id = a.id
			WHERE s.user_id = $1
			  AND s.status = 'in_progress'
			  AND a.mode IN ('mock_exam', 'proctored_test')
			  AND at.task_id = $2
		)
	`
	var exists bool
	err := r.pool.QueryRow(ctx, query, userID, taskID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check active exam task: %w", err)
	}
	return exists, nil
}

// ListBankTasks retrieves a filtered, paginated list of public bank tasks.
func (r *pgRepository) ListBankTasks(ctx context.Context, filter ListTasksFilter) ([]TaskDTO, int, error) {
	var conditions []string
	var args []any
	argIdx := 1

	conditions = append(conditions, "t.deleted_at IS NULL")

	if filter.IsPublicBank != nil {
		conditions = append(conditions, fmt.Sprintf("t.is_public_bank = $%d", argIdx))
		args = append(args, *filter.IsPublicBank)
		argIdx++
	} else if filter.OwnerID == nil {
		conditions = append(conditions, "t.is_public_bank = TRUE")
	}

	if filter.OwnerID != nil {
		conditions = append(conditions, fmt.Sprintf("t.owner_id = $%d", argIdx))
		args = append(args, *filter.OwnerID)
		argIdx++
	}

	// Filter by KIM number (array contains operator)
	if filter.Kim != nil && *filter.Kim >= 1 && *filter.Kim <= 27 {
		conditions = append(conditions, fmt.Sprintf("t.kim_numbers @> ARRAY[$%d::smallint]", argIdx))
		args = append(args, int16(*filter.Kim))
		argIdx++
	}

	// Filter by topic ID
	if filter.TopicID != nil && *filter.TopicID > 0 {
		conditions = append(conditions, fmt.Sprintf("t.topic_id = $%d", argIdx))
		args = append(args, *filter.TopicID)
		argIdx++
	}

	// Filter by difficulty
	if filter.Difficulty != nil && *filter.Difficulty >= 1 && *filter.Difficulty <= 4 {
		conditions = append(conditions, fmt.Sprintf("t.difficulty = $%d", argIdx))
		args = append(args, int16(*filter.Difficulty))
		argIdx++
	}

	// Filter by query: if starts with # or is numeric -> exact ID match; otherwise trigram LIKE on lower(text_md)
	q := strings.TrimSpace(filter.Query)
	if q != "" {
		if strings.HasPrefix(q, "#") {
			idStr := strings.TrimPrefix(q, "#")
			if id, err := strconv.Atoi(idStr); err == nil {
				conditions = append(conditions, fmt.Sprintf("t.id = $%d", argIdx))
				args = append(args, id)
				argIdx++
			}
		} else if id, err := strconv.Atoi(q); err == nil {
			conditions = append(conditions, fmt.Sprintf("(t.id = $%d OR lower(t.text_md) LIKE $%d)", argIdx, argIdx+1))
			args = append(args, id, "%"+strings.ToLower(q)+"%")
			argIdx += 2
		} else {
			conditions = append(conditions, fmt.Sprintf("lower(t.text_md) LIKE $%d", argIdx))
			args = append(args, "%"+strings.ToLower(q)+"%")
			argIdx++
		}
	}

	// Anti-cheat: exclude tasks in active exam sessions
	if filter.UserID != nil {
		conditions = append(conditions, fmt.Sprintf(`
			NOT EXISTS (
				SELECT 1
				FROM assignment_sessions s
				JOIN assignments a ON a.id = s.assignment_id
				JOIN assignment_tasks at ON at.assignment_id = a.id
				WHERE s.user_id = $%d
				  AND s.status = 'in_progress'
				  AND a.mode IN ('mock_exam', 'proctored_test')
				  AND at.task_id = t.id
			)
		`, argIdx))
		args = append(args, *filter.UserID)
		argIdx++
	}

	// User status filter: solved, unsolved, attempted
	if filter.UserID != nil && filter.Status != "" && filter.Status != "all" {
		switch filter.Status {
		case "solved":
			conditions = append(conditions, "ut.is_correct = TRUE")
		case "unsolved", "failed":
			conditions = append(conditions, "(ut.total_attempts > 0 AND ut.is_correct = FALSE)")
		case "attempted":
			conditions = append(conditions, "ut.total_attempts > 0")
		case "not_attempted":
			conditions = append(conditions, "(ut.total_attempts IS NULL OR ut.total_attempts = 0)")
		}
	}

	// Sorting
	orderBy := "t.id ASC"
	switch filter.Sort {
	case "newest":
		orderBy = "t.created_at DESC"
	case "oldest":
		orderBy = "t.created_at ASC"
	case "solve_rate_asc":
		orderBy = "t.solve_rate ASC NULLS LAST, t.id ASC"
	case "solve_rate_desc":
		orderBy = "t.solve_rate DESC NULLS LAST, t.id ASC"
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}

	whereClause := strings.Join(conditions, " AND ")

	var userJoin string
	var userSelect string
	if filter.UserID != nil {
		userJoin = fmt.Sprintf("LEFT JOIN user_tasks ut ON ut.task_id = t.id AND ut.user_id = $%d", argIdx)
		args = append(args, *filter.UserID)
		argIdx++
		userSelect = ", ut.is_correct, ut.best_score, ut.total_attempts, ut.last_user_answer"
	} else {
		userSelect = ", NULL::boolean, NULL::smallint, NULL::integer, NULL::jsonb"
	}

	query := fmt.Sprintf(`
		SELECT
			t.id, t.owner_id, t.is_public_bank, t.kim_numbers, t.topic_id,
			tp.kim_number, tp.name, tp.sort_order,
			t.difficulty, t.source, t.text_md, t.images, t.files,
			t.answer_config, t.correct_answer, t.solution_md, t.solution_code,
			t.avg_solve_time, t.solve_rate, t.avg_attempts, t.attempts_histogram,
			t.bank_stats_count, t.created_at,
			COUNT(*) OVER() AS full_count
			%s
		FROM tasks t
		JOIN topics tp ON tp.id = t.topic_id AND tp.deleted_at IS NULL
		%s
		WHERE %s
		ORDER BY %s
		LIMIT $%d OFFSET $%d
	`, userSelect, userJoin, whereClause, orderBy, argIdx, argIdx+1)

	args = append(args, limit, offset)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list bank tasks query: %w", err)
	}
	defer rows.Close()

	var items []TaskDTO
	total := 0

	for rows.Next() {
		var (
			id                                    int
			ownerID                               *uuid.UUID
			isPublic                              bool
			kimNumbers                            []int16
			topicID                               int
			tpKimNumber                           int16
			tpName                                string
			tpSortOrder                           int
			difficulty                            int16
			source                                *string
			textMD                                string
			images                                []string
			filesJSON                             []byte
			cfgJSON                               []byte
			correctJSON                           []byte
			solutionMD                            *string
			solutionCode                          *string
			avgSolveTime                          *int
			solveRate                             *float64
			avgAttempts                           *float64
			attemptsHist                          [15]int32
			bankStatsCount                        int
			createdAt                             time.Time
			fullCount                             int
			utIsCorrect                           *bool
			utBestScore                           *int16
			utTotalAttempts                       *int
			utLastAnswer                          []byte
		)

		err := rows.Scan(
			&id, &ownerID, &isPublic, &kimNumbers, &topicID,
			&tpKimNumber, &tpName, &tpSortOrder,
			&difficulty, &source, &textMD, &images, &filesJSON,
			&cfgJSON, &correctJSON, &solutionMD, &solutionCode,
			&avgSolveTime, &solveRate, &avgAttempts, &attemptsHist,
			&bankStatsCount, &createdAt, &fullCount,
			&utIsCorrect, &utBestScore, &utTotalAttempts, &utLastAnswer,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("scan task row: %w", err)
		}

		total = fullCount

		var files []FileAttachment
		if len(filesJSON) > 0 {
			_ = json.Unmarshal(filesJSON, &files)
		}
		if files == nil {
			files = []FileAttachment{}
		}

		var answerCfg AnswerConfig
		if len(cfgJSON) > 0 {
			_ = json.Unmarshal(cfgJSON, &answerCfg)
		}

		var correctAns CorrectAnswer
		if len(correctJSON) > 0 {
			_ = json.Unmarshal(correctJSON, &correctAns)
		}

		var hist [15]int
		for i, v := range attemptsHist {
			hist[i] = int(v)
		}

		var kimInts []int
		for _, k := range kimNumbers {
			kimInts = append(kimInts, int(k))
		}

		stats := TaskStats{
			SolveRate:         solveRate,
			AvgAttempts:       avgAttempts,
			AvgSolveTimeSec:   avgSolveTime,
			AttemptsHistogram: hist,
			BankSolversCount:  bankStatsCount,
		}

		var userState *UserState
		canViewSolution := false

		if filter.UserID != nil && utTotalAttempts != nil && *utTotalAttempts > 0 {
			isCorrect := utIsCorrect != nil && *utIsCorrect
			earnedScore := 0
			if utBestScore != nil {
				earnedScore = int(*utBestScore)
			}
			totAttempts := *utTotalAttempts
			canViewSolution = isCorrect || totAttempts >= 3

			var lastAns any
			if len(utLastAnswer) > 0 {
				_ = json.Unmarshal(utLastAnswer, &lastAns)
			}

			userState = &UserState{
				IsAttempted:     true,
				IsCorrect:       isCorrect,
				EarnedScore:     earnedScore,
				TotalAttempts:   totAttempts,
				LastUserAnswer:  lastAns,
				CanViewSolution: canViewSolution,
			}
		}

		var solutionDTO *SolutionDTO
		if canViewSolution {
			solutionDTO = &SolutionDTO{
				CorrectAnswer: &correctAns,
				SolutionMD:    solutionMD,
				SolutionCode:  solutionCode,
			}
		}

		dto := TaskDTO{
			ID:           id,
			OwnerID:      ownerID,
			IsPublicBank: isPublic,
			KimNumbers:   kimInts,
			Topic: TopicDTO{
				ID:        topicID,
				KimNumber: int(tpKimNumber),
				Name:      tpName,
				SortOrder: tpSortOrder,
			},
			Difficulty:   int(difficulty),
			Source:       source,
			TextMD:       textMD,
			Images:       images,
			Files:        files,
			AnswerConfig: answerCfg,
			Stats:        stats,
			UserState:    userState,
			Solution:     solutionDTO,
			CreatedAt:    createdAt.UTC().Format(time.RFC3339),
		}

		items = append(items, dto)
	}

	if items == nil {
		items = []TaskDTO{}
	}

	return items, total, nil
}

// GetTaskByID retrieves a single task by ID, enforcing user state and solution visibility rules.
func (r *pgRepository) GetTaskByID(ctx context.Context, id int, userID *uuid.UUID) (*TaskDTO, error) {
	var userJoin string
	var userSelect string
	var args []any
	args = append(args, id)

	if userID != nil {
		userJoin = "LEFT JOIN user_tasks ut ON ut.task_id = t.id AND ut.user_id = $2"
		args = append(args, *userID)
		userSelect = ", ut.is_correct, ut.best_score, ut.total_attempts, ut.last_user_answer"
	} else {
		userSelect = ", NULL::boolean, NULL::smallint, NULL::integer, NULL::jsonb"
	}

	query := fmt.Sprintf(`
		SELECT
			t.id, t.owner_id, t.is_public_bank, t.kim_numbers, t.topic_id,
			tp.kim_number, tp.name, tp.sort_order,
			t.difficulty, t.source, t.text_md, t.images, t.files,
			t.answer_config, t.correct_answer, t.solution_md, t.solution_code,
			t.avg_solve_time, t.solve_rate, t.avg_attempts, t.attempts_histogram,
			t.bank_stats_count, t.created_at
			%s
		FROM tasks t
		JOIN topics tp ON tp.id = t.topic_id AND tp.deleted_at IS NULL
		%s
		WHERE t.id = $1 AND t.deleted_at IS NULL
	`, userSelect, userJoin)

	var (
		taskID          int
		ownerID         *uuid.UUID
		isPublic        bool
		kimNumbers      []int16
		topicID         int
		tpKimNumber     int16
		tpName          string
		tpSortOrder     int
		difficulty      int16
		source          *string
		textMD          string
		images          []string
		filesJSON       []byte
		cfgJSON         []byte
		correctJSON     []byte
		solutionMD      *string
		solutionCode    *string
		avgSolveTime    *int
		solveRate       *float64
		avgAttempts     *float64
		attemptsHist    [15]int32
		bankStatsCount  int
		createdAt       time.Time
		utIsCorrect     *bool
		utBestScore     *int16
		utTotalAttempts *int
		utLastAnswer    []byte
	)

	err := r.pool.QueryRow(ctx, query, args...).Scan(
		&taskID, &ownerID, &isPublic, &kimNumbers, &topicID,
		&tpKimNumber, &tpName, &tpSortOrder,
		&difficulty, &source, &textMD, &images, &filesJSON,
		&cfgJSON, &correctJSON, &solutionMD, &solutionCode,
		&avgSolveTime, &solveRate, &avgAttempts, &attemptsHist,
		&bankStatsCount, &createdAt,
		&utIsCorrect, &utBestScore, &utTotalAttempts, &utLastAnswer,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTaskNotFound
		}
		return nil, fmt.Errorf("get task by id: %w", err)
	}

	var files []FileAttachment
	if len(filesJSON) > 0 {
		_ = json.Unmarshal(filesJSON, &files)
	}
	if files == nil {
		files = []FileAttachment{}
	}

	var answerCfg AnswerConfig
	if len(cfgJSON) > 0 {
		_ = json.Unmarshal(cfgJSON, &answerCfg)
	}

	var correctAns CorrectAnswer
	if len(correctJSON) > 0 {
		_ = json.Unmarshal(correctJSON, &correctAns)
	}

	var hist [15]int
	for i, v := range attemptsHist {
		hist[i] = int(v)
	}

	var kimInts []int
	for _, k := range kimNumbers {
		kimInts = append(kimInts, int(k))
	}

	stats := TaskStats{
		SolveRate:         solveRate,
		AvgAttempts:       avgAttempts,
		AvgSolveTimeSec:   avgSolveTime,
		AttemptsHistogram: hist,
		BankSolversCount:  bankStatsCount,
	}

	var userState *UserState
	canViewSolution := false

	if userID != nil && utTotalAttempts != nil && *utTotalAttempts > 0 {
		isCorrect := utIsCorrect != nil && *utIsCorrect
		earnedScore := 0
		if utBestScore != nil {
			earnedScore = int(*utBestScore)
		}
		totAttempts := *utTotalAttempts
		canViewSolution = isCorrect || totAttempts >= 3

		var lastAns any
		if len(utLastAnswer) > 0 {
			_ = json.Unmarshal(utLastAnswer, &lastAns)
		}

		userState = &UserState{
			IsAttempted:     true,
			IsCorrect:       isCorrect,
			EarnedScore:     earnedScore,
			TotalAttempts:   totAttempts,
			LastUserAnswer:  lastAns,
			CanViewSolution: canViewSolution,
		}
	}

	var solutionDTO *SolutionDTO
	if canViewSolution {
		solutionDTO = &SolutionDTO{
			CorrectAnswer: &correctAns,
			SolutionMD:    solutionMD,
			SolutionCode:  solutionCode,
		}
	}

	return &TaskDTO{
		ID:           taskID,
		OwnerID:      ownerID,
		IsPublicBank: isPublic,
		KimNumbers:   kimInts,
		Topic: TopicDTO{
			ID:        topicID,
			KimNumber: int(tpKimNumber),
			Name:      tpName,
			SortOrder: tpSortOrder,
		},
		Difficulty:   int(difficulty),
		Source:       source,
		TextMD:       textMD,
		Images:       images,
		Files:        files,
		AnswerConfig: answerCfg,
		Stats:        stats,
		UserState:    userState,
		Solution:     solutionDTO,
		CreatedAt:    createdAt.UTC().Format(time.RFC3339),
	}, nil
}

// RecordSubmission records answer attempt in user_tasks, triggers stats update, and logs recommendation response.
func (r *pgRepository) RecordSubmission(
	ctx context.Context,
	taskID int,
	userID uuid.UUID,
	res GradeResult,
	answerJSON []byte,
	recLogID *int64,
) (*TaskSubmitResponse, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin submission tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Fetch task topic_id, answer_config and correct_answer for response
	var topicID int
	var correctJSON []byte
	var solutionMD *string
	var solutionCode *string

	err = tx.QueryRow(ctx, `
		SELECT topic_id, correct_answer, solution_md, solution_code
		FROM tasks
		WHERE id = $1 AND deleted_at IS NULL
	`, taskID).Scan(&topicID, &correctJSON, &solutionMD, &solutionCode)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTaskNotFound
		}
		return nil, fmt.Errorf("fetch task for submit: %w", err)
	}

	// Upsert user_tasks
	upsertQuery := `
		INSERT INTO user_tasks (
			user_id, task_id, topic_id, is_correct, best_score,
			total_attempts, attempts_since_last_solve, last_user_answer,
			first_solved_at, last_solved_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, 1,
			CASE WHEN $4 THEN 0 ELSE 1 END,
			$6,
			CASE WHEN $4 THEN now() ELSE NULL END,
			CASE WHEN $4 THEN now() ELSE NULL END,
			now()
		)
		ON CONFLICT (user_id, task_id) DO UPDATE SET
			is_correct = user_tasks.is_correct OR EXCLUDED.is_correct,
			best_score = GREATEST(user_tasks.best_score, EXCLUDED.best_score),
			total_attempts = user_tasks.total_attempts + 1,
			attempts_since_last_solve = CASE WHEN EXCLUDED.is_correct THEN 0 ELSE user_tasks.attempts_since_last_solve + 1 END,
			last_user_answer = EXCLUDED.last_user_answer,
			first_solved_at = COALESCE(user_tasks.first_solved_at, CASE WHEN EXCLUDED.is_correct THEN now() ELSE NULL END),
			last_solved_at = CASE WHEN EXCLUDED.is_correct THEN now() ELSE user_tasks.last_solved_at END,
			updated_at = now()
		RETURNING is_correct, total_attempts
	`

	var isCorrect bool
	var totalAttempts int
	err = tx.QueryRow(ctx, upsertQuery, userID, taskID, topicID, res.IsCorrect, int16(res.Score), answerJSON).
		Scan(&isCorrect, &totalAttempts)
	if err != nil {
		return nil, fmt.Errorf("upsert user_tasks: %w", err)
	}

	// Update task_recommendation_logs if requested
	if recLogID != nil && *recLogID > 0 {
		_, _ = tx.Exec(ctx, `
			UPDATE task_recommendation_logs
			SET was_solved = $1
			WHERE id = $2 AND user_id = $3
		`, isCorrect, *recLogID, userID)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit submission tx: %w", err)
	}

	canViewSolution := isCorrect || totalAttempts >= 3

	var solutionDTO *SolutionDTO
	if canViewSolution {
		var correctAns CorrectAnswer
		if len(correctJSON) > 0 {
			_ = json.Unmarshal(correctJSON, &correctAns)
		}
		solutionDTO = &SolutionDTO{
			CorrectAnswer: &correctAns,
			SolutionMD:    solutionMD,
			SolutionCode:  solutionCode,
		}
	}

	return &TaskSubmitResponse{
		IsCorrect:       res.IsCorrect,
		EarnedScore:     res.Score,
		MaxScore:        res.MaxScore,
		TotalAttempts:   totalAttempts,
		CanViewSolution: canViewSolution,
		Solution:        solutionDTO,
	}, nil
}

// CreateTask inserts a new task.
func (r *pgRepository) CreateTask(ctx context.Context, task *Task) (int, error) {
	filesJSON, err := json.Marshal(task.Files)
	if err != nil {
		filesJSON = []byte("[]")
	}
	cfgJSON, err := json.Marshal(task.AnswerConfig)
	if err != nil {
		return 0, fmt.Errorf("marshal answer_config: %w", err)
	}
	correctJSON, err := json.Marshal(task.CorrectAnswer)
	if err != nil {
		return 0, fmt.Errorf("marshal correct_answer: %w", err)
	}

	var kim16 []int16
	for _, k := range task.KimNumbers {
		kim16 = append(kim16, int16(k))
	}

	query := `
		INSERT INTO tasks (
			owner_id, is_public_bank, kim_numbers, topic_id, difficulty,
			source, text_md, images, files, answer_config, correct_answer,
			solution_md, solution_code, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, now(), now()
		) RETURNING id
	`

	var newID int
	err = r.pool.QueryRow(ctx, query,
		task.OwnerID, task.IsPublicBank, kim16, task.TopicID, int16(task.Difficulty),
		task.Source, task.TextMD, task.Images, filesJSON, cfgJSON, correctJSON,
		task.SolutionMD, task.SolutionCode,
	).Scan(&newID)
	if err != nil {
		return 0, fmt.Errorf("create task: %w", err)
	}

	return newID, nil
}

// UpdateTask modifies an existing task.
func (r *pgRepository) UpdateTask(ctx context.Context, id int, task *Task) error {
	filesJSON, err := json.Marshal(task.Files)
	if err != nil {
		filesJSON = []byte("[]")
	}
	cfgJSON, err := json.Marshal(task.AnswerConfig)
	if err != nil {
		return fmt.Errorf("marshal answer_config: %w", err)
	}
	correctJSON, err := json.Marshal(task.CorrectAnswer)
	if err != nil {
		return fmt.Errorf("marshal correct_answer: %w", err)
	}

	var kim16 []int16
	for _, k := range task.KimNumbers {
		kim16 = append(kim16, int16(k))
	}

	query := `
		UPDATE tasks
		SET
			kim_numbers = $1,
			topic_id = $2,
			difficulty = $3,
			source = $4,
			text_md = $5,
			images = $6,
			files = $7,
			answer_config = $8,
			correct_answer = $9,
			solution_md = $10,
			solution_code = $11,
			updated_at = now()
		WHERE id = $12 AND deleted_at IS NULL
	`

	cmd, err := r.pool.Exec(ctx, query,
		kim16, task.TopicID, int16(task.Difficulty),
		task.Source, task.TextMD, task.Images, filesJSON, cfgJSON, correctJSON,
		task.SolutionMD, task.SolutionCode, id,
	)
	if err != nil {
		return fmt.Errorf("update task: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return ErrTaskNotFound
	}
	return nil
}

// DeleteTask soft-deletes a task.
func (r *pgRepository) DeleteTask(ctx context.Context, id int) error {
	cmd, err := r.pool.Exec(ctx, `UPDATE tasks SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("delete task: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return ErrTaskNotFound
	}
	return nil
}

// ListAuthorTasks lists tasks created by a specific teacher.
func (r *pgRepository) ListAuthorTasks(ctx context.Context, ownerID uuid.UUID, filter ListTasksFilter) ([]TaskDTO, int, error) {
	filter.OwnerID = &ownerID
	isPublic := false
	filter.IsPublicBank = &isPublic
	return r.ListBankTasks(ctx, filter)
}

// GetTopics returns all topics grouped by KIM number.
func (r *pgRepository) GetTopics(ctx context.Context) ([]KimGroupDTO, error) {
	query := `
		SELECT id, kim_number, name, sort_order
		FROM topics
		WHERE deleted_at IS NULL
		ORDER BY kim_number ASC, sort_order ASC, name ASC
	`
	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("get topics query: %w", err)
	}
	defer rows.Close()

	groupMap := make(map[int][]TopicDTO)
	var orderedKims []int

	for rows.Next() {
		var t TopicDTO
		var kim int16
		if err := rows.Scan(&t.ID, &kim, &t.Name, &t.SortOrder); err != nil {
			return nil, fmt.Errorf("scan topic: %w", err)
		}
		t.KimNumber = int(kim)

		if _, exists := groupMap[t.KimNumber]; !exists {
			orderedKims = append(orderedKims, t.KimNumber)
		}
		groupMap[t.KimNumber] = append(groupMap[t.KimNumber], t)
	}

	var res []KimGroupDTO
	for _, kim := range orderedKims {
		res = append(res, KimGroupDTO{
			KimNumber: kim,
			Topics:    groupMap[kim],
		})
	}
	if res == nil {
		res = []KimGroupDTO{}
	}

	return res, nil
}

// CreateTopic adds a new topic.
func (r *pgRepository) CreateTopic(ctx context.Context, kimNumber int, name string, sortOrder int) (*TopicDTO, error) {
	query := `
		INSERT INTO topics (kim_number, name, sort_order, created_at, updated_at)
		VALUES ($1, $2, $3, now(), now())
		RETURNING id
	`
	var id int
	err := r.pool.QueryRow(ctx, query, int16(kimNumber), name, sortOrder).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("create topic: %w", err)
	}
	return &TopicDTO{
		ID:        id,
		KimNumber: kimNumber,
		Name:      name,
		SortOrder: sortOrder,
	}, nil
}

// UpdateTopic modifies a topic.
func (r *pgRepository) UpdateTopic(ctx context.Context, id int, name string, sortOrder int) (*TopicDTO, error) {
	query := `
		UPDATE topics
		SET name = $1, sort_order = $2, updated_at = now()
		WHERE id = $3 AND deleted_at IS NULL
		RETURNING kim_number
	`
	var kim int16
	err := r.pool.QueryRow(ctx, query, name, sortOrder, id).Scan(&kim)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTopicNotFound
		}
		return nil, fmt.Errorf("update topic: %w", err)
	}
	return &TopicDTO{
		ID:        id,
		KimNumber: int(kim),
		Name:      name,
		SortOrder: sortOrder,
	}, nil
}

// DeleteTopic soft-deletes a topic.
func (r *pgRepository) DeleteTopic(ctx context.Context, id int) error {
	cmd, err := r.pool.Exec(ctx, `UPDATE topics SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("delete topic: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return ErrTopicNotFound
	}
	return nil
}

// GetOrCreateTopic finds topic by (kim_number, name) or creates a new one.
func (r *pgRepository) GetOrCreateTopic(ctx context.Context, kimNumber int, name string) (int, error) {
	var id int
	err := r.pool.QueryRow(ctx, `
		SELECT id FROM topics WHERE kim_number = $1 AND name = $2 AND deleted_at IS NULL
	`, int16(kimNumber), name).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("check existing topic: %w", err)
	}

	err = r.pool.QueryRow(ctx, `
		INSERT INTO topics (kim_number, name, sort_order, created_at, updated_at)
		VALUES ($1, $2, 0, now(), now())
		RETURNING id
	`, int16(kimNumber), name).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert new topic: %w", err)
	}
	return id, nil
}
