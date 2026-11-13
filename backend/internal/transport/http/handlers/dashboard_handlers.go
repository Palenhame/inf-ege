package handlers

import (
	"net/http"

	"backend/internal/domain/recommendations"
	"backend/internal/transport/http/middleware"
	"backend/internal/transport/http/response"
)

// DashboardHandler handles student home screen data.
type DashboardHandler struct {
	recService recommendations.Service
}

// NewDashboardHandler creates a new DashboardHandler instance.
func NewDashboardHandler(recService recommendations.Service) *DashboardHandler {
	return &DashboardHandler{recService: recService}
}

// GetStudentDashboard handles GET /api/v1/dashboard/student.
func (h *DashboardHandler) GetStudentDashboard(w http.ResponseWriter, r *http.Request) {
	uCtx, ok := middleware.GetUserContext(r.Context())
	if !ok || !uCtx.IsAuthenticated {
		response.Unauthorized(w, "UNAUTHORIZED", "Требуется авторизация для доступа к дашборду")
		return
	}

	dashboard, err := h.recService.GetStudentDashboard(r.Context(), uCtx.UserID)
	if err != nil {
		response.InternalServerError(w, "Ошибка формирования аналитики дашборда")
		return
	}

	response.JSON(w, http.StatusOK, dashboard)
}
