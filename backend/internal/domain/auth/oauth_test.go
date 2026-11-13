package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestOAuth_YandexFlow(t *testing.T) {
	// Mock Yandex OAuth server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_ = r.ParseForm()
			code := r.Form.Get("code")
			if code != "valid-yandex-code" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"bad_code","error_description":"invalid code"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "mock-yandex-token",
				"token_type":   "bearer",
				"expires_in":   3600,
			})

		case "/info":
			authHeader := r.Header.Get("Authorization")
			if authHeader != "OAuth mock-yandex-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(YandexUserInfoResponse{
				ID:           "yandex-user-999",
				Login:        "super_teacher",
				FirstName:    "Алексей",
				LastName:     "Смирнов",
				DefaultEmail: "alex@yandex.ru",
			})

		default:
			http.NotFound(w, r)
		}
	}))
	defer mockServer.Close()

	cfg := YandexConfig{
		ClientID:           "mock-client-id",
		ClientSecret:       "mock-secret",
		RedirectURI:        "http://localhost:8000/callback",
		BootstrapSuperuser: "yandex-user-999", // should become superuser
		TokenURL:           mockServer.URL + "/token",
		UserInfoURL:        mockServer.URL + "/info",
		HTTPClient:         mockServer.Client(),
	}

	provider := NewYandexProvider(cfg)

	// Test BuildAuthURL
	authURL, err := provider.BuildAuthURL("csrf-state-123")
	if err != nil {
		t.Fatalf("unexpected error building auth URL: %v", err)
	}
	parsedURL, _ := url.Parse(authURL)
	if parsedURL.Query().Get("client_id") != "mock-client-id" {
		t.Errorf("expected client_id in query, got %s", parsedURL.Query().Get("client_id"))
	}
	if parsedURL.Query().Get("state") != "csrf-state-123" {
		t.Errorf("expected state in query, got %s", parsedURL.Query().Get("state"))
	}

	// Test Code Exchange
	info, err := provider.ExchangeCodeAndGetUserInfo(context.Background(), "valid-yandex-code")
	if err != nil {
		t.Fatalf("unexpected error exchanging code: %v", err)
	}

	if info.Provider != "yandex" {
		t.Errorf("expected provider yandex, got %s", info.Provider)
	}
	if info.ProviderID != "yandex-user-999" {
		t.Errorf("expected providerID yandex-user-999, got %s", info.ProviderID)
	}
	if info.FirstName != "Алексей" {
		t.Errorf("expected first name Алексей, got %s", info.FirstName)
	}
	if info.LastName != "Смирнов" {
		t.Errorf("expected last name Смирнов, got %s", info.LastName)
	}
	if !info.IsSuperuser {
		t.Error("expected IsSuperuser to be true due to bootstrap match")
	}
}

func TestOAuth_VKFlow(t *testing.T) {
	// Mock VK OAuth server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_ = r.ParseForm()
			code := r.Form.Get("code")
			verifier := r.Form.Get("code_verifier")
			if code != "valid-vk-code" || verifier == "" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_request"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "mock-vk-token",
				"token_type":   "bearer",
				"expires_in":   3600,
				"user_id":      "vk-user-456",
			})

		case "/user_info":
			authHeader := r.Header.Get("Authorization")
			if !strings.Contains(authHeader, "mock-vk-token") {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(VKUserInfoWrapper{
				User: VKUser{
					UserID:    "vk-user-456",
					FirstName: "Дмитрий",
					LastName:  "Ковалёв",
					Email:     "dmitry@vk.com",
				},
			})

		default:
			http.NotFound(w, r)
		}
	}))
	defer mockServer.Close()

	cfg := VKConfig{
		ClientID:           "vk-client-id",
		ClientSecret:       "vk-secret",
		RedirectURI:        "http://localhost:8000/callback",
		BootstrapSuperuser: "vk-superuser-root",
		TokenURL:           mockServer.URL + "/token",
		UserInfoURL:        mockServer.URL + "/user_info",
		HTTPClient:         mockServer.Client(),
	}

	provider := NewVKProvider(cfg)

	// Test PKCE generation
	pkce, err := GeneratePKCE()
	if err != nil {
		t.Fatalf("unexpected error generating pkce: %v", err)
	}
	if pkce.Verifier == "" || pkce.Challenge == "" {
		t.Fatal("expected non-empty pkce verifier and challenge")
	}

	// Test BuildAuthURL
	authURL, err := provider.BuildAuthURL("vk-state-1", pkce.Challenge)
	if err != nil {
		t.Fatalf("unexpected error building vk auth URL: %v", err)
	}
	if !strings.Contains(authURL, pkce.Challenge) {
		t.Error("expected auth URL to contain PKCE challenge")
	}

	// Test Code Exchange
	info, err := provider.ExchangeCodeAndGetUserInfo(context.Background(), "valid-vk-code", pkce.Verifier)
	if err != nil {
		t.Fatalf("unexpected error exchanging vk code: %v", err)
	}

	if info.Provider != "vk" {
		t.Errorf("expected provider vk, got %s", info.Provider)
	}
	if info.ProviderID != "vk-user-456" {
		t.Errorf("expected providerID vk-user-456, got %s", info.ProviderID)
	}
	if info.FirstName != "Дмитрий" {
		t.Errorf("expected first name Дмитрий, got %s", info.FirstName)
	}
	if info.IsSuperuser {
		t.Error("expected IsSuperuser to be false (not in bootstrap)")
	}
}
