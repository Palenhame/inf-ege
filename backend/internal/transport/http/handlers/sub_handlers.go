package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"backend/internal/domain/subscriptions"
	"backend/internal/domain/users"
	"backend/internal/transport/http/middleware"
	"backend/internal/transport/http/response"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// SubscriptionHandler handles public and admin subscription HTTP requests.
type SubscriptionHandler struct {
	subService *subscriptions.Service
}

// NewSubscriptionHandler creates a new SubscriptionHandler.
func NewSubscriptionHandler(subService *subscriptions.Service) *SubscriptionHandler {
	return &SubscriptionHandler{subService: subService}
}

// Routes returns a sub-router suitable for mounting under /api/v1/subscriptions.
func (h *SubscriptionHandler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/tiers", h.ListPublicTiers)
	r.Get("/invites/{code}", h.PreviewInvite)
	r.Post("/activate", h.ActivateInvite)
	r.Post("/invites/{code}/claim", h.ActivateInvite)
	return r
}

// ServeHTTP implements http.Handler.
func (h *SubscriptionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.Routes().ServeHTTP(w, r)
}

// AdminRoutes returns an admin sub-router for subscriptions.
func (h *SubscriptionHandler) AdminRoutes() chi.Router {
	r := chi.NewRouter()
	r.Get("/tiers", h.AdminListTiers)
	r.Post("/tiers", h.AdminCreateTier)
	r.Patch("/tiers/{id}", h.AdminUpdateTier)

	r.Get("/invites", h.AdminListInvites)
	r.Post("/invites", h.AdminCreateInvite)
	r.Delete("/invites/{id}", h.AdminDeleteInvite)
	return r
}

// RegisterRoutes registers all subscription routes onto a chi router.
func (h *SubscriptionHandler) RegisterRoutes(r chi.Router) {
	// Public routes
	r.Get("/api/v1/subscriptions/tiers", h.ListPublicTiers)
	r.Get("/api/v1/subscriptions/invites/{code}", h.PreviewInvite)

	// User claim / activation routes
	r.Post("/api/v1/subscriptions/activate", h.ActivateInvite)
	r.Post("/api/v1/subscriptions/invites/{code}/claim", h.ActivateInvite)

	// Admin routes
	r.Get("/api/v1/admin/subscriptions/tiers", h.AdminListTiers)
	r.Post("/api/v1/admin/subscriptions/tiers", h.AdminCreateTier)
	r.Patch("/api/v1/admin/subscriptions/tiers/{id}", h.AdminUpdateTier)

	r.Get("/api/v1/admin/subscriptions/invites", h.AdminListInvites)
	r.Post("/api/v1/admin/subscriptions/invites", h.AdminCreateInvite)
	r.Delete("/api/v1/admin/subscriptions/invites/{id}", h.AdminDeleteInvite)
}

// ListPublicTiers handles GET /api/v1/subscriptions/tiers.
func (h *SubscriptionHandler) ListPublicTiers(w http.ResponseWriter, r *http.Request) {
	tiers, err := h.subService.ListTiers(r.Context(), false)
	if err != nil {
		response.InternalServerError(w, "Не удалось получить список тарифов")
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"items": tiers})
}

// PreviewInvite handles GET /api/v1/subscriptions/invites/{code}.
func (h *SubscriptionHandler) PreviewInvite(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	if strings.TrimSpace(code) == "" {
		response.BadRequest(w, "VALIDATION_ERROR", "Инвайт-код не указан", nil)
		return
	}

	preview, err := h.subService.GetInvitePreview(r.Context(), code)
	if err != nil {
		if errors.Is(err, subscriptions.ErrInviteNotFound) {
			response.NotFound(w, "NOT_FOUND", "Инвайт-код не найден")
			return
		}
		response.InternalServerError(w, "Не удалось проверить инвайт-код")
		return
	}

	if !preview.IsValid {
		response.BadRequest(w, "LIMIT_EXCEEDED", "Инвайт-код недействителен или лимит исчерпан", nil)
		return
	}

	response.JSON(w, http.StatusOK, preview)
}

type activateBody struct {
	Code string `json:"code"`
}

