package jobs

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestCalculateWeaknessScore(t *testing.T) {
	// Case 1: attempted = 0 -> score must be 0
	if s := CalculateWeaknessScore(0, 0, 100); s != 0.0 {
		t.Errorf("expected 0 for attempted=0, got %f", s)
	}

	// Case 2: 100% success rate -> score must be 0
	if s := CalculateWeaknessScore(100.0, 50, 100); s != 0.0 {
		t.Errorf("expected 0 for success_rate=100, got %f", s)
	}

	// Case 3: 50% success rate, 5 attempts, normalizer 100
	// (100 - 50) * ln(6) / 100 * 100 = 50 * 1.791759 = 89.59
	s3 := CalculateWeaknessScore(50.0, 5, 100.0)
	expected3 := math.Round(50.0*math.Log(6.0)*100.0) / 100.0
	if math.Abs(s3-expected3) > 0.01 {
		t.Errorf("expected %f, got %f", expected3, s3)
	}

	// Case 4: Capped at 100.0
	// 0% success, 10 attempts, normalizer 10
	// 100 * ln(11) / 10 * 100 = 2397.89 -> capped at 100.00
	s4 := CalculateWeaknessScore(0.0, 10, 10.0)
	if s4 != 100.0 {
		t.Errorf("expected capped 100.0, got %f", s4)
	}

	// Case 5: normalizer <= 0 defaults to 1.0
	s5 := CalculateWeaknessScore(80.0, 2, 0)
	expected5 := math.Round(math.Min((100.0-80.0)*math.Log(3.0)/1.0*100.0, 100.0)*100.0) / 100.0
	if math.Abs(s5-expected5) > 0.01 {
		t.Errorf("expected %f, got %f", expected5, s5)
	}
}

func TestRecalculateTopicWeaknessScoreJob_Success(t *testing.T) {
	ctx := context.Background()
	rdb := newMockRedisClient()

	checkpointTime := time.Date(2026, 11, 12, 1, 0, 0, 0, time.UTC)
	rdb.Set(ctx, topicScoreCheckpointKey, checkpointTime.Format(time.RFC3339), 0)

	var capturedNormalizer float64
	var capturedFull bool
	var capturedSince time.Time

	db := &mockDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			return &mockRow{values: []any{145.5}}
		},
		execFn: func(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
			if len(arguments) >= 3 {
				if n, ok := arguments[0].(float64); ok {
					capturedNormalizer = n
				}
				if f, ok := arguments[1].(bool); ok {
					capturedFull = f
				}
				if s, ok := arguments[2].(time.Time); ok {
					capturedSince = s
				}
			}
			return pgconn.NewCommandTag("UPDATE 27"), nil
		},
	}

	job := NewRecalculateTopicWeaknessScoreJob(db, rdb, nil)
	if err := job.Run(ctx); err != nil {
		t.Fatalf("job.Run failed: %v", err)
	}

	if capturedNormalizer != 145.5 {
		t.Errorf("expected normalizer 145.5, got %f", capturedNormalizer)
	}
	if capturedFull {
		t.Errorf("expected capturedFull = false, got true")
	}
	if !capturedSince.Equal(checkpointTime) {
		t.Errorf("expected since %v, got %v", checkpointTime, capturedSince)
	}

	// Verify checkpoint updated in redis
	newVal, err := rdb.Get(ctx, topicScoreCheckpointKey).Result()
	if err != nil || newVal == "" {
		t.Fatalf("expected updated checkpoint in redis, got %v", newVal)
	}
}

func TestRecalculateTopicWeaknessScoreJob_NormalizerError(t *testing.T) {
	ctx := context.Background()
	expectedErr := errors.New("normalizer query failed")

	db := &mockDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			return &mockRow{err: expectedErr}
		},
	}

	job := NewRecalculateTopicWeaknessScoreJob(db, nil, nil)
	err := job.Run(ctx)
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected %v, got %v", expectedErr, err)
	}
}

func TestRecalculateTopicWeaknessScoreJob_UpdateError(t *testing.T) {
	ctx := context.Background()
	expectedErr := errors.New("update query failed")

	db := &mockDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			return &mockRow{values: []any{100.0}}
		},
		execFn: func(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
			return pgconn.CommandTag{}, expectedErr
		},
	}

	job := NewRecalculateTopicWeaknessScoreJob(db, nil, nil)
	err := job.Run(ctx)
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected %v, got %v", expectedErr, err)
	}
}
