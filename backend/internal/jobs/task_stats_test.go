package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestRecalculateTaskStatsJob_WithCheckpoint(t *testing.T) {
	ctx := context.Background()
	rdb := newMockRedisClient()

	checkpointTime := time.Date(2026, 11, 12, 1, 0, 0, 0, time.UTC)
	rdb.Set(ctx, taskStatsCheckpointKey, checkpointTime.Format(time.RFC3339), 0)

	var capturedSince time.Time
	var capturedFull bool
	db := &mockDB{
		execFn: func(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
			if len(arguments) >= 2 {
				if s, ok := arguments[0].(time.Time); ok {
					capturedSince = s
				}
				if f, ok := arguments[1].(bool); ok {
					capturedFull = f
				}
			}
			return pgconn.NewCommandTag("UPDATE 15"), nil
		},
	}

	job := NewRecalculateTaskStatsJob(db, rdb, nil)
	if err := job.Run(ctx); err != nil {
		t.Fatalf("job.Run failed: %v", err)
	}

	if !capturedSince.Equal(checkpointTime) {
		t.Fatalf("expected since %v, got %v", checkpointTime, capturedSince)
	}
	if capturedFull {
		t.Fatalf("expected capturedFull = false, got true")
	}

	// Verify checkpoint was updated
	newVal, err := rdb.Get(ctx, taskStatsCheckpointKey).Result()
	if err != nil || newVal == "" {
		t.Fatalf("expected updated checkpoint in redis, got %v (err: %v)", newVal, err)
	}
}

func TestRecalculateTaskStatsJob_FullRecalculate(t *testing.T) {
	ctx := context.Background()
	rdb := newMockRedisClient()

	var capturedFull bool
	db := &mockDB{
		execFn: func(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
			if len(arguments) >= 2 {
				if f, ok := arguments[1].(bool); ok {
					capturedFull = f
				}
			}
			return pgconn.NewCommandTag("UPDATE 100"), nil
		},
	}

	job := NewRecalculateTaskStatsJob(db, rdb, nil)
	if err := job.RunWithOptions(ctx, true); err != nil {
		t.Fatalf("job.RunWithOptions failed: %v", err)
	}

	if !capturedFull {
		t.Fatalf("expected capturedFull = true, got false")
	}
}

func TestRecalculateTaskStatsJob_DBError(t *testing.T) {
	ctx := context.Background()
	expectedErr := errors.New("db error")

	db := &mockDB{
		execFn: func(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
			return pgconn.CommandTag{}, expectedErr
		},
	}

	job := NewRecalculateTaskStatsJob(db, nil, nil)
	err := job.Run(ctx)
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected %v, got %v", expectedErr, err)
	}
}