// ActivateInvite handles POST /api/v1/subscriptions/activate and POST /api/v1/subscriptions/invites/{code}/claim.
func (h *SubscriptionHandler) ActivateInvite(w http.ResponseWriter, r *http.Request) {
	userCtx := middleware.MustGetUserContext(r.Context())
	if !userCtx.IsAuthenticated {
		response.Unauthorized(w, "UNAUTHORIZED", "Требуется авторизация")
		return
	}

	code := chi.URLParam(r, "code")
	if code == "" && r.Body != nil {
		var body activateBody
		_ = json.NewDecoder(r.Body).Decode(&body)
		code = body.Code
	}
	code = strings.TrimSpace(code)

	if code == "" {
		response.BadRequest(w, "VALIDATION_ERROR", "Инвайт-код не указан", nil)
		return
	}

	sub, err := h.subService.ActivateInviteCode(r.Context(), code, userCtx.UserID)
	if err != nil {
		if errors.Is(err, subscriptions.ErrInviteNotFound) {
			response.NotFound(w, "NOT_FOUND", "Инвайт-код не найден")
			return
		}
		if errors.Is(err, subscriptions.ErrInviteExpired) {
			response.BadRequest(w, "LIMIT_EXCEEDED", "Срок действия инвайт-кода истёк", nil)
			return
		}
		if errors.Is(err, subscriptions.ErrInviteExhausted) {
			response.BadRequest(w, "LIMIT_EXCEEDED", "Лимит активаций инвайт-кода исчерпан", nil)
			return
		}
		if errors.Is(err, subscriptions.ErrInviteAlreadyClaimed) {
			response.Conflict(w, "CONFLICT", "Вы уже активировали этот инвайт-код")
			return
		}
		response.InternalServerError(w, "Не удалось активировать инвайт-код")
		return
	}

	var tierInfo *users.SubscriptionTier
	if sub.Tier != nil {
		tierInfo = &users.SubscriptionTier{
			ID:       sub.Tier.ID,
			Slug:     sub.Tier.Slug,
			Name:     sub.Tier.Name,
			PriceRub: sub.Tier.PriceRub,
			Features: sub.Tier.Features,
		}
	}

	result := subscriptions.ClaimInviteResult{
		OK:   true,
		Role: string(users.RoleTeacher),
		Subscription: &users.UserSubscriptionInfo{
			IsActive:   true,
			IsReadOnly: false,
			Tier:       tierInfo,
			StartsAt:   &sub.StartsAt,
			ExpiresAt:  &sub.ExpiresAt,
		},
	}

	response.JSON(w, http.StatusOK, result)
}

// AdminListTiers handles GET /api/v1/admin/subscriptions/tiers.
func (h *SubscriptionHandler) AdminListTiers(w http.ResponseWriter, r *http.Request) {
	userCtx := middleware.MustGetUserContext(r.Context())
	if !userCtx.IsAuthenticated {
		response.Unauthorized(w, "UNAUTHORIZED", "Требуется авторизация")
		return
	}
	if !userCtx.IsSuperuser {
		response.Forbidden(w, "FORBIDDEN", "Доступ разрешён только администраторам")
		return
	}

	tiers, err := h.subService.ListTiers(r.Context(), true)
	if err != nil {
		response.InternalServerError(w, "Не удалось получить список всех тарифов")
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"items": tiers})
}

// AdminCreateTier handles POST /api/v1/admin/subscriptions/tiers.
func (h *SubscriptionHandler) AdminCreateTier(w http.ResponseWriter, r *http.Request) {
	userCtx := middleware.MustGetUserContext(r.Context())
	if !userCtx.IsAuthenticated {
		response.Unauthorized(w, "UNAUTHORIZED", "Требуется авторизация")
		return
	}
	if !userCtx.IsSuperuser {
		response.Forbidden(w, "FORBIDDEN", "Доступ разрешён только администраторам")
		return
	}

	var req subscriptions.CreateTierRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", "Некорректное тело запроса", map[string]string{"error": err.Error()})
		return
	}

	created, err := h.subService.CreateTier(r.Context(), &req)
	if err != nil {
		if errors.Is(err, subscriptions.ErrTierSlugTaken) {
			response.Conflict(w, "CONFLICT", "Тариф с таким slug уже существует")
			return
		}
		if errors.Is(err, subscriptions.ErrInvalidTierData) {
			response.BadRequest(w, "VALIDATION_ERROR", "Некорректные данные тарифа", nil)
			return
		}
		response.InternalServerError(w, "Не удалось создать тариф")
		return
	}

	response.JSON(w, http.StatusCreated, created)
}

