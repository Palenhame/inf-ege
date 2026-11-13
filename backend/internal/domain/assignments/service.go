package assignments

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"backend/internal/domain/tasks"
	"github.com/google/uuid"
)

// Service defines the assignment business operations.
type Service interface {
	CreateAssignment(ctx context.Context, creatorID, roomID uuid.UUID, req CreateAssignmentRequest) (*AssignmentDTO, error)
	UpdateAssignment(ctx context.Context, callerID, assignmentID uuid.UUID, req UpdateAssignmentRequest) (*AssignmentDTO, error)
	DeleteAssignment(ctx context.Context, callerID, assignmentID uuid.UUID) error

	SelfStartExam(ctx context.Context, userID uuid.UUID, req SelfStartRequest) (*SelfStartResponse, error)
	StartSession(ctx context.Context, userID, assignmentID uuid.UUID) (*StartSessionResponse, error)
	GetSession(ctx context.Context, callerID, sessionID uuid.UUID) (*SessionDetailDTO, error)
	AnswerTask(ctx context.Context, userID, sessionID uuid.UUID, taskID int, req AnswerTaskRequest) (*AnswerTaskResponse, error)
	FinishSession(ctx context.Context, userID, sessionID uuid.UUID) (*SessionFinishDTO, error)

	GetExamScale(ctx context.Context) ([]ExamScaleEntry, error)
	UpdateExamScale(ctx context.Context, entries []ExamScaleEntry) error
}

// AssignmentService implements Service.
type AssignmentService struct {
	repo    Repository
	storage CodeStorage
}

// NewService constructs an AssignmentService.
func NewService(repo Repository, storage CodeStorage) *AssignmentService {
	return &AssignmentService{
		repo:    repo,
		storage: storage,
	}
}

// CreateAssignment validates parameters, verifies creator ownership of the room, and persists the assignment.
func (s *AssignmentService) CreateAssignment(ctx context.Context, creatorID, roomID uuid.UUID, req CreateAssignmentRequest) (*AssignmentDTO, error) {
	if req.Title == "" {
		return nil, fmt.Errorf("title cannot be empty")
	}
	if len(req.TaskIDs) == 0 {
		return nil, fmt.Errorf("task_ids cannot be empty")
	}

	validModes := map[AssignmentMode]bool{
		ModeHomework:      true,
		ModeClasswork:     true,
		ModePractice:      true,
		ModeProctoredTest: true,
		ModeMockExam:      true,
	}
	if !validModes[req.Mode] {
		req.Mode = ModeHomework
	}

	// Verify room ownership
	if roomID != uuid.Nil {
		isCreator, err := s.repo.CheckRoomCreator(ctx, roomID, creatorID)
		if err != nil {
			return nil, fmt.Errorf("check room creator: %w", err)
		}
		if !isCreator {
			return nil, ErrForbidden
		}
	}

	assignment := &Assignment{
		ID:              uuid.New(),
		RoomID:          &roomID,
		CreatorID:       creatorID,
		Title:           req.Title,
		Mode:            req.Mode,
		Settings:        req.Settings,
		ResultsReleased: false,
	}

	if err := s.repo.CreateAssignment(ctx, assignment, req.TaskIDs); err != nil {
		return nil, fmt.Errorf("create assignment: %w", err)
	}

	return toAssignmentDTO(assignment), nil
}

// UpdateAssignment applies partial updates to assignment header.
func (s *AssignmentService) UpdateAssignment(ctx context.Context, callerID, assignmentID uuid.UUID, req UpdateAssignmentRequest) (*AssignmentDTO, error) {
	a, err := s.repo.GetAssignmentByID(ctx, assignmentID)
	if err != nil {
		return nil, err
	}
	if a.CreatorID != callerID {
		return nil, ErrForbidden
	}

	updated, err := s.repo.UpdateAssignment(ctx, assignmentID, req.Title, req.Settings, req.ResultsReleased)
	if err != nil {
		return nil, err
	}
	return toAssignmentDTO(updated), nil
}

// DeleteAssignment soft-deletes an assignment.
func (s *AssignmentService) DeleteAssignment(ctx context.Context, callerID, assignmentID uuid.UUID) error {
	a, err := s.repo.GetAssignmentByID(ctx, assignmentID)
	if err != nil {
		return err
	}
	if a.CreatorID != callerID {
		return ErrForbidden
	}
	return s.repo.DeleteAssignment(ctx, assignmentID)
}

