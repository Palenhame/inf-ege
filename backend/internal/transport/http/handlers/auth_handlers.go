package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"backend/internal/domain/auth"
	"backend/internal/transport/http/middleware"
	"backend/internal/transport/http/response"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// AuthHandler handles authentication endpoints.
type AuthHandler struct {
	service   *auth.Service
	jwtMgr    *auth.JWTManager
	cookieCfg auth.CookieConfig
}

// NewAuthHandler creates a new AuthHandler.
func NewAuthHandler(service *auth.Service, jwtMgr *auth.JWTManager, cookieCfg auth.CookieConfig) *AuthHandler {
	return &AuthHandler{
		service:   service,
		jwtMgr:    jwtMgr,
		cookieCfg: cookieCfg,
	}
}

// RegisterRoutes registers auth routes on a chi router.
func (h *AuthHandler) RegisterRoutes(r chi.Router) {
	r.Get("/auth/oauth/{provider}/login", h.OAuthLogin)
	r.Get("/auth/oauth/{provider}/callback", h.OAuthCallback)
	r.Post("/auth/dev-login", h.DevLogin)
	r.Post("/auth/refresh", h.Refresh)
	r.Post("/auth/logout", h.Logout)
	r.Get("/auth/me", h.Me)
}

// OAuthLogin initiates OAuth 2.0 flow for Yandex or VK.
// GET /api/v1/auth/oauth/{provider}/login?redirect_to=...
func (h *AuthHandler) OAuthLogin(w http.ResponseWriter, r *http.Request) {
	provider := strings.ToLower(chi.URLParam(r, "provider"))
	if provider != "yandex" && provider != "vk" {
		response.BadRequest(w, "VALIDATION_ERROR", "Неподдерживаемый OAuth-провайдер. Доступны: yandex, vk", nil)
		return
	}

	redirectTo := r.URL.Query().Get("redirect_to")
	authURL, stateCookieValue, err := h.service.InitiateOAuth(provider, redirectTo)
	if err != nil {
		response.InternalServerError(w, "Не удалось сформировать ссылку авторизации: "+err.Error())
		return
	}

	auth.SetOAuthStateCookie(w, stateCookieValue, h.cookieCfg)
	http.Redirect(w, r, authURL, http.StatusFound)
}

// OAuthCallback handles provider callback, exchanges authorization code, and sets auth cookies.
// GET /api/v1/auth/oauth/{provider}/callback?code=...&state=...
func (h *AuthHandler) OAuthCallback(w http.ResponseWriter, r *http.Request) {
	provider := strings.ToLower(chi.URLParam(r, "provider"))
	if provider != "yandex" && provider != "vk" {
		response.BadRequest(w, "VALIDATION_ERROR", "Неподдерживаемый OAuth-провайдер", nil)
		return
	}

	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		response.BadRequest(w, "VALIDATION_ERROR", "Параметры code и state обязательны", nil)
		return
	}

	stateCookie := auth.GetCookieValue(r, auth.OAuthStateCookieName)
	if stateCookie == "" {
		response.BadRequest(w, "VALIDATION_ERROR", "Отсутствует cookie состояния OAuth (сессия истекла)", nil)
		return
	}

	tokens, redirectURL, _, err := h.service.HandleOAuthCallback(r.Context(), provider, code, state, stateCookie)
	if err != nil {
		auth.ClearOAuthStateCookie(w, h.cookieCfg)
		if errors.Is(err, auth.ErrInvalidState) {
			response.BadRequest(w, "VALIDATION_ERROR", "Некорректное состояние OAuth или CSRF", nil)
			return
		}
		response.BadRequest(w, "VALIDATION_ERROR", "Ошибка авторизации: "+err.Error(), nil)
		return
	}

	// Issue authentication cookies
	auth.SetAuthCookies(w, tokens.AccessToken, tokens.RefreshToken, h.cookieCfg)
	auth.ClearOAuthStateCookie(w, h.cookieCfg)

	if redirectURL == "" {
		redirectURL = "/"
	}
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

