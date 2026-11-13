package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"backend/internal/domain/assignments"
	"backend/internal/transport/http/middleware"
	"backend/internal/transport/http/response"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// AssignmentHandler handles HTTP endpoints for assignments, sessions, and self-mock exams.
type AssignmentHandler struct {
	svc assignments.Service
}

// NewAssignmentHandler constructs an AssignmentHandler.
func NewAssignmentHandler(svc assignments.Service) *AssignmentHandler {
	return &AssignmentHandler{svc: svc}
}

// RegisterRoutes mounts all assignment, exam, and session endpoints onto a chi router.
func (h *AssignmentHandler) RegisterRoutes(r chi.Router) {
	r.Post("/rooms/{room_id}/assignments", h.CreateAssignment)
	r.Patch("/assignments/{id}", h.UpdateAssignment)
	r.Delete("/assignments/{id}", h.DeleteAssignment)
	r.Post("/exams/self-start", h.SelfStartExam)
	r.Post("/assignments/{id}/start", h.StartSession)
	r.Get("/sessions/{id}", h.GetSession)
	r.Post("/sessions/{id}/tasks/{task_id}/answer", h.AnswerTask)
	r.Post("/sessions/{id}/finish", h.FinishSession)
}

// Routes returns sub-router when mounted directly under /assignments.
func (h *AssignmentHandler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Patch("/{id}", h.UpdateAssignment)
	r.Delete("/{id}", h.DeleteAssignment)
	r.Post("/{id}/start", h.StartSession)
	return r
}

// ExamRoutes returns sub-router when mounted directly under /exams.
func (h *AssignmentHandler) ExamRoutes() chi.Router {
	r := chi.NewRouter()
	r.Post("/self-start", h.SelfStartExam)
	return r
}

// SessionRoutes returns sub-router when mounted directly under /sessions.
func (h *AssignmentHandler) SessionRoutes(inspectHandler http.HandlerFunc) chi.Router {
	r := chi.NewRouter()
	r.Get("/{id}", h.GetSession)
	r.Post("/{id}/tasks/{task_id}/answer", h.AnswerTask)
	r.Post("/{id}/finish", h.FinishSession)
	if inspectHandler != nil {
		r.Get("/{id}/inspect", inspectHandler)
	}
	return r
}

// ServeHTTP satisfies http.Handler interface for HandlerRegistry.
func (h *AssignmentHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.Routes().ServeHTTP(w, r)
}

// CreateAssignment handles POST /api/v1/rooms/{room_id}/assignments.
func (h *AssignmentHandler) CreateAssignment(w http.ResponseWriter, r *http.Request) {
	userCtx := middleware.MustGetUserContext(r.Context())
	if !userCtx.IsAuthenticated {
		response.Unauthorized(w, "UNAUTHORIZED", "Требуется авторизация")
		return
	}

	roomIDStr := chi.URLParam(r, "room_id")
	roomID, err := uuid.Parse(roomIDStr)
	if err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", "Некорректный UUID группы", nil)
		return
	}

	var req assignments.CreateAssignmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", "Некорректный JSON тела запроса", nil)
		return
	}

	dto, err := h.svc.CreateAssignment(r.Context(), userCtx.UserID, roomID, req)
	if err != nil {
		switch {
		case errors.Is(err, assignments.ErrForbidden):
			response.Forbidden(w, "FORBIDDEN", "Создавать работы в группе может только её преподаватель")
		default:
			response.BadRequest(w, "VALIDATION_ERROR", err.Error(), nil)
		}
		return
	}

	response.JSON(w, http.StatusCreated, dto)
}

