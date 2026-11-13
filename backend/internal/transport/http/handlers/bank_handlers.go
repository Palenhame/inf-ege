package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"backend/internal/domain/tasks"
	"backend/internal/transport/http/middleware"
	"backend/internal/transport/http/response"
)

// BankHandler provides HTTP handlers for topics, task bank, and admin/teacher management.
type BankHandler struct {
	tasksService tasks.Service
	importer     *tasks.Importer
	storage      tasks.MediaStorage
	bucket       string
}

// NewBankHandler creates a new BankHandler.
func NewBankHandler(tasksService tasks.Service, importer *tasks.Importer, storage tasks.MediaStorage, bucket string) *BankHandler {
	if bucket == "" {
		bucket = "egeinf-media"
	}
	return &BankHandler{
		tasksService: tasksService,
		importer:     importer,
		storage:      storage,
		bucket:       bucket,
	}
}

// GetTopics handles GET /api/v1/topics.
func (h *BankHandler) GetTopics(w http.ResponseWriter, r *http.Request) {
	groups, err := h.tasksService.GetTopics(r.Context())
	if err != nil {
		response.InternalServerError(w, "Ошибка получения каталога тем")
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"items": groups})
}

// ListBankTasks handles GET /api/v1/bank/tasks.
func (h *BankHandler) ListBankTasks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	status := r.URL.Query().Get("status")
	sortParam := r.URL.Query().Get("sort")

	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if val, err := strconv.Atoi(l); err == nil && val > 0 && val <= 100 {
			limit = val
		}
	}

	offset := 0
	if o := r.URL.Query().Get("offset"); o != "" {
		if val, err := strconv.Atoi(o); err == nil && val >= 0 {
			offset = val
		}
	}

	var kim *int
	if k := r.URL.Query().Get("kim"); k != "" {
		if val, err := strconv.Atoi(k); err == nil && val >= 1 && val <= 27 {
			kim = &val
		}
	}

	var topicID *int
	if t := r.URL.Query().Get("topic_id"); t != "" {
		if val, err := strconv.Atoi(t); err == nil && val > 0 {
			topicID = &val
		}
	}

	var difficulty *int
	if d := r.URL.Query().Get("difficulty"); d != "" {
		if val, err := strconv.Atoi(d); err == nil && val >= 1 && val <= 4 {
			difficulty = &val
		}
	}

	isPublic := true
	filter := tasks.ListTasksFilter{
		Query:        q,
		Kim:          kim,
		TopicID:      topicID,
		Difficulty:   difficulty,
		Status:       status,
		Sort:         sortParam,
		Limit:        limit,
		Offset:       offset,
		IsPublicBank: &isPublic,
	}

	uCtx, ok := middleware.GetUserContext(r.Context())
	if ok && uCtx.IsAuthenticated {
		filter.UserID = &uCtx.UserID
	}

	items, total, err := h.tasksService.ListTasks(r.Context(), filter)
	if err != nil {
		response.InternalServerError(w, "Ошибка поиска в банке задач")
		return
	}

	response.JSONPaginated(w, http.StatusOK, items, total, limit, offset)
}

// GetBankTask handles GET /api/v1/bank/tasks/{id}.
func (h *BankHandler) GetBankTask(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		response.BadRequest(w, "VALIDATION_ERROR", "Неверный идентификатор задачи", nil)
		return
	}

	var userID *uuid.UUID
	uCtx, ok := middleware.GetUserContext(r.Context())
	if ok && uCtx.IsAuthenticated {
		userID = &uCtx.UserID
	}

	task, err := h.tasksService.GetTask(r.Context(), id, userID)
	if err != nil {
		if err == tasks.ErrTaskLockedByActiveExam {
			response.Error(w, http.StatusForbidden, "TASK_LOCKED_BY_ACTIVE_EXAM", "Задача заблокирована активным экзаменом", nil)
			return
		}
		if err == tasks.ErrTaskNotFound {
			response.NotFound(w, "NOT_FOUND", "Задача не найдена")
			return
		}
		response.InternalServerError(w, "Ошибка получения задачи")
		return
	}

	response.JSON(w, http.StatusOK, task)
}

