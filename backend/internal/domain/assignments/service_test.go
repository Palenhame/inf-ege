package assignments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"backend/internal/domain/tasks"
	"github.com/google/uuid"
)

type mockRepository struct {
	assignments     map[uuid.UUID]*Assignment
	assignmentTasks map[uuid.UUID][]int
	sessions        map[uuid.UUID]*AssignmentSession
	userTasks       map[string]*UserAssignmentTask
	tasks           map[int]*tasks.Task
	events          []string
	secondaryScale  map[int16]int16
	isRoomMember    bool
	isRoomCreator   bool
}

func newMockRepository() *mockRepository {
	return &mockRepository{
		assignments:     make(map[uuid.UUID]*Assignment),
		assignmentTasks: make(map[uuid.UUID][]int),
		sessions:        make(map[uuid.UUID]*AssignmentSession),
		userTasks:       make(map[string]*UserAssignmentTask),
		tasks:           make(map[int]*tasks.Task),
		secondaryScale: map[int16]int16{
			0: 0, 1: 7, 14: 62, 26: 90, 29: 100,
		},
		isRoomMember:  true,
		isRoomCreator: true,
	}
}

func (m *mockRepository) CreateAssignment(ctx context.Context, a *Assignment, taskIDs []int) error {
	m.assignments[a.ID] = a
	m.assignmentTasks[a.ID] = taskIDs
	a.TasksCount = len(taskIDs)
	return nil
}

func (m *mockRepository) GetAssignmentByID(ctx context.Context, id uuid.UUID) (*Assignment, error) {
	a, ok := m.assignments[id]
	if !ok || a.DeletedAt != nil {
		return nil, ErrNotFound
	}
	return a, nil
}

func (m *mockRepository) UpdateAssignment(ctx context.Context, id uuid.UUID, title *string, settings *AssignmentSettings, resultsReleased *bool) (*Assignment, error) {
	a, ok := m.assignments[id]
	if !ok || a.DeletedAt != nil {
		return nil, ErrNotFound
	}
	if title != nil {
		a.Title = *title
	}
	if settings != nil {
		a.Settings = *settings
	}
	if resultsReleased != nil {
		a.ResultsReleased = *resultsReleased
	}
	return a, nil
}

func (m *mockRepository) DeleteAssignment(ctx context.Context, id uuid.UUID) error {
	a, ok := m.assignments[id]
	if !ok || a.DeletedAt != nil {
		return ErrNotFound
	}
	now := time.Now()
	a.DeletedAt = &now
	return nil
}

func (m *mockRepository) CreateSession(ctx context.Context, s *AssignmentSession) error {
	m.sessions[s.ID] = s
	return nil
}

func (m *mockRepository) GetSessionByID(ctx context.Context, id uuid.UUID) (*AssignmentSession, error) {
	s, ok := m.sessions[id]
	if !ok {
		return nil, ErrNotFound
	}
	return s, nil
}

func (m *mockRepository) GetSessionByAssignmentAndUser(ctx context.Context, assignmentID, userID uuid.UUID) (*AssignmentSession, error) {
	for _, s := range m.sessions {
		if s.AssignmentID == assignmentID && s.UserID == userID {
			return s, nil
		}
	}
	return nil, ErrNotFound
}

func (m *mockRepository) UpdateSessionCounts(ctx context.Context, sessionID uuid.UUID, blurDelta, pasteDelta int) error {
	s, ok := m.sessions[sessionID]
	if !ok {
		return ErrNotFound
	}
	s.BlurCount += blurDelta
	s.PasteCount += pasteDelta
	return nil
}

func (m *mockRepository) FinishSession(ctx context.Context, sessionID uuid.UUID, primaryScore, secondaryScore int16) error {
	s, ok := m.sessions[sessionID]
	if !ok {
		return ErrNotFound
	}
	s.Status = SessionFinished
	now := time.Now().UTC()
	s.FinishedAt = &now
	s.PrimaryScore = primaryScore
	s.SecondaryScore = &secondaryScore
	return nil
}

func (m *mockRepository) ExpireSession(ctx context.Context, sessionID uuid.UUID, primaryScore, secondaryScore int16) error {
	s, ok := m.sessions[sessionID]
	if !ok {
		return ErrNotFound
	}
	s.Status = SessionExpired
	now := time.Now().UTC()
	s.FinishedAt = &now
	s.PrimaryScore = primaryScore
	s.SecondaryScore = &secondaryScore
	return nil
}

