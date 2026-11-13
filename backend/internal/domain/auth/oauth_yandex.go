package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OAuthUserInfo holds normalized user profile information returned by OAuth providers.
type OAuthUserInfo struct {
	Provider    string
	ProviderID  string
	Username    string
	FirstName   string
	LastName    string
	Email       string
	IsSuperuser bool
}

// YandexConfig holds configuration for Yandex ID OAuth.
type YandexConfig struct {
	ClientID          string
	ClientSecret      string
	RedirectURI       string
	BootstrapSuperuser string // BOOTSTRAP_SUPERUSER_YANDEX_ID

	// Optional endpoint overrides for testing
	AuthURL     string
	TokenURL    string
	UserInfoURL string
	HTTPClient  *http.Client
}

// YandexProvider handles Yandex ID OAuth 2.0 flow.
type YandexProvider struct {
	cfg        YandexConfig
	httpClient *http.Client
}

// NewYandexProvider creates a new Yandex OAuth provider.
func NewYandexProvider(cfg YandexConfig) *YandexProvider {
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	if cfg.AuthURL == "" {
		cfg.AuthURL = "https://oauth.yandex.ru/authorize"
	}
	if cfg.TokenURL == "" {
		cfg.TokenURL = "https://oauth.yandex.ru/token"
	}
	if cfg.UserInfoURL == "" {
		cfg.UserInfoURL = "https://login.yandex.ru/info"
	}
	return &YandexProvider{
		cfg:        cfg,
		httpClient: client,
	}
}

// BuildAuthURL constructs the authorization URL for Yandex ID.
func (p *YandexProvider) BuildAuthURL(state string) (string, error) {
	if p.cfg.ClientID == "" {
		return "", errors.New("yandex client ID is not configured")
	}

	u, err := url.Parse(p.cfg.AuthURL)
	if err != nil {
		return "", fmt.Errorf("invalid yandex auth URL: %w", err)
	}

	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", p.cfg.ClientID)
	q.Set("state", state)
	if p.cfg.RedirectURI != "" {
		q.Set("redirect_uri", p.cfg.RedirectURI)
	}
	u.RawQuery = q.Encode()

	return u.String(), nil
}

// YandexTokenResponse holds the token exchange response from Yandex.
type YandexTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

// YandexUserInfoResponse holds user profile data from Yandex login.yandex.ru/info.
type YandexUserInfoResponse struct {
	ID           string   `json:"id"`
	Login        string   `json:"login"`
	FirstName    string   `json:"first_name"`
	LastName     string   `json:"last_name"`
	DisplayName  string   `json:"display_name"`
	DefaultEmail string   `json:"default_email"`
	Emails       []string `json:"emails"`
}

// ExchangeCodeAndGetUserInfo exchanges authorization code for user info.
func (p *YandexProvider) ExchangeCodeAndGetUserInfo(ctx context.Context, code string) (*OAuthUserInfo, error) {
	if code == "" {
		return nil, errors.New("authorization code is empty")
	}

	// 1. Exchange code for access token
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {p.cfg.ClientID},
		"client_secret": {p.cfg.ClientSecret},
	}
	if p.cfg.RedirectURI != "" {
		form.Set("redirect_uri", p.cfg.RedirectURI)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create yandex token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("yandex token request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read yandex token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("yandex token endpoint returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var tokenResp YandexTokenResponse
	if err := json.Unmarshal(bodyBytes, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse yandex token response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("yandex returned empty access token: %s", tokenResp.ErrorDesc)
	}

	// 2. Fetch user profile
	infoURL := fmt.Sprintf("%s?format=json", p.cfg.UserInfoURL)
	infoReq, err := http.NewRequestWithContext(ctx, http.MethodGet, infoURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create yandex user info request: %w", err)
	}
	infoReq.Header.Set("Authorization", "OAuth "+tokenResp.AccessToken)

	infoResp, err := p.httpClient.Do(infoReq)
	if err != nil {
		return nil, fmt.Errorf("yandex user info request failed: %w", err)
	}
	defer infoResp.Body.Close()

	infoBody, err := io.ReadAll(infoResp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read yandex user info response: %w", err)
	}

	if infoResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("yandex user info returned status %d: %s", infoResp.StatusCode, string(infoBody))
	}

	var userInfo YandexUserInfoResponse
	if err := json.Unmarshal(infoBody, &userInfo); err != nil {
		return nil, fmt.Errorf("failed to parse yandex user info response: %w", err)
	}

	if userInfo.ID == "" {
		return nil, errors.New("yandex user info missing ID")
	}

	email := userInfo.DefaultEmail
	if email == "" && len(userInfo.Emails) > 0 {
		email = userInfo.Emails[0]
	}

	username := userInfo.Login
	if username == "" {
		username = userInfo.DisplayName
	}

	isSuperuser := p.cfg.BootstrapSuperuser != "" && p.cfg.BootstrapSuperuser == userInfo.ID

	return &OAuthUserInfo{
		Provider:    "yandex",
		ProviderID:  userInfo.ID,
		Username:    username,
		FirstName:   userInfo.FirstName,
		LastName:    userInfo.LastName,
		Email:       email,
		IsSuperuser: isSuperuser,
	}, nil
}
