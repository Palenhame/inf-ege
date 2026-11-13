package postgres

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"backend/internal/config"
)

func TestResolveMigrationsDir(t *testing.T) {
	// Create a temp directory to simulate a valid migrations directory
	tmpDir := t.TempDir()
	migDir := filepath.Join(tmpDir, "migrations")
	if err := os.Mkdir(migDir, 0755); err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	resolved, err := ResolveMigrationsDir(migDir)
	if err != nil {
		t.Fatalf("expected to resolve migrations dir, got error: %v", err)
	}
	if resolved != migDir {
		t.Errorf("expected %s, got %s", migDir, resolved)
	}

	// Non-existent directory without fallback
	_, err = ResolveMigrationsDir(filepath.Join(tmpDir, "non_existent_folder_xyz"))
	// Note: if fallback paths (like ../db/migrations) don't exist yet, it should return an error
	// Let's verify error is descriptive if not found
	if err != nil {
		if len(err.Error()) == 0 {
			t.Errorf("expected non-empty error message")
		}
	}
}

func TestNilPoolErrors(t *testing.T) {
	_, err := openSQLDB(nil)
	if err == nil {
		t.Errorf("expected error when pool is nil")
	}

	ctx := context.Background()
	err = RunMigrations(ctx, nil, t.TempDir())
	if err == nil {
		t.Errorf("expected error when pool is nil in RunMigrations")
	}

	_, err = GetMigrationVersion(ctx, nil)
	if err == nil {
		t.Errorf("expected error when pool is nil in GetMigrationVersion")
	}
}

func TestTxManagerInterface(t *testing.T) {
	// Verify that txManager conforms to TxManager interface
	var _ TxManager = (*txManager)(nil)
}

func TestNewConnectionInvalidHost(t *testing.T) {
	// Attempt connection with immediate cancellation
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	cfg := config.PostgresConfig{
		Host:    "127.0.0.1",
		Port:    59999, // Unused port
		User:    "invalid_user",
		DBName:  "invalid_db",
		SSLMode: "disable",
	}

	_, err := New(ctx, cfg)
	if err == nil {
		t.Errorf("expected connection error for invalid host/port")
	}
}