// SubmitBankTask handles POST /api/v1/bank/tasks/{id}/submit.
func (h *BankHandler) SubmitBankTask(w http.ResponseWriter, r *http.Request) {
	uCtx, ok := middleware.GetUserContext(r.Context())
	if !ok || !uCtx.IsAuthenticated {
		response.Unauthorized(w, "UNAUTHORIZED", "Требуется авторизация для отправки ответа")
		return
	}

	idStr := chi.URLParam(r, "id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		response.BadRequest(w, "VALIDATION_ERROR", "Неверный идентификатор задачи", nil)
		return
	}

	var req tasks.TaskSubmitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", "Некорректное тело запроса", nil)
		return
	}

	res, err := h.tasksService.SubmitTask(r.Context(), id, uCtx.UserID, req)
	if err != nil {
		if err == tasks.ErrTaskLockedByActiveExam {
			response.Error(w, http.StatusForbidden, "TASK_LOCKED_BY_ACTIVE_EXAM", "Задача заблокирована активным экзаменом", nil)
			return
		}
		if err == tasks.ErrTaskNotFound {
			response.NotFound(w, "NOT_FOUND", "Задача не найдена")
			return
		}
		response.InternalServerError(w, "Ошибка проверки ответа")
		return
	}

	response.JSON(w, http.StatusOK, res)
}

// CreateAdminTask handles POST /api/v1/admin/tasks.
func (h *BankHandler) CreateAdminTask(w http.ResponseWriter, r *http.Request) {
	var t tasks.Task
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", "Некорректные данные задачи", nil)
		return
	}

	newID, err := h.tasksService.CreateAdminTask(r.Context(), &t)
	if err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", err.Error(), nil)
		return
	}

	created, err := h.tasksService.GetTask(r.Context(), newID, nil)
	if err != nil {
		response.JSON(w, http.StatusCreated, map[string]any{"id": newID})
		return
	}

	response.JSON(w, http.StatusCreated, created)
}

// UpdateAdminTask handles PATCH /api/v1/admin/tasks/{id}.
func (h *BankHandler) UpdateAdminTask(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		response.BadRequest(w, "VALIDATION_ERROR", "Неверный ID задачи", nil)
		return
	}

	var t tasks.Task
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", "Некорректные данные задачи", nil)
		return
	}

	if err := h.tasksService.UpdateTask(r.Context(), id, &t); err != nil {
		if err == tasks.ErrTaskNotFound {
			response.NotFound(w, "NOT_FOUND", "Задача не найдена")
			return
		}
		response.BadRequest(w, "VALIDATION_ERROR", err.Error(), nil)
		return
	}

	updated, err := h.tasksService.GetTask(r.Context(), id, nil)
	if err != nil {
		response.JSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}

	response.JSON(w, http.StatusOK, updated)
}

// DeleteAdminTask handles DELETE /api/v1/admin/tasks/{id}.
func (h *BankHandler) DeleteAdminTask(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		response.BadRequest(w, "VALIDATION_ERROR", "Неверный ID задачи", nil)
		return
	}

	if err := h.tasksService.DeleteTask(r.Context(), id); err != nil {
		if err == tasks.ErrTaskNotFound {
			response.NotFound(w, "NOT_FOUND", "Задача не найдена")
			return
		}
		response.InternalServerError(w, "Ошибка удаления задачи")
		return
	}

	response.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ImportTasks handles POST /api/v1/admin/tasks/import.
func (h *BankHandler) ImportTasks(w http.ResponseWriter, r *http.Request) {
	if h.importer == nil {
		response.InternalServerError(w, "Импортёр задач не сконфигурирован")
		return
	}

	dryRun := r.URL.Query().Get("dry_run") == "true"

	// Parse multipart form with 150 MB max memory limit
	if err := r.ParseMultipartForm(150 << 20); err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", "Не удалось прочитать multipart-форму", nil)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", "Файл для импорта не передан (ожидалось поле 'file')", nil)
		return
	}
	defer file.Close()

	fileName := strings.ToLower(header.Filename)
	isZip := strings.HasSuffix(fileName, ".zip")

	report, err := h.importer.Import(r.Context(), file, header.Size, isZip, dryRun)
	if err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", err.Error(), nil)
		return
	}

	response.JSON(w, http.StatusOK, report)
}

// UploadMedia handles POST /api/v1/admin/media/upload.
func (h *BankHandler) UploadMedia(w http.ResponseWriter, r *http.Request) {
	if h.storage == nil {
		response.InternalServerError(w, "Хранилище S3 не сконфигурировано")
		return
	}

	if err := r.ParseMultipartForm(50 << 20); err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", "Ошибка парсинга формы", nil)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", "Файл не передан", nil)
		return
	}
	defer file.Close()

	kind := r.FormValue("kind")
	if kind == "" {
		kind = "image"
	}

	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(header.Filename), "."))
	if ext == "" {
		ext = "bin"
	}

	folder := "images"
	if kind == "file" {
		folder = "files"
	}
	s3Key := fmt.Sprintf("tasks/%s/%s.%s", folder, uuid.New().String(), ext)

	contentType := header.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	url, err := h.storage.Upload(r.Context(), h.bucket, s3Key, file, header.Size, contentType)
	if err != nil {
		response.InternalServerError(w, "Ошибка сохранения файла в хранилище")
		return
	}

	response.JSON(w, http.StatusCreated, map[string]any{
		"s3_key":     s3Key,
		"url":        url,
		"size_bytes": header.Size,
		"ext":        ext,
	})
}

