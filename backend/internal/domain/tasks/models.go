package tasks

import (
	"time"

	"github.com/google/uuid"
)

// FileAttachment represents a file dataset attached to a task (e.g. 24.txt, 27_A.txt).
type FileAttachment struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	SizeBytes int64  `json:"size_bytes"`
	Ext       string `json:"ext"`
}

// TaskStats holds solving metrics.
type TaskStats struct {
	SolveRate        *float64 `json:"solve_rate"`
	AvgAttempts      *float64 `json:"avg_attempts"`
	AvgSolveTimeSec  *int     `json:"avg_solve_time_sec"`
	AttemptsHistogram [15]int `json:"attempts_histogram"`
	BankSolversCount int      `json:"bank_solvers_count"`
}

// UserState holds student's personal interaction status with a task.
type UserState struct {
	IsAttempted     bool `json:"is_attempted"`
	IsCorrect       bool `json:"is_correct"`
	EarnedScore     int  `json:"earned_score"`
	TotalAttempts   int  `json:"total_attempts"`
	LastUserAnswer  any  `json:"last_user_answer"`
	CanViewSolution bool `json:"can_view_solution"`
}

// SolutionDTO holds answer explanation and code.
type SolutionDTO struct {
	CorrectAnswer *CorrectAnswer `json:"correct_answer,omitempty"`
	SolutionMD    *string        `json:"solution_md,omitempty"`
	SolutionCode  *string        `json:"solution_code,omitempty"`
}

// TopicDTO represents a topic within a KIM assignment.
type TopicDTO struct {
	ID        int    `json:"id"`
	KimNumber int    `json:"kim_number"`
	Name      string `json:"name"`
	SortOrder int    `json:"sort_order"`
}

// KimGroupDTO groups topics under a single KIM number.
type KimGroupDTO struct {
	KimNumber int        `json:"kim_number"`
	Topics    []TopicDTO `json:"topics"`
}

// Task is the database entity representation.
type Task struct {
	ID                int
	OwnerID           *uuid.UUID
	IsPublicBank      bool
	KimNumbers        []int
	TopicID           int
	Difficulty        int
	Source            *string
	TextMD            string
	Images            []string
	Files             []FileAttachment
	AnswerConfig      AnswerConfig
	CorrectAnswer     CorrectAnswer
	SolutionMD        *string
	SolutionCode      *string
	AvgSolveTime      *int
	SolveRate         *float64
	AvgAttempts       *float64
	AttemptsHistogram [15]int
	BankStatsCount    int
	TestStatsCount    int
	CreatedAt         time.Time
	UpdatedAt         time.Time
	DeletedAt         *time.Time
}

// TaskDTO represents the public API task card according to Contract.md §2.3.
type TaskDTO struct {
	ID           int              `json:"id"`
	OwnerID      *uuid.UUID       `json:"owner_id"`
	IsPublicBank bool             `json:"is_public_bank"`
	KimNumbers   []int            `json:"kim_numbers"`
	Topic        TopicDTO         `json:"topic"`
	Difficulty   int              `json:"difficulty"`
	Source       *string          `json:"source"`
	TextMD       string           `json:"text_md"`
	Images       []string         `json:"images"`
	Files        []FileAttachment `json:"files"`
	AnswerConfig AnswerConfig     `json:"answer_config"`
	Stats        TaskStats        `json:"stats"`
	UserState    *UserState       `json:"user_state,omitempty"`
	Solution     *SolutionDTO     `json:"solution"`
	CreatedAt    string           `json:"created_at"`
}

// ListTasksFilter defines search and pagination parameters.
type ListTasksFilter struct {
	Query        string     // q (GIN trigram on text_md or #id)
	Kim          *int       // kim number 1..27
	TopicID      *int       // topic_id
	Difficulty   *int       // 1..4
	Status       string     // "all", "solved", "unsolved", "attempted"
	Sort         string     // "newest", "oldest", "solve_rate_asc", "solve_rate_desc"
	Limit        int        // default 20
	Offset       int        // default 0
	UserID       *uuid.UUID // caller user id (for status & active exam filter)
	OwnerID      *uuid.UUID // for author tasks
	IsPublicBank *bool      // true for public bank
}

// TaskSubmitRequest is the request body for POST /bank/tasks/{id}/submit.
type TaskSubmitRequest struct {
	UserAnswer           UserAnswer `json:"user_answer"`
	RecommendationLogID *int64     `json:"recommendation_log_id,omitempty"`
}

// TaskSubmitResponse is the response body for POST /bank/tasks/{id}/submit.
type TaskSubmitResponse struct {
	IsCorrect       bool         `json:"is_correct"`
	EarnedScore     int          `json:"earned_score"`
	MaxScore        int          `json:"max_score"`
	TotalAttempts   int          `json:"total_attempts"`
	CanViewSolution bool         `json:"can_view_solution"`
	Solution        *SolutionDTO `json:"solution,omitempty"`
}

// ImportRowError details an issue in a CSV/ZIP task row.
type ImportRowError struct {
	RowNumber    int    `json:"row_number"`
	Column       string `json:"column"`
	ErrorMessage string `json:"error_message"`
}

// ImportReportDTO describes the outcome of CSV/ZIP task import.
type ImportReportDTO struct {
	DryRun       bool             `json:"dry_run"`
	TotalRows    int              `json:"total_rows"`
	ValidCount   int              `json:"valid_count"`
	ErrorCount   int              `json:"error_count"`
	Errors       []ImportRowError `json:"errors"`
	PreviewTasks []TaskDTO        `json:"preview_tasks,omitempty"`
}
