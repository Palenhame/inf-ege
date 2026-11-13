package response

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	payload := map[string]string{"message": "hello"}

	JSON(rec, http.StatusOK, payload)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	contentType := rec.Header().Get("Content-Type")
	if contentType != "application/json; charset=utf-8" {
		t.Fatalf("expected Content-Type application/json; charset=utf-8, got %s", contentType)
	}

	var res map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}
	if res["message"] != "hello" {
		t.Fatalf("expected 'hello', got '%s'", res["message"])
	}
}

func TestJSON_NilData(t *testing.T) {
	rec := httptest.NewRecorder()
	JSON(rec, http.StatusNoContent, nil)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected status 204, got %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("expected empty body, got %s", rec.Body.String())
	}
}

func TestJSONPaginated(t *testing.T) {
	t.Run("with non-empty slice", func(t *testing.T) {
		rec := httptest.NewRecorder()
		items := []string{"a", "b", "c"}

		JSONPaginated(rec, http.StatusOK, items, 10, 3, 0)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}

		var envelope PaginatedEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("failed to unmarshal JSON: %v", err)
		}

		if envelope.Total != 10 || envelope.Limit != 3 || envelope.Offset != 0 {
			t.Fatalf("unexpected pagination metadata: %+v", envelope)
		}
	})

	t.Run("with nil slice converts to empty array [] not null", func(t *testing.T) {
		rec := httptest.NewRecorder()

		JSONPaginated(rec, http.StatusOK, nil, 0, 20, 0)

		body := rec.Body.String()
		expectedSubstring := `"items":[]`
		if !jsonSubstringExists(body, expectedSubstring) {
			t.Fatalf("expected body to contain %s, got: %s", expectedSubstring, body)
		}
	})
}

func TestError(t *testing.T) {
	t.Run("with details", func(t *testing.T) {
		rec := httptest.NewRecorder()
		details := map[string]any{"field": "username", "issue": "required"}

		Error(rec, http.StatusBadRequest, "VALIDATION_ERROR", "invalid request", details)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d", rec.Code)
		}

		var env ErrorEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("failed to unmarshal JSON: %v", err)
		}

		if env.Error.Code != "VALIDATION_ERROR" || env.Error.Message != "invalid request" {
			t.Fatalf("unexpected error payload: %+v", env)
		}
	})

	t.Run("with nil details converts to empty object {} not null", func(t *testing.T) {
		rec := httptest.NewRecorder()

		Error(rec, http.StatusForbidden, "SUBSCRIPTION_READ_ONLY", "Read-only access", nil)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected status 403, got %d", rec.Code)
		}

		body := rec.Body.String()
		expectedSubstring := `"details":{}`
		if !jsonSubstringExists(body, expectedSubstring) {
			t.Fatalf("expected body to contain %s, got: %s", expectedSubstring, body)
		}
	})
}

func TestErrorHelpers(t *testing.T) {
	tests := []struct {
		name       string
		call       func(w http.ResponseWriter)
		wantStatus int
		wantCode   string
	}{
		{
			name:       "BadRequest",
			call:       func(w http.ResponseWriter) { BadRequest(w, "", "bad", nil) },
			wantStatus: http.StatusBadRequest,
			wantCode:   "VALIDATION_ERROR",
		},
		{
			name:       "Unauthorized",
			call:       func(w http.ResponseWriter) { Unauthorized(w, "", "unauthorized") },
			wantStatus: http.StatusUnauthorized,
			wantCode:   "UNAUTHORIZED",
		},
		{
			name:       "Forbidden",
			call:       func(w http.ResponseWriter) { Forbidden(w, "", "forbidden") },
			wantStatus: http.StatusForbidden,
			wantCode:   "FORBIDDEN",
		},
		{
			name:       "NotFound",
			call:       func(w http.ResponseWriter) { NotFound(w, "", "not found") },
			wantStatus: http.StatusNotFound,
			wantCode:   "NOT_FOUND",
		},
		{
			name:       "Conflict",
			call:       func(w http.ResponseWriter) { Conflict(w, "", "conflict") },
			wantStatus: http.StatusConflict,
			wantCode:   "CONFLICT",
		},
		{
			name:       "InternalServerError",
			call:       func(w http.ResponseWriter) { InternalServerError(w, "") },
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL_SERVER_ERROR",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tc.call(rec)

			if rec.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d", tc.wantStatus, rec.Code)
			}

			var env ErrorEnvelope
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
				t.Fatalf("failed to parse json: %v", err)
			}
			if env.Error.Code != tc.wantCode {
				t.Fatalf("expected code %s, got %s", tc.wantCode, env.Error.Code)
			}
		})
	}
}

func jsonSubstringExists(str, substr string) bool {
	// Normalize spaces
	var compactStr []byte
	for i := 0; i < len(str); i++ {
		if str[i] != ' ' && str[i] != '\n' && str[i] != '\r' && str[i] != '\t' {
			compactStr = append(compactStr, str[i])
		}
	}
	var compactSub []byte
	for i := 0; i < len(substr); i++ {
		if substr[i] != ' ' && substr[i] != '\n' && substr[i] != '\r' && substr[i] != '\t' {
			compactSub = append(compactSub, substr[i])
		}
	}
	return contains(string(compactStr), string(compactSub))
}

func contains(s, substr string) bool {
	return len(substr) == 0 || (len(s) >= len(substr) && searchString(s, substr))
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
