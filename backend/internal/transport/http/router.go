package http

import (
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"backend/internal/transport/http/middleware"
	"backend/internal/transport/http/response"

	"github.com/go-chi/chi/v5"
)

// RouterConfig holds configuration and dependencies for HTTP transport router.
type RouterConfig struct {
	Logger              *slog.Logger
	JWTSecret           string
	AllowedOrigins      []string
	PublicStorage       http.Handler
	RateLimiter         func(http.Handler) http.Handler
	SubscriptionChecker middleware.SubscriptionChecker
	ImpersonateOpts     []middleware.ImpersonationOption
}

// HandlerRegistry provides route handlers for different domain subsystems.
type HandlerRegistry struct {
	AuthHandler       http.Handler
	UserHandler       http.Handler
	UserAliasHandler  http.Handler
	SubHandler        http.Handler
	TopicHandler      http.Handler
	BankHandler       http.Handler
	DashboardHandler  http.Handler
	RoomHandler       http.Handler
	AssignmentHandler http.Handler
	ExamHandler       http.Handler
	SessionHandler    http.Handler
	TeacherHandler    http.Handler
	AdminHandler      http.Handler
	WSHandler         http.Handler
}

// NewRouter initializes and configures the main Chi router with standard middlewares,
// /storage/* media handler, and /api/v1 route tree.
func NewRouter(cfg RouterConfig, reg *HandlerRegistry) *chi.Mux {
	r := chi.NewRouter()

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// 1. Mount Global Middleware Stack
	r.Use(CORS(cfg.AllowedOrigins))
	r.Use(middleware.Recoverer(logger))
	r.Use(middleware.Logger(logger))
	if cfg.JWTSecret != "" {
		r.Use(middleware.AuthCookie(cfg.JWTSecret))
	}
	r.Use(middleware.Impersonation(cfg.ImpersonateOpts...))
	if cfg.RateLimiter != nil {
		r.Use(cfg.RateLimiter)
	}

	// 2. Health check endpoints
	healthHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Get("/health", healthHandler)

	// 3. Mount Public Storage (/storage/*)
	storageHandler := cfg.PublicStorage
	if storageHandler == nil {
		storageHandler = defaultStorageFallback()
	}
	r.Handle("/storage/*", http.StripPrefix("/storage/", storageHandler))
	r.Handle("/storage", http.StripPrefix("/storage", storageHandler))

	// 4. WebSocket route (/ws)
	if reg != nil && reg.WSHandler != nil {
		r.Handle("/ws", reg.WSHandler)
	}

	// 5. API v1 route tree (/api/v1/*)
	r.Route("/api/v1", func(api chi.Router) {
		api.Get("/health", healthHandler)

		if reg != nil {
			mountDomainHandlers(api, reg, cfg)
		}
	})

	return r
}

func mountDomainHandlers(api chi.Router, reg *HandlerRegistry, cfg RouterConfig) {
	var subGuard func(http.Handler) http.Handler
	if cfg.SubscriptionChecker != nil {
		subGuard = middleware.SubscriptionGuard(cfg.SubscriptionChecker)
	}

	if reg.AuthHandler != nil {
		api.Mount("/auth", reg.AuthHandler)
	}
	if reg.UserHandler != nil {
		api.Mount("/users", reg.UserHandler)
	}
	if reg.UserAliasHandler != nil {
		api.Mount("/user", reg.UserAliasHandler)
	}
	if reg.SubHandler != nil {
		api.Mount("/subscriptions", reg.SubHandler)
	}
	if reg.TopicHandler != nil {
		api.Mount("/topics", reg.TopicHandler)
		api.Get("/topics", func(w http.ResponseWriter, r *http.Request) {
			if rctx := chi.RouteContext(r.Context()); rctx != nil {
				rctx.RoutePath = "/"
			}
			reg.TopicHandler.ServeHTTP(w, r)
		})
	}
	if reg.BankHandler != nil {
		api.Mount("/bank", reg.BankHandler)
	}
	if reg.DashboardHandler != nil {
		api.Mount("/dashboard", reg.DashboardHandler)
	}
	if reg.RoomHandler != nil {
		h := reg.RoomHandler
		if subGuard != nil {
			h = selectiveMiddleware(subGuard, isRoomSubscriptionProtected)(h)
		}
		api.Mount("/rooms", h)
	}
	if reg.AssignmentHandler != nil {
		h := reg.AssignmentHandler
		if subGuard != nil {
			h = selectiveMiddleware(subGuard, isAssignmentSubscriptionProtected)(h)
		}
		api.Mount("/assignments", h)
	}
	if reg.ExamHandler != nil {
		api.Mount("/exams", reg.ExamHandler)
	}
	if reg.SessionHandler != nil {
		api.Mount("/sessions", reg.SessionHandler)
	}
	if reg.TeacherHandler != nil {
		h := reg.TeacherHandler
		if subGuard != nil {
			h = selectiveMiddleware(subGuard, isTeacherSubscriptionProtected)(h)
		}
		api.Mount("/teacher", h)
	}
	if reg.AdminHandler != nil {
		api.Mount("/admin", reg.AdminHandler)
	}
}