// UpdateAssignment handles PATCH /api/v1/assignments/{id}.
func (h *AssignmentHandler) UpdateAssignment(w http.ResponseWriter, r *http.Request) {
	userCtx := middleware.MustGetUserContext(r.Context())
	if !userCtx.IsAuthenticated {
		response.Unauthorized(w, "UNAUTHORIZED", "Требуется авторизация")
		return
	}

	idStr := chi.URLParam(r, "id")
	assignmentID, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", "Некорректный UUID работы", nil)
		return
	}

	var req assignments.UpdateAssignmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", "Некорректный JSON тела запроса", nil)
		return
	}

	dto, err := h.svc.UpdateAssignment(r.Context(), userCtx.UserID, assignmentID, req)
	if err != nil {
		switch {
		case errors.Is(err, assignments.ErrNotFound):
			response.NotFound(w, "NOT_FOUND", "Работа не найдена")
		case errors.Is(err, assignments.ErrForbidden):
			response.Forbidden(w, "FORBIDDEN", "Недостаточно прав для редактирования работы")
		default:
			response.BadRequest(w, "VALIDATION_ERROR", err.Error(), nil)
		}
		return
	}

	response.JSON(w, http.StatusOK, dto)
}

// DeleteAssignment handles DELETE /api/v1/assignments/{id}.
func (h *AssignmentHandler) DeleteAssignment(w http.ResponseWriter, r *http.Request) {
	userCtx := middleware.MustGetUserContext(r.Context())
	if !userCtx.IsAuthenticated {
		response.Unauthorized(w, "UNAUTHORIZED", "Требуется авторизация")
		return
	}

	idStr := chi.URLParam(r, "id")
	assignmentID, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", "Некорректный UUID работы", nil)
		return
	}

	if err := h.svc.DeleteAssignment(r.Context(), userCtx.UserID, assignmentID); err != nil {
		switch {
		case errors.Is(err, assignments.ErrNotFound):
			response.NotFound(w, "NOT_FOUND", "Работа не найдена")
		case errors.Is(err, assignments.ErrForbidden):
			response.Forbidden(w, "FORBIDDEN", "Удалять работу может только её создатель")
		default:
			response.InternalServerError(w, "Ошибка удаления работы")
		}
		return
	}

	response.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// SelfStartExam handles POST /api/v1/exams/self-start.
func (h *AssignmentHandler) SelfStartExam(w http.ResponseWriter, r *http.Request) {
	userCtx := middleware.MustGetUserContext(r.Context())
	if !userCtx.IsAuthenticated {
		response.Unauthorized(w, "UNAUTHORIZED", "Требуется авторизация")
		return
	}

	var req assignments.SelfStartRequest
	if r.Body != nil && r.ContentLength > 0 {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	resp, err := h.svc.SelfStartExam(r.Context(), userCtx.UserID, req)
	if err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", err.Error(), nil)
		return
	}

	response.JSON(w, http.StatusCreated, resp)
}

// StartSession handles POST /api/v1/assignments/{id}/start.
func (h *AssignmentHandler) StartSession(w http.ResponseWriter, r *http.Request) {
	userCtx := middleware.MustGetUserContext(r.Context())
	if !userCtx.IsAuthenticated {
		response.Unauthorized(w, "UNAUTHORIZED", "Требуется авторизация")
		return
	}

	idStr := chi.URLParam(r, "id")
	assignmentID, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", "Некорректный UUID работы", nil)
		return
	}

	resp, err := h.svc.StartSession(r.Context(), userCtx.UserID, assignmentID)
	if err != nil {
		switch {
		case errors.Is(err, assignments.ErrNotFound):
			response.NotFound(w, "NOT_FOUND", "Работа не найдена")
		case errors.Is(err, assignments.ErrForbidden):
			response.Forbidden(w, "FORBIDDEN", "Вы не являетесь участником данной группы")
		case errors.Is(err, assignments.ErrDeadlinePassed):
			response.Forbidden(w, "DEADLINE_PASSED", "Срок сдачи работы истёк")
		case errors.Is(err, assignments.ErrSessionFinished):
			response.BadRequest(w, "SESSION_FINISHED", "Сессия уже завершена", nil)
		default:
			response.BadRequest(w, "VALIDATION_ERROR", err.Error(), nil)
		}
		return
	}

	response.JSON(w, http.StatusOK, resp)
}

