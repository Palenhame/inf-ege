package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"backend/internal/domain/rooms"
	"backend/internal/transport/http/middleware"
	"backend/internal/transport/http/response"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// RoomHandler handles REST API endpoints for rooms and user_rooms.
type RoomHandler struct {
	svc *rooms.Service
}

// NewRoomHandler creates a new RoomHandler.
func NewRoomHandler(svc *rooms.Service) *RoomHandler {
	return &RoomHandler{svc: svc}
}

// Routes creates a chi subrouter with all rooms endpoints mounted for /rooms.
func (h *RoomHandler) Routes() chi.Router {
	r := chi.NewRouter()

	// Subrouter routes (when mounted at /rooms or /api/v1/rooms)
	r.Get("/", h.ListRooms)
	r.Post("/", h.CreateRoom)
	r.Get("/{id}", h.GetRoom)
	r.Patch("/{id}", h.UpdateRoom)
	r.Delete("/{id}", h.DeleteRoom)
	r.Get("/join/{invite_code}", h.PreviewJoin)
	r.Post("/join/{invite_code}", h.JoinRoom)
	r.Delete("/{id}/members/{user_id}", h.RemoveMember)
	r.Put("/{id}/free-code", h.SaveFreeCode)

	// Direct routes (when mounted at root)
	r.Get("/rooms", h.ListRooms)
	r.Post("/rooms", h.CreateRoom)
	r.Get("/rooms/{id}", h.GetRoom)
	r.Patch("/rooms/{id}", h.UpdateRoom)
	r.Delete("/rooms/{id}", h.DeleteRoom)
	r.Get("/rooms/join/{invite_code}", h.PreviewJoin)
	r.Post("/rooms/join/{invite_code}", h.JoinRoom)
	r.Delete("/rooms/{id}/members/{user_id}", h.RemoveMember)
	r.Put("/rooms/{id}/free-code", h.SaveFreeCode)

	return r
}

// ServeHTTP implements http.Handler for RoomHandler.
func (h *RoomHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.Routes().ServeHTTP(w, r)
}

// RegisterRoutes registers all rooms REST routes onto the provided chi.Router.
func (h *RoomHandler) RegisterRoutes(r chi.Router) {
	r.Get("/rooms", h.ListRooms)
	r.Post("/rooms", h.CreateRoom)
	r.Get("/rooms/{id}", h.GetRoom)
	r.Patch("/rooms/{id}", h.UpdateRoom)
	r.Delete("/rooms/{id}", h.DeleteRoom)
	r.Get("/rooms/join/{invite_code}", h.PreviewJoin)
	r.Post("/rooms/join/{invite_code}", h.JoinRoom)
	r.Delete("/rooms/{id}/members/{user_id}", h.RemoveMember)
	r.Put("/rooms/{id}/free-code", h.SaveFreeCode)

	r.Get("/api/v1/rooms", h.ListRooms)
	r.Post("/api/v1/rooms", h.CreateRoom)
	r.Get("/api/v1/rooms/{id}", h.GetRoom)
	r.Patch("/api/v1/rooms/{id}", h.UpdateRoom)
	r.Delete("/api/v1/rooms/{id}", h.DeleteRoom)
	r.Get("/api/v1/rooms/join/{invite_code}", h.PreviewJoin)
	r.Post("/api/v1/rooms/join/{invite_code}", h.JoinRoom)
	r.Delete("/api/v1/rooms/{id}/members/{user_id}", h.RemoveMember)
	r.Put("/api/v1/rooms/{id}/free-code", h.SaveFreeCode)
}

