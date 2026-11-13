package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// VKConfig holds configuration for VK ID OAuth.
type VKConfig struct {
	ClientID           string
	ClientSecret       string
	RedirectURI        string
	BootstrapSuperuser string // BOOTSTRAP_SUPERUSER_VK_ID

	// Optional endpoint overrides for testing
	AuthURL     string
	TokenURL    string
	UserInfoURL string
	HTTPClient  *http.Client
}

// VKProvider handles VK ID OAuth flow with PKCE support.
type VKProvider struct {
	cfg        VKConfig
	httpClient *http.Client
}

// NewVKProvider creates a new VK OAuth provider.
func NewVKProvider(cfg VKConfig) *VKProvider {
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	if cfg.AuthURL == "" {
		cfg.AuthURL = "https://id.vk.com/auth"
	}
	if cfg.TokenURL == "" {
		cfg.TokenURL = "https://id.vk.com/oauth2/auth"
	}
	if cfg.UserInfoURL == "" {
		cfg.UserInfoURL = "https://id.vk.com/oauth2/user_info"
	}
	return &VKProvider{
		cfg:        cfg,
		httpClient: client,
	}
}

// PKCEPair holds a cryptographically secure verifier and challenge.
type PKCEPair struct {
	Verifier  string `json:"verifier"`
	Challenge string `json:"challenge"`
}

// GeneratePKCE generates an RFC 7636 compliant PKCE code verifier and SHA-256 code challenge.
func GeneratePKCE() (*PKCEPair, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return nil, fmt.Errorf("failed to generate random bytes for PKCE: %w", err)
	}

	verifier := base64.RawURLEncoding.EncodeToString(bytes)
	h := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(h[:])

	return &PKCEPair{
		Verifier:  verifier,
		Challenge: challenge,
	}, nil
}

// BuildAuthURL constructs the authorization URL for VK ID with PKCE.
func (p *VKProvider) BuildAuthURL(state, codeChallenge string) (string, error) {
	if p.cfg.ClientID == "" {
		return "", errors.New("vk client ID is not configured")
	}

	u, err := url.Parse(p.cfg.AuthURL)
	if err != nil {
		return "", fmt.Errorf("invalid vk auth URL: %w", err)
	}

	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", p.cfg.ClientID)
	q.Set("state", state)
	if codeChallenge != "" {
		q.Set("code_challenge", codeChallenge)
		q.Set("code_challenge_method", "s256")
	}
	if p.cfg.RedirectURI != "" {
		q.Set("redirect_uri", p.cfg.RedirectURI)
	}
	q.Set("scope", "vkid.personal_info")
	u.RawQuery = q.Encode()

	return u.String(), nil
}

// VKTokenResponse holds token response fields from VK.
type VKTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	UserID      any    `json:"user_id"` // can be string or int
	IDToken     string `json:"id_token"`
	Error       string `json:"error"`
	ErrorDesc   string `json:"error_description"`
}

// VKUserInfoWrapper holds user info payload from VK user_info endpoint.
type VKUserInfoWrapper struct {
	User VKUser `json:"user"`
	// Also fallback for standard users.get response
	Response []VKUser `json:"response"`
}

type VKUser struct {
	UserID    any    `json:"user_id"`
	ID        any    `json:"id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Email     string `json:"email"`
	Phone     string `json:"phone"`
}

// ExchangeCodeAndGetUserInfo exchanges code (with codeVerifier) for VK profile info.
func (p *VKProvider) ExchangeCodeAndGetUserInfo(ctx context.Context, code, codeVerifier string) (*OAuthUserInfo, error) {
	if code == "" {
		return nil, errors.New("authorization code is empty")
	}

	form := url.Values{
		"grant_type": {"authorization_code"},
		"code":       {code},
		"client_id":  {p.cfg.ClientID},
	}
	if p.cfg.ClientSecret != "" {
		form.Set("client_secret", p.cfg.ClientSecret)
	}
	if codeVerifier != "" {
		form.Set("code_verifier", codeVerifier)
	}
	if p.cfg.RedirectURI != "" {
		form.Set("redirect_uri", p.cfg.RedirectURI)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create vk token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("vk token request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read vk token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("vk token endpoint returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var tokenResp VKTokenResponse
	if err := json.Unmarshal(bodyBytes, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse vk token response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("vk returned empty access token: %s", tokenResp.ErrorDesc)
	}

	// 2. Fetch user profile
	var userInfoReq *http.Request
	// Support both ID.VK and classic API
	if strings.Contains(p.cfg.UserInfoURL, "users.get") {
		u, _ := url.Parse(p.cfg.UserInfoURL)
		q := u.Query()
		q.Set("access_token", tokenResp.AccessToken)
		q.Set("v", "5.199")
		q.Set("fields", "first_name,last_name,screen_name")
		u.RawQuery = q.Encode()
		userInfoReq, err = http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	} else {
		userInfoReq, err = http.NewRequestWithContext(ctx, http.MethodGet, p.cfg.UserInfoURL, nil)
		if err == nil {
			userInfoReq.Header.Set("Authorization", "Bearer "+tokenResp.AccessToken)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create vk user info request: %w", err)
	}

	infoResp, err := p.httpClient.Do(userInfoReq)
	if err != nil {
		return nil, fmt.Errorf("vk user info request failed: %w", err)
	}
	defer infoResp.Body.Close()

	infoBody, err := io.ReadAll(infoResp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read vk user info response: %w", err)
	}

	if infoResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("vk user info returned status %d: %s", infoResp.StatusCode, string(infoBody))
	}

	var wrapper VKUserInfoWrapper
	if err := json.Unmarshal(infoBody, &wrapper); err != nil {
		return nil, fmt.Errorf("failed to parse vk user info: %w", err)
	}

	var user VKUser
	if wrapper.User.FirstName != "" || wrapper.User.ID != nil || wrapper.User.UserID != nil {
		user = wrapper.User
	} else if len(wrapper.Response) > 0 {
		user = wrapper.Response[0]
	}

	userIDStr := extractID(user.UserID)
	if userIDStr == "" {
		userIDStr = extractID(user.ID)
	}
	if userIDStr == "" {
		userIDStr = extractID(tokenResp.UserID)
	}

	if userIDStr == "" {
		return nil, errors.New("vk user info missing ID")
	}

	isSuperuser := p.cfg.BootstrapSuperuser != "" && p.cfg.BootstrapSuperuser == userIDStr

	return &OAuthUserInfo{
		Provider:    "vk",
		ProviderID:  userIDStr,
		Username:    "",
		FirstName:   user.FirstName,
		LastName:    user.LastName,
		Email:       user.Email,
		IsSuperuser: isSuperuser,
	}, nil
}

func extractID(val any) string {
	if val == nil {
		return ""
	}
	switch v := val.(type) {
	case string:
		return v
	case float64:
		return strconv.FormatInt(int64(v), 10)
	case int64:
		return strconv.FormatInt(v, 10)
	case int:
		return strconv.Itoa(v)
	default:
		return fmt.Sprintf("%v", v)
	}
}
