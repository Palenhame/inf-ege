package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// ResolveMigrationsDir inspects dir, environment variable MIGRATIONS_DIR, and common relative fallback paths.
func ResolveMigrationsDir(dir string) (string, error) {
	candidates := make([]string, 0, 5)
	if dir != "" {
		candidates = append(candidates, dir)
	}
	if envDir := os.Getenv("MIGRATIONS_DIR"); envDir != "" {
		candidates = append(candidates, envDir)
	}
	candidates = append(candidates,
		"../db/migrations",
		"db/migrations",
		"/app/db/migrations",
		"../../db/migrations",
	)

	for _, c := range candidates {
		abs, err := filepath.Abs(c)
		if err != nil {
			continue
		}
		info, err := os.Stat(abs)
		if err == nil && info.IsDir() {
			return abs, nil
		}
	}

	return "", fmt.Errorf("postgres: migrations directory not found in candidates: %v", candidates)
}

func openSQLDB(pool *pgxpool.Pool) (*sql.DB, error) {
	if pool == nil {
		return nil, fmt.Errorf("postgres: pgxpool is nil")
	}
	db := stdlib.OpenDBFromPool(pool)
	if err := goose.SetDialect("postgres"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("postgres: failed to set goose dialect: %w", err)
	}
	return db, nil
}

// RunMigrations applies all pending migrations using goose.
func RunMigrations(ctx context.Context, pool *pgxpool.Pool, dir string) error {
	resolvedDir, err := ResolveMigrationsDir(dir)
	if err != nil {
		return err
	}

	db, err := openSQLDB(pool)
	if err != nil {
		return err
	}
	defer db.Close()

	if err := goose.UpContext(ctx, db, resolvedDir); err != nil {
		return fmt.Errorf("postgres: goose up failed in %s: %w", resolvedDir, err)
	}

	return nil
}

// RunMigrationsDown rolls back the most recent migration using goose.
func RunMigrationsDown(ctx context.Context, pool *pgxpool.Pool, dir string) error {
	resolvedDir, err := ResolveMigrationsDir(dir)
	if err != nil {
		return err
	}

	db, err := openSQLDB(pool)
	if err != nil {
		return err
	}
	defer db.Close()

	if err := goose.DownContext(ctx, db, resolvedDir); err != nil {
		return fmt.Errorf("postgres: goose down failed in %s: %w", resolvedDir, err)
	}

	return nil
}

// GetMigrationVersion returns the current goose migration version from the database.
func GetMigrationVersion(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	db, err := openSQLDB(pool)
	if err != nil {
		return 0, err
	}
	defer db.Close()

	version, err := goose.GetDBVersionContext(ctx, db)
	if err != nil {
		return 0, fmt.Errorf("postgres: failed to get migration version: %w", err)
	}

	return version, nil
}