// AdminUpdateTier handles PATCH /api/v1/admin/subscriptions/tiers/{id}.
func (h *SubscriptionHandler) AdminUpdateTier(w http.ResponseWriter, r *http.Request) {
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
	id, err := strconv.Atoi(idStr)
	if err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", "Некорректный ID тарифа", map[string]string{"id": idStr})
		return
	}

	var req subscriptions.UpdateTierRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", "Некорректное тело запроса", map[string]string{"error": err.Error()})
		return
	}

	updated, err := h.subService.UpdateTier(r.Context(), id, &req)
	if err != nil {
		if errors.Is(err, subscriptions.ErrTierNotFound) {
			response.NotFound(w, "NOT_FOUND", "Тариф не найден")
			return
		}
		if errors.Is(err, subscriptions.ErrTierSlugTaken) {
			response.Conflict(w, "CONFLICT", "Тариф с таким slug уже существует")
			return
		}
		if errors.Is(err, subscriptions.ErrInvalidTierData) {
			response.BadRequest(w, "VALIDATION_ERROR", "Некорректные данные тарифа", nil)
			return
		}
		response.InternalServerError(w, "Не удалось обновить тариф")
		return
	}

	response.JSON(w, http.StatusOK, updated)
}

// AdminListInvites handles GET /api/v1/admin/subscriptions/invites.
func (h *SubscriptionHandler) AdminListInvites(w http.ResponseWriter, r *http.Request) {
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

	invites, total, err := h.subService.ListInvites(r.Context(), limit, offset)
	if err != nil {
		response.InternalServerError(w, "Не удалось получить список инвайт-кодов")
		return
	}

	response.JSONPaginated(w, http.StatusOK, invites, total, limit, offset)
}

// AdminCreateInvite handles POST /api/v1/admin/subscriptions/invites.
func (h *SubscriptionHandler) AdminCreateInvite(w http.ResponseWriter, r *http.Request) {
	userCtx := middleware.MustGetUserContext(r.Context())
	if !userCtx.IsAuthenticated {
		response.Unauthorized(w, "UNAUTHORIZED", "Требуется авторизация")
		return
	}
	if !userCtx.IsSuperuser {
		response.Forbidden(w, "FORBIDDEN", "Доступ разрешён только администраторам")
		return
	}

	var req subscriptions.CreateInviteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", "Некорректное тело запроса", map[string]string{"error": err.Error()})
		return
	}

	invite, err := h.subService.CreateInvite(r.Context(), &req, &userCtx.UserID)
	if err != nil {
		if errors.Is(err, subscriptions.ErrTierNotFound) {
			response.BadRequest(w, "VALIDATION_ERROR", "Указанный тариф не существует", nil)
			return
		}
		if errors.Is(err, subscriptions.ErrInvalidInviteData) {
			response.BadRequest(w, "VALIDATION_ERROR", "Некорректные параметры инвайта", nil)
			return
		}
		response.InternalServerError(w, "Не удалось создать инвайт-код")
		return
	}

	response.JSON(w, http.StatusCreated, invite)
}

// AdminDeleteInvite handles DELETE /api/v1/admin/subscriptions/invites/{id}.
func (h *SubscriptionHandler) AdminDeleteInvite(w http.ResponseWriter, r *http.Request) {
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
	id, err := uuid.Parse(idStr)
	if err != nil {
		response.BadRequest(w, "VALIDATION_ERROR", "Некорректный UUID инвайта", map[string]string{"id": idStr})
		return
	}

	if err := h.subService.DeleteInvite(r.Context(), id); err != nil {
		if errors.Is(err, subscriptions.ErrInviteNotFound) {
			response.NotFound(w, "NOT_FOUND", "Инвайт-код не найден")
			return
		}
		response.InternalServerError(w, "Не удалось удалить инвайт-код")
		return
	}

	response.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}
