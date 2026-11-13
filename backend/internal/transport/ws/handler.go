package ws

import (
	"errors"
	"net/http"

	"backend/internal/transport/http/middleware"
	"backend/internal/transport/http/response"

	"github.com/coder/websocket"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// HandlerConfig holds configuration for the WebSocket HTTP upgrade handler.
type HandlerConfig struct {
	Hub           *Hub
	JWTSecret     string
	AcceptOptions *websocket.AcceptOptions
}

// Handler implements http.Handler for upgrading HTTP requests at /ws.
type Handler struct {
	hub           *Hub
	jwtSecret     []byte
	acceptOptions *websocket.AcceptOptions
}

// NewHandler creates a new WebSocket Handler.
func NewHandler(cfg HandlerConfig) *Handler {
	opts := cfg.AcceptOptions
	if opts == nil {
		opts = &websocket.AcceptOptions{
			InsecureSkipVerify: true, // Allow same-origin & dev proxies
		}
	}
	return &Handler{
		hub:           cfg.Hub,
		jwtSecret:     []byte(cfg.JWTSecret),
		acceptOptions: opts,
	}
}

// ServeHTTP authenticates the connection, upgrades to WebSocket, registers client to Hub,
// and runs the read and write pumps.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 1. Resolve authenticated user context
	userID, role, err := h.authenticate(r)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			response.Error(w, http.StatusUnauthorized, "TOKEN_EXPIRED", "Токен доступа истёк", nil)
			return
		}
		response.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "Требуется авторизация для подключения к WebSocket", nil)
		return
	}

	// 2. Upgrade HTTP connection to WebSocket
	conn, err := websocket.Accept(w, r, h.acceptOptions)
	if err != nil {
		// websocket.Accept writes HTTP error response automatically
		return
	}

	// 3. Create client and register in Hub
	client := NewClient(userID, role, conn, h.hub)
	if h.hub != nil {
		h.hub.Register(client)
	}

	// 4. Start WritePump in background goroutine, run ReadPump in current handler
	go client.WritePump(r.Context())
	client.ReadPump(r.Context())
}

// authenticate extracts user ID and role from request context, cookie, or query param.
func (h *Handler) authenticate(r *http.Request) (uuid.UUID, string, error) {
	// First check if middleware already injected UserContext into request context
	if u, ok := middleware.GetUserContext(r.Context()); ok && u.IsAuthenticated {
		return u.UserID, u.Role, nil
	}

	// Otherwise, inspect cookie "ege_access"
	var tokenStr string
	if cookie, err := r.Cookie("ege_access"); err == nil && cookie != nil && cookie.Value != "" {
		tokenStr = cookie.Value
	} else if qToken := r.URL.Query().Get("token"); qToken != "" {
		// Fallback for WebSocket clients that cannot pass cookies directly
		tokenStr = qToken
	}

	if tokenStr == "" {
		return uuid.Nil, "", errors.New("missing authentication token")
	}

	// Parse JWT token
	claims := &middleware.UserClaims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return h.jwtSecret, nil
	})

	if err != nil {
		return uuid.Nil, "", err
	}

	if !token.Valid || claims.Subject == "" {
		return uuid.Nil, "", errors.New("invalid token claims")
	}

	parsedID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return uuid.Nil, "", errors.New("invalid subject UUID in token")
	}

	role := claims.Role
	if role == "" {
		role = "student"
	}

	return parsedID, role, nil
}
