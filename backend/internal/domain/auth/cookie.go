package auth

import (
	"net/http"
	"time"
)

const (
	AccessCookieName     = "ege_access"
	RefreshCookieName    = "ege_refresh"
	OAuthStateCookieName = "ege_oauth_state"

	DefaultAccessCookieTTL  = 15 * time.Minute
	DefaultRefreshCookieTTL = 30 * 24 * time.Hour
	DefaultOAuthStateTTL    = 5 * time.Minute
)

// CookieConfig holds cookie configuration settings.
type CookieConfig struct {
	Secure   bool
	Domain   string
	SameSite http.SameSite
}

// DefaultCookieConfig returns default security settings for auth cookies.
func DefaultCookieConfig(secure bool) CookieConfig {
	return CookieConfig{
		Secure:   secure,
		Domain:   "",
		SameSite: http.SameSiteLaxMode,
	}
}

// SetAccessCookie sets the httpOnly ege_access cookie with 15m TTL.
func SetAccessCookie(w http.ResponseWriter, token string, cfg CookieConfig) {
	sameSite := cfg.SameSite
	if sameSite == 0 {
		sameSite = http.SameSiteLaxMode
	}
	cookie := &http.Cookie{
		Name:     AccessCookieName,
		Value:    token,
		Path:     "/",
		Domain:   cfg.Domain,
		MaxAge:   int(DefaultAccessCookieTTL.Seconds()),
		Expires:  time.Now().Add(DefaultAccessCookieTTL),
		HttpOnly: true,
		Secure:   cfg.Secure,
		SameSite: sameSite,
	}
	http.SetCookie(w, cookie)
}

// SetRefreshCookie sets the httpOnly ege_refresh cookie with 30-day TTL.
func SetRefreshCookie(w http.ResponseWriter, token string, cfg CookieConfig) {
	sameSite := cfg.SameSite
	if sameSite == 0 {
		sameSite = http.SameSiteLaxMode
	}
	cookie := &http.Cookie{
		Name:     RefreshCookieName,
		Value:    token,
		Path:     "/",
		Domain:   cfg.Domain,
		MaxAge:   int(DefaultRefreshCookieTTL.Seconds()),
		Expires:  time.Now().Add(DefaultRefreshCookieTTL),
		HttpOnly: true,
		Secure:   cfg.Secure,
		SameSite: sameSite,
	}
	http.SetCookie(w, cookie)
}

// SetAuthCookies sets both ege_access and ege_refresh cookies.
func SetAuthCookies(w http.ResponseWriter, accessToken, refreshToken string, cfg CookieConfig) {
	SetAccessCookie(w, accessToken, cfg)
	SetRefreshCookie(w, refreshToken, cfg)
}

// ClearAccessCookie deletes the ege_access cookie.
func ClearAccessCookie(w http.ResponseWriter, cfg CookieConfig) {
	sameSite := cfg.SameSite
	if sameSite == 0 {
		sameSite = http.SameSiteLaxMode
	}
	cookie := &http.Cookie{
		Name:     AccessCookieName,
		Value:    "",
		Path:     "/",
		Domain:   cfg.Domain,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		Secure:   cfg.Secure,
		SameSite: sameSite,
	}
	http.SetCookie(w, cookie)
}

// ClearRefreshCookie deletes the ege_refresh cookie.
func ClearRefreshCookie(w http.ResponseWriter, cfg CookieConfig) {
	sameSite := cfg.SameSite
	if sameSite == 0 {
		sameSite = http.SameSiteLaxMode
	}
	cookie := &http.Cookie{
		Name:     RefreshCookieName,
		Value:    "",
		Path:     "/",
		Domain:   cfg.Domain,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		Secure:   cfg.Secure,
		SameSite: sameSite,
	}
	http.SetCookie(w, cookie)
}

// ClearAuthCookies deletes both ege_access and ege_refresh cookies.
func ClearAuthCookies(w http.ResponseWriter, cfg CookieConfig) {
	ClearAccessCookie(w, cfg)
	ClearRefreshCookie(w, cfg)
}

// SetOAuthStateCookie sets a short-lived (5 min) httpOnly cookie containing state and optional PKCE/redirect.
func SetOAuthStateCookie(w http.ResponseWriter, stateValue string, cfg CookieConfig) {
	sameSite := cfg.SameSite
	if sameSite == 0 {
		sameSite = http.SameSiteLaxMode
	}
	cookie := &http.Cookie{
		Name:     OAuthStateCookieName,
		Value:    stateValue,
		Path:     "/",
		Domain:   cfg.Domain,
		MaxAge:   int(DefaultOAuthStateTTL.Seconds()),
		Expires:  time.Now().Add(DefaultOAuthStateTTL),
		HttpOnly: true,
		Secure:   cfg.Secure,
		SameSite: sameSite,
	}
	http.SetCookie(w, cookie)
}

// ClearOAuthStateCookie deletes the OAuth state cookie.
func ClearOAuthStateCookie(w http.ResponseWriter, cfg CookieConfig) {
	sameSite := cfg.SameSite
	if sameSite == 0 {
		sameSite = http.SameSiteLaxMode
	}
	cookie := &http.Cookie{
		Name:     OAuthStateCookieName,
		Value:    "",
		Path:     "/",
		Domain:   cfg.Domain,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		Secure:   cfg.Secure,
		SameSite: sameSite,
	}
	http.SetCookie(w, cookie)
}

// GetCookieValue retrieves the cookie value by name or returns empty string if absent.
func GetCookieValue(r *http.Request, name string) string {
	cookie, err := r.Cookie(name)
	if err != nil || cookie == nil {
		return ""
	}
	return cookie.Value
}
