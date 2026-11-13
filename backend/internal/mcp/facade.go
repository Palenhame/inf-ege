package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"backend/internal/domain/assignments"
	"backend/internal/domain/proctoring"
	"backend/internal/domain/tasks"
	"github.com/google/uuid"
)

var (
	ErrUnknownTool     = errors.New("mcp: unknown tool")
	ErrInvalidArgument = errors.New("mcp: invalid argument")
)

// MediaUploader defines storage operations required for MCP media uploads.
type MediaUploader interface {
	Upload(ctx context.Context, bucket, objectKey string, reader io.Reader, size int64, contentType string) (string, error)
}

// PropertySchema defines a JSON Schema property definition.
type PropertySchema struct {
	Type        string                    `json:"type"`
	Description string                    `json:"description,omitempty"`
	Enum        []string                  `json:"enum,omitempty"`
	Items       *PropertySchema           `json:"items,omitempty"`
	Properties  map[string]PropertySchema `json:"properties,omitempty"`
	Required    []string                  `json:"required,omitempty"`
}

// ToolInputSchema defines standard JSON Schema for MCP tool arguments.
type ToolInputSchema struct {
	Type       string                    `json:"type"`
	Properties map[string]PropertySchema `json:"properties"`
	Required   []string                  `json:"required,omitempty"`
}

// Tool describes an MCP tool declaration.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema ToolInputSchema `json:"inputSchema"`
}

// ContentItem represents a content piece in an MCP ToolCallResult.
type ContentItem struct {
	Type string `json:"type"` // "text" | "json"
	Text string `json:"text,omitempty"`
}

