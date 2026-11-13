package mcp_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"testing"

	"backend/internal/domain/assignments"
	"backend/internal/domain/proctoring"
	"backend/internal/domain/tasks"
	"backend/internal/mcp"
	"github.com/google/uuid"
)

type mockProctoringService struct {
	createdTask   *tasks.TaskDTO
	teacherTasks  []tasks.TaskDTO
	snapshot      *proctoring.MatrixSnapshotDTO
	dossier       *proctoring.SessionInspectionDTO
	createTaskErr error
	listErr       error
	snapshotErr   error
	dossierErr    error
}

func (m *mockProctoringService) CreateTeacherTask(ctx context.Context, teacherID uuid.UUID, req proctoring.CreateTeacherTaskRequest) (*tasks.TaskDTO, error) {
	if m.createTaskErr != nil {
		return nil, m.createTaskErr
	}
	return m.createdTask, nil
}

func (m *mockProctoringService) ListTeacherTasks(ctx context.Context, teacherID uuid.UUID, limit, offset int, kim *int, topicID *int, q string) ([]tasks.TaskDTO, int, error) {
	if m.listErr != nil {
		return nil, 0, m.listErr
	}
	return m.teacherTasks, len(m.teacherTasks), nil
}

func (m *mockProctoringService) GetMatrixSnapshot(ctx context.Context, teacherID, assignmentID uuid.UUID) (*proctoring.MatrixSnapshotDTO, error) {
	if m.snapshotErr != nil {
		return nil, m.snapshotErr
	}
	return m.snapshot, nil
}

func (m *mockProctoringService) GetSessionDossier(ctx context.Context, teacherID, sessionID uuid.UUID) (*proctoring.SessionInspectionDTO, error) {
	if m.dossierErr != nil {
		return nil, m.dossierErr
	}
	return m.dossier, nil
}

type mockAssignmentsService struct {
	createdAssignment *assignments.AssignmentDTO
	createErr         error
}

func (m *mockAssignmentsService) CreateAssignment(ctx context.Context, creatorID, roomID uuid.UUID, req assignments.CreateAssignmentRequest) (*assignments.AssignmentDTO, error) {
	if m.createErr != nil {
		return nil, m.createErr
	}
	return m.createdAssignment, nil
}

func (m *mockAssignmentsService) UpdateAssignment(ctx context.Context, callerID, assignmentID uuid.UUID, req assignments.UpdateAssignmentRequest) (*assignments.AssignmentDTO, error) {
	return nil, nil
}

func (m *mockAssignmentsService) DeleteAssignment(ctx context.Context, callerID, assignmentID uuid.UUID) error {
	return nil
}

func (m *mockAssignmentsService) SelfStartExam(ctx context.Context, userID uuid.UUID, req assignments.SelfStartRequest) (*assignments.SelfStartResponse, error) {
	return nil, nil
}

func (m *mockAssignmentsService) StartSession(ctx context.Context, userID, assignmentID uuid.UUID) (*assignments.StartSessionResponse, error) {
	return nil, nil
}

func (m *mockAssignmentsService) GetSession(ctx context.Context, callerID, sessionID uuid.UUID) (*assignments.SessionDetailDTO, error) {
	return nil, nil
}

func (m *mockAssignmentsService) AnswerTask(ctx context.Context, userID, sessionID uuid.UUID, taskID int, req assignments.AnswerTaskRequest) (*assignments.AnswerTaskResponse, error) {
	return nil, nil
}

func (m *mockAssignmentsService) FinishSession(ctx context.Context, userID, sessionID uuid.UUID) (*assignments.SessionFinishDTO, error) {
	return nil, nil
}

func (m *mockAssignmentsService) GetExamScale(ctx context.Context) ([]assignments.ExamScaleEntry, error) {
	return nil, nil
}

func (m *mockAssignmentsService) UpdateExamScale(ctx context.Context, entries []assignments.ExamScaleEntry) error {
	return nil
}

type mockUploader struct {
	uploadedURL string
	uploadErr   error
}

func (m *mockUploader) Upload(ctx context.Context, bucket, objectKey string, reader io.Reader, size int64, contentType string) (string, error) {
	if m.uploadErr != nil {
		return "", m.uploadErr
	}
	return m.uploadedURL, nil
}