// DevLoginRequest payload for dev-login.
type DevLoginRequest struct {
	Role     string `json:"role"`
	Username string `json:"username"`
}

// DevLogin creates or logs in a mock user in development mode.
// POST /api/v1/auth/dev-login
func (h *AuthHandler) DevLogin(w http.ResponseWriter, r *http.Request) {
	var req DevLoginRequest
	if r.Body != nil && r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			response.BadRequest(w, "VALIDATION_ERROR", "Некорректный JSON запроса", nil)
			return
		}
	}

	tokens, user, err := h.service.DevLogin(r.Context(), req.Role, req.Username)
	if err != nil {
		if errors.Is(err, auth.ErrDevLoginDisabled) {
			response.Forbidden(w, "FORBIDDEN", "Dev-логин доступен только в режиме разработки")
			return
		}
		response.BadRequest(w, "VALIDATION_ERROR", err.Error(), nil)
		return
	}

	auth.SetAuthCookies(w, tokens.AccessToken, tokens.RefreshToken, h.cookieCfg)
	response.JSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"user": user,
	})
}

// Refresh rotates the refresh token and updates both access and refresh cookies.
// POST /api/v1/auth/refresh
func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	refreshToken := auth.GetCookieValue(r, auth.RefreshCookieName)
	if refreshToken == "" {
		response.Unauthorized(w, "UNAUTHORIZED", "Отсутствует refresh-токен")
		return
	}

	tokens, err := h.service.RefreshToken(r.Context(), refreshToken)
	if err != nil {
		auth.ClearAuthCookies(w, h.cookieCfg)
		if errors.Is(err, auth.ErrSessionNotFound) || errors.Is(err, auth.ErrInvalidSession) {
			response.Unauthorized(w, "TOKEN_EXPIRED", "Сессия истекла или недействительна")
			return
		}
		response.Unauthorized(w, "UNAUTHORIZED", "Ошибка обновления сессии: "+err.Error())
		return
	}

	auth.SetAuthCookies(w, tokens.AccessToken, tokens.RefreshToken, h.cookieCfg)
	response.JSON(w, http.StatusOK, map[string]any{
		"ok": true,
	})
}

// Logout invalidates the refresh session in Redis and clears cookies.
// POST /api/v1/auth/logout
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	refreshToken := auth.GetCookieValue(r, auth.RefreshCookieName)
	_ = h.service.Logout(r.Context(), refreshToken)

	auth.ClearAuthCookies(w, h.cookieCfg)
	response.JSON(w, http.StatusOK, map[string]any{
		"ok": true,
	})
}

// Me returns the current authenticated user session or an unauthenticated object.
// GET /api/v1/auth/me
func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	// First check UserContext from middleware
	userCtx, ok := middleware.GetUserContext(r.Context())
	var targetUserID string
	var impersonatorID *string

	if ok && userCtx.IsAuthenticated {
		targetUserID = userCtx.UserID.String()
		if userCtx.ImpersonatedBy != nil {
			imp := userCtx.ImpersonatedBy.String()
			impersonatorID = &imp
		}
	} else {
		// Fallback: check access cookie directly if middleware wasn't executed
		accessCookie := auth.GetCookieValue(r, auth.AccessCookieName)
		if accessCookie != "" && h.jwtMgr != nil {
			claims, err := h.jwtMgr.ValidateAccessToken(accessCookie)
			if err == nil && claims != nil && claims.Subject != "" {
				if _, err := uuid.Parse(claims.Subject); err == nil {
					targetUserID = claims.Subject
				}
			}
		}
	}

	res, err := h.service.GetMe(r.Context(), targetUserID, impersonatorID)
	if err != nil {
		response.InternalServerError(w, "Ошибка получения данных профиля: "+err.Error())
		return
	}

	response.JSON(w, http.StatusOK, res)
}
