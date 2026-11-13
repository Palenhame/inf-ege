package assignments

import (
	"encoding/json"
	"time"

	"backend/internal/domain/tasks"
	"github.com/google/uuid"
)

// AssignmentMode represents the mode of an assignment.
type AssignmentMode string

const (
	ModeHomework      AssignmentMode = "homework"
	ModeClasswork     AssignmentMode = "classwork"
	ModePractice      AssignmentMode = "practice"
	ModeProctoredTest AssignmentMode = "proctored_test"
	ModeMockExam      AssignmentMode = "mock_exam"
)

// SessionStatus represents the state of a student's session.
type SessionStatus string

const (
	SessionInProgress SessionStatus = "in_progress"
	SessionFinished   SessionStatus = "finished"
	SessionExpired    SessionStatus = "expired"
)

// RevealPolicy defines when task solutions and scores become visible.
type RevealPolicy string

const (
	RevealImmediate     RevealPolicy = "immediate"
	RevealAfterSubmit   RevealPolicy = "after_submit"
	RevealAfterDeadline RevealPolicy = "after_deadline"
	RevealManual        RevealPolicy = "manual"
)

// AssignmentSettings holds configuration parameters for test execution.
type AssignmentSettings struct {
	MaxAttempts        int             `json:"max_attempts"`
	DurationMinutes    *int            `json:"duration_minutes"`
	StartsAt           *time.Time      `json:"starts_at"`
	DeadlineAt         *time.Time      `json:"deadline_at"`
	AllowAfterDeadline bool            `json:"allow_after_deadline"`
	RevealPolicy       RevealPolicy    `json:"reveal_policy"`
	ShowSecondaryScore bool            `json:"show_secondary_score"`
	ProctoringEnabled  bool            `json:"proctoring_enabled"`
	ToolsAllowed       map[string]bool `json:"tools_allowed"`
}

// Assignment represents an assignment database record.
type Assignment struct {
	ID              uuid.UUID          `json:"id"`
	RoomID          *uuid.UUID         `json:"room_id"`
	CreatorID       uuid.UUID          `json:"creator_id"`
	Title           string             `json:"title"`
	Mode            AssignmentMode     `json:"mode"`
	Settings        AssignmentSettings `json:"settings"`
	ResultsReleased bool               `json:"results_released"`
	CreatedAt       time.Time          `json:"created_at"`
	UpdatedAt       time.Time          `json:"updated_at"`
	DeletedAt       *time.Time         `json:"deleted_at,omitempty"`
	TasksCount      int                `json:"tasks_count,omitempty"`
}

// AssignmentDTO is the external representation of an assignment.
type AssignmentDTO struct {
	ID              uuid.UUID          `json:"id"`
	RoomID          *uuid.UUID         `json:"room_id,omitempty"`
	Title           string             `json:"title"`
	Mode            AssignmentMode     `json:"mode"`
	Settings        AssignmentSettings `json:"settings"`
	ResultsReleased bool               `json:"results_released"`
	TasksCount      int                `json:"tasks_count"`
	CreatedAt       string             `json:"created_at"`
}

// AssignmentTask maps a task into an assignment with specific position ordering.
type AssignmentTask struct {
	AssignmentID uuid.UUID `json:"assignment_id"`
	TaskID       int       `json:"task_id"`
	Position     int16     `json:"position"`
}

// AssignmentSession represents an active or completed attempt on an assignment.
type AssignmentSession struct {
	ID             uuid.UUID     `json:"id"`
	AssignmentID   uuid.UUID     `json:"assignment_id"`
	UserID         uuid.UUID     `json:"user_id"`
	Status         SessionStatus `json:"status"`
	StartedAt      time.Time     `json:"started_at"`
	ExpiresAt      *time.Time    `json:"expires_at"`
	FinishedAt     *time.Time    `json:"finished_at"`
	PrimaryScore   int16         `json:"primary_score"`
	SecondaryScore *int16        `json:"secondary_score"`
	BlurCount      int           `json:"blur_count"`
	PasteCount     int           `json:"paste_count"`
}