func (m *mockRepository) GetExamSecondaryScore(ctx context.Context, primaryScore int16) (int16, error) {
	if sec, ok := m.secondaryScale[primaryScore]; ok {
		return sec, nil
	}
	return (primaryScore * 100) / 29, nil
}

func (m *mockRepository) GetExamScale(ctx context.Context) ([]ExamScaleEntry, error) {
	entries := make([]ExamScaleEntry, 0, 30)
	for p := int16(0); p <= 29; p++ {
		sec := defaultKegeScale[p]
		if v, ok := m.secondaryScale[p]; ok {
			sec = v
		}
		entries = append(entries, ExamScaleEntry{PrimaryScore: p, SecondaryScore: sec})
	}
	return entries, nil
}

func (m *mockRepository) UpdateExamScale(ctx context.Context, entries []ExamScaleEntry) error {
	for _, e := range entries {
		m.secondaryScale[e.PrimaryScore] = e.SecondaryScore
	}
	return nil
}

func (m *mockRepository) GetTaskByID(ctx context.Context, taskID int) (*tasks.Task, error) {
	t, ok := m.tasks[taskID]
	if !ok {
		return nil, ErrNotFound
	}
	return t, nil
}

func (m *mockRepository) GetAssignmentTasksWithDetails(ctx context.Context, assignmentID uuid.UUID) ([]SessionTaskDetailRow, error) {
	taskIDs, ok := m.assignmentTasks[assignmentID]
	if !ok {
		return nil, nil
	}
	var rows []SessionTaskDetailRow
	for idx, id := range taskIDs {
		t, exists := m.tasks[id]
		if !exists {
			continue
		}
		var kimSmallints []int16
		for _, k := range t.KimNumbers {
			kimSmallints = append(kimSmallints, int16(k))
		}
		ansCfgBytes, _ := json.Marshal(t.AnswerConfig)
		corrAnsBytes, _ := json.Marshal(t.CorrectAnswer)
		kimNum := 1
		if len(t.KimNumbers) > 0 {
			kimNum = t.KimNumbers[0]
		}
		topicName := "Test Topic"
		rows = append(rows, SessionTaskDetailRow{
			Position:      int16(idx + 1),
			TaskID:        t.ID,
			KimNumbers:    kimSmallints,
			TopicID:       t.TopicID,
			TopicKim:      &kimNum,
			TopicName:     &topicName,
			Difficulty:    int16(t.Difficulty),
			Source:        t.Source,
			TextMD:        t.TextMD,
			AnswerConfig:  ansCfgBytes,
			CorrectAnswer: corrAnsBytes,
			SolutionMD:    t.SolutionMD,
			SolutionCode:  t.SolutionCode,
		})
	}
	return rows, nil
}

func (m *mockRepository) GetUserAssignmentTask(ctx context.Context, sessionID uuid.UUID, taskID int) (*UserAssignmentTask, error) {
	key := fmt.Sprintf("%s:%d", sessionID.String(), taskID)
	uat, ok := m.userTasks[key]
	if !ok {
		return nil, ErrNotFound
	}
	return uat, nil
}

func (m *mockRepository) UpsertUserAssignmentTask(ctx context.Context, uat *UserAssignmentTask) error {
	key := fmt.Sprintf("%s:%d", uat.SessionID.String(), uat.TaskID)
	m.userTasks[key] = uat
	return nil
}

func (m *mockRepository) ListUserAssignmentTasks(ctx context.Context, sessionID uuid.UUID) ([]UserAssignmentTask, error) {
	var list []UserAssignmentTask
	for _, uat := range m.userTasks {
		if uat.SessionID == sessionID {
			list = append(list, *uat)
		}
	}
	return list, nil
}

func (m *mockRepository) SelectSelfExamTasks(ctx context.Context, userID uuid.UUID) ([]int, error) {
	// Return 25 tasks
	var list []int
	for i := 1; i <= 25; i++ {
		list = append(list, 100+i)
	}
	return list, nil
}

func (m *mockRepository) LogTaskEvent(ctx context.Context, sessionID, assignmentID, userID uuid.UUID, taskID *int, event string, payload any) error {
	m.events = append(m.events, event)
	return nil
}

