package jobs

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestNextDailyTime(t *testing.T) {
	// Base time: 2026-11-12 02:15:00 UTC
	base := time.Date(2026, 11, 12, 2, 15, 0, 0, time.UTC)

	// Target: 03:00 UTC (later today)
	next := NextDailyTime(base, 3, 0)
	expected := time.Date(2026, 11, 12, 3, 0, 0, 0, time.UTC)
	if !next.Equal(expected) {
		t.Fatalf("expected %v, got %v", expected, next)
	}

	// Target: 01:00 UTC (already passed today -> tomorrow)
	nextTomorrow := NextDailyTime(base, 1, 0)
	expectedTomorrow := time.Date(2026, 11, 13, 1, 0, 0, 0, time.UTC)
	if !nextTomorrow.Equal(expectedTomorrow) {
		t.Fatalf("expected %v, got %v", expectedTomorrow, nextTomorrow)
	}

	// Target: exactly equal to base -> tomorrow
	nextEqual := NextDailyTime(base, 2, 15)
	expectedEqual := time.Date(2026, 11, 13, 2, 15, 0, 0, time.UTC)
	if !nextEqual.Equal(expectedEqual) {
		t.Fatalf("expected %v, got %v", expectedEqual, nextEqual)
	}

	// Year boundary: Dec 31 23:30 -> Jan 1 03:00
	yearEnd := time.Date(2026, 12, 31, 23, 30, 0, 0, time.UTC)
	nextYear := NextDailyTime(yearEnd, 3, 0)
	expectedYear := time.Date(2027, 1, 1, 3, 0, 0, 0, time.UTC)
	if !nextYear.Equal(expectedYear) {
		t.Fatalf("expected %v, got %v", expectedYear, nextYear)
	}
}

func TestRedisLocker(t *testing.T) {
	ctx := context.Background()
	rdb := newMockRedisClient()
	locker := NewRedisLocker(rdb, nil)

	// 1. Acquire first lock
	unlock1, acquired1, err1 := locker.Acquire(ctx, "test_job", 10*time.Second)
	if err1 != nil || !acquired1 {
		t.Fatalf("expected first acquire to succeed, got acquired=%v, err=%v", acquired1, err1)
	}

	// 2. Second acquire while locked should fail
	_, acquired2, err2 := locker.Acquire(ctx, "test_job", 10*time.Second)
	if err2 != nil || acquired2 {
		t.Fatalf("expected second acquire to fail, got acquired=%v, err=%v", acquired2, err2)
	}

	// 3. Unlock first
	unlock1()

	// 4. Third acquire should now succeed
	unlock3, acquired3, err3 := locker.Acquire(ctx, "test_job", 10*time.Second)
	if err3 != nil || !acquired3 {
		t.Fatalf("expected third acquire to succeed after unlock, got acquired=%v, err=%v", acquired3, err3)
	}
	unlock3()

	// Test nil redis client defaults to acquired
	nilLocker := NewRedisLocker(nil, nil)
	unlockNil, acquiredNil, errNil := nilLocker.Acquire(ctx, "nil_job", time.Minute)
	if errNil != nil || !acquiredNil {
		t.Fatalf("expected nil client to acquire, got %v, %v", acquiredNil, errNil)
	}
	unlockNil()
}

