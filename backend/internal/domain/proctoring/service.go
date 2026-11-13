package proctoring

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"backend/internal/domain/assignments"
	"backend/internal/domain/tasks"
	"github.com/google/uuid"
)

// Service defines proctoring analytics and teacher author task operations.
type Service interface {
	GetMatrixSnapshot(ctx context.Context, teacherID, assignmentID uuid.UUID) (*MatrixSnapshotDTO, error)
	GetSessionDossier(ctx context.Context, teacherID, sessionID uuid.UUID) (*SessionInspectionDTO, error)
	CreateTeacherTask(ctx context.Context, teacherID uuid.UUID, req CreateTeacherTaskRequest) (*tasks.TaskDTO, error)
	ListTeacherTasks(ctx context.Context, teacherID uuid.UUID, limit, offset int, kim *int, topicID *int, q string) ([]tasks.TaskDTO, int, error)
}

// ProctoringService implements Service.
type ProctoringService struct {
	repo    Repository
	storage assignments.CodeStorage
}

// NewService constructs a ProctoringService.
func NewService(repo Repository, storage assignments.CodeStorage) *ProctoringService {
	return &ProctoringService{
		repo:    repo,
		storage: storage,
	}
}

// GetMatrixSnapshot returns class live matrix snapshot for assignment.
// Accessible even if teacher subscription has expired (graceful read-only mode).
func (s *ProctoringService) GetMatrixSnapshot(ctx context.Context, teacherID, assignmentID uuid.UUID) (*MatrixSnapshotDTO, error) {
	header, err := s.repo.GetAssignmentHeader(ctx, assignmentID)
	if err != nil {
		return nil, err
	}
	if header.CreatorID != teacherID {
		return nil, ErrForbidden
	}

	rows, err := s.repo.GetMatrixStudents(ctx, assignmentID, header.RoomID)
	if err != nil {
		return nil, fmt.Errorf("get matrix students: %w", err)
	}

	stats, err := s.repo.GetMatrixTaskStats(ctx, assignmentID)
	if err != nil {
		return nil, fmt.Errorf("get matrix task stats: %w", err)
	}

	studentMap := make(map[uuid.UUID]*MatrixStudentDTO)
	var orderedStudentIDs []uuid.UUID

	for _, r := range rows {
		student, exists := studentMap[r.UserID]
		if !exists {
			var blurCount, pasteCount int
			if r.BlurCount != nil {
				blurCount = *r.BlurCount
			}
			if r.PasteCount != nil {
				pasteCount = *r.PasteCount
			}

			var primaryScore int16
			if r.PrimaryScore != nil {
				primaryScore = *r.PrimaryScore
			}

			student = &MatrixStudentDTO{
				UserID:         r.UserID,
				Username:       r.Username,
				FirstName:      r.FirstName,
				LastName:       r.LastName,
				SessionID:      r.SessionID,
				Status:         r.Status,
				IsOnline:       false,
				TabFocused:     true,
				CurrentTaskID:  nil,
				BlurCount:      blurCount,
				PasteCount:     pasteCount,
				PrimaryScore:   primaryScore,
				SecondaryScore: r.SecondaryScore,
				LastActivityAt: nil,
				Tasks:          make(map[string]MatrixStudentTaskDTO),
			}
			studentMap[r.UserID] = student
			orderedStudentIDs = append(orderedStudentIDs, r.UserID)
		}

		if r.TaskID != nil && r.Position != nil {
			var earned int16
			if r.EarnedScore != nil {
				earned = *r.EarnedScore
			}
			var attempts int
			if r.AttemptsUsed != nil {
				attempts = *r.AttemptsUsed
			}
			var isCorr bool
			if r.IsCorrect != nil {
				isCorr = *r.IsCorrect
			}

			taskKey := strconv.Itoa(*r.TaskID)
			student.Tasks[taskKey] = MatrixStudentTaskDTO{
				TaskID:       *r.TaskID,
				Position:     int(*r.Position),
				IsCorrect:    isCorr,
				EarnedScore:  earned,
				AttemptsUsed: attempts,
			}

			// Current task is the last non-empty position
			taskIDVal := *r.TaskID
			student.CurrentTaskID = &taskIDVal
		}
	}

	var students []MatrixStudentDTO
	for _, id := range orderedStudentIDs {
		students = append(students, *studentMap[id])
	}
	if students == nil {
		students = []MatrixStudentDTO{}
	}
	if stats == nil {
		stats = []MatrixTaskStatDTO{}
	}

	return &MatrixSnapshotDTO{
		AssignmentID: header.ID,
		RoomID:       header.RoomID,
		Title:        header.Title,
		Mode:         header.Mode,
		Students:     students,
		TaskStats:    stats,
	}, nil
}