// SelfStartExam initiates a standalone 27-KIM full simulation (25 cards, 29 points, 235 min).
func (s *AssignmentService) SelfStartExam(ctx context.Context, userID uuid.UUID, req SelfStartRequest) (*SelfStartResponse, error) {
	taskIDs, err := s.repo.SelectSelfExamTasks(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("generate self exam tasks: %w", err)
	}
	if len(taskIDs) == 0 {
		return nil, fmt.Errorf("no tasks available for self-mock exam generation")
	}

	duration := 235
	now := time.Now().UTC()
	expiresAt := now.Add(time.Duration(duration) * time.Minute)

	assignment := &Assignment{
		ID:        uuid.New(),
		RoomID:    nil,
		CreatorID: userID,
		Title:     "Самостоятельный пробник КЕГЭ",
		Mode:      ModeMockExam,
		Settings: AssignmentSettings{
			MaxAttempts:        1,
			DurationMinutes:    &duration,
			RevealPolicy:       RevealAfterSubmit,
			ShowSecondaryScore: true,
			ProctoringEnabled:  false,
			ToolsAllowed: map[string]bool{
				"ide":         true,
				"spreadsheet": true,
				"drawing":     true,
			},
		},
		ResultsReleased: false,
	}

	if err := s.repo.CreateAssignment(ctx, assignment, taskIDs); err != nil {
		return nil, fmt.Errorf("create self exam assignment: %w", err)
	}

	session := &AssignmentSession{
		ID:           uuid.New(),
		AssignmentID: assignment.ID,
		UserID:       userID,
		Status:       SessionInProgress,
		StartedAt:    now,
		ExpiresAt:    &expiresAt,
		PrimaryScore: 0,
		BlurCount:    0,
		PasteCount:   0,
	}

	if err := s.repo.CreateSession(ctx, session); err != nil {
		return nil, fmt.Errorf("create self exam session: %w", err)
	}

	_ = s.repo.LogTaskEvent(ctx, session.ID, assignment.ID, userID, nil, "session_started", map[string]any{
		"mode": assignment.Mode,
	})

	return &SelfStartResponse{
		SessionID:    session.ID,
		AssignmentID: assignment.ID,
		ExpiresAt:    expiresAt.Format(time.RFC3339),
	}, nil
}

