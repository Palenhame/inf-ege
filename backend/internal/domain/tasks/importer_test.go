package tasks_test

import (
	"archive/zip"
	"bytes"
	"context"
	"strings"
	"testing"

	"backend/internal/domain/tasks"
	"github.com/google/uuid"
)

// mockRepo implements tasks.Repository for testing importer.
type mockRepo struct {
	tasks  map[int]*tasks.Task
	topics map[string]int
	nextID int
}

func newMockRepo() *mockRepo {
	return &mockRepo{
		tasks:  make(map[int]*tasks.Task),
		topics: make(map[string]int),
		nextID: 1,
	}
}

func (m *mockRepo) ListBankTasks(ctx context.Context, filter tasks.ListTasksFilter) ([]tasks.TaskDTO, int, error) {
	return nil, 0, nil
}
func (m *mockRepo) GetTaskByID(ctx context.Context, id int, userID *uuid.UUID) (*tasks.TaskDTO, error) {
	return nil, nil
}
func (m *mockRepo) IsTaskInActiveExam(ctx context.Context, taskID int, userID uuid.UUID) (bool, error) {
	return false, nil
}
func (m *mockRepo) RecordSubmission(ctx context.Context, taskID int, userID uuid.UUID, res tasks.GradeResult, answerJSON []byte, recLogID *int64) (*tasks.TaskSubmitResponse, error) {
	return nil, nil
}
func (m *mockRepo) CreateTask(ctx context.Context, task *tasks.Task) (int, error) {
	id := m.nextID
	m.nextID++
	m.tasks[id] = task
	return id, nil
}
func (m *mockRepo) UpdateTask(ctx context.Context, id int, task *tasks.Task) error {
	return nil
}
func (m *mockRepo) DeleteTask(ctx context.Context, id int) error {
	return nil
}
func (m *mockRepo) ListAuthorTasks(ctx context.Context, ownerID uuid.UUID, filter tasks.ListTasksFilter) ([]tasks.TaskDTO, int, error) {
	return nil, 0, nil
}
func (m *mockRepo) GetTopics(ctx context.Context) ([]tasks.KimGroupDTO, error) {
	return nil, nil
}
func (m *mockRepo) CreateTopic(ctx context.Context, kimNumber int, name string, sortOrder int) (*tasks.TopicDTO, error) {
	return nil, nil
}
func (m *mockRepo) UpdateTopic(ctx context.Context, id int, name string, sortOrder int) (*tasks.TopicDTO, error) {
	return nil, nil
}
func (m *mockRepo) DeleteTopic(ctx context.Context, id int) error {
	return nil
}
func (m *mockRepo) GetOrCreateTopic(ctx context.Context, kimNumber int, name string) (int, error) {
	key := name
	if id, exists := m.topics[key]; exists {
		return id, nil
	}
	id := len(m.topics) + 1
	m.topics[key] = id
	return id, nil
}

func TestImporterCSV(t *testing.T) {
	repo := newMockRepo()
	imp := tasks.NewImporter(repo, nil, "test-bucket")

	csvContent := `kim_numbers,topic_name,difficulty,source,text_md,answer_type,correct_answer_json
24,Строки,3,Демо 2026,"Текстовый файл 24.txt...",single,42
15,Отрезки,2,ФИПИ,"На числовой прямой...",single,"{""answer_type"":""single"",""accepted"":[""15"",""16""]}"
`

	t.Run("dry run parses and validates without modifying repository", func(t *testing.T) {
		report, err := imp.Import(context.Background(), strings.NewReader(csvContent), int64(len(csvContent)), false, true)
		if err != nil {
			t.Fatalf("import dry run failed: %v", err)
		}

		if !report.DryRun {
			t.Errorf("expected dry_run=true")
		}
		if report.TotalRows != 2 {
			t.Errorf("expected 2 total rows, got %d", report.TotalRows)
		}
		if report.ValidCount != 2 {
			t.Errorf("expected 2 valid tasks, got %d", report.ValidCount)
		}
		if len(report.Errors) != 0 {
			t.Errorf("expected 0 errors, got %d: %+v", len(report.Errors), report.Errors)
		}
		if len(repo.tasks) != 0 {
			t.Errorf("expected 0 tasks committed to repository during dry run, got %d", len(repo.tasks))
		}
	})

	t.Run("real import commits tasks to repository", func(t *testing.T) {
		report, err := imp.Import(context.Background(), strings.NewReader(csvContent), int64(len(csvContent)), false, false)
		if err != nil {
			t.Fatalf("real import failed: %v", err)
		}

		if report.DryRun {
			t.Errorf("expected dry_run=false")
		}
		if len(repo.tasks) != 2 {
			t.Errorf("expected 2 tasks committed, got %d", len(repo.tasks))
		}
	})

	t.Run("invalid rows report errors gracefully", func(t *testing.T) {
		invalidCSV := `kim_numbers,topic_name,difficulty,source,text_md,answer_type,correct_answer_json
99,Неизвестный КИМ,5,,"",unknown,""
`
		report, err := imp.Import(context.Background(), strings.NewReader(invalidCSV), int64(len(invalidCSV)), false, true)
		if err != nil {
			t.Fatalf("import failed: %v", err)
		}
		if len(report.Errors) == 0 {
			t.Errorf("expected errors for invalid rows, got 0")
		}
	})
}

func TestImporterZIP(t *testing.T) {
	repo := newMockRepo()
	imp := tasks.NewImporter(repo, nil, "test-bucket")

	// Create an in-memory zip archive
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	csvContent := `kim_numbers,topic_name,difficulty,source,text_md,answer_type,correct_answer_json,images,files
24,Обработка строк,3,2026,"Дано изображение ![рис](media/graph.png)",single,1024,media/graph.png,media/24.txt
`
	fCsv, err := zw.Create("tasks.csv")
	if err != nil {
		t.Fatalf("create zip tasks.csv: %v", err)
	}
	_, _ = fCsv.Write([]byte(csvContent))

	fImg, err := zw.Create("media/graph.png")
	if err != nil {
		t.Fatalf("create zip media/graph.png: %v", err)
	}
	_, _ = fImg.Write([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A})

	fTxt, err := zw.Create("media/24.txt")
	if err != nil {
		t.Fatalf("create zip media/24.txt: %v", err)
	}
	_, _ = fTxt.Write([]byte("ABCABCABC"))

	_ = zw.Close()

	report, err := imp.Import(context.Background(), bytes.NewReader(buf.Bytes()), int64(buf.Len()), true, true)
	if err != nil {
		t.Fatalf("import zip dry run failed: %v", err)
	}

	if report.ValidCount != 1 {
		t.Errorf("expected 1 valid task from zip, got %d", report.ValidCount)
	}
	if len(report.PreviewTasks) > 0 {
		preview := report.PreviewTasks[0]
		if !strings.Contains(preview.TextMD, "/storage/tasks/images/") {
			t.Errorf("expected image link in text_md rewritten to /storage/tasks/images/, got: %s", preview.TextMD)
		}
		if len(preview.Files) != 1 || !strings.Contains(preview.Files[0].URL, "/storage/tasks/files/") {
			t.Errorf("expected file attachment URL rewritten to /storage/tasks/files/, got: %+v", preview.Files)
		}
	}
}