func (m *mockRepository) CheckRoomMembership(ctx context.Context, roomID, userID uuid.UUID) (bool, error) {
	return m.isRoomMember, nil
}

func (m *mockRepository) CheckRoomCreator(ctx context.Context, roomID, userID uuid.UUID) (bool, error) {
	return m.isRoomCreator, nil
}

func setupTestService() (*AssignmentService, *mockRepository, *MemoryCodeStorage) {
	repo := newMockRepository()
	storage := NewMemoryCodeStorage()
	svc := NewService(repo, storage)

	// Populate demo task #1042 (single answer, KIM 24)
	src := "Основная волна 2025"
	solMD := "Подробное объяснение"
	solCode := "print(1024)"
	repo.tasks[1042] = &tasks.Task{
		ID:            1042,
		KimNumbers:    []int{24},
		TopicID:       2401,
		Difficulty:    3,
		Source:        &src,
		TextMD:        "Текстовый файл 24.txt...",
		AnswerConfig:  tasks.AnswerConfig{AnswerType: "single", MaxScore: 1, TrimWhitespace: true},
		CorrectAnswer: tasks.CorrectAnswer{AnswerType: "single", Accepted: []string{"1024"}},
		SolutionMD:    &solMD,
		SolutionCode:  &solCode,
	}

	return svc, repo, storage
}

func TestCreateAssignment_ModesAndValidation(t *testing.T) {
	svc, repo, _ := setupTestService()
	ctx := context.Background()
	creatorID := uuid.New()
	roomID := uuid.New()

	modes := []AssignmentMode{ModeHomework, ModeClasswork, ModePractice, ModeProctoredTest, ModeMockExam}
	for _, mode := range modes {
		req := CreateAssignmentRequest{
			Title: string(mode) + " title",
			Mode:  mode,
			Settings: AssignmentSettings{
				MaxAttempts:  2,
				RevealPolicy: RevealAfterSubmit,
			},
			TaskIDs: []int{1042},
		}

		dto, err := svc.CreateAssignment(ctx, creatorID, roomID, req)
		if err != nil {
			t.Fatalf("CreateAssignment failed for mode %s: %v", mode, err)
		}
		if dto.Mode != mode {
			t.Errorf("expected mode %s, got %s", mode, dto.Mode)
		}
		if dto.TasksCount != 1 {
			t.Errorf("expected 1 task, got %d", dto.TasksCount)
		}
	}

	// Empty title rejected
	_, err := svc.CreateAssignment(ctx, creatorID, roomID, CreateAssignmentRequest{Title: "", TaskIDs: []int{1042}})
	if err == nil {
		t.Errorf("expected error on empty title, got nil")
	}

	// Empty tasks rejected
	_, err = svc.CreateAssignment(ctx, creatorID, roomID, CreateAssignmentRequest{Title: "Test", TaskIDs: []int{}})
	if err == nil {
		t.Errorf("expected error on empty task_ids, got nil")
	}

	// Non-creator forbidden
	repo.isRoomCreator = false
	_, err = svc.CreateAssignment(ctx, creatorID, roomID, CreateAssignmentRequest{Title: "Test", TaskIDs: []int{1042}})
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("expected ErrForbidden for non-creator, got %v", err)
	}
}

func TestSelfStartExam_Generator(t *testing.T) {
	svc, repo, _ := setupTestService()
	ctx := context.Background()
	userID := uuid.New()

	resp, err := svc.SelfStartExam(ctx, userID, SelfStartRequest{})
	if err != nil {
		t.Fatalf("SelfStartExam failed: %v", err)
	}

	if resp.SessionID == uuid.Nil || resp.AssignmentID == uuid.Nil {
		t.Errorf("expected valid session and assignment IDs")
	}

	// Verify generated assignment
	a := repo.assignments[resp.AssignmentID]
	if a == nil {
		t.Fatalf("assignment not saved in repo")
	}
	if a.RoomID != nil {
		t.Errorf("expected standalone exam room_id to be nil, got %v", a.RoomID)
	}
	if a.Mode != ModeMockExam {
		t.Errorf("expected mock_exam mode, got %s", a.Mode)
	}
	if a.Settings.DurationMinutes == nil || *a.Settings.DurationMinutes != 235 {
		t.Errorf("expected 235 minutes timer, got %v", a.Settings.DurationMinutes)
	}

	// Verify 25 cards associated
	taskIDs := repo.assignmentTasks[resp.AssignmentID]
	if len(taskIDs) != 25 {
		t.Errorf("expected 25 cards covering 27 KIM numbers, got %d", len(taskIDs))
	}

	// Verify session
	s := repo.sessions[resp.SessionID]
	if s == nil {
		t.Fatalf("session not saved in repo")
	}
	if s.Status != SessionInProgress {
		t.Errorf("expected session in_progress, got %s", s.Status)
	}
}

