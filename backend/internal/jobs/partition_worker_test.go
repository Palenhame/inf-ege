package jobs

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestGetMonthlyPartitionsToCreate(t *testing.T) {
	// Base time: 2026-11-15 UTC
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	specs := GetMonthlyPartitionsToCreate(now, 3)

	if len(specs) != 4 {
		t.Fatalf("expected 4 partition specs, got %d", len(specs))
	}

	expected := []struct {
		table string
		from  string
		to    string
		year  int
		month int
	}{
		{"user_task_events_2026_11", "2026-11-01", "2026-12-01", 2026, 11},
		{"user_task_events_2026_12", "2026-12-01", "2027-01-01", 2026, 12},
		{"user_task_events_2027_01", "2027-01-01", "2027-02-01", 2027, 1},
		{"user_task_events_2027_02", "2027-02-01", "2027-03-01", 2027, 2},
	}

	for i, exp := range expected {
		s := specs[i]
		if s.TableName != exp.table {
			t.Errorf("[%d] expected table %s, got %s", i, exp.table, s.TableName)
		}
		if s.FromDate != exp.from {
			t.Errorf("[%d] expected from %s, got %s", i, exp.from, s.FromDate)
		}
		if s.ToDate != exp.to {
			t.Errorf("[%d] expected to %s, got %s", i, exp.to, s.ToDate)
		}
		if s.Year != exp.year || s.Month != exp.month {
			t.Errorf("[%d] expected year/month %d/%d, got %d/%d", i, exp.year, exp.month, s.Year, s.Month)
		}
	}
}

func TestGetPartitionsToDetach(t *testing.T) {
	// Base time: 2026-11-15 -> 12 months ago is 2025-11-01
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

	existing := []string{
		"user_task_events_2025_09", // > 12 months -> DETACH
		"user_task_events_2025_10", // > 12 months -> DETACH
		"user_task_events_2025_11", // exactly 12 months -> KEEP
		"user_task_events_2025_12", // 11 months -> KEEP
		"user_task_events_2026_05", // 6 months -> KEEP
		"user_task_events_2026_11", // current month -> KEEP
		"user_task_events_2026_12", // future month -> KEEP
		"user_task_events_custom",  // invalid pattern -> IGNORE
	}

	toDetach := GetPartitionsToDetach(existing, now, 12)

	if len(toDetach) != 2 {
		t.Fatalf("expected 2 partitions to detach, got %d: %v", len(toDetach), toDetach)
	}

	if toDetach[0] != "user_task_events_2025_09" || toDetach[1] != "user_task_events_2025_10" {
		t.Fatalf("unexpected partitions to detach: %v", toDetach)
	}
}

func TestPartitionMaintenanceJob_Run(t *testing.T) {
	ctx := context.Background()

	var executedDDLs []string
	db := &mockDB{
		execFn: func(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
			executedDDLs = append(executedDDLs, sql)
			return pgconn.NewCommandTag("CREATE TABLE"), nil
		},
		queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			// Return list of existing partitions including one old partition
			rows := [][]any{
				{"user_task_events_2024_01"}, // old partition from 2024
				{"user_task_events_2026_11"}, // current month
			}
			return newSliceRows(rows), nil
		},
	}

	job := NewPartitionMaintenanceJob(db, nil)
	if err := job.Run(ctx); err != nil {
		t.Fatalf("job.Run failed: %v", err)
	}

	// Verify CREATE TABLE statements were executed for the 4 upcoming months
	createdCount := 0
	detachedCount := 0
	for _, sql := range executedDDLs {
		if strings.HasPrefix(sql, "CREATE TABLE IF NOT EXISTS user_task_events_") {
			createdCount++
		}
		if strings.HasPrefix(sql, "ALTER TABLE user_task_events DETACH PARTITION user_task_events_2024_01") {
			detachedCount++
		}
	}

	if createdCount != 4 {
		t.Errorf("expected 4 partition creation statements, got %d", createdCount)
	}
	if detachedCount != 1 {
		t.Errorf("expected 1 partition detachment statement, got %d", detachedCount)
	}
}

func TestPartitionMaintenanceJob_CreateError(t *testing.T) {
	ctx := context.Background()
	expectedErr := errors.New("cannot create partition")

	db := &mockDB{
		execFn: func(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
			return pgconn.CommandTag{}, expectedErr
		},
	}

	job := NewPartitionMaintenanceJob(db, nil)
	err := job.Run(ctx)
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error %v, got %v", expectedErr, err)
	}
}

func TestPartitionMaintenanceJob_QueryError(t *testing.T) {
	ctx := context.Background()
	expectedErr := errors.New("cannot query partitions")

	db := &mockDB{
		queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			return nil, expectedErr
		},
	}

	job := NewPartitionMaintenanceJob(db, nil)
	err := job.Run(ctx)
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error %v, got %v", expectedErr, err)
	}
}
