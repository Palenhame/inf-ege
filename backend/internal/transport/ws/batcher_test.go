package ws

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type mockBatchResults struct {
	execCount int
	closed    bool
}

func (m *mockBatchResults) Exec() (pgconn.CommandTag, error) {
	m.execCount++
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

func (m *mockBatchResults) Query() (pgx.Rows, error) {
	return nil, nil
}

func (m *mockBatchResults) QueryRow() pgx.Row {
	return nil
}

func (m *mockBatchResults) Close() error {
	m.closed = true
	return nil
}

func (m *mockBatchResults) Tx() pgx.Tx {
	return nil
}

type mockBatchDatabase struct {
	mu            sync.Mutex
	queuedBatches []*pgx.Batch
}

func (m *mockBatchDatabase) SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queuedBatches = append(m.queuedBatches, b)
	return &mockBatchResults{}
}

type mockHubBroadcaster struct {
	mu             sync.Mutex
	timelineEvents []*MatrixTimelineEventPayload
	presenceEvents []EventRow
}

func (m *mockHubBroadcaster) BroadcastTimelineEvent(assignmentID uuid.UUID, payload *MatrixTimelineEventPayload) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.timelineEvents = append(m.timelineEvents, payload)
}

func (m *mockHubBroadcaster) UpdatePresenceFromEvent(row EventRow) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.presenceEvents = append(m.presenceEvents, row)
}

func TestTelemetryBatcher_SizeBasedFlush(t *testing.T) {
	mockDB := &mockBatchDatabase{}
	mockHub := &mockHubBroadcaster{}

	batcher := NewTelemetryBatcher(
		mockDB,
		mockHub,
		WithBatchSize(200),
		WithFlushInterval(10*time.Second), // high interval so flush is purely size-based
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	batcher.Start(ctx)

	sessionID := uuid.New()
	assignmentID := uuid.New()
	userID := uuid.New()

	// Enqueue 205 events (including some tab_blur and insert_in_ide)
	events := make([]TelemetryEventItem, 205)
	for i := 0; i < 205; i++ {
		evtType := "task_opened"
		if i%10 == 0 {
			evtType = "tab_blur"
		} else if i%15 == 0 {
			evtType = "insert_in_ide"
		}
		events[i] = TelemetryEventItem{
			Event:    evtType,
			ClientTS: time.Now().UTC(),
			Payload:  json.RawMessage("{}"),
		}
	}

	batcher.EnqueueBatch(sessionID, assignmentID, userID, events)

	// Wait for worker to flush the first 200 items
	time.Sleep(100 * time.Millisecond)

	mockDB.mu.Lock()
	if len(mockDB.queuedBatches) == 0 {
		mockDB.mu.Unlock()
		t.Fatal("expected at least 1 database batch flush triggered by 200 items accumulation")
	}
	firstBatch := mockDB.queuedBatches[0]
	// First batch should have 200 INSERTs plus UPDATE queries for blurs/pastes
	if firstBatch.Len() < 200 {
		t.Errorf("expected at least 200 queued statements in batch, got %d", firstBatch.Len())
	}
	mockDB.mu.Unlock()

	// Stop batcher to drain the remaining 5 items
	batcher.Stop()

	mockDB.mu.Lock()
	if len(mockDB.queuedBatches) != 2 {
		t.Fatalf("expected 2 total batches after Stop, got %d", len(mockDB.queuedBatches))
	}
	mockDB.mu.Unlock()

	_, flushed, dropped := batcher.Stats()
	if flushed != 205 {
		t.Fatalf("expected 205 flushed items, got %d", flushed)
	}
	if dropped != 0 {
		t.Fatalf("expected 0 dropped items, got %d", dropped)
	}

	mockHub.mu.Lock()
	if len(mockHub.timelineEvents) != 205 {
		t.Errorf("expected 205 timeline events broadcast, got %d", len(mockHub.timelineEvents))
	}
	mockHub.mu.Unlock()
}

func TestTelemetryBatcher_IntervalFlush(t *testing.T) {
	mockDB := &mockBatchDatabase{}
	mockHub := &mockHubBroadcaster{}

	batcher := NewTelemetryBatcher(
		mockDB,
		mockHub,
		WithBatchSize(200),
		WithFlushInterval(50*time.Millisecond), // short interval
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	batcher.Start(ctx)

	sessionID := uuid.New()
	assignmentID := uuid.New()
	userID := uuid.New()

	batcher.Enqueue(EventRow{
		SessionID:    sessionID,
		AssignmentID: assignmentID,
		UserID:       userID,
		Event:        "tab_blur",
		OccurredAt:   time.Now().UTC(),
	})

	// Wait for interval flush
	time.Sleep(150 * time.Millisecond)

	mockDB.mu.Lock()
	if len(mockDB.queuedBatches) != 1 {
		t.Fatalf("expected 1 batch flush after interval, got %d", len(mockDB.queuedBatches))
	}
	mockDB.mu.Unlock()

	batcher.Stop()
}