// UserAssignmentTask stores student answers and grading result for one task in a session.
type UserAssignmentTask struct {
	SessionID      uuid.UUID       `json:"session_id"`
	AssignmentID   uuid.UUID       `json:"assignment_id"`
	UserID         uuid.UUID       `json:"user_id"`
	TaskID         int             `json:"task_id"`
	TopicID        int             `json:"topic_id"`
	IsCorrect      bool            `json:"is_correct"`
	EarnedScore    int16           `json:"earned_score"`
	AttemptsUsed   int             `json:"attempts_used"`
	UserAnswer     json.RawMessage `json:"user_answer"`
	TimeSpentSec   int             `json:"time_spent_sec"`
	CodeS3Key      *string         `json:"code_s3_key,omitempty"`
	PasteRanges    json.RawMessage `json:"paste_ranges"`
	StudentComment *string         `json:"student_comment,omitempty"`
	UpdatedAt      time.Time       `json:"updated_at"`
	CreatedAt      time.Time       `json:"created_at"`
}

// AssignmentTaskStat holds aggregated metrics for a task in an assignment.
type AssignmentTaskStat struct {
	AssignmentID   uuid.UUID `json:"assignment_id"`
	TaskID         int       `json:"task_id"`
	Position       int       `json:"position,omitempty"`
	AvgTimeSec     int       `json:"avg_time_sec"`
	SolvedCount    int       `json:"solved_count"`
	AttemptedCount int       `json:"attempted_count"`
	AvgAttempts    float64   `json:"avg_attempts"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// ExamScoreScale maps primary KEGE score (0..29) to secondary 100-point scale.
type ExamScoreScale struct {
	PrimaryScore   int16 `json:"primary_score"`
	SecondaryScore int16 `json:"secondary_score"`
}

// ExamScaleEntry represents a single mapping in the primary-to-secondary KEGE score scale.
type ExamScaleEntry struct {
	PrimaryScore   int16 `json:"primary_score"`
	SecondaryScore int16 `json:"secondary_score"`
}

// Request & Response DTOs

// CreateAssignmentRequest is the body for POST /rooms/{room_id}/assignments.
type CreateAssignmentRequest struct {
	Title    string             `json:"title"`
	Mode     AssignmentMode     `json:"mode"`
	Settings AssignmentSettings `json:"settings"`
	TaskIDs  []int              `json:"task_ids"`
}

// UpdateAssignmentRequest is the body for PATCH /assignments/{id}.
type UpdateAssignmentRequest struct {
	Title           *string             `json:"title,omitempty"`
	Settings        *AssignmentSettings `json:"settings,omitempty"`
	ResultsReleased *bool               `json:"results_released,omitempty"`
}

// SelfStartRequest is the optional body for POST /api/v1/exams/self-start.
type SelfStartRequest struct {
	PresetID *int `json:"preset_id,omitempty"`
}

// SelfStartResponse is returned by POST /api/v1/exams/self-start.
type SelfStartResponse struct {
	SessionID    uuid.UUID `json:"session_id"`
	AssignmentID uuid.UUID `json:"assignment_id"`
	ExpiresAt    string    `json:"expires_at"`
}

// StartSessionResponse is returned by POST /assignments/{id}/start.
type StartSessionResponse struct {
	SessionID    uuid.UUID     `json:"session_id"`
	AssignmentID uuid.UUID     `json:"assignment_id"`
	Status       SessionStatus `json:"status"`
	StartedAt    string        `json:"started_at"`
	ExpiresAt    *string       `json:"expires_at"`
}

// SessionTaskDTO represents a single task card inside a session response.
type SessionTaskDTO struct {
	Position       int             `json:"position"`
	Task           SessionTaskCard `json:"task"`
	UserAnswer     any             `json:"user_answer"`
	IsCorrect      *bool           `json:"is_correct"`
	EarnedScore    *int16          `json:"earned_score"`
	AttemptsUsed   int             `json:"attempts_used"`
	StudentComment *string         `json:"student_comment"`
}

// SessionTaskCard represents the task payload with anti-cheat masking applied.
type SessionTaskCard struct {
	ID           int                    `json:"id"`
	KimNumbers   []int                  `json:"kim_numbers"`
	Topic        *tasks.TopicDTO        `json:"topic,omitempty"`
	Difficulty   int                    `json:"difficulty"`
	TextMD       string                 `json:"text_md"`
	Images       []string               `json:"images"`
	Files        []tasks.FileAttachment `json:"files"`
	AnswerConfig tasks.AnswerConfig     `json:"answer_config"`
	Source       *string                `json:"source"`
	Solution     *tasks.SolutionDTO     `json:"solution"`
}

// SessionDetailDTO is returned by GET /sessions/{session_id}.
type SessionDetailDTO struct {
	ID             uuid.UUID          `json:"id"`
	AssignmentID   uuid.UUID          `json:"assignment_id"`
	UserID         uuid.UUID          `json:"user_id"`
	Status         SessionStatus      `json:"status"`
	ServerNow      string             `json:"server_now"`
	StartedAt      string             `json:"started_at"`
	ExpiresAt      *string            `json:"expires_at"`
	FinishedAt     *string            `json:"finished_at"`
	PrimaryScore   int16              `json:"primary_score"`
	SecondaryScore *int16             `json:"secondary_score"`
	Settings       AssignmentSettings `json:"settings"`
	Tasks          []SessionTaskDTO   `json:"tasks"`
}

// AnswerTaskRequest is the body for POST /sessions/{id}/tasks/{task_id}/answer.
type AnswerTaskRequest struct {
	UserAnswer     json.RawMessage `json:"user_answer"`
	Code           string          `json:"code,omitempty"`
	PasteRanges    [][]int         `json:"paste_ranges,omitempty"`
	StudentComment *string         `json:"student_comment,omitempty"`
}

// AnswerTaskResponse is returned by POST /sessions/{id}/tasks/{task_id}/answer.
type AnswerTaskResponse struct {
	IsCorrect    *bool        `json:"is_correct"`
	EarnedScore  *int         `json:"earned_score"`
	MaxScore     int          `json:"max_score"`
	AttemptsUsed int          `json:"attempts_used"`
	MaxAttempts  int          `json:"max_attempts"`
	RevealPolicy RevealPolicy `json:"reveal_policy"`
}

// ProtocolTaskDTO represents a task outcome in the final session finish protocol.
type ProtocolTaskDTO struct {
	TaskID       int                `json:"task_id"`
	Position     int                `json:"position"`
	IsCorrect    bool               `json:"is_correct"`
	EarnedScore  int16              `json:"earned_score"`
	MaxScore     int                `json:"max_score"`
	AttemptsUsed int                `json:"attempts_used"`
	Solution     *tasks.SolutionDTO `json:"solution,omitempty"`
}

// SessionProtocolDTO represents the finish protocol according to reveal policy.
type SessionProtocolDTO struct {
	RevealPolicy    RevealPolicy      `json:"reveal_policy"`
	ResultsReleased bool              `json:"results_released"`
	CanViewResults  bool              `json:"can_view_results"`
	Tasks           []ProtocolTaskDTO `json:"tasks,omitempty"`
}

// SessionFinishDTO is returned by POST /sessions/{id}/finish.
type SessionFinishDTO struct {
	SessionID      uuid.UUID          `json:"session_id"`
	Status         SessionStatus      `json:"status"`
	FinishedAt     string             `json:"finished_at"`
	PrimaryScore   int16              `json:"primary_score"`
	SecondaryScore int16              `json:"secondary_score"`
	Protocol       SessionProtocolDTO `json:"protocol"`
}