func selectiveMiddleware(mw func(http.Handler) http.Handler, match func(*http.Request) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		guarded := mw(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if match(r) {
				guarded.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func splitSubPath(r *http.Request, mountPrefix string) []string {
	var p string
	if rctx := chi.RouteContext(r.Context()); rctx != nil && rctx.RoutePath != "" {
		p = rctx.RoutePath
	} else {
		p = strings.TrimPrefix(r.URL.Path, "/api/v1"+mountPrefix)
	}
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

func isRoomSubscriptionProtected(r *http.Request) bool {
	segs := splitSubPath(r, "/rooms")
	switch r.Method {
	case http.MethodPost:
		// POST / (create room) and POST /{room_id}/assignments (create assignment)
		return len(segs) == 0 || (len(segs) == 2 && segs[1] == "assignments")
	case http.MethodPatch:
		// PATCH /{id} (update room, excluding /join/...)
		return len(segs) == 1 && segs[0] != "join"
	default:
		return false
	}
}

func isAssignmentSubscriptionProtected(r *http.Request) bool {
	if r.Method != http.MethodPatch {
		return false
	}
	segs := splitSubPath(r, "/assignments")
	// PATCH /{id}
	return len(segs) == 1
}

func isTeacherSubscriptionProtected(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	segs := splitSubPath(r, "/teacher")
	// POST /tasks
	return len(segs) == 1 && segs[0] == "tasks"
}

// CORS middleware handles Cross-Origin Resource Sharing with credentials and preflight OPTIONS.
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	allowedMap := make(map[string]struct{}, len(allowedOrigins))
	allowAll := len(allowedOrigins) == 0
	for _, o := range allowedOrigins {
		if o == "*" {
			allowAll = true
		} else {
			allowedMap[o] = struct{}{}
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			if origin != "" {
				_, isAllowed := allowedMap[origin]
				if allowAll || isAllowed {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Access-Control-Allow-Credentials", "true")
				}
			} else if allowAll {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			}

			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS, HEAD")
			w.Header().Set("Access-Control-Allow-Headers", "Accept, Authorization, Content-Type, X-CSRF-Token, X-Impersonate-User")
			w.Header().Set("Access-Control-Expose-Headers", "Link, X-RateLimit-Limit, X-RateLimit-Remaining, Retry-After")

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// NewMinIOStorageReverseProxy creates a reverse proxy to stream public objects from MinIO bucket.
func NewMinIOStorageReverseProxy(minioEndpoint, publicBucket string) (http.Handler, error) {
	if !strings.HasPrefix(minioEndpoint, "http://") && !strings.HasPrefix(minioEndpoint, "https://") {
		minioEndpoint = "http://" + minioEndpoint
	}
	targetURL, err := url.Parse(minioEndpoint)
	if err != nil {
		return nil, err
	}

	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	originalDirector := proxy.Director

	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		// Rewrite path: /{key} -> /{publicBucket}/{key}
		key := strings.TrimPrefix(req.URL.Path, "/")
		req.URL.Path = "/" + publicBucket + "/" + key
		req.Host = targetURL.Host
	}

	return proxy, nil
}

func defaultStorageFallback() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response.NotFound(w, "NOT_FOUND", "Storage object not found")
	})
}