// GetSession handles GET /api/v1/sessions/{id}.
func (h *AssignmentHandler) GetSession(w http.ResponseWriter, r *http.Request) {
	userCtx := middleware.MustGetUserContext(r.Context())
	if !userCtx.IsAuthenticated {
		response.Unauthorized(w, "UNAUTHORIZED", "Требуется авторизация")
		return
	}

	idStr := chi.URLParam(r, "id")
	sessionID, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", "Некорректный UUID сессии", nil)
		return
	}

	detail, err := h.svc.GetSession(r.Context(), userCtx.UserID, sessionID)
	if err != nil {
		switch {
		case errors.Is(err, assignments.ErrNotFound):
			response.NotFound(w, "NOT_FOUND", "Сессия не найдена")
		case errors.Is(err, assignments.ErrForbidden):
			response.Forbidden(w, "FORBIDDEN", "Доступ к данной сессии запрещён")
		default:
			response.InternalServerError(w, "Ошибка получения данных сессии")
		}
		return
	}

	response.JSON(w, http.StatusOK, detail)
}

// AnswerTask handles POST /api/v1/sessions/{id}/tasks/{task_id}/answer.
func (h *AssignmentHandler) AnswerTask(w http.ResponseWriter, r *http.Request) {
	userCtx := middleware.MustGetUserContext(r.Context())
	if !userCtx.IsAuthenticated {
		response.Unauthorized(w, "UNAUTHORIZED", "Требуется авторизация")
		return
	}

	sessionIDStr := chi.URLParam(r, "id")
	sessionID, err := uuid.Parse(sessionIDStr)
	if err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", "Некорректный UUID сессии", nil)
		return
	}

	taskIDStr := chi.URLParam(r, "task_id")
	taskID, err := strconv.Atoi(taskIDStr)
	if err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", "Некорректный идентификатор задачи", nil)
		return
	}

	var req assignments.AnswerTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", "Некорректный JSON в ответе", nil)
		return
	}

	resp, err := h.svc.AnswerTask(r.Context(), userCtx.UserID, sessionID, taskID, req)
	if err != nil {
		switch {
		case errors.Is(err, assignments.ErrNotFound):
			response.NotFound(w, "NOT_FOUND", "Сессия или задача не найдена")
		case errors.Is(err, assignments.ErrForbidden):
			response.Forbidden(w, "FORBIDDEN", "Отправлять ответы может только владелец сессии")
		case errors.Is(err, assignments.ErrDeadlinePassed):
			response.BadRequest(w, "DEADLINE_PASSED", "Срок сдачи работы истёк", nil)
		case errors.Is(err, assignments.ErrSessionFinished):
			response.BadRequest(w, "SESSION_FINISHED", "Сессия завершена или истёк таймер", nil)
		case errors.Is(err, assignments.ErrAttemptsExceeded):
			response.BadRequest(w, "ATTEMPTS_EXCEEDED", "Превышено максимальное число попыток", nil)
		default:
			response.BadRequest(w, "VALIDATION_ERROR", err.Error(), nil)
		}
		return
	}

	response.JSON(w, http.StatusOK, resp)
}

// FinishSession handles POST /api/v1/sessions/{id}/finish.
func (h *AssignmentHandler) FinishSession(w http.ResponseWriter, r *http.Request) {
	userCtx := middleware.MustGetUserContext(r.Context())
	if !userCtx.IsAuthenticated {
		response.Unauthorized(w, "UNAUTHORIZED", "Требуется авторизация")
		return
	}

	idStr := chi.URLParam(r, "id")
	sessionID, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", "Некорректный UUID сессии", nil)
		return
	}

	dto, err := h.svc.FinishSession(r.Context(), userCtx.UserID, sessionID)
	if err != nil {
		switch {
		case errors.Is(err, assignments.ErrNotFound):
			response.NotFound(w, "NOT_FOUND", "Сессия не найдена")
		case errors.Is(err, assignments.ErrForbidden):
			response.Forbidden(w, "FORBIDDEN", "Завершить работу может только её исполнитель")
		case errors.Is(err, assignments.ErrSessionFinished):
			response.BadRequest(w, "SESSION_FINISHED", "Сессия уже была завершена ранее", nil)
		default:
			response.InternalServerError(w, "Ошибка завершения сессии")
		}
		return
	}

	response.JSON(w, http.StatusOK, dto)
}

