package response

import (
	"encoding/json"
	"net/http"
)

// ErrorBody matches Contract.md uniform error object.
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details"`
}

// ErrorEnvelope wraps ErrorBody under "error" key.
type ErrorEnvelope struct {
	Error ErrorBody `json:"error"`
}

// PaginatedEnvelope matches Contract.md pagination standard:
// {"items": [], "total": int, "limit": int, "offset": int}
type PaginatedEnvelope struct {
	Items  any `json:"items"`
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

// JSON writes a JSON response with the given HTTP status code.
func JSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if data == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(data); err != nil {
		http.Error(w, `{"error":{"code":"INTERNAL_ERROR","message":"Failed to encode JSON","details":{}}}`, http.StatusInternalServerError)
	}
}

// JSONPaginated writes a paginated JSON response matching Contract.md standard.
func JSONPaginated(w http.ResponseWriter, status int, items any, total, limit, offset int) {
	if items == nil {
		items = []any{}
	}
	JSON(w, status, PaginatedEnvelope{
		Items:  items,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	})
}

// Error writes a standard error JSON response matching Contract.md.
func Error(w http.ResponseWriter, status int, code, message string, details any) {
	if details == nil {
		details = map[string]any{}
	}
	JSON(w, status, ErrorEnvelope{
		Error: ErrorBody{
			Code:    code,
			Message: message,
			Details: details,
		},
	})
}

// BadRequest writes a 400 error.
func BadRequest(w http.ResponseWriter, code, message string, details any) {
	if code == "" {
		code = "VALIDATION_ERROR"
	}
	Error(w, http.StatusBadRequest, code, message, details)
}

// Unauthorized writes a 401 error.
func Unauthorized(w http.ResponseWriter, code, message string) {
	if code == "" {
		code = "UNAUTHORIZED"
	}
	Error(w, http.StatusUnauthorized, code, message, nil)
}

// Forbidden writes a 403 error.
func Forbidden(w http.ResponseWriter, code, message string) {
	if code == "" {
		code = "FORBIDDEN"
	}
	Error(w, http.StatusForbidden, code, message, nil)
}

// NotFound writes a 404 error.
func NotFound(w http.ResponseWriter, code, message string) {
	if code == "" {
		code = "NOT_FOUND"
	}
	Error(w, http.StatusNotFound, code, message, nil)
}

// Conflict writes a 409 error.
func Conflict(w http.ResponseWriter, code, message string) {
	if code == "" {
		code = "CONFLICT"
	}
	Error(w, http.StatusConflict, code, message, nil)
}

// InternalServerError writes a 500 error.
func InternalServerError(w http.ResponseWriter, message string) {
	if message == "" {
		message = "Внутренняя ошибка сервера"
	}
	Error(w, http.StatusInternalServerError, "INTERNAL_SERVER_ERROR", message, nil)
}