// StartSession creates or resumes a student's session for an assignment.
func (s *AssignmentService) StartSession(ctx context.Context, userID, assignmentID uuid.UUID) (*StartSessionResponse, error) {
	a, err := s.repo.GetAssignmentByID(ctx, assignmentID)
	if err != nil {
		return nil, err
	}

	// Verify room membership if assignment is inside a room
	if a.RoomID != nil && *a.RoomID != uuid.Nil {
		isMember, err := s.repo.CheckRoomMembership(ctx, *a.RoomID, userID)
		if err != nil {
			return nil, fmt.Errorf("check membership: %w", err)
		}
		if !isMember {
			isCreator, _ := s.repo.CheckRoomCreator(ctx, *a.RoomID, userID)
			if !isCreator {
				return nil, ErrForbidden
			}
		}
	}

	now := time.Now().UTC()

	// Check assignment deadline
	if a.Settings.DeadlineAt != nil && now.After(*a.Settings.DeadlineAt) && !a.Settings.AllowAfterDeadline {
		return nil, ErrDeadlinePassed
	}

	// Check if student already has a session
	existingSession, err := s.repo.GetSessionByAssignmentAndUser(ctx, assignmentID, userID)
	if err == nil && existingSession != nil {
		if existingSession.Status == SessionFinished || existingSession.Status == SessionExpired {
			return nil, ErrSessionFinished
		}

		// Check if timer expired while session was in progress
		if existingSession.ExpiresAt != nil && now.After(*existingSession.ExpiresAt) {
			_ = s.repo.ExpireSession(ctx, existingSession.ID, existingSession.PrimaryScore, 0)
			return nil, ErrSessionFinished
		}

		// Resume existing in-progress session
		var expiresStr *string
		if existingSession.ExpiresAt != nil {
			f := existingSession.ExpiresAt.Format(time.RFC3339)
			expiresStr = &f
		}
		return &StartSessionResponse{
			SessionID:    existingSession.ID,
			AssignmentID: existingSession.AssignmentID,
			Status:       existingSession.Status,
			StartedAt:    existingSession.StartedAt.Format(time.RFC3339),
			ExpiresAt:    expiresStr,
		}, nil
	}

	// Calculate expiration timestamp
	var expiresAt *time.Time
	if a.Settings.DurationMinutes != nil && *a.Settings.DurationMinutes > 0 {
		exp := now.Add(time.Duration(*a.Settings.DurationMinutes) * time.Minute)
		expiresAt = &exp
	}
	if a.Settings.DeadlineAt != nil && !a.Settings.AllowAfterDeadline {
		if expiresAt == nil || expiresAt.After(*a.Settings.DeadlineAt) {
			expiresAt = a.Settings.DeadlineAt
		}
	}

	newSession := &AssignmentSession{
		ID:           uuid.New(),
		AssignmentID: assignmentID,
		UserID:       userID,
		Status:       SessionInProgress,
		StartedAt:    now,
		ExpiresAt:    expiresAt,
		PrimaryScore: 0,
		BlurCount:    0,
		PasteCount:   0,
	}

	if err := s.repo.CreateSession(ctx, newSession); err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}

	_ = s.repo.LogTaskEvent(ctx, newSession.ID, assignmentID, userID, nil, "session_started", map[string]any{
		"mode": a.Mode,
	})

	var expiresStr *string
	if newSession.ExpiresAt != nil {
		f := newSession.ExpiresAt.Format(time.RFC3339)
		expiresStr = &f
	}

	return &StartSessionResponse{
		SessionID:    newSession.ID,
		AssignmentID: newSession.AssignmentID,
		Status:       newSession.Status,
		StartedAt:    newSession.StartedAt.Format(time.RFC3339),
		ExpiresAt:    expiresStr,
	}, nil
}