// GetExamScale handles GET /api/v1/admin/exam-scale.
func (h *AssignmentHandler) GetExamScale(w http.ResponseWriter, r *http.Request) {
	userCtx := middleware.MustGetUserContext(r.Context())
	if !userCtx.IsAuthenticated {
		response.Unauthorized(w, "UNAUTHORIZED", "Требуется авторизация")
		return
	}
	if !userCtx.IsSuperuser {
		response.Forbidden(w, "FORBIDDEN", "Доступ разрешён только администраторам")
		return
	}

	entries, err := h.svc.GetExamScale(r.Context())
	if err != nil {
		response.InternalServerError(w, "Ошибка получения шкалы перевода баллов")
		return
	}

	response.JSON(w, http.StatusOK, map[string]any{"items": entries})
}

// UpdateExamScale handles PUT /api/v1/admin/exam-scale.
func (h *AssignmentHandler) UpdateExamScale(w http.ResponseWriter, r *http.Request) {
	userCtx := middleware.MustGetUserContext(r.Context())
	if !userCtx.IsAuthenticated {
		response.Unauthorized(w, "UNAUTHORIZED", "Требуется авторизация")
		return
	}
	if !userCtx.IsSuperuser {
		response.Forbidden(w, "FORBIDDEN", "Доступ разрешён только администраторам")
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", "Ошибка чтения тела запроса", nil)
		return
	}
	trimmed := bytes.TrimSpace(bodyBytes)
	if len(trimmed) == 0 {
		response.BadRequest(w, "VALIDATION_ERROR", "Пустое тело запроса", nil)
		return
	}

	var entries []assignments.ExamScaleEntry
	if trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &entries); err != nil {
			response.BadRequest(w, "VALIDATION_ERROR", "Некорректный JSON шкалы баллов", nil)
			return
		}
	} else {
		var wrapper struct {
			Items []assignments.ExamScaleEntry `json:"items"`
			Scale []assignments.ExamScaleEntry `json:"scale"`
		}
		if err := json.Unmarshal(trimmed, &wrapper); err != nil {
			response.BadRequest(w, "VALIDATION_ERROR", "Некорректный JSON тела запроса", nil)
			return
		}
		if len(wrapper.Items) > 0 {
			entries = wrapper.Items
		} else {
			entries = wrapper.Scale
		}
	}

	if len(entries) == 0 {
		response.BadRequest(w, "VALIDATION_ERROR", "Список значений шкалы не может быть пустым", nil)
		return
	}
	for _, e := range entries {
		if e.PrimaryScore < 0 || e.PrimaryScore > 29 {
			response.BadRequest(w, "VALIDATION_ERROR", "Первичный балл должен быть в диапазоне от 0 до 29", nil)
			return
		}
		if e.SecondaryScore < 0 || e.SecondaryScore > 100 {
			response.BadRequest(w, "VALIDATION_ERROR", "Тестовый балл должен быть в диапазоне от 0 до 100", nil)
			return
		}
	}

	if err := h.svc.UpdateExamScale(r.Context(), entries); err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", err.Error(), nil)
		return
	}

	updated, err := h.svc.GetExamScale(r.Context())
	if err != nil {
		updated = entries
	}
	response.JSON(w, http.StatusOK, map[string]any{"ok": true, "items": updated})
}