// ListRooms handles GET /api/v1/rooms?view_mode=teacher|student
func (h *RoomHandler) ListRooms(w http.ResponseWriter, r *http.Request) {
	userCtx := middleware.MustGetUserContext(r.Context())
	if !userCtx.IsAuthenticated {
		response.Unauthorized(w, "UNAUTHORIZED", "Требуется авторизация")
		return
	}

	viewMode := r.URL.Query().Get("view_mode")
	if viewMode == "" {
		viewMode = "student"
		if userCtx.Role == "teacher" {
			viewMode = "teacher"
		}
	}

	items, err := h.svc.ListRooms(r.Context(), userCtx.UserID, viewMode)
	if err != nil {
		if errors.Is(err, rooms.ErrInvalidInput) {
			response.BadRequest(w, "VALIDATION_ERROR", err.Error(), nil)
			return
		}
		response.InternalServerError(w, "Ошибка получения списка комнат")
		return
	}

	if items == nil {
		items = []rooms.RoomListItem{}
	}

	response.JSON(w, http.StatusOK, map[string]any{
		"items": items,
	})
}

// CreateRoom handles POST /api/v1/rooms
func (h *RoomHandler) CreateRoom(w http.ResponseWriter, r *http.Request) {
	userCtx := middleware.MustGetUserContext(r.Context())
	if !userCtx.IsAuthenticated {
		response.Unauthorized(w, "UNAUTHORIZED", "Требуется авторизация")
		return
	}

	if !userCtx.IsSuperuser && userCtx.Role != "teacher" {
		response.Forbidden(w, "FORBIDDEN", "Создавать группы могут только преподаватели")
		return
	}

	var req rooms.CreateRoomRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", "Некорректный JSON в теле запроса", nil)
		return
	}

	room, err := h.svc.CreateRoom(r.Context(), userCtx.UserID, userCtx.Role, req)
	if err != nil {
		switch {
		case errors.Is(err, rooms.ErrNotCreator):
			response.Forbidden(w, "FORBIDDEN", "Недостаточно прав для создания комнаты")
		case errors.Is(err, rooms.ErrSubscriptionReadOnly):
			response.Forbidden(w, "SUBSCRIPTION_READ_ONLY", "Срок действия подписки истёк. Создание новых комнат недоступно.")
		case errors.Is(err, rooms.ErrSubscriptionRequired):
			response.Forbidden(w, "SUBSCRIPTION_REQUIRED", "Для создания комнаты требуется активная подписка")
		case errors.Is(err, rooms.ErrLimitExceeded):
			response.BadRequest(w, "LIMIT_EXCEEDED", err.Error(), nil)
		case errors.Is(err, rooms.ErrInvalidInput):
			response.BadRequest(w, "VALIDATION_ERROR", err.Error(), nil)
		default:
			response.InternalServerError(w, "Не удалось создать комнату: "+err.Error())
		}
		return
	}

	response.JSON(w, http.StatusCreated, room)
}

// GetRoom handles GET /api/v1/rooms/{id}
func (h *RoomHandler) GetRoom(w http.ResponseWriter, r *http.Request) {
	userCtx := middleware.MustGetUserContext(r.Context())
	if !userCtx.IsAuthenticated {
		response.Unauthorized(w, "UNAUTHORIZED", "Требуется авторизация")
		return
	}

	roomID, err := getUUIDParam(r, "id", "room_id")
	if err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", "Некорректный ID комнаты", nil)
		return
	}

	detail, err := h.svc.GetRoomDetail(r.Context(), userCtx.UserID, userCtx.IsSuperuser, roomID)
	if err != nil {
		switch {
		case errors.Is(err, rooms.ErrRoomNotFound):
			response.NotFound(w, "NOT_FOUND", "Комната не найдена")
		case errors.Is(err, rooms.ErrNotMember):
			response.Forbidden(w, "FORBIDDEN", "Вы не являетесь участником этой комнаты")
		default:
			response.InternalServerError(w, "Ошибка загрузки комнаты")
		}
		return
	}

	response.JSON(w, http.StatusOK, detail)
}

