package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	// Clean env vars for test isolation
	envKeys := []string{
		"APP_ENV", "APP_BASE_URL", "BACKEND_PORT",
		"POSTGRES_HOST", "POSTGRES_PORT", "POSTGRES_USER", "POSTGRES_PASSWORD", "POSTGRES_DB", "POSTGRES_SSLMODE",
		"REDIS_ADDR", "REDIS_PASSWORD", "REDIS_DB",
		"S3_ENDPOINT", "S3_ACCESS_KEY", "S3_SECRET_KEY", "S3_USE_SSL", "S3_MEDIA_BUCKET", "S3_PRIVATE_BUCKET",
		"JWT_SECRET", "JWT_ACCESS_TTL", "JWT_REFRESH_TTL", "COOKIE_DOMAIN", "COOKIE_SECURE",
		"YANDEX_CLIENT_ID", "YANDEX_CLIENT_SECRET", "YANDEX_REDIRECT_URL",
		"VK_CLIENT_ID", "VK_CLIENT_SECRET", "VK_REDIRECT_URL",
		"BOOTSTRAP_SUPERUSER_YANDEX_ID", "BOOTSTRAP_SUPERUSER_VK_ID",
		"MIGRATIONS_DIR",
	}
	for _, k := range envKeys {
		os.Unsetenv(k)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.App.Env != "development" {
		t.Errorf("expected App.Env = development, got %s", cfg.App.Env)
	}
	if !cfg.IsDevelopment() {
		t.Errorf("expected IsDevelopment() to be true")
	}
	if cfg.IsProduction() {
		t.Errorf("expected IsProduction() to be false")
	}
	if cfg.App.Port != 8000 {
		t.Errorf("expected App.Port = 8000, got %d", cfg.App.Port)
	}
	if cfg.ListenAddr() != ":8000" {
		t.Errorf("expected ListenAddr() = :8000, got %s", cfg.ListenAddr())
	}
	if cfg.Postgres.Host != "localhost" || cfg.Postgres.Port != 5432 || cfg.Postgres.DBName != "egeinf" {
		t.Errorf("unexpected Postgres config: %+v", cfg.Postgres)
	}
	if cfg.Redis.Addr != "localhost:6379" {
		t.Errorf("expected Redis.Addr = localhost:6379, got %s", cfg.Redis.Addr)
	}
	if cfg.S3.MediaBucket != "egeinf-media" || cfg.S3.PrivateBucket != "egeinf-private" {
		t.Errorf("unexpected S3 buckets: %+v", cfg.S3)
	}
	if cfg.Auth.AccessTTL != 15*time.Minute {
		t.Errorf("expected AccessTTL = 15m, got %v", cfg.Auth.AccessTTL)
	}
	if cfg.Auth.RefreshTTL != 720*time.Hour {
		t.Errorf("expected RefreshTTL = 720h, got %v", cfg.Auth.RefreshTTL)
	}
	if cfg.Migrations.Dir != "../db/migrations" {
		t.Errorf("expected Migrations.Dir = ../db/migrations, got %s", cfg.Migrations.Dir)
	}
}

func TestPostgresDSN(t *testing.T) {
	p := PostgresConfig{
		Host:     "db.example.com",
		Port:     5432,
		User:     "myuser",
		Password: "secret/password!#",
		DBName:   "ege_db",
		SSLMode:  "require",
	}

	dsn := p.DSN()
	expected := "postgres://myuser:secret%2Fpassword%21%23@db.example.com:5432/ege_db?sslmode=require"
	if dsn != expected {
		t.Errorf("expected DSN:\n%s\ngot:\n%s", expected, dsn)
	}

	pNoPass := PostgresConfig{
		Host:    "localhost",
		Port:    5432,
		User:    "egeinf",
		DBName:  "egeinf",
		SSLMode: "disable",
	}
	dsnNoPass := pNoPass.DSN()
	expectedNoPass := "postgres://egeinf@localhost:5432/egeinf?sslmode=disable"
	if dsnNoPass != expectedNoPass {
		t.Errorf("expected DSN without pass:\n%s\ngot:\n%s", expectedNoPass, dsnNoPass)
	}
}

func TestEnvOverride(t *testing.T) {
	os.Setenv("APP_ENV", "production")
	os.Setenv("BACKEND_PORT", "9090")
	os.Setenv("POSTGRES_PORT", "5438")
	os.Setenv("S3_USE_SSL", "true")
	os.Setenv("COOKIE_SECURE", "true")
	defer func() {
		os.Unsetenv("APP_ENV")
		os.Unsetenv("BACKEND_PORT")
		os.Unsetenv("POSTGRES_PORT")
		os.Unsetenv("S3_USE_SSL")
		os.Unsetenv("COOKIE_SECURE")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.App.Env != "production" || !cfg.IsProduction() {
		t.Errorf("expected production env")
	}
	if cfg.App.Port != 9090 {
		t.Errorf("expected port 9090, got %d", cfg.App.Port)
	}
	if cfg.Postgres.Port != 5438 {
		t.Errorf("expected postgres port 5438, got %d", cfg.Postgres.Port)
	}
	if !cfg.S3.UseSSL {
		t.Errorf("expected S3.UseSSL = true")
	}
	if !cfg.Auth.CookieSecure {
		t.Errorf("expected CookieSecure = true")
	}
}

func TestLoadDotEnv(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")
	content := `
# Comment line
TEST_KEY_FOO=bar123
TEST_KEY_QUOTED="quoted_val"
TEST_KEY_INLINE=val_here # inline comment
`
	if err := os.WriteFile(envPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test .env: %v", err)
	}

	os.Unsetenv("TEST_KEY_FOO")
	os.Unsetenv("TEST_KEY_QUOTED")
	os.Unsetenv("TEST_KEY_INLINE")

	loadDotEnv(envPath)

	if os.Getenv("TEST_KEY_FOO") != "bar123" {
		t.Errorf("expected TEST_KEY_FOO = bar123, got %s", os.Getenv("TEST_KEY_FOO"))
	}
	if os.Getenv("TEST_KEY_QUOTED") != "quoted_val" {
		t.Errorf("expected TEST_KEY_QUOTED = quoted_val, got %s", os.Getenv("TEST_KEY_QUOTED"))
	}
	if os.Getenv("TEST_KEY_INLINE") != "val_here" {
		t.Errorf("expected TEST_KEY_INLINE = val_here, got %s", os.Getenv("TEST_KEY_INLINE"))
	}
}
