package ws

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// WebSocket Message Types as defined in Contract.md §4.
const (
	// Client -> Server
	TypeStudentSessionJoin       = "student.session_join"
	TypeStudentTelemetryBatch    = "student.telemetry_batch"
	TypeTeacherWatchAssignment   = "teacher.watch_assignment"
	TypeTeacherUnwatchAssignment = "teacher.unwatch_assignment"
	TypeTeacherWatchStudentLive  = "teacher.watch_student_live"
	TypeTeacherUnwatchStudentLive = "teacher.unwatch_student_live"
	TypeStudentLiveCodeFrame     = "student.live_code_frame"

	// Server -> Client
	TypeMatrixPresencePatch     = "matrix.presence_patch"
	TypeMatrixCellPatch         = "matrix.cell_patch"
	TypeMatrixTimelineEvent     = "matrix.timeline_event"
	TypeServerLiveStreamToggle  = "server.live_stream_toggle"
	TypeTeacherStudentLiveFrame = "teacher.student_live_frame"
	TypeServerError             = "server.error"
)

// Envelope represents standard WebSocket frame envelope matching Contract.md §4:
//
//	{
//	  "type": "event_name",
//	  "ts": "2026-11-12T14:35:12.410Z",
//	  "data": {}
//	}
type Envelope struct {
	Type string          `json:"type"`
	TS   time.Time       `json:"ts"`
	Data json.RawMessage `json:"data,omitempty"`
}

// FormatMessage marshals payload into a standard JSON envelope with UTC timestamp.
func FormatMessage(msgType string, payload any) ([]byte, error) {
	var raw json.RawMessage
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		raw = b
	}
	env := Envelope{
		Type: msgType,
		TS:   time.Now().UTC(),
		Data: raw,
	}
	return json.Marshal(env)
}

// =============================================================================
// Client -> Server Payload Definitions
// =============================================================================

// StudentSessionJoinPayload represents payload for "student.session_join".
type StudentSessionJoinPayload struct {
	SessionID     uuid.UUID `json:"session_id"`
	AssignmentID  uuid.UUID `json:"assignment_id,omitempty"`
	CurrentTaskID int       `json:"current_task_id"`
}

// TelemetryEventItem represents a single telemetry event within "student.telemetry_batch".
type TelemetryEventItem struct {
	Event    string          `json:"event"`
	TaskID   *int            `json:"task_id,omitempty"`
	ClientTS time.Time       `json:"client_ts"`
	Payload  json.RawMessage `json:"payload"`
}

// StudentTelemetryBatchPayload represents payload for "student.telemetry_batch".
type StudentTelemetryBatchPayload struct {
	SessionID uuid.UUID            `json:"session_id"`
	Events    []TelemetryEventItem `json:"events"`
}

// TeacherWatchAssignmentPayload represents payload for "teacher.watch_assignment".
type TeacherWatchAssignmentPayload struct {
	AssignmentID uuid.UUID `json:"assignment_id"`
}

// TeacherUnwatchAssignmentPayload represents payload for "teacher.unwatch_assignment".
type TeacherUnwatchAssignmentPayload struct {
	AssignmentID uuid.UUID `json:"assignment_id"`
}

// TeacherWatchStudentLivePayload represents payload for "teacher.watch_student_live".
type TeacherWatchStudentLivePayload struct {
	SessionID uuid.UUID `json:"session_id"`
}

// TeacherUnwatchStudentLivePayload represents payload for "teacher.unwatch_student_live".
type TeacherUnwatchStudentLivePayload struct {
	SessionID uuid.UUID `json:"session_id"`
}

// CursorPosition represents cursor line and column in Monaco editor.
type CursorPosition struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