// UpdateRoom handles PATCH /api/v1/rooms/{id}
func (h *RoomHandler) UpdateRoom(w http.ResponseWriter, r *http.Request) {
	userCtx := middleware.MustGetUserContext(r.Context())
	if !userCtx.IsAuthenticated {
		response.Unauthorized(w, "UNAUTHORIZED", "Требуется авторизация")
		return
	}

	roomID, err := getUUIDParam(r, "id", "room_id")
	if err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", "Некорректный ID комнаты", nil)
		return
	}

	var req rooms.UpdateRoomRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", "Некорректный JSON в теле запроса", nil)
		return
	}

	updated, err := h.svc.UpdateRoom(r.Context(), userCtx.UserID, userCtx.IsSuperuser, roomID, req)
	if err != nil {
		switch {
		case errors.Is(err, rooms.ErrRoomNotFound):
			response.NotFound(w, "NOT_FOUND", "Комната не найдена")
		case errors.Is(err, rooms.ErrNotCreator):
			response.Forbidden(w, "FORBIDDEN", "Редактировать комнату может только её создатель")
		case errors.Is(err, rooms.ErrSubscriptionReadOnly):
			response.Forbidden(w, "SUBSCRIPTION_READ_ONLY", "Срок действия подписки истёк. Редактирование комнат недоступно.")
		case errors.Is(err, rooms.ErrLimitExceeded):
			response.BadRequest(w, "LIMIT_EXCEEDED", err.Error(), nil)
		case errors.Is(err, rooms.ErrInvalidInput):
			response.BadRequest(w, "VALIDATION_ERROR", err.Error(), nil)
		default:
			response.InternalServerError(w, "Ошибка обновления комнаты")
		}
		return
	}

	response.JSON(w, http.StatusOK, updated)
}

// DeleteRoom handles DELETE /api/v1/rooms/{id}
func (h *RoomHandler) DeleteRoom(w http.ResponseWriter, r *http.Request) {
	userCtx := middleware.MustGetUserContext(r.Context())
	if !userCtx.IsAuthenticated {
		response.Unauthorized(w, "UNAUTHORIZED", "Требуется авторизация")
		return
	}

	roomID, err := getUUIDParam(r, "id", "room_id")
	if err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", "Некорректный ID комнаты", nil)
		return
	}

	err = h.svc.DeleteRoom(r.Context(), userCtx.UserID, userCtx.IsSuperuser, roomID)
	if err != nil {
		switch {
		case errors.Is(err, rooms.ErrRoomNotFound):
			response.NotFound(w, "NOT_FOUND", "Комната не найдена")
		case errors.Is(err, rooms.ErrNotCreator):
			response.Forbidden(w, "FORBIDDEN", "Удалить комнату может только её создатель")
		default:
			response.InternalServerError(w, "Ошибка удаления комнаты")
		}
		return
	}

	response.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

// PreviewJoin handles GET /api/v1/rooms/join/{invite_code}
func (h *RoomHandler) PreviewJoin(w http.ResponseWriter, r *http.Request) {
	code := getParam(r, "invite_code", "code")
	if code == "" {
		response.BadRequest(w, "VALIDATION_ERROR", "Инвайт-код не указан", nil)
		return
	}

	preview, err := h.svc.PreviewJoin(r.Context(), code)
	if err != nil {
		switch {
		case errors.Is(err, rooms.ErrRoomNotFound):
			response.NotFound(w, "NOT_FOUND", "Комната с таким кодом не найдена")
		case errors.Is(err, rooms.ErrInvalidInput):
			response.BadRequest(w, "VALIDATION_ERROR", err.Error(), nil)
		default:
			response.InternalServerError(w, "Ошибка предпросмотра комнаты")
		}
		return
	}

	response.JSON(w, http.StatusOK, preview)
}