// GetSession returns session detail with anti-cheat masking applied according to exam progress and reveal policy.
func (s *AssignmentService) GetSession(ctx context.Context, callerID, sessionID uuid.UUID) (*SessionDetailDTO, error) {
	session, err := s.repo.GetSessionByID(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	a, err := s.repo.GetAssignmentByID(ctx, session.AssignmentID)
	if err != nil {
		return nil, err
	}

	// Authorization: session owner or assignment creator
	if callerID != session.UserID && callerID != a.CreatorID {
		return nil, ErrForbidden
	}

	now := time.Now().UTC()

	// Check timer expiration
	if session.Status == SessionInProgress && session.ExpiresAt != nil && now.After(*session.ExpiresAt) {
		session.Status = SessionExpired
		_ = s.repo.ExpireSession(ctx, session.ID, session.PrimaryScore, 0)
	}

	taskRows, err := s.repo.GetAssignmentTasksWithDetails(ctx, session.AssignmentID)
	if err != nil {
		return nil, fmt.Errorf("get assignment tasks: %w", err)
	}

	userTasksList, err := s.repo.ListUserAssignmentTasks(ctx, session.ID)
	if err != nil {
		return nil, fmt.Errorf("get user assignment tasks: %w", err)
	}

	userTasksMap := make(map[int]UserAssignmentTask)
	for _, ut := range userTasksList {
		userTasksMap[ut.TaskID] = ut
	}

	isInProgress := (session.Status == SessionInProgress)

	// Can reveal answers/scores?
	canReveal := false
	if !isInProgress {
		switch a.Settings.RevealPolicy {
		case RevealImmediate, RevealAfterSubmit:
			canReveal = true
		case RevealAfterDeadline:
			if a.Settings.DeadlineAt == nil || now.After(*a.Settings.DeadlineAt) {
				canReveal = true
			}
		case RevealManual:
			canReveal = a.ResultsReleased
		}
	} else if a.Settings.RevealPolicy == RevealImmediate {
		canReveal = true
	}

	var sessionTasks []SessionTaskDTO
	for _, row := range taskRows {
		ut, hasSubmitted := userTasksMap[row.TaskID]

		// Build task card
		var kimNums []int
		for _, k := range row.KimNumbers {
			kimNums = append(kimNums, int(k))
		}

		var ansCfg tasks.AnswerConfig
		if len(row.AnswerConfig) > 0 {
			_ = json.Unmarshal(row.AnswerConfig, &ansCfg)
		}

		var files []tasks.FileAttachment
		if len(row.Files) > 0 {
			_ = json.Unmarshal(row.Files, &files)
		}

		var topicDTO *tasks.TopicDTO
		if row.TopicKim != nil && row.TopicName != nil {
			topicDTO = &tasks.TopicDTO{
				ID:        row.TopicID,
				KimNumber: *row.TopicKim,
				Name:      *row.TopicName,
			}
		}

		// Security: Mask source & solution during in-progress exam
		var source *string
		if !isInProgress {
			source = row.Source
		}

		var solutionDTO *tasks.SolutionDTO
		if canReveal {
			var corrAns tasks.CorrectAnswer
			if len(row.CorrectAnswer) > 0 {
				_ = json.Unmarshal(row.CorrectAnswer, &corrAns)
			}
			solutionDTO = &tasks.SolutionDTO{
				CorrectAnswer: &corrAns,
				SolutionMD:    row.SolutionMD,
				SolutionCode:  row.SolutionCode,
			}
		}

		card := SessionTaskCard{
			ID:           row.TaskID,
			KimNumbers:   kimNums,
			Topic:        topicDTO,
			Difficulty:   int(row.Difficulty),
			TextMD:       row.TextMD,
			Images:       row.Images,
			Files:        files,
			AnswerConfig: ansCfg,
			Source:       source,
			Solution:     solutionDTO,
		}

		var userAns any
		var isCorrect *bool
		var earnedScore *int16
		attempts := 0
		var studentComment *string

		if hasSubmitted {
			_ = json.Unmarshal(ut.UserAnswer, &userAns)
			attempts = ut.AttemptsUsed
			studentComment = ut.StudentComment
			if canReveal {
				isCorrectVal := ut.IsCorrect
				isCorrect = &isCorrectVal
				earnedVal := ut.EarnedScore
				earnedScore = &earnedVal
			}
		}

		sessionTasks = append(sessionTasks, SessionTaskDTO{
			Position:       int(row.Position),
			Task:           card,
			UserAnswer:     userAns,
			IsCorrect:      isCorrect,
			EarnedScore:    earnedScore,
			AttemptsUsed:   attempts,
			StudentComment: studentComment,
		})
	}

	var expiresStr *string
	if session.ExpiresAt != nil {
		f := session.ExpiresAt.Format(time.RFC3339)
		expiresStr = &f
	}
	var finishedStr *string
	if session.FinishedAt != nil {
		f := session.FinishedAt.Format(time.RFC3339)
		finishedStr = &f
	}

	return &SessionDetailDTO{
		ID:             session.ID,
		AssignmentID:   session.AssignmentID,
		UserID:         session.UserID,
		Status:         session.Status,
		ServerNow:      now.Format(time.RFC3339),
		StartedAt:      session.StartedAt.Format(time.RFC3339),
		ExpiresAt:      expiresStr,
		FinishedAt:     finishedStr,
		PrimaryScore:   session.PrimaryScore,
		SecondaryScore: session.SecondaryScore,
		Settings:       a.Settings,
		Tasks:          sessionTasks,
	}, nil
}

// AnswerTask evaluates student answer with Grader, saves code to private S3, logs paste ranges and attempts.
func (s *AssignmentService) AnswerTask(ctx context.Context, userID, sessionID uuid.UUID, taskID int, req AnswerTaskRequest) (*AnswerTaskResponse, error) {
	session, err := s.repo.GetSessionByID(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if session.UserID != userID {
		return nil, ErrForbidden
	}
	if session.Status != SessionInProgress {
		return nil, ErrSessionFinished
	}

	now := time.Now().UTC()
	if session.ExpiresAt != nil && now.After(*session.ExpiresAt) {
		_ = s.repo.ExpireSession(ctx, session.ID, session.PrimaryScore, 0)
		return nil, ErrDeadlinePassed
	}

	a, err := s.repo.GetAssignmentByID(ctx, session.AssignmentID)
	if err != nil {
		return nil, err
	}

	if a.Settings.DeadlineAt != nil && now.After(*a.Settings.DeadlineAt) && !a.Settings.AllowAfterDeadline {
		return nil, ErrDeadlinePassed
	}

	taskEntity, err := s.repo.GetTaskByID(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("task not found: %w", err)
	}

	// Check attempt limits
	existingUAT, _ := s.repo.GetUserAssignmentTask(ctx, sessionID, taskID)
	attemptsUsed := 1
	if existingUAT != nil {
		attemptsUsed = existingUAT.AttemptsUsed + 1
	}

	if a.Settings.MaxAttempts > 0 && attemptsUsed > a.Settings.MaxAttempts {
		return nil, ErrAttemptsExceeded
	}

	// Parse UserAnswer safely
	userAns, err := tasks.ParseUserAnswer(req.UserAnswer)
	if err != nil {
		return nil, fmt.Errorf("invalid user answer: %w", err)
	}

	// Evaluate answer with polymorphic Grader engine
	gradeRes := tasks.GradeAnswer(taskEntity.AnswerConfig, taskEntity.CorrectAnswer, userAns)

	// Persist Python code snapshot in MinIO egeinf-private if provided
	var codeS3Key *string
	if req.Code != "" && s.storage != nil {
		key, s3Err := s.storage.SaveCode(ctx, sessionID.String(), taskID, req.Code)
		if s3Err == nil {
			codeS3Key = &key
		}
	}

	// Handle paste ranges
	pasteRangesJSON, _ := json.Marshal(req.PasteRanges)
	if len(req.PasteRanges) > 0 {
		_ = s.repo.UpdateSessionCounts(ctx, sessionID, 0, len(req.PasteRanges))
	}

	uat := &UserAssignmentTask{
		SessionID:      sessionID,
		AssignmentID:   a.ID,
		UserID:         userID,
		TaskID:         taskID,
		TopicID:        taskEntity.TopicID,
		IsCorrect:      gradeRes.IsCorrect,
		EarnedScore:    int16(gradeRes.Score),
		AttemptsUsed:   attemptsUsed,
		UserAnswer:     req.UserAnswer,
		CodeS3Key:      codeS3Key,
		PasteRanges:    pasteRangesJSON,
		StudentComment: req.StudentComment,
	}

	if err := s.repo.UpsertUserAssignmentTask(ctx, uat); err != nil {
		return nil, fmt.Errorf("save task answer: %w", err)
	}

	// Log answer_submitted event
	_ = s.repo.LogTaskEvent(ctx, sessionID, a.ID, userID, &taskID, "answer_submitted", map[string]any{
		"earned_score":  gradeRes.Score,
		"is_correct":    gradeRes.IsCorrect,
		"attempts_used": attemptsUsed,
	})

	var respIsCorrect *bool
	var respEarnedScore *int
	if a.Settings.RevealPolicy == RevealImmediate {
		isCorr := gradeRes.IsCorrect
		respIsCorrect = &isCorr
		scoreVal := gradeRes.Score
		respEarnedScore = &scoreVal
	}

	return &AnswerTaskResponse{
		IsCorrect:    respIsCorrect,
		EarnedScore:  respEarnedScore,
		MaxScore:     gradeRes.MaxScore,
		AttemptsUsed: attemptsUsed,
		MaxAttempts:  a.Settings.MaxAttempts,
		RevealPolicy: a.Settings.RevealPolicy,
	}, nil
}

// FinishSession completes attempt, aggregates primary points (0..29), maps to secondary (0..100), and outputs protocol.
func (s *AssignmentService) FinishSession(ctx context.Context, userID, sessionID uuid.UUID) (*SessionFinishDTO, error) {
	session, err := s.repo.GetSessionByID(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if session.UserID != userID {
		return nil, ErrForbidden
	}
	if session.Status == SessionFinished {
		return nil, ErrSessionFinished
	}

	a, err := s.repo.GetAssignmentByID(ctx, session.AssignmentID)
	if err != nil {
		return nil, err
	}

	// Compute primary points from all submitted tasks in this session
	uats, err := s.repo.ListUserAssignmentTasks(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list tasks for scoring: %w", err)
	}

	var primaryTotal int16
	for _, u := range uats {
		primaryTotal += u.EarnedScore
	}
	if primaryTotal > 29 {
		primaryTotal = 29
	}

	secondaryScore, err := s.repo.GetExamSecondaryScore(ctx, primaryTotal)
	if err != nil {
		return nil, fmt.Errorf("calculate secondary score: %w", err)
	}

	if err := s.repo.FinishSession(ctx, sessionID, primaryTotal, secondaryScore); err != nil {
		return nil, fmt.Errorf("finish session: %w", err)
	}

	now := time.Now().UTC()
	_ = s.repo.LogTaskEvent(ctx, sessionID, a.ID, userID, nil, "session_finished", map[string]any{
		"primary_score":   primaryTotal,
		"secondary_score": secondaryScore,
	})

	// Build protocol based on reveal policy
	canView := false
	switch a.Settings.RevealPolicy {
	case RevealImmediate, RevealAfterSubmit:
		canView = true
	case RevealAfterDeadline:
		if a.Settings.DeadlineAt == nil || now.After(*a.Settings.DeadlineAt) {
			canView = true
		}
	case RevealManual:
		canView = a.ResultsReleased
	}

	var protocolTasks []ProtocolTaskDTO
	if canView {
		taskRows, _ := s.repo.GetAssignmentTasksWithDetails(ctx, a.ID)
		uatMap := make(map[int]UserAssignmentTask)
		for _, u := range uats {
			uatMap[u.TaskID] = u
		}

		for _, row := range taskRows {
			ut := uatMap[row.TaskID]

			var corr tasks.CorrectAnswer
			if len(row.CorrectAnswer) > 0 {
				_ = json.Unmarshal(row.CorrectAnswer, &corr)
			}

			maxScore := 1
			if len(row.AnswerConfig) > 0 {
				var ac tasks.AnswerConfig
				_ = json.Unmarshal(row.AnswerConfig, &ac)
				if ac.MaxScore > 0 {
					maxScore = ac.MaxScore
				}
			}

			protocolTasks = append(protocolTasks, ProtocolTaskDTO{
				TaskID:       row.TaskID,
				Position:     int(row.Position),
				IsCorrect:    ut.IsCorrect,
				EarnedScore:  ut.EarnedScore,
				MaxScore:     maxScore,
				AttemptsUsed: ut.AttemptsUsed,
				Solution: &tasks.SolutionDTO{
					CorrectAnswer: &corr,
					SolutionMD:    row.SolutionMD,
					SolutionCode:  row.SolutionCode,
				},
			})
		}
	}

	return &SessionFinishDTO{
		SessionID:      sessionID,
		Status:         SessionFinished,
		FinishedAt:     now.Format(time.RFC3339),
		PrimaryScore:   primaryTotal,
		SecondaryScore: secondaryScore,
		Protocol: SessionProtocolDTO{
			RevealPolicy:    a.Settings.RevealPolicy,
			ResultsReleased: a.ResultsReleased,
			CanViewResults:  canView,
			Tasks:           protocolTasks,
		},
	}, nil
}

func toAssignmentDTO(a *Assignment) *AssignmentDTO {
	return &AssignmentDTO{
		ID:              a.ID,
		RoomID:          a.RoomID,
		Title:           a.Title,
		Mode:            a.Mode,
		Settings:        a.Settings,
		ResultsReleased: a.ResultsReleased,
		TasksCount:      a.TasksCount,
		CreatedAt:       a.CreatedAt.Format(time.RFC3339),
	}
}

// GetExamScale returns the KEGE primary-to-secondary conversion scale.
func (s *AssignmentService) GetExamScale(ctx context.Context) ([]ExamScaleEntry, error) {
	entries, err := s.repo.GetExamScale(ctx)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return defaultKegeScaleEntries(), nil
	}
	return entries, nil
}

// UpdateExamScale validates and persists the KEGE primary-to-secondary conversion scale.
func (s *AssignmentService) UpdateExamScale(ctx context.Context, entries []ExamScaleEntry) error {
	if len(entries) == 0 {
		return fmt.Errorf("scale entries cannot be empty")
	}
	for _, e := range entries {
		if e.PrimaryScore < 0 || e.PrimaryScore > 29 {
			return fmt.Errorf("primary_score %d must be between 0 and 29", e.PrimaryScore)
		}
		if e.SecondaryScore < 0 || e.SecondaryScore > 100 {
			return fmt.Errorf("secondary_score %d must be between 0 and 100", e.SecondaryScore)
		}
	}
	return s.repo.UpdateExamScale(ctx, entries)
}