// ToolCallResult represents standard MCP tool execution response.
type ToolCallResult struct {
	Content []ContentItem `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// TeacherAutomationFacade provides the automation interface for AI teacher agents.
type TeacherAutomationFacade struct {
	proctoringSvc  proctoring.Service
	tasksSvc       tasks.Service
	assignmentsSvc assignments.Service
	uploader       MediaUploader
	mediaBucket    string
	tools          map[string]Tool
}

// NewTeacherAutomationFacade creates a new TeacherAutomationFacade.
func NewTeacherAutomationFacade(
	proctoringSvc proctoring.Service,
	tasksSvc tasks.Service,
	assignmentsSvc assignments.Service,
	uploader MediaUploader,
	mediaBucket string,
) *TeacherAutomationFacade {
	if mediaBucket == "" {
		mediaBucket = "egeinf-media"
	}

	facade := &TeacherAutomationFacade{
		proctoringSvc:  proctoringSvc,
		tasksSvc:       tasksSvc,
		assignmentsSvc: assignmentsSvc,
		uploader:       uploader,
		mediaBucket:    mediaBucket,
		tools:          make(map[string]Tool),
	}

	facade.registerDefaultTools()
	return facade
}

func (f *TeacherAutomationFacade) registerDefaultTools() {
	f.tools["create_teacher_task"] = Tool{
		Name:        "create_teacher_task",
		Description: "Creates a new custom teacher author task with polymorphic grading config and solutions.",
		InputSchema: ToolInputSchema{
			Type: "object",
			Properties: map[string]PropertySchema{
				"kim_numbers": {
					Type:        "array",
					Description: "KIM problem numbers (e.g. [24] or [19, 20, 21]).",
					Items:       &PropertySchema{Type: "integer"},
				},
				"topic_id": {
					Type:        "integer",
					Description: "Identifier of the topic within KIM 1..27.",
				},
				"difficulty": {
					Type:        "integer",
					Description: "Difficulty scale 1 (Базовый), 2 (Средний), 3 (Сложный), 4 (Гроб).",
				},
				"source": {
					Type:        "string",
					Description: "Optional origin attribution or school name.",
				},
				"text_md": {
					Type:        "string",
					Description: "Task formulation in Markdown format.",
				},
				"images": {
					Type:        "array",
					Description: "Array of image URLs or storage keys.",
					Items:       &PropertySchema{Type: "string"},
				},
				"answer_config": {
					Type:        "object",
					Description: "Polymorphic grading rules (single, pair, table, triplet_19_21).",
				},
				"correct_answer": {
					Type:        "object",
					Description: "Canonical correct answer matching the answer_config type.",
				},
				"solution_md": {
					Type:        "string",
					Description: "Markdown explanation shown after attempts or release.",
				},
				"solution_code": {
					Type:        "string",
					Description: "Python solution reference code.",
				},
			},
			Required: []string{"kim_numbers", "topic_id", "difficulty", "text_md", "answer_config", "correct_answer"},
		},
	}

	f.tools["upload_task_attachment"] = Tool{
		Name:        "upload_task_attachment",
		Description: "Uploads an attachment file or dataset (e.g. 24.txt, 27_A.txt) for a task into object storage.",
		InputSchema: ToolInputSchema{
			Type: "object",
			Properties: map[string]PropertySchema{
				"task_id": {
					Type:        "integer",
					Description: "Identifier of the task.",
				},
				"filename": {
					Type:        "string",
					Description: "Filename including extension (e.g. '24.txt', 'diagram.png').",
				},
				"content_base64": {
					Type:        "string",
					Description: "Base64-encoded raw file content.",
				},
				"content_type": {
					Type:        "string",
					Description: "MIME type (e.g. 'text/plain', 'image/png').",
				},
			},
			Required: []string{"task_id", "filename", "content_base64"},
		},
	}

	f.tools["build_variant"] = Tool{
		Name:        "build_variant",
		Description: "Assembles and publishes a new assignment variant for a teacher's classroom.",
		InputSchema: ToolInputSchema{
			Type: "object",
			Properties: map[string]PropertySchema{
				"room_id": {
					Type:        "string",
					Description: "Target room UUID where the variant is deployed.",
				},
				"title": {
					Type:        "string",
					Description: "Assignment title (e.g. 'Контрольная работа № 4').",
				},
				"mode": {
					Type:        "string",
					Description: "Test mode: homework, classwork, practice, proctored_test, mock_exam.",
					Enum:        []string{"homework", "classwork", "practice", "proctored_test", "mock_exam"},
				},
				"task_ids": {
					Type:        "array",
					Description: "Ordered list of task IDs comprising the assignment.",
					Items:       &PropertySchema{Type: "integer"},
				},
				"settings": {
					Type:        "object",
					Description: "Execution settings (max_attempts, duration_minutes, starts_at, deadline_at, reveal_policy, etc.).",
				},
			},
			Required: []string{"room_id", "title", "mode", "task_ids"},
		},
	}

	f.tools["list_teacher_tasks"] = Tool{
		Name:        "list_teacher_tasks",
		Description: "Lists author tasks created by the teacher with optional topic and KIM filters.",
		InputSchema: ToolInputSchema{
			Type: "object",
			Properties: map[string]PropertySchema{
				"limit": {
					Type:        "integer",
					Description: "Page size (default 20, max 100).",
				},
				"offset": {
					Type:        "integer",
					Description: "Offset for pagination.",
				},
				"kim": {
					Type:        "integer",
					Description: "Optional filter by KIM number 1..27.",
				},
				"topic_id": {
					Type:        "integer",
					Description: "Optional filter by topic ID.",
				},
				"q": {
					Type:        "string",
					Description: "Optional search query substring.",
				},
			},
		},
	}

	f.tools["get_assignment_matrix"] = Tool{
		Name:        "get_assignment_matrix",
		Description: "Retrieves live real-time matrix snapshot for an assignment showing each student's task progress.",
		InputSchema: ToolInputSchema{
			Type: "object",
			Properties: map[string]PropertySchema{
				"assignment_id": {
					Type:        "string",
					Description: "Assignment UUID to inspect.",
				},
			},
			Required: []string{"assignment_id"},
		},
	}

	f.tools["get_session_dossier"] = Tool{
		Name:        "get_session_dossier",
		Description: "Retrieves detailed student session dossier including proctoring events timeline and Monaco code snapshots.",
		InputSchema: ToolInputSchema{
			Type: "object",
			Properties: map[string]PropertySchema{
				"session_id": {
					Type:        "string",
					Description: "Session UUID to inspect.",
				},
			},
			Required: []string{"session_id"},
		},
	}
}

// ListTools returns all registered MCP tool specifications.
func (f *TeacherAutomationFacade) ListTools() []Tool {
	result := make([]Tool, 0, len(f.tools))
	for _, tool := range f.tools {
		result = append(result, tool)
	}
	return result
}

// GetTool returns the tool specification for a given name.
func (f *TeacherAutomationFacade) GetTool(name string) (Tool, bool) {
	t, ok := f.tools[name]
	return t, ok
}

// CreateTaskArgs defines argument deserialization for create_teacher_task tool.
type CreateTaskArgs struct {
	KimNumbers   []int              `json:"kim_numbers"`
	TopicID      int                `json:"topic_id"`
	Difficulty   int                `json:"difficulty"`
	Source       string             `json:"source"`
	TextMD       string             `json:"text_md"`
	Images       []string           `json:"images"`
	Files        []tasks.FileAttachment `json:"files"`
	AnswerConfig tasks.AnswerConfig `json:"answer_config"`
	CorrectAnswer tasks.CorrectAnswer `json:"correct_answer"`
	SolutionMD   *string            `json:"solution_md"`
	SolutionCode *string            `json:"solution_code"`
}

// UploadAttachmentArgs defines argument deserialization for upload_task_attachment tool.
type UploadAttachmentArgs struct {
	TaskID        int    `json:"task_id"`
	Filename      string `json:"filename"`
	ContentBase64 string `json:"content_base64"`
	ContentType   string `json:"content_type"`
}

// BuildVariantArgs defines argument deserialization for build_variant tool.
type BuildVariantArgs struct {
	RoomID   string                         `json:"room_id"`
	Title    string                         `json:"title"`
	Mode     string                         `json:"mode"`
	TaskIDs  []int                          `json:"task_ids"`
	Settings assignments.AssignmentSettings `json:"settings"`
}

// ListTeacherTasksArgs defines argument deserialization for list_teacher_tasks tool.
type ListTeacherTasksArgs struct {
	Limit   int    `json:"limit"`
	Offset  int    `json:"offset"`
	KIM     *int   `json:"kim"`
	TopicID *int   `json:"topic_id"`
	Q       string `json:"q"`
}

// AssignmentIDArgs defines single assignment_id argument.
type AssignmentIDArgs struct {
	AssignmentID string `json:"assignment_id"`
}

// SessionIDArgs defines single session_id argument.
type SessionIDArgs struct {
	SessionID string `json:"session_id"`
}

// CallTool dispatches a tool execution on behalf of a teacher.
func (f *TeacherAutomationFacade) CallTool(ctx context.Context, teacherID uuid.UUID, toolName string, rawArgs json.RawMessage) (*ToolCallResult, error) {
	switch toolName {
	case "create_teacher_task":
		return f.handleCreateTeacherTask(ctx, teacherID, rawArgs)
	case "upload_task_attachment":
		return f.handleUploadTaskAttachment(ctx, teacherID, rawArgs)
	case "build_variant":
		return f.handleBuildVariant(ctx, teacherID, rawArgs)
	case "list_teacher_tasks":
		return f.handleListTeacherTasks(ctx, teacherID, rawArgs)
	case "get_assignment_matrix":
		return f.handleGetAssignmentMatrix(ctx, teacherID, rawArgs)
	case "get_session_dossier":
		return f.handleGetSessionDossier(ctx, teacherID, rawArgs)
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnknownTool, toolName)
	}
}

func (f *TeacherAutomationFacade) handleCreateTeacherTask(ctx context.Context, teacherID uuid.UUID, rawArgs json.RawMessage) (*ToolCallResult, error) {
	var args CreateTaskArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return errorResult(fmt.Sprintf("Invalid JSON arguments: %v", err)), nil
	}

	if len(args.KimNumbers) == 0 {
		return errorResult("kim_numbers must contain at least 1 number"), nil
	}
	if args.TopicID <= 0 {
		return errorResult("topic_id must be a positive integer"), nil
	}
	if args.Difficulty < 1 || args.Difficulty > 4 {
		return errorResult("difficulty must be between 1 and 4"), nil
	}
	if strings.TrimSpace(args.TextMD) == "" {
		return errorResult("text_md cannot be empty"), nil
	}

	req := proctoring.CreateTeacherTaskRequest{
		KimNumbers:    args.KimNumbers,
		TopicID:       args.TopicID,
		Difficulty:    args.Difficulty,
		Source:        args.Source,
		TextMD:        args.TextMD,
		Images:        args.Images,
		Files:         args.Files,
		AnswerConfig:  args.AnswerConfig,
		CorrectAnswer: args.CorrectAnswer,
		SolutionMD:    args.SolutionMD,
		SolutionCode:  args.SolutionCode,
	}

	taskDTO, err := f.proctoringSvc.CreateTeacherTask(ctx, teacherID, req)
	if err != nil {
		return errorResult(fmt.Sprintf("Failed to create task: %v", err)), nil
	}

	return jsonResult(taskDTO)
}

func (f *TeacherAutomationFacade) handleUploadTaskAttachment(ctx context.Context, _ uuid.UUID, rawArgs json.RawMessage) (*ToolCallResult, error) {
	var args UploadAttachmentArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return errorResult(fmt.Sprintf("Invalid JSON arguments: %v", err)), nil
	}

	if args.TaskID <= 0 {
		return errorResult("task_id must be positive"), nil
	}
	if args.Filename == "" {
		return errorResult("filename cannot be empty"), nil
	}
	if args.ContentBase64 == "" {
		return errorResult("content_base64 cannot be empty"), nil
	}

	decoded, err := base64.StdEncoding.DecodeString(args.ContentBase64)
	if err != nil {
		return errorResult(fmt.Sprintf("Failed to decode base64: %v", err)), nil
	}

	contentType := args.ContentType
	if contentType == "" {
		contentType = http.DetectContentType(decoded)
	}

	objectKey := fmt.Sprintf("tasks/%d/%s", args.TaskID, args.Filename)
	reader := bytes.NewReader(decoded)
	uploadedURL, err := f.uploader.Upload(ctx, f.mediaBucket, objectKey, reader, int64(len(decoded)), contentType)
	if err != nil {
		return errorResult(fmt.Sprintf("Failed to upload to storage: %v", err)), nil
	}

	attachment := tasks.FileAttachment{
		ID:        fmt.Sprintf("task_%d_%s", args.TaskID, args.Filename),
		Name:      args.Filename,
		URL:       uploadedURL,
		SizeBytes: int64(len(decoded)),
	}

	return jsonResult(attachment)
}

func (f *TeacherAutomationFacade) handleBuildVariant(ctx context.Context, teacherID uuid.UUID, rawArgs json.RawMessage) (*ToolCallResult, error) {
	var args BuildVariantArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return errorResult(fmt.Sprintf("Invalid JSON arguments: %v", err)), nil
	}

	roomUUID, err := uuid.Parse(args.RoomID)
	if err != nil {
		return errorResult(fmt.Sprintf("Invalid room_id UUID: %v", err)), nil
	}
	if strings.TrimSpace(args.Title) == "" {
		return errorResult("title cannot be empty"), nil
	}
	if len(args.TaskIDs) == 0 {
		return errorResult("task_ids cannot be empty"), nil
	}

	req := assignments.CreateAssignmentRequest{
		Title:    args.Title,
		Mode:     assignments.AssignmentMode(args.Mode),
		Settings: args.Settings,
		TaskIDs:  args.TaskIDs,
	}

	assignmentDTO, err := f.assignmentsSvc.CreateAssignment(ctx, teacherID, roomUUID, req)
	if err != nil {
		return errorResult(fmt.Sprintf("Failed to build variant: %v", err)), nil
	}

	return jsonResult(assignmentDTO)
}

func (f *TeacherAutomationFacade) handleListTeacherTasks(ctx context.Context, teacherID uuid.UUID, rawArgs json.RawMessage) (*ToolCallResult, error) {
	var args ListTeacherTasksArgs
	if len(rawArgs) > 0 && string(rawArgs) != "null" {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			return errorResult(fmt.Sprintf("Invalid JSON arguments: %v", err)), nil
		}
	}

	if args.Limit <= 0 {
		args.Limit = 20
	}
	if args.Limit > 100 {
		args.Limit = 100
	}

	tasksList, total, err := f.proctoringSvc.ListTeacherTasks(ctx, teacherID, args.Limit, args.Offset, args.KIM, args.TopicID, args.Q)
	if err != nil {
		return errorResult(fmt.Sprintf("Failed to list tasks: %v", err)), nil
	}

	response := map[string]any{
		"items":  tasksList,
		"total":  total,
		"limit":  args.Limit,
		"offset": args.Offset,
	}

	return jsonResult(response)
}

func (f *TeacherAutomationFacade) handleGetAssignmentMatrix(ctx context.Context, teacherID uuid.UUID, rawArgs json.RawMessage) (*ToolCallResult, error) {
	var args AssignmentIDArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return errorResult(fmt.Sprintf("Invalid JSON arguments: %v", err)), nil
	}

	assignmentUUID, err := uuid.Parse(args.AssignmentID)
	if err != nil {
		return errorResult(fmt.Sprintf("Invalid assignment_id UUID: %v", err)), nil
	}

	snapshot, err := f.proctoringSvc.GetMatrixSnapshot(ctx, teacherID, assignmentUUID)
	if err != nil {
		return errorResult(fmt.Sprintf("Failed to get matrix snapshot: %v", err)), nil
	}

	return jsonResult(snapshot)
}

func (f *TeacherAutomationFacade) handleGetSessionDossier(ctx context.Context, teacherID uuid.UUID, rawArgs json.RawMessage) (*ToolCallResult, error) {
	var args SessionIDArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return errorResult(fmt.Sprintf("Invalid JSON arguments: %v", err)), nil
	}

	sessionUUID, err := uuid.Parse(args.SessionID)
	if err != nil {
		return errorResult(fmt.Sprintf("Invalid session_id UUID: %v", err)), nil
	}

	dossier, err := f.proctoringSvc.GetSessionDossier(ctx, teacherID, sessionUUID)
	if err != nil {
		return errorResult(fmt.Sprintf("Failed to get session dossier: %v", err)), nil
	}

	return jsonResult(dossier)
}

func errorResult(msg string) *ToolCallResult {
	return &ToolCallResult{
		IsError: true,
		Content: []ContentItem{
			{
				Type: "text",
				Text: msg,
			},
		},
	}
}

func jsonResult(data any) (*ToolCallResult, error) {
	bytes, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal tool result: %w", err)
	}

	return &ToolCallResult{
		Content: []ContentItem{
			{
				Type: "json",
				Text: string(bytes),
			},
		},
	}, nil
}