func TestScheduler_Register(t *testing.T) {
	s := NewScheduler(SchedulerConfig{})

	job := JobFunc{
		JobName: "dummy",
		RunFunc: func(ctx context.Context) error { return nil },
	}

	// Register valid interval with default lockTTL (LockTTL = 0)
	if err := s.Register(job, ScheduleSpec{Kind: ScheduleInterval, Interval: 10 * time.Second}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Duplicate registration should fail
	if err := s.RegisterInterval(job, 5*time.Second, 5*time.Second); err == nil {
		t.Fatalf("expected error for duplicate job registration, got nil")
	}

	// Invalid daily time should fail
	invalidDailyJob := JobFunc{
		JobName: "invalid_daily",
		RunFunc: func(ctx context.Context) error { return nil },
	}
	if err := s.RegisterDaily(invalidDailyJob, 25, 0, time.Minute); err == nil {
		t.Fatalf("expected error for hour 25, got nil")
	}
	if err := s.RegisterDaily(invalidDailyJob, 3, 70, time.Minute); err == nil {
		t.Fatalf("expected error for minute 70, got nil")
	}
}

func TestScheduler_RunJobNow(t *testing.T) {
	s := NewScheduler(SchedulerConfig{})

	var executed bool
	job := JobFunc{
		JobName: "manual_trigger",
		RunFunc: func(ctx context.Context) error {
			executed = true
			return nil
		},
	}

	if err := s.RegisterInterval(job, time.Hour, time.Minute); err != nil {
		t.Fatalf("register failed: %v", err)
	}

	ctx := context.Background()
	if err := s.RunJobNow(ctx, "manual_trigger"); err != nil {
		t.Fatalf("RunJobNow failed: %v", err)
	}

	if !executed {
		t.Fatalf("job was not executed by RunJobNow")
	}

	// Unknown job
	if err := s.RunJobNow(ctx, "non_existent"); err == nil {
		t.Fatalf("expected error for non_existent job, got nil")
	}
}

type failingLocker struct {
	err error
}

func (f failingLocker) Acquire(ctx context.Context, jobName string, ttl time.Duration) (func(), bool, error) {
	return nil, false, f.err
}

func TestScheduler_LockFailure(t *testing.T) {
	locker := failingLocker{err: errors.New("redis connection refused")}
	s := NewScheduler(SchedulerConfig{Locker: locker})

	job := JobFunc{
		JobName: "locked_job",
		RunFunc: func(ctx context.Context) error { return nil },
	}
	_ = s.RegisterInterval(job, time.Hour, time.Minute)

	err := s.RunJobNow(context.Background(), "locked_job")
	if err == nil {
		t.Fatalf("expected error from lock failure, got nil")
	}
}

func TestScheduler_Lifecycle(t *testing.T) {
	s := NewScheduler(SchedulerConfig{})

	var count int32
	job := JobFunc{
		JobName: "ticker",
		RunFunc: func(ctx context.Context) error {
			atomic.AddInt32(&count, 1)
			return nil
		},
	}

	if err := s.RegisterInterval(job, 15*time.Millisecond, 10*time.Millisecond); err != nil {
		t.Fatalf("failed to register job: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.Start(ctx)

	// Second start call should be ignored
	s.Start(ctx)

	// Wait for a few executions
	time.Sleep(60 * time.Millisecond)
	s.Stop()

	finalCount := atomic.LoadInt32(&count)
	if finalCount < 2 {
		t.Fatalf("expected at least 2 executions, got %d", finalCount)
	}
}

func TestScheduler_RegisterDefaults(t *testing.T) {
	s := NewScheduler(SchedulerConfig{})
	db := &mockDB{}
	rdb := newMockRedisClient()

	if err := s.RegisterDefaults(db, rdb, nil); err != nil {
		t.Fatalf("RegisterDefaults failed: %v", err)
	}

	s.mu.RLock()
	count := len(s.jobs)
	s.mu.RUnlock()

	if count != 4 {
		t.Fatalf("expected 4 default jobs, got %d", count)
	}

	// Verify each expected job is present
	expectedJobs := []string{
		"expire_sessions",
		"partition_maintenance",
		"recalculate_task_stats",
		"recalculate_topic_weakness_score",
	}

	for _, name := range expectedJobs {
		s.mu.RLock()
		_, exists := s.jobs[name]
		s.mu.RUnlock()
		if !exists {
			t.Errorf("expected job %q to be registered", name)
		}
	}
}

func TestScheduler_JobFailure(t *testing.T) {
	s := NewScheduler(SchedulerConfig{})

	expectedErr := errors.New("database connection down")
	job := JobFunc{
		JobName: "failing_job",
		RunFunc: func(ctx context.Context) error {
			return expectedErr
		},
	}

	if err := s.RegisterInterval(job, time.Hour, time.Minute); err != nil {
		t.Fatalf("failed to register job: %v", err)
	}

	err := s.RunJobNow(context.Background(), "failing_job")
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected %v, got %v", expectedErr, err)
	}
}
