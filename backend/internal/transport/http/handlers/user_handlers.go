package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"backend/internal/domain/subscriptions"
	"backend/internal/domain/users"
	"backend/internal/transport/http/middleware"
	"backend/internal/transport/http/response"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// UserHandler handles user profile, onboarding, and admin user management HTTP requests.
type UserHandler struct {
	userService *users.Service
	subService  *subscriptions.Service
}

// NewUserHandler creates a new UserHandler.
func NewUserHandler(userService *users.Service, subService *subscriptions.Service) *UserHandler {
	return &UserHandler{
		userService: userService,
		subService:  subService,
	}
}

// Routes returns a sub-router suitable for mounting under /api/v1/users or /api/v1/user.
func (h *UserHandler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Post("/onboarding", h.Onboarding)
	r.Patch("/onboarding", h.Onboarding)
	r.Post("/me/onboarding", h.Onboarding)
	r.Patch("/me/onboarding", h.Onboarding)
	r.Patch("/profile", h.UpdateProfile)
	r.Patch("/me", h.UpdateProfile)
	return r
}

// ServeHTTP implements http.Handler.
func (h *UserHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.Routes().ServeHTTP(w, r)
}

// AdminRoutes returns an admin sub-router for users.
func (h *UserHandler) AdminRoutes() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.AdminListUsers)
	r.Patch("/{id}", h.AdminUpdateUser)
	return r
}

// RegisterRoutes registers all user routes onto a chi router.
func (h *UserHandler) RegisterRoutes(r chi.Router) {
	// Onboarding routes (supporting both POST /user/onboarding and PATCH /users/me/onboarding)
	r.Post("/api/v1/user/onboarding", h.Onboarding)
	r.Patch("/api/v1/user/onboarding", h.Onboarding)
	r.Post("/api/v1/users/me/onboarding", h.Onboarding)
	r.Patch("/api/v1/users/me/onboarding", h.Onboarding)
	r.Post("/api/v1/users/onboarding", h.Onboarding)
	r.Patch("/api/v1/users/onboarding", h.Onboarding)

	// Profile update routes (supporting both /user/profile and /users/me)
	r.Patch("/api/v1/user/profile", h.UpdateProfile)
	r.Patch("/api/v1/users/me", h.UpdateProfile)
	r.Patch("/api/v1/users/profile", h.UpdateProfile)

	// Admin user routes
	r.Get("/api/v1/admin/users", h.AdminListUsers)
	r.Patch("/api/v1/admin/users/{id}", h.AdminUpdateUser)
}

// Onboarding handles POST /api/v1/user/onboarding and PATCH /api/v1/users/me/onboarding.
func (h *UserHandler) Onboarding(w http.ResponseWriter, r *http.Request) {
	userCtx := middleware.MustGetUserContext(r.Context())
	if !userCtx.IsAuthenticated {
		response.Unauthorized(w, "UNAUTHORIZED", "Требуется авторизация")
		return
	}

	var req users.OnboardingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", "Некорректное тело запроса", map[string]string{"error": err.Error()})
		return
	}

	updatedUser, err := h.userService.OnboardUser(r.Context(), userCtx.UserID, req.FirstName, req.LastName, req.Username)
	if err != nil {
		if errors.Is(err, users.ErrUsernameTaken) {
			response.Conflict(w, "CONFLICT", "Имя пользователя уже занято")
			return
		}
		if errors.Is(err, users.ErrInvalidUsername) || errors.Is(err, users.ErrInvalidName) {
			response.BadRequest(w, "VALIDATION_ERROR", err.Error(), nil)
			return
		}
		if errors.Is(err, users.ErrNotFound) {
			response.NotFound(w, "NOT_FOUND", "Пользователь не найден")
			return
		}
		response.InternalServerError(w, "Не удалось завершить онбординг")
		return
	}

	dto, err := h.userService.BuildUserDTO(r.Context(), updatedUser, userCtx.ImpersonatedBy)
	if err != nil {
		response.InternalServerError(w, "Не удалось сформировать данные профиля")
		return
	}

	response.JSON(w, http.StatusOK, dto)
}

// UpdateProfile handles PATCH /api/v1/user/profile and PATCH /api/v1/users/me.
func (h *UserHandler) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	userCtx := middleware.MustGetUserContext(r.Context())
	if !userCtx.IsAuthenticated {
		response.Unauthorized(w, "UNAUTHORIZED", "Требуется авторизация")
		return
	}

	var req users.UpdateProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", "Некорректное тело запроса", map[string]string{"error": err.Error()})
		return
	}

	updatedUser, err := h.userService.UpdateProfile(r.Context(), userCtx.UserID, req.FirstName, req.LastName, req.Username)
	if err != nil {
		if errors.Is(err, users.ErrUsernameTaken) {
			response.Conflict(w, "CONFLICT", "Имя пользователя уже занято")
			return
		}
		if errors.Is(err, users.ErrInvalidUsername) || errors.Is(err, users.ErrInvalidName) {
			response.BadRequest(w, "VALIDATION_ERROR", err.Error(), nil)
			return
		}
		if errors.Is(err, users.ErrNotFound) {
			response.NotFound(w, "NOT_FOUND", "Пользователь не найден")
			return
		}
		response.InternalServerError(w, "Не удалось обновить профиль")
		return
	}

	dto, err := h.userService.BuildUserDTO(r.Context(), updatedUser, userCtx.ImpersonatedBy)
	if err != nil {
		response.InternalServerError(w, "Не удалось сформировать данные профиля")
		return
	}

	response.JSON(w, http.StatusOK, dto)
}