// GetSessionDossier returns chronological telemetry timeline events, paste ranges, and S3 student code snapshots.
func (s *ProctoringService) GetSessionDossier(ctx context.Context, teacherID, sessionID uuid.UUID) (*SessionInspectionDTO, error) {
	row, err := s.repo.GetInspectionSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if row.CreatorID != teacherID {
		return nil, ErrForbidden
	}

	events, err := s.repo.GetTimelineEvents(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("get timeline events: %w", err)
	}

	codeRows, err := s.repo.GetSessionTaskCodes(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("get session task codes: %w", err)
	}

	var taskCodes []TaskCodeSnapshotDTO
	for _, cr := range codeRows {
		var codeContent string
		if cr.CodeS3Key != nil && s.storage != nil {
			codeContent, _ = s.storage.GetCode(ctx, *cr.CodeS3Key)
		}

		var pasteRanges any
		if len(cr.PasteRanges) > 0 {
			_ = json.Unmarshal(cr.PasteRanges, &pasteRanges)
		}
		if pasteRanges == nil {
			pasteRanges = [][]int{}
		}

		taskCodes = append(taskCodes, TaskCodeSnapshotDTO{
			TaskID:         cr.TaskID,
			Code:           codeContent,
			PasteRanges:    pasteRanges,
			StudentComment: cr.StudentComment,
			UpdatedAt:      cr.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}

	var finishedAtStr *string
	if row.FinishedAt != nil {
		f := row.FinishedAt.UTC().Format(time.RFC3339)
		finishedAtStr = &f
	}

	if events == nil {
		events = []TimelineEventDTO{}
	}
	if taskCodes == nil {
		taskCodes = []TaskCodeSnapshotDTO{}
	}

	return &SessionInspectionDTO{
		SessionID:      row.SessionID,
		AssignmentID:   row.AssignmentID,
		UserID:         row.UserID,
		UserName:       row.UserName,
		Status:         row.Status,
		StartedAt:      row.StartedAt.UTC().Format(time.RFC3339),
		FinishedAt:     finishedAtStr,
		PrimaryScore:   row.PrimaryScore,
		SecondaryScore: row.SecondaryScore,
		BlurCount:      row.BlurCount,
		PasteCount:     row.PasteCount,
		Events:         events,
		TaskCodes:      taskCodes,
	}, nil
}

// CreateTeacherTask creates private task belonging to teacher.
func (s *ProctoringService) CreateTeacherTask(ctx context.Context, teacherID uuid.UUID, req CreateTeacherTaskRequest) (*tasks.TaskDTO, error) {
	if len(req.KimNumbers) == 0 {
		return nil, fmt.Errorf("kim_numbers cannot be empty")
	}
	if req.TextMD == "" {
		return nil, fmt.Errorf("text_md cannot be empty")
	}
	if req.Difficulty < 1 || req.Difficulty > 4 {
		req.Difficulty = 2
	}
	return s.repo.CreateTeacherTask(ctx, teacherID, req)
}

// ListTeacherTasks returns author tasks created by teacher.
func (s *ProctoringService) ListTeacherTasks(ctx context.Context, teacherID uuid.UUID, limit, offset int, kim *int, topicID *int, q string) ([]tasks.TaskDTO, int, error) {
	return s.repo.ListTeacherTasks(ctx, teacherID, limit, offset, kim, topicID, q)
}