func TestSession_AntiCheatMaskingAndFlow(t *testing.T) {
	svc, repo, _ := setupTestService()
	ctx := context.Background()
	teacherID := uuid.New()
	studentID := uuid.New()
	roomID := uuid.New()

	// 1. Create assignment with RevealAfterSubmit
	dur := 120
	createReq := CreateAssignmentRequest{
		Title: "Контрольный тест",
		Mode:  ModeProctoredTest,
		Settings: AssignmentSettings{
			DurationMinutes: &dur,
			MaxAttempts:     3,
			RevealPolicy:    RevealAfterSubmit,
		},
		TaskIDs: []int{1042},
	}
	aDTO, err := svc.CreateAssignment(ctx, teacherID, roomID, createReq)
	if err != nil {
		t.Fatalf("create assignment: %v", err)
	}

	// 2. Student starts session
	startResp, err := svc.StartSession(ctx, studentID, aDTO.ID)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	if startResp.Status != SessionInProgress {
		t.Errorf("expected in_progress, got %s", startResp.Status)
	}

	// 3. Inspect session state during exam (anti-cheat masking check!)
	detail, err := svc.GetSession(ctx, studentID, startResp.SessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if len(detail.Tasks) != 1 {
		t.Fatalf("expected 1 task in session, got %d", len(detail.Tasks))
	}

	st := detail.Tasks[0]
	if st.Task.Source != nil {
		t.Errorf("ANTI-CHEAT VIOLATION: Source must be nil during in-progress session, got %s", *st.Task.Source)
	}
	if st.Task.Solution != nil {
		t.Errorf("ANTI-CHEAT VIOLATION: Solution must be nil during in-progress session, got %v", st.Task.Solution)
	}

	// 4. Submit incorrect answer
	ansReq := AnswerTaskRequest{
		UserAnswer:  json.RawMessage(`{"answer_type":"single","value":"999"}`),
		Code:        "print(999)",
		PasteRanges: [][]int{{0, 10}},
	}
	ansResp, err := svc.AnswerTask(ctx, studentID, startResp.SessionID, 1042, ansReq)
	if err != nil {
		t.Fatalf("answer task: %v", err)
	}
	if ansResp.AttemptsUsed != 1 {
		t.Errorf("expected attempts_used = 1, got %d", ansResp.AttemptsUsed)
	}
	// Under RevealAfterSubmit, score and is_correct are hidden during exam!
	if ansResp.IsCorrect != nil || ansResp.EarnedScore != nil {
		t.Errorf("ANTI-CHEAT VIOLATION: Score and is_correct must be masked when reveal_policy is after_submit")
	}

	// Verify paste_count incremented
	session := repo.sessions[startResp.SessionID]
	if session.PasteCount != 1 {
		t.Errorf("expected paste_count = 1, got %d", session.PasteCount)
	}

	// 5. Submit correct answer
	ansReq2 := AnswerTaskRequest{
		UserAnswer: json.RawMessage(`{"answer_type":"single","value":"1024"}`),
		Code:       "with open('24.txt') as f: print(1024)",
	}
	ansResp2, err := svc.AnswerTask(ctx, studentID, startResp.SessionID, 1042, ansReq2)
	if err != nil {
		t.Fatalf("answer task 2: %v", err)
	}
	if ansResp2.AttemptsUsed != 2 {
		t.Errorf("expected attempts_used = 2, got %d", ansResp2.AttemptsUsed)
	}

	// 6. Finish session
	finishDTO, err := svc.FinishSession(ctx, studentID, startResp.SessionID)
	if err != nil {
		t.Fatalf("finish session: %v", err)
	}
	if finishDTO.PrimaryScore != 1 {
		t.Errorf("expected primary_score = 1, got %d", finishDTO.PrimaryScore)
	}
	if finishDTO.SecondaryScore != 7 {
		t.Errorf("expected secondary_score mapped to 7, got %d", finishDTO.SecondaryScore)
	}
	if !finishDTO.Protocol.CanViewResults {
		t.Errorf("expected CanViewResults = true after submit")
	}

	// 7. Verify session detail after finish: source and solutions are now revealed!
	finishedDetail, err := svc.GetSession(ctx, studentID, startResp.SessionID)
	if err != nil {
		t.Fatalf("get finished session: %v", err)
	}
	ft := finishedDetail.Tasks[0]
	if ft.Task.Source == nil || *ft.Task.Source != "Основная волна 2025" {
		t.Errorf("expected source revealed after finish, got %v", ft.Task.Source)
	}
	if ft.Task.Solution == nil {
		t.Errorf("expected solution revealed after finish, got nil")
	}
	if ft.IsCorrect == nil || !*ft.IsCorrect {
		t.Errorf("expected is_correct = true after finish")
	}
	if ft.EarnedScore == nil || *ft.EarnedScore != 1 {
		t.Errorf("expected earned_score = 1 after finish")
	}

	// 8. Further submit after finish should be rejected
	_, err = svc.AnswerTask(ctx, studentID, startResp.SessionID, 1042, ansReq)
	if !errors.Is(err, ErrSessionFinished) {
		t.Errorf("expected ErrSessionFinished on submitting to finished session, got %v", err)
	}
}

func TestSession_TimerExpiration(t *testing.T) {
	svc, repo, _ := setupTestService()
	ctx := context.Background()
	studentID := uuid.New()
	assignmentID := uuid.New()

	pastTime := time.Now().UTC().Add(-10 * time.Minute)
	sessionID := uuid.New()
	repo.sessions[sessionID] = &AssignmentSession{
		ID:           sessionID,
		AssignmentID: assignmentID,
		UserID:       studentID,
		Status:       SessionInProgress,
		StartedAt:    pastTime.Add(-2 * time.Hour),
		ExpiresAt:    &pastTime,
		PrimaryScore: 0,
	}
	repo.assignments[assignmentID] = &Assignment{
		ID:        assignmentID,
		CreatorID: uuid.New(),
		Title:     "Expired assignment",
		Mode:      ModeMockExam,
	}

	// Attempting answer after expires_at should fail
	_, err := svc.AnswerTask(ctx, studentID, sessionID, 1042, AnswerTaskRequest{
		UserAnswer: json.RawMessage(`{"answer_type":"single","value":"1024"}`),
	})
	if !errors.Is(err, ErrDeadlinePassed) && !errors.Is(err, ErrSessionFinished) {
		t.Errorf("expected deadline or session finished error, got %v", err)
	}
}

func TestExamScale_GetAndUpdate(t *testing.T) {
	svc, _, _ := setupTestService()
	ctx := context.Background()

	scale, err := svc.GetExamScale(ctx)
	if err != nil {
		t.Fatalf("GetExamScale failed: %v", err)
	}
	if len(scale) != 30 {
		t.Fatalf("expected 30 scale entries, got %d", len(scale))
	}

	if err := svc.UpdateExamScale(ctx, []ExamScaleEntry{{PrimaryScore: 1, SecondaryScore: 8}}); err != nil {
		t.Fatalf("UpdateExamScale failed: %v", err)
	}

	scale, err = svc.GetExamScale(ctx)
	if err != nil {
		t.Fatalf("GetExamScale after update failed: %v", err)
	}
	if scale[1].SecondaryScore != 8 {
		t.Errorf("expected primary 1 -> secondary 8, got %d", scale[1].SecondaryScore)
	}

	// Out of range primary score
	if err := svc.UpdateExamScale(ctx, []ExamScaleEntry{{PrimaryScore: 30, SecondaryScore: 100}}); err == nil {
		t.Errorf("expected error for primary_score > 29")
	}
	// Out of range secondary score
	if err := svc.UpdateExamScale(ctx, []ExamScaleEntry{{PrimaryScore: 10, SecondaryScore: 101}}); err == nil {
		t.Errorf("expected error for secondary_score > 100")
	}
	// Empty entries
	if err := svc.UpdateExamScale(ctx, nil); err == nil {
		t.Errorf("expected error for empty scale entries")
	}
}