func TestTeacherAutomationFacade_ListTools(t *testing.T) {
	facade := mcp.NewTeacherAutomationFacade(nil, nil, nil, nil, "test-bucket")
	tools := facade.ListTools()

	if len(tools) < 5 {
		t.Fatalf("expected at least 5 tools, got %d", len(tools))
	}

	toolMap := make(map[string]mcp.Tool)
	for _, tool := range tools {
		toolMap[tool.Name] = tool
	}

	expectedTools := []string{
		"create_teacher_task",
		"upload_task_attachment",
		"build_variant",
		"list_teacher_tasks",
		"get_assignment_matrix",
		"get_session_dossier",
	}

	for _, name := range expectedTools {
		tool, ok := toolMap[name]
		if !ok {
			t.Errorf("tool %q missing from registered tools", name)
		}
		if tool.Description == "" {
			t.Errorf("tool %q has empty description", name)
		}
		if tool.InputSchema.Type != "object" {
			t.Errorf("tool %q schema type expected 'object', got %q", name, tool.InputSchema.Type)
		}
	}
}

func TestTeacherAutomationFacade_CreateTeacherTask(t *testing.T) {
	mockProc := &mockProctoringService{
		createdTask: &tasks.TaskDTO{
			ID:         101,
			Difficulty: 2,
			TextMD:     "Тестовая задача",
		},
	}
	facade := mcp.NewTeacherAutomationFacade(mockProc, nil, nil, nil, "test-bucket")
	teacherID := uuid.New()

	args := mcp.CreateTaskArgs{
		KimNumbers: []int{24},
		TopicID:    24,
		Difficulty: 2,
		TextMD:     "Текстовый файл 24.txt...",
		AnswerConfig: tasks.AnswerConfig{
			AnswerType: "single",
			MaxScore:   1,
		},
		CorrectAnswer: tasks.CorrectAnswer{
			AnswerType: "single",
			Accepted:   []string{"42"},
		},
	}
	rawArgs, _ := json.Marshal(args)

	res, err := facade.CallTool(context.Background(), teacherID, "create_teacher_task", rawArgs)
	if err != nil {
		t.Fatalf("unexpected call error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error: %s", res.Content[0].Text)
	}

	var dto tasks.TaskDTO
	if err := json.Unmarshal([]byte(res.Content[0].Text), &dto); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if dto.ID != 101 {
		t.Errorf("expected task ID 101, got %d", dto.ID)
	}
}

func TestTeacherAutomationFacade_UploadTaskAttachment(t *testing.T) {
	mockUp := &mockUploader{
		uploadedURL: "https://minio.local/media/tasks/101/24.txt",
	}
	facade := mcp.NewTeacherAutomationFacade(nil, nil, nil, mockUp, "test-bucket")

	content := "ABCDEF12345"
	b64 := base64.StdEncoding.EncodeToString([]byte(content))

	args := mcp.UploadAttachmentArgs{
		TaskID:        101,
		Filename:      "24.txt",
		ContentBase64: b64,
		ContentType:   "text/plain",
	}
	rawArgs, _ := json.Marshal(args)

	res, err := facade.CallTool(context.Background(), uuid.New(), "upload_task_attachment", rawArgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got: %s", res.Content[0].Text)
	}

	var attachment tasks.FileAttachment
	if err := json.Unmarshal([]byte(res.Content[0].Text), &attachment); err != nil {
		t.Fatalf("failed to unmarshal attachment: %v", err)
	}
	if attachment.Name != "24.txt" || attachment.URL != mockUp.uploadedURL {
		t.Errorf("unexpected attachment: %+v", attachment)
	}
}

func TestTeacherAutomationFacade_BuildVariant(t *testing.T) {
	roomID := uuid.New()
	mockAssign := &mockAssignmentsService{
		createdAssignment: &assignments.AssignmentDTO{
			ID:         uuid.New(),
			RoomID:     &roomID,
			Title:      "Проверочная работа",
			Mode:       assignments.ModeHomework,
			TasksCount: 3,
		},
	}
	facade := mcp.NewTeacherAutomationFacade(nil, nil, mockAssign, nil, "test-bucket")

	args := mcp.BuildVariantArgs{
		RoomID:  roomID.String(),
		Title:   "Проверочная работа",
		Mode:    "homework",
		TaskIDs: []int{101, 102, 103},
		Settings: assignments.AssignmentSettings{
			MaxAttempts: 3,
		},
	}
	rawArgs, _ := json.Marshal(args)

	res, err := facade.CallTool(context.Background(), uuid.New(), "build_variant", rawArgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got: %s", res.Content[0].Text)
	}

	var dto assignments.AssignmentDTO
	if err := json.Unmarshal([]byte(res.Content[0].Text), &dto); err != nil {
		t.Fatalf("failed to unmarshal assignment: %v", err)
	}
	if dto.Title != "Проверочная работа" || dto.TasksCount != 3 {
		t.Errorf("unexpected assignment DTO: %+v", dto)
	}
}

func TestTeacherAutomationFacade_UnknownTool(t *testing.T) {
	facade := mcp.NewTeacherAutomationFacade(nil, nil, nil, nil, "")
	_, err := facade.CallTool(context.Background(), uuid.New(), "non_existent_tool", []byte("{}"))
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}
