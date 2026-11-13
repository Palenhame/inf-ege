package jobs

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type mockNotifier struct {
	mu     sync.Mutex
	events []SessionExpiredEvent
	err    error
}

func (m *mockNotifier) NotifySessionExpired(ctx context.Context, event SessionExpiredEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, event)
	return m.err
}

func TestExpireSessionsJob_Success(t *testing.T) {
	ctx := context.Background()

	session1 := uuid.New()
	session2 := uuid.New()
	assign1 := uuid.New()
	assign2 := uuid.New()
	user1 := uuid.New()
	user2 := uuid.New()
	now := time.Now().UTC()

	secScore1 := int16(72)
	secScore2 := int16(100)

	callCount := 0
	db := &mockDB{
		queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			callCount++
			if callCount == 1 {
				rows := [][]any{
					{session1, assign1, user1, int16(18), &secScore1, now},
					{session2, assign2, user2, int16(29), &secScore2, now},
				}
				return newSliceRows(rows), nil
			}
			return &emptyRows{}, nil
		},
	}

	notifier := &mockNotifier{}
	job := NewExpireSessionsJob(db, notifier, nil)
	job.batchSize = 2

	if err := job.Run(ctx); err != nil {
		t.Fatalf("job.Run failed: %v", err)
	}

	notifier.mu.Lock()
	events := notifier.events
	notifier.mu.Unlock()

	if len(events) != 2 {
		t.Fatalf("expected 2 expired events, got %d", len(events))
	}

	if events[0].SessionID != session1 || events[0].PrimaryScore != 18 || *events[0].SecondaryScore != 72 {
		t.Errorf("unexpected event 0: %+v", events[0])
	}

	if events[1].SessionID != session2 || events[1].PrimaryScore != 29 || *events[1].SecondaryScore != 100 {
		t.Errorf("unexpected event 1: %+v", events[1])
	}
}

func TestExpireSessionsJob_NotifierError(t *testing.T) {
	ctx := context.Background()
	session := uuid.New()
	assign := uuid.New()
	user := uuid.New()
	now := time.Now().UTC()
	secScore := int16(50)

	db := &mockDB{
		queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			rows := [][]any{
				{session, assign, user, int16(10), &secScore, now},
			}
			return newSliceRows(rows), nil
		},
	}

	// Notifier returns error; job should log and continue
	notifier := &mockNotifier{err: errors.New("ws failed")}
	job := NewExpireSessionsJob(db, notifier, nil)

	if err := job.Run(ctx); err != nil {
		t.Fatalf("expected job to succeed even if notifier fails, got: %v", err)
	}
}

func TestExpireSessionsJob_IterationError(t *testing.T) {
	ctx := context.Background()
	expectedErr := errors.New("iteration failed")

	rows := &sliceRows{err: expectedErr}
	db := &mockDB{
		queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			return rows, nil
		},
	}

	job := NewExpireSessionsJob(db, nil, nil)
	err := job.Run(ctx)
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected iteration error %v, got %v", expectedErr, err)
	}
}

func TestExpireSessionsJob_NoOverdueSessions(t *testing.T) {
	ctx := context.Background()
	db := &mockDB{
		queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			return &emptyRows{}, nil
		},
	}

	notifier := &mockNotifier{}
	job := NewExpireSessionsJob(db, notifier, nil)

	if err := job.Run(ctx); err != nil {
		t.Fatalf("expected success, got %v", err)
	}

	if len(notifier.events) != 0 {
		t.Fatalf("expected 0 events, got %d", len(notifier.events))
	}
}

func TestExpireSessionsJob_DBError(t *testing.T) {
	ctx := context.Background()
	expectedErr := errors.New("query failed")

	db := &mockDB{
		queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			return nil, expectedErr
		},
	}

	job := NewExpireSessionsJob(db, nil, nil)
	err := job.Run(ctx)
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error %v, got %v", expectedErr, err)
	}
}

func TestExpireSessionsJob_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	db := &mockDB{}
	job := NewExpireSessionsJob(db, nil, nil)

	err := job.Run(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