// CreateTopic handles POST /api/v1/admin/topics.
func (h *BankHandler) CreateTopic(w http.ResponseWriter, r *http.Request) {
	var req struct {
		KimNumber int    `json:"kim_number"`
		Name      string `json:"name"`
		SortOrder int    `json:"sort_order"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", "Некорректные данные темы", nil)
		return
	}

	topic, err := h.tasksService.CreateTopic(r.Context(), req.KimNumber, req.Name, req.SortOrder)
	if err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", err.Error(), nil)
		return
	}

	response.JSON(w, http.StatusCreated, topic)
}

// UpdateTopic handles PATCH /api/v1/admin/topics/{id}.
func (h *BankHandler) UpdateTopic(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		response.BadRequest(w, "VALIDATION_ERROR", "Неверный ID темы", nil)
		return
	}

	var req struct {
		Name      string `json:"name"`
		SortOrder int    `json:"sort_order"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", "Некорректные данные темы", nil)
		return
	}

	topic, err := h.tasksService.UpdateTopic(r.Context(), id, req.Name, req.SortOrder)
	if err != nil {
		if err == tasks.ErrTopicNotFound {
			response.NotFound(w, "NOT_FOUND", "Тема не найдена")
			return
		}
		response.BadRequest(w, "VALIDATION_ERROR", err.Error(), nil)
		return
	}

	response.JSON(w, http.StatusOK, topic)
}

// DeleteTopic handles DELETE /api/v1/admin/topics/{id}.
func (h *BankHandler) DeleteTopic(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		response.BadRequest(w, "VALIDATION_ERROR", "Неверный ID темы", nil)
		return
	}

	if err := h.tasksService.DeleteTopic(r.Context(), id); err != nil {
		if err == tasks.ErrTopicNotFound {
			response.NotFound(w, "NOT_FOUND", "Тема не найдена")
			return
		}
		response.InternalServerError(w, "Ошибка удаления темы")
		return
	}

	response.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

// CreateTeacherTask handles POST /api/v1/teacher/tasks.
func (h *BankHandler) CreateTeacherTask(w http.ResponseWriter, r *http.Request) {
	uCtx, ok := middleware.GetUserContext(r.Context())
	if !ok || !uCtx.IsAuthenticated {
		response.Unauthorized(w, "UNAUTHORIZED", "Требуется авторизация")
		return
	}

	var t tasks.Task
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", "Некорректные данные задачи", nil)
		return
	}

	newID, err := h.tasksService.CreateAuthorTask(r.Context(), uCtx.UserID, &t)
	if err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", err.Error(), nil)
		return
	}

	created, err := h.tasksService.GetTask(r.Context(), newID, &uCtx.UserID)
	if err != nil {
		response.JSON(w, http.StatusCreated, map[string]any{"id": newID})
		return
	}

	response.JSON(w, http.StatusCreated, created)
}

// ListTeacherTasks handles GET /api/v1/teacher/tasks.
func (h *BankHandler) ListTeacherTasks(w http.ResponseWriter, r *http.Request) {
	uCtx, ok := middleware.GetUserContext(r.Context())
	if !ok || !uCtx.IsAuthenticated {
		response.Unauthorized(w, "UNAUTHORIZED", "Требуется авторизация")
		return
	}

	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if val, err := strconv.Atoi(l); err == nil && val > 0 && val <= 100 {
			limit = val
		}
	}

	offset := 0
	if o := r.URL.Query().Get("offset"); o != "" {
		if val, err := strconv.Atoi(o); err == nil && val >= 0 {
			offset = val
		}
	}

	q := r.URL.Query().Get("q")

	var kim *int
	if k := r.URL.Query().Get("kim"); k != "" {
		if val, err := strconv.Atoi(k); err == nil && val >= 1 && val <= 27 {
			kim = &val
		}
	}

	var topicID *int
	if t := r.URL.Query().Get("topic_id"); t != "" {
		if val, err := strconv.Atoi(t); err == nil && val > 0 {
			topicID = &val
		}
	}

	filter := tasks.ListTasksFilter{
		Query:   q,
		Kim:     kim,
		TopicID: topicID,
		Limit:   limit,
		Offset:  offset,
	}

	items, total, err := h.tasksService.ListAuthorTasks(r.Context(), uCtx.UserID, filter)
	if err != nil {
		response.InternalServerError(w, "Ошибка получения авторских задач")
		return
	}

	response.JSONPaginated(w, http.StatusOK, items, total, limit, offset)
}
