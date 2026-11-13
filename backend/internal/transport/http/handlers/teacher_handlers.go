package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"backend/internal/domain/proctoring"
	"backend/internal/domain/tasks"
	"backend/internal/transport/http/middleware"
	"backend/internal/transport/http/response"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// TeacherHandler handles HTTP endpoints for proctoring live matrix, session inspection, and teacher author tasks.
type TeacherHandler struct {
	svc proctoring.Service
}

// NewTeacherHandler constructs a TeacherHandler.
func NewTeacherHandler(svc proctoring.Service) *TeacherHandler {
	return &TeacherHandler{svc: svc}
}

// RegisterRoutes mounts teacher and proctoring endpoints onto a chi router.
func (h *TeacherHandler) RegisterRoutes(r chi.Router) {
	r.Get("/assignments/{id}/matrix", h.GetMatrixSnapshot)
	r.Get("/sessions/{id}/inspect", h.GetSessionDossier)
	r.Post("/teacher/tasks", h.CreateTeacherTask)
	r.Get("/teacher/tasks", h.ListTeacherTasks)
}

// Routes returns sub-router when mounted directly under /teacher.
func (h *TeacherHandler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Post("/tasks", h.CreateTeacherTask)
	r.Get("/tasks", h.ListTeacherTasks)
	return r
}

// ServeHTTP satisfies http.Handler interface for HandlerRegistry.
func (h *TeacherHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.Routes().ServeHTTP(w, r)
}

// GetMatrixSnapshot handles GET /api/v1/assignments/{id}/matrix.
// Accessible even if the teacher's subscription is expired (Read-Only analytics mode).
func (h *TeacherHandler) GetMatrixSnapshot(w http.ResponseWriter, r *http.Request) {
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

	snapshot, err := h.svc.GetMatrixSnapshot(r.Context(), userCtx.UserID, assignmentID)
	if err != nil {
		switch {
		case errors.Is(err, proctoring.ErrNotFound):
			response.NotFound(w, "NOT_FOUND", "Работа не найдена")
		case errors.Is(err, proctoring.ErrForbidden):
			response.Forbidden(w, "FORBIDDEN", "Просмотр матрицы доступен только преподавателю работы")
		default:
			response.InternalServerError(w, "Ошибка получения live-матрицы")
		}
		return
	}

	response.JSON(w, http.StatusOK, snapshot)
}

// GetSessionDossier handles GET /api/v1/sessions/{id}/inspect.
func (h *TeacherHandler) GetSessionDossier(w http.ResponseWriter, r *http.Request) {
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

	dossier, err := h.svc.GetSessionDossier(r.Context(), userCtx.UserID, sessionID)
	if err != nil {
		switch {
		case errors.Is(err, proctoring.ErrNotFound):
			response.NotFound(w, "NOT_FOUND", "Сессия не найдена")
		case errors.Is(err, proctoring.ErrForbidden):
			response.Forbidden(w, "FORBIDDEN", "Инспекция сессии доступна только преподавателю")
		default:
			response.InternalServerError(w, "Ошибка выгрузки досье инспекции")
		}
		return
	}

	response.JSON(w, http.StatusOK, dossier)
}

// CreateTeacherTask handles POST /api/v1/teacher/tasks.
func (h *TeacherHandler) CreateTeacherTask(w http.ResponseWriter, r *http.Request) {
	userCtx := middleware.MustGetUserContext(r.Context())
	if !userCtx.IsAuthenticated {
		response.Unauthorized(w, "UNAUTHORIZED", "Требуется авторизация")
		return
	}

	if !userCtx.IsSuperuser && userCtx.Role != "teacher" {
		response.Forbidden(w, "FORBIDDEN", "Создавать авторские задачи могут только преподаватели")
		return
	}

	var req proctoring.CreateTeacherTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", "Некорректный JSON тела запроса", nil)
		return
	}

	taskDTO, err := h.svc.CreateTeacherTask(r.Context(), userCtx.UserID, req)
	if err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", err.Error(), nil)
		return
	}

	response.JSON(w, http.StatusCreated, taskDTO)
}

// ListTeacherTasks handles GET /api/v1/teacher/tasks.
func (h *TeacherHandler) ListTeacherTasks(w http.ResponseWriter, r *http.Request) {
	userCtx := middleware.MustGetUserContext(r.Context())
	if !userCtx.IsAuthenticated {
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

	var kim *int
	if k := r.URL.Query().Get("kim"); k != "" {
		if val, err := strconv.Atoi(k); err == nil && val >= 1 && val <= 27 {
			kim = &val
		}
	}

	var topicID *int
	if t := r.URL.Query().Get("topic_id"); t != "" {
		if val, err := strconv.Atoi(t); err == nil {
			topicID = &val
		}
	}

	q := r.URL.Query().Get("q")

	items, total, err := h.svc.ListTeacherTasks(r.Context(), userCtx.UserID, limit, offset, kim, topicID, q)
	if err != nil {
		response.InternalServerError(w, "Ошибка получения авторских задач")
		return
	}

	if items == nil {
		items = []tasks.TaskDTO{}
	}

	response.JSONPaginated(w, http.StatusOK, items, total, limit, offset)
}
