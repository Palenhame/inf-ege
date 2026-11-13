package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCookie_SetAndClearAuthCookies(t *testing.T) {
	w := httptest.NewRecorder()
	cfg := DefaultCookieConfig(true)

	SetAuthCookies(w, "access-token-123", "refresh-token-456", cfg)

	resp := w.Result()
	cookies := resp.Cookies()

	var accessCookie, refreshCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == AccessCookieName {
			accessCookie = c
		}
		if c.Name == RefreshCookieName {
			refreshCookie = c
		}
	}

	if accessCookie == nil {
		t.Fatal("ege_access cookie was not set")
	}
	if accessCookie.Value != "access-token-123" {
		t.Errorf("expected access cookie value 'access-token-123', got '%s'", accessCookie.Value)
	}
	if !accessCookie.HttpOnly {
		t.Error("expected access cookie to be HttpOnly")
	}
	if !accessCookie.Secure {
		t.Error("expected access cookie to be Secure")
	}
	if accessCookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("expected access cookie SameSite=Lax, got %v", accessCookie.SameSite)
	}
	if accessCookie.Path != "/" {
		t.Errorf("expected path '/', got '%s'", accessCookie.Path)
	}

	if refreshCookie == nil {
		t.Fatal("ege_refresh cookie was not set")
	}
	if refreshCookie.Value != "refresh-token-456" {
		t.Errorf("expected refresh cookie value 'refresh-token-456', got '%s'", refreshCookie.Value)
	}
	if !refreshCookie.HttpOnly {
		t.Error("expected refresh cookie to be HttpOnly")
	}
	if !refreshCookie.Secure {
		t.Error("expected refresh cookie to be Secure")
	}

	// Test Clearing cookies
	wClear := httptest.NewRecorder()
	ClearAuthCookies(wClear, cfg)

	clearResp := wClear.Result()
	clearCookies := clearResp.Cookies()

	for _, c := range clearCookies {
		if c.Name == AccessCookieName || c.Name == RefreshCookieName {
			if c.MaxAge != -1 {
				t.Errorf("expected cleared cookie %s to have MaxAge=-1, got %d", c.Name, c.MaxAge)
			}
			if c.Value != "" {
				t.Errorf("expected cleared cookie %s to have empty value, got '%s'", c.Name, c.Value)
			}
		}
	}
}

func TestCookie_GetCookieValue(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{
		Name:  "test_cookie",
		Value: "test_val",
	})

	val := GetCookieValue(req, "test_cookie")
	if val != "test_val" {
		t.Errorf("expected 'test_val', got '%s'", val)
	}

	missing := GetCookieValue(req, "non_existent")
	if missing != "" {
		t.Errorf("expected empty string for missing cookie, got '%s'", missing)
	}
}