// AdminListUsers handles GET /api/v1/admin/users (requires superuser role).
func (h *UserHandler) AdminListUsers(w http.ResponseWriter, r *http.Request) {
	userCtx := middleware.MustGetUserContext(r.Context())
	if !userCtx.IsAuthenticated {
		response.Unauthorized(w, "UNAUTHORIZED", "Требуется авторизация")
		return
	}
	if !userCtx.IsSuperuser {
		response.Forbidden(w, "FORBIDDEN", "Доступ разрешён только администраторам")
		return
	}

	query := r.URL.Query()
	q := query.Get("q")
	role := query.Get("role")

	limit := 20
	if l := query.Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	offset := 0
	if o := query.Get("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	userList, total, err := h.userService.ListUsers(r.Context(), q, role, limit, offset)
	if err != nil {
		if errors.Is(err, users.ErrInvalidRole) {
			response.BadRequest(w, "VALIDATION_ERROR", "Некорректная роль пользователя", nil)
			return
		}
		response.InternalServerError(w, "Не удалось получить список пользователей")
		return
	}

	items := make([]*users.UserDTO, 0, len(userList))
	for _, u := range userList {
		dto, err := h.userService.BuildUserDTO(r.Context(), u, nil)
		if err != nil {
			dto = u.ToDTO(nil, nil)
		}
		items = append(items, dto)
	}

	response.JSONPaginated(w, http.StatusOK, items, total, limit, offset)
}

// AdminUpdateUser handles PATCH /api/v1/admin/users/{id} (requires superuser role).
func (h *UserHandler) AdminUpdateUser(w http.ResponseWriter, r *http.Request) {
	userCtx := middleware.MustGetUserContext(r.Context())
	if !userCtx.IsAuthenticated {
		response.Unauthorized(w, "UNAUTHORIZED", "Требуется авторизация")
		return
	}
	if !userCtx.IsSuperuser {
		response.Forbidden(w, "FORBIDDEN", "Доступ разрешён только администраторам")
		return
	}

	idStr := chi.URLParam(r, "id")
	targetID, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", "Некорректный ID пользователя", map[string]string{"id": idStr})
		return
	}

	var req users.AdminUpdateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", "Некорректное тело запроса", map[string]string{"error": err.Error()})
		return
	}

	// 1. Update role if requested
	if req.Role != nil {
		if _, err := h.userService.UpdateRole(r.Context(), targetID, *req.Role); err != nil {
			if errors.Is(err, users.ErrInvalidRole) {
				response.BadRequest(w, "VALIDATION_ERROR", "Недопустимая роль", nil)
				return
			}
			if errors.Is(err, users.ErrNotFound) {
				response.NotFound(w, "NOT_FOUND", "Пользователь не найден")
				return
			}
			response.InternalServerError(w, "Не удалось обновить роль пользователя")
			return
		}
	}

	// 2. Grant subscription if requested
	if req.GrantSubscription != nil {
		if h.subService == nil {
			response.InternalServerError(w, "Сервис подписок не настроен")
			return
		}
		_, err := h.subService.GrantSubscription(
			r.Context(),
			targetID,
			req.GrantSubscription.TierID,
			req.GrantSubscription.DurationDays,
			req.GrantSubscription.Note,
			&userCtx.UserID,
		)
		if err != nil {
			if errors.Is(err, subscriptions.ErrTierNotFound) {
				response.BadRequest(w, "VALIDATION_ERROR", "Тариф подписки не найден", nil)
				return
			}
			response.InternalServerError(w, "Не удалось выдать подписку пользователю")
			return
		}
	}

	// 3. Retrieve final updated user
	finalUser, err := h.userService.GetByID(r.Context(), targetID)
	if err != nil {
		if errors.Is(err, users.ErrNotFound) {
			response.NotFound(w, "NOT_FOUND", "Пользователь не найден")
			return
		}
		response.InternalServerError(w, "Не удалось получить обновлённые данные пользователя")
		return
	}

	dto, err := h.userService.BuildUserDTO(r.Context(), finalUser, nil)
	if err != nil {
		response.InternalServerError(w, "Не удалось сформировать данные профиля")
		return
	}

	response.JSON(w, http.StatusOK, dto)
}
