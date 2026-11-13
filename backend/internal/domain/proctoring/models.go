package proctoring

import (
	"encoding/json"

	"backend/internal/domain/tasks"
	"github.com/google/uuid"
)

// MatrixSnapshotDTO is returned by GET /api/v1/assignments/{id}/matrix.
type MatrixSnapshotDTO struct {
	AssignmentID uuid.UUID           `json:"assignment_id"`
	RoomID       *uuid.UUID          `json:"room_id"`
	Title        string              `json:"title"`
	Mode         string              `json:"mode"`
	Students     []MatrixStudentDTO  `json:"students"`
	TaskStats    []MatrixTaskStatDTO `json:"task_stats"`
}

// MatrixStudentDTO represents a student row in the teacher live matrix.
type MatrixStudentDTO struct {
	UserID         uuid.UUID                       `json:"user_id"`
	Username       string                          `json:"username"`
	FirstName      string                          `json:"first_name"`
	LastName       string                          `json:"last_name"`
	SessionID      *uuid.UUID                      `json:"session_id"`
	Status         *string                         `json:"status"`
	IsOnline       bool                            `json:"is_online"`
	TabFocused     bool                            `json:"tab_focused"`
	CurrentTaskID  *int                            `json:"current_task_id"`
	BlurCount      int                             `json:"blur_count"`
	PasteCount     int                             `json:"paste_count"`
	PrimaryScore   int16                           `json:"primary_score"`
	SecondaryScore *int16                          `json:"secondary_score"`
	LastActivityAt *string                         `json:"last_activity_at"`
	Tasks          map[string]MatrixStudentTaskDTO `json:"tasks"`
}

// MatrixStudentTaskDTO represents one cell in the student's task matrix.
type MatrixStudentTaskDTO struct {
	TaskID       int   `json:"task_id"`
	Position     int   `json:"position"`
	IsCorrect    bool  `json:"is_correct"`
	EarnedScore  int16 `json:"earned_score"`
	AttemptsUsed int   `json:"attempts_used"`
}

// MatrixTaskStatDTO represents column aggregations for a task in the matrix.
type MatrixTaskStatDTO struct {
	TaskID         int     `json:"task_id"`
	Position       int     `json:"position"`
	SolvedCount    int     `json:"solved_count"`
	AttemptedCount int     `json:"attempted_count"`
	AvgAttempts    float64 `json:"avg_attempts"`
	AvgTimeSec     int     `json:"avg_time_sec"`
}

// SessionInspectionDTO is returned by GET /api/v1/sessions/{id}/inspect.
type SessionInspectionDTO struct {
	SessionID      uuid.UUID             `json:"session_id"`
	AssignmentID   uuid.UUID             `json:"assignment_id"`
	UserID         uuid.UUID             `json:"user_id"`
	UserName       string                `json:"user_name"`
	Status         string                `json:"status"`
	StartedAt      string                `json:"started_at"`
	FinishedAt     *string               `json:"finished_at"`
	PrimaryScore   int16                 `json:"primary_score"`
	SecondaryScore *int16                `json:"secondary_score"`
	BlurCount      int                   `json:"blur_count"`
	PasteCount     int                   `json:"paste_count"`
	Events         []TimelineEventDTO    `json:"events"`
	TaskCodes      []TaskCodeSnapshotDTO `json:"task_codes"`
}

// TimelineEventDTO represents one audit or telemetric event from user_task_events.
type TimelineEventDTO struct {
	ID         int64           `json:"id"`
	TaskID     *int            `json:"task_id"`
	Event      string          `json:"event"`
	Payload    json.RawMessage `json:"payload"`
	OccurredAt string          `json:"occurred_at"`
}

// TaskCodeSnapshotDTO holds student's saved python code and paste ranges for a task.
type TaskCodeSnapshotDTO struct {
	TaskID         int     `json:"task_id"`
	Code           string  `json:"code"`
	PasteRanges    any     `json:"paste_ranges"`
	StudentComment *string `json:"student_comment"`
	UpdatedAt      string  `json:"updated_at"`
}

// CreateTeacherTaskRequest is the body for POST /api/v1/teacher/tasks.
type CreateTeacherTaskRequest struct {
	KimNumbers    []int                  `json:"kim_numbers"`
	TopicID       int                    `json:"topic_id"`
	Difficulty    int                    `json:"difficulty"`
	Source        string                 `json:"source"`
	TextMD        string                 `json:"text_md"`
	Images        []string               `json:"images"`
	Files         []tasks.FileAttachment `json:"files"`
	AnswerConfig  tasks.AnswerConfig     `json:"answer_config"`
	CorrectAnswer tasks.CorrectAnswer    `json:"correct_answer"`
	SolutionMD    *string                `json:"solution_md"`
	SolutionCode  *string                `json:"solution_code"`
}
