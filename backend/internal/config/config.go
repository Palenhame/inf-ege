package config

import (
	"bufio"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config aggregates all platform configuration.
type Config struct {
	App        AppConfig
	Postgres   PostgresConfig
	Redis      RedisConfig
	S3         S3Config
	Auth       AuthConfig
	OAuth      OAuthConfig
	Bootstrap  BootstrapConfig
	Migrations MigrationsConfig
}

// AppConfig defines core application HTTP settings.
type AppConfig struct {
	Env     string // "development" | "production"
	BaseURL string // default "http://localhost:5173"
	Port    int    // default 8000
}

// PostgresConfig holds PostgreSQL connection and pool parameters.
type PostgresConfig struct {
	Host              string
	Port              int
	User              string
	Password          string
	DBName            string
	SSLMode           string
	MaxConns          int32
	MinConns          int32
	MaxConnLifetime   time.Duration
	MaxConnIdleTime   time.Duration
	HealthCheckPeriod time.Duration
}

// DSN returns the PostgreSQL connection string.
func (p *PostgresConfig) DSN() string {
	var userPass string
	if p.Password != "" {
		userPass = fmt.Sprintf("%s:%s@", url.QueryEscape(p.User), url.QueryEscape(p.Password))
	} else if p.User != "" {
		userPass = fmt.Sprintf("%s@", url.QueryEscape(p.User))
	}
	return fmt.Sprintf("postgres://%s%s:%d/%s?sslmode=%s",
		userPass,
		p.Host,
		p.Port,
		p.DBName,
		p.SSLMode,
	)
}

// RedisConfig defines Redis connection parameters.
type RedisConfig struct {
	Addr     string
	Password string
	DB       int
}

// S3Config holds MinIO / S3 object storage configuration.
type S3Config struct {
	Endpoint      string
	AccessKey     string
	SecretKey     string
	UseSSL        bool
	MediaBucket   string
	PrivateBucket string
}

// AuthConfig defines JWT and cookie security parameters.
type AuthConfig struct {
	JWTSecret    string
	AccessTTL    time.Duration
	RefreshTTL   time.Duration
	CookieDomain string
	CookieSecure bool
}

// OAuthConfig defines OAuth provider settings for Yandex and VK.
type OAuthConfig struct {
	YandexClientID     string
	YandexClientSecret string
	YandexRedirectURL  string

	VKClientID     string
	VKClientSecret string
	VKRedirectURL  string
}

// BootstrapConfig holds user identifiers to promote to superuser on first login.
type BootstrapConfig struct {
	SuperuserYandexID string
	SuperuserVKID     string
}

// MigrationsConfig holds goose migrations configuration.
type MigrationsConfig struct {
	Dir string
}

// IsDevelopment returns true if current environment is development.
func (c *Config) IsDevelopment() bool {
	return strings.ToLower(c.App.Env) == "development"
}

// IsProduction returns true if current environment is production.
func (c *Config) IsProduction() bool {
	return strings.ToLower(c.App.Env) == "production"
}

// ListenAddr returns the formatted HTTP listen address (e.g. ":8000").
func (c *Config) ListenAddr() string {
	return fmt.Sprintf(":%d", c.App.Port)
}

// Load reads configuration from environment variables with sensible defaults.
// If a .env file is present in the working directory or parent directory, it is loaded first.
func Load() (*Config, error) {
	loadDotEnv(".env")
	loadDotEnv("../.env")

	cfg := &Config{
		App: AppConfig{
			Env:     getEnv("APP_ENV", "development"),
			BaseURL: getEnv("APP_BASE_URL", "http://localhost:5173"),
			Port:    getEnvInt("BACKEND_PORT", 8000),
		},
		Postgres: PostgresConfig{
			Host:              getEnv("POSTGRES_HOST", "localhost"),
			Port:              getEnvInt("POSTGRES_PORT", 5432),
			User:              getEnv("POSTGRES_USER", "egeinf"),
			Password:          getEnv("POSTGRES_PASSWORD", ""),
			DBName:            getEnv("POSTGRES_DB", "egeinf"),
			SSLMode:           getEnv("POSTGRES_SSLMODE", "disable"),
			MaxConns:          int32(getEnvInt("POSTGRES_MAX_CONNS", 25)),
			MinConns:          int32(getEnvInt("POSTGRES_MIN_CONNS", 5)),
			MaxConnLifetime:   getEnvDuration("POSTGRES_MAX_CONN_LIFETIME", time.Hour),
			MaxConnIdleTime:   getEnvDuration("POSTGRES_MAX_CONN_IDLE_TIME", 30*time.Minute),
			HealthCheckPeriod: getEnvDuration("POSTGRES_HEALTH_CHECK_PERIOD", time.Minute),
		},
		Redis: RedisConfig{
			Addr:     getEnv("REDIS_ADDR", "localhost:6379"),
			Password: getEnv("REDIS_PASSWORD", ""),
			DB:       getEnvInt("REDIS_DB", 0),
		},
		S3: S3Config{
			Endpoint:      getEnv("S3_ENDPOINT", "localhost:9000"),
			AccessKey:     getEnv("S3_ACCESS_KEY", "minioadmin"),
			SecretKey:     getEnv("S3_SECRET_KEY", "minioadmin_secret"),
			UseSSL:        getEnvBool("S3_USE_SSL", false),
			MediaBucket:   getEnv("S3_MEDIA_BUCKET", "egeinf-media"),
			PrivateBucket: getEnv("S3_PRIVATE_BUCKET", "egeinf-private"),
		},
		Auth: AuthConfig{
			JWTSecret:    getEnv("JWT_SECRET", "super_secret_32_bytes_minimum_key_here"),
			AccessTTL:    getEnvDuration("JWT_ACCESS_TTL", 15*time.Minute),
			RefreshTTL:   getEnvDuration("JWT_REFRESH_TTL", 720*time.Hour),
			CookieDomain: getEnv("COOKIE_DOMAIN", ""),
			CookieSecure: getEnvBool("COOKIE_SECURE", false),
		},
		OAuth: OAuthConfig{
			YandexClientID:     getEnv("YANDEX_CLIENT_ID", ""),
			YandexClientSecret: getEnv("YANDEX_CLIENT_SECRET", ""),
			YandexRedirectURL:  getEnv("YANDEX_REDIRECT_URL", "http://localhost:5173/api/v1/auth/oauth/yandex/callback"),
			VKClientID:         getEnv("VK_CLIENT_ID", ""),
			VKClientSecret:     getEnv("VK_CLIENT_SECRET", ""),
			VKRedirectURL:      getEnv("VK_REDIRECT_URL", "http://localhost:5173/api/v1/auth/oauth/vk/callback"),
		},
		Bootstrap: BootstrapConfig{
			SuperuserYandexID: getEnv("BOOTSTRAP_SUPERUSER_YANDEX_ID", ""),
			SuperuserVKID:     getEnv("BOOTSTRAP_SUPERUSER_VK_ID", ""),
		},
		Migrations: MigrationsConfig{
			Dir: getEnv("MIGRATIONS_DIR", "../db/migrations"),
		},
	}

	// In production, ensure COOKIE_SECURE is enabled if not explicitly disabled
	if cfg.IsProduction() && os.Getenv("COOKIE_SECURE") == "" {
		cfg.Auth.CookieSecure = true
	}

	return cfg, nil
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	valStr := os.Getenv(key)
	if valStr == "" {
		return defaultVal
	}
	val, err := strconv.Atoi(valStr)
	if err != nil {
		return defaultVal
	}
	return val
}

func getEnvBool(key string, defaultVal bool) bool {
	valStr := os.Getenv(key)
	if valStr == "" {
		return defaultVal
	}
	val, err := strconv.ParseBool(valStr)
	if err != nil {
		return defaultVal
	}
	return val
}

func getEnvDuration(key string, defaultVal time.Duration) time.Duration {
	valStr := os.Getenv(key)
	if valStr == "" {
		return defaultVal
	}
	val, err := time.ParseDuration(valStr)
	if err != nil {
		return defaultVal
	}
	return val
}

// loadDotEnv parses a simple KEY=VALUE file and sets environment variables if not already set.
func loadDotEnv(filepath string) {
	file, err := os.Open(filepath)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Split on first '='
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		// Strip inline comment if any
		if idx := strings.Index(val, " #"); idx != -1 {
			val = strings.TrimSpace(val[:idx])
		}

		// Strip quotes if any
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}

		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, val)
		}
	}
}