// StudentLiveCodeFramePayload represents payload for "student.live_code_frame".
type StudentLiveCodeFramePayload struct {
	SessionID       uuid.UUID       `json:"session_id"`
	TaskID          int             `json:"task_id"`
	IDEOpen         bool            `json:"ide_open"`
	SpreadsheetOpen bool            `json:"spreadsheet_open"`
	Code            string          `json:"code"`
	Cursor          *CursorPosition `json:"cursor,omitempty"`
	PasteRanges     [][2]int        `json:"paste_ranges,omitempty"`
	TerminalTail    string          `json:"terminal_tail,omitempty"`
}

// =============================================================================
// Server -> Client Payload Definitions
// =============================================================================

// MatrixPresencePatchPayload represents payload for "matrix.presence_patch".
type MatrixPresencePatchPayload struct {
	AssignmentID   uuid.UUID `json:"assignment_id"`
	UserID         uuid.UUID `json:"user_id"`
	SessionID      uuid.UUID `json:"session_id"`
	Online         bool      `json:"online"`
	TabFocused     bool      `json:"tab_focused"`
	CurrentTaskID  *int      `json:"current_task_id,omitempty"`
	IDEOpen        bool      `json:"ide_open"`
	BlurCount      int       `json:"blur_count"`
	PasteCount     int       `json:"paste_count"`
	LastActivityAt time.Time `json:"last_activity_at"`
}

// TaskStatSummary represents summary statistics for a task in the matrix header.
type TaskStatSummary struct {
	SolvedCount    int     `json:"solved_count"`
	AttemptedCount int     `json:"attempted_count"`
	AvgAttempts    float64 `json:"avg_attempts"`
	AvgTimeSec     int     `json:"avg_time_sec"`
}

// MatrixCellPatchPayload represents payload for "matrix.cell_patch".
type MatrixCellPatchPayload struct {
	AssignmentID        uuid.UUID        `json:"assignment_id"`
	UserID              uuid.UUID        `json:"user_id"`
	SessionID           uuid.UUID        `json:"session_id"`
	TaskID              int              `json:"task_id"`
	IsCorrect           bool             `json:"is_correct"`
	EarnedScore         int              `json:"earned_score"`
	Attempts            int              `json:"attempts"`
	TotalPrimaryScore   int              `json:"total_primary_score"`
	TotalSecondaryScore int              `json:"total_secondary_score"`
	TaskStat            *TaskStatSummary `json:"task_stat,omitempty"`
}

// TimelineEventData represents the inner event detail for "matrix.timeline_event".
type TimelineEventData struct {
	ID         int64           `json:"id,omitempty"`
	TaskID     *int            `json:"task_id,omitempty"`
	Event      string          `json:"event"`
	Payload    json.RawMessage `json:"payload"`
	OccurredAt time.Time       `json:"occurred_at"`
}

// MatrixTimelineEventPayload represents payload for "matrix.timeline_event".
type MatrixTimelineEventPayload struct {
	AssignmentID uuid.UUID         `json:"assignment_id"`
	UserID       uuid.UUID         `json:"user_id"`
	SessionID    uuid.UUID         `json:"session_id"`
	Event        TimelineEventData `json:"event"`
}

// ServerLiveStreamTogglePayload represents payload for "server.live_stream_toggle".
type ServerLiveStreamTogglePayload struct {
	SessionID  uuid.UUID `json:"session_id"`
	Enabled    bool      `json:"enabled"`
	DebounceMS int       `json:"debounce_ms,omitempty"`
}

// TeacherStudentLiveFramePayload represents payload for "teacher.student_live_frame".
type TeacherStudentLiveFramePayload struct {
	SessionID       uuid.UUID       `json:"session_id"`
	TaskID          int             `json:"task_id"`
	IDEOpen         bool            `json:"ide_open"`
	SpreadsheetOpen bool            `json:"spreadsheet_open"`
	Code            string          `json:"code"`
	Cursor          *CursorPosition `json:"cursor,omitempty"`
	PasteRanges     [][2]int        `json:"paste_ranges,omitempty"`
	TerminalTail    string          `json:"terminal_tail,omitempty"`
	FrameTS         time.Time       `json:"frame_ts"`
}

// ServerErrorPayload represents payload for "server.error".
type ServerErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