// JoinRoom handles POST /api/v1/rooms/join/{invite_code}
func (h *RoomHandler) JoinRoom(w http.ResponseWriter, r *http.Request) {
	userCtx := middleware.MustGetUserContext(r.Context())
	if !userCtx.IsAuthenticated {
		response.Unauthorized(w, "UNAUTHORIZED", "Требуется авторизация")
		return
	}

	code := getParam(r, "invite_code", "code")
	if code == "" {
		response.BadRequest(w, "VALIDATION_ERROR", "Инвайт-код не указан", nil)
		return
	}

	roomID, err := h.svc.JoinRoom(r.Context(), userCtx.UserID, code)
	if err != nil {
		switch {
		case errors.Is(err, rooms.ErrRoomNotFound):
			response.NotFound(w, "NOT_FOUND", "Комната с таким кодом не найдена")
		case errors.Is(err, rooms.ErrAlreadyMember):
			response.Conflict(w, "CONFLICT", "Вы уже являетесь участником этой группы")
		case errors.Is(err, rooms.ErrRoomFull) || errors.Is(err, rooms.ErrLimitExceeded):
			response.BadRequest(w, "LIMIT_EXCEEDED", "В комнате достигнут лимит участников", nil)
		case errors.Is(err, rooms.ErrInvalidInput):
			response.BadRequest(w, "VALIDATION_ERROR", err.Error(), nil)
		default:
			response.InternalServerError(w, "Ошибка вступления в комнату")
		}
		return
	}

	response.JSON(w, http.StatusOK, rooms.JoinRoomResponse{
		OK:     true,
		RoomID: roomID,
	})
}

// RemoveMember handles DELETE /api/v1/rooms/{id}/members/{user_id}
func (h *RoomHandler) RemoveMember(w http.ResponseWriter, r *http.Request) {
	userCtx := middleware.MustGetUserContext(r.Context())
	if !userCtx.IsAuthenticated {
		response.Unauthorized(w, "UNAUTHORIZED", "Требуется авторизация")
		return
	}

	roomID, err := getUUIDParam(r, "id", "room_id")
	if err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", "Некорректный ID комнаты", nil)
		return
	}

	targetUserIDStr := chi.URLParam(r, "user_id")
	targetUserID, err := uuid.Parse(targetUserIDStr)
	if err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", "Некорректный ID пользователя", nil)
		return
	}

	err = h.svc.RemoveMember(r.Context(), userCtx.UserID, userCtx.IsSuperuser, roomID, targetUserID)
	if err != nil {
		switch {
		case errors.Is(err, rooms.ErrRoomNotFound):
			response.NotFound(w, "NOT_FOUND", "Комната не найдена")
		case errors.Is(err, rooms.ErrNotCreator):
			response.Forbidden(w, "FORBIDDEN", "Недостаточно прав для исключения участника")
		case errors.Is(err, rooms.ErrNotMember):
			response.NotFound(w, "NOT_FOUND", "Участник не найден в комнате")
		default:
			response.InternalServerError(w, "Ошибка исключения участника")
		}
		return
	}

	response.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

// SaveFreeCode handles PUT /api/v1/rooms/{id}/free-code
func (h *RoomHandler) SaveFreeCode(w http.ResponseWriter, r *http.Request) {
	userCtx := middleware.MustGetUserContext(r.Context())
	if !userCtx.IsAuthenticated {
		response.Unauthorized(w, "UNAUTHORIZED", "Требуется авторизация")
		return
	}

	roomID, err := getUUIDParam(r, "id", "room_id")
	if err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", "Некорректный ID комнаты", nil)
		return
	}

	var req rooms.SaveFreeCodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", "Некорректный JSON в теле запроса", nil)
		return
	}

	s3Key, err := h.svc.SaveFreeCode(r.Context(), userCtx.UserID, roomID, req.Code)
	if err != nil {
		switch {
		case errors.Is(err, rooms.ErrRoomNotFound):
			response.NotFound(w, "NOT_FOUND", "Комната не найдена")
		case errors.Is(err, rooms.ErrNotMember):
			response.Forbidden(w, "FORBIDDEN", "Вы не являетесь участником этой комнаты")
		default:
			response.InternalServerError(w, "Ошибка сохранения свободного кода")
		}
		return
	}

	response.JSON(w, http.StatusOK, rooms.SaveFreeCodeResponse{
		OK:    true,
		S3Key: s3Key,
	})
}

func getParam(r *http.Request, keys ...string) string {
	for _, k := range keys {
		val := chi.URLParam(r, k)
		if val != "" {
			return strings.TrimSpace(val)
		}
	}
	return ""
}

func getUUIDParam(r *http.Request, keys ...string) (uuid.UUID, error) {
	val := getParam(r, keys...)
	if val == "" {
		return uuid.Nil, errors.New("missing param")
	}
	return uuid.Parse(val)
}
