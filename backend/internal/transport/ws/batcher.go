package ws

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	// DefaultBatcherCapacity is the in-memory buffered channel capacity (16,384).
	DefaultBatcherCapacity = 16384

	// DefaultFlushInterval is 500ms.
	DefaultFlushInterval = 500 * time.Millisecond

	// DefaultBatchSize is 200 items.
	DefaultBatchSize = 200
)

// EventRow represents a row to be inserted into partitioned user_task_events.
type EventRow struct {
	SessionID    uuid.UUID       `json:"session_id"`
	AssignmentID uuid.UUID       `json:"assignment_id"`
	UserID       uuid.UUID       `json:"user_id"`
	TaskID       *int            `json:"task_id,omitempty"`
	Event        string          `json:"event"`
	Payload      json.RawMessage `json:"payload"`
	OccurredAt   time.Time       `json:"occurred_at"`
}

// BatchDatabase defines interface for executing batches in pgxpool.
type BatchDatabase interface {
	SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
}

// HubBroadcaster represents the subset of Hub methods needed by TelemetryBatcher.
type HubBroadcaster interface {
	BroadcastTimelineEvent(assignmentID uuid.UUID, payload *MatrixTimelineEventPayload)
	UpdatePresenceFromEvent(row EventRow)
}

// SessionAssignmentResolver resolves assignment_id for a session if not populated.
type SessionAssignmentResolver func(sessionID uuid.UUID) uuid.UUID

// BatcherOption configures TelemetryBatcher.
type BatcherOption func(*TelemetryBatcher)

// WithBatchSize sets maximum items accumulated before a flush is triggered.
func WithBatchSize(size int) BatcherOption {
	return func(b *TelemetryBatcher) {
		if size > 0 {
			b.batchSize = size
		}
	}
}

// WithFlushInterval sets the periodic flush timer.
func WithFlushInterval(d time.Duration) BatcherOption {
	return func(b *TelemetryBatcher) {
		if d > 0 {
			b.flushInterval = d
		}
	}
}

// WithChannelCapacity sets custom queue capacity.
func WithChannelCapacity(c int) BatcherOption {
	return func(b *TelemetryBatcher) {
		if c > 0 {
			b.queueCapacity = c
		}
	}
}

// WithSessionResolver sets a resolver for session -> assignment mapping.
func WithSessionResolver(resolver SessionAssignmentResolver) BatcherOption {
	return func(b *TelemetryBatcher) {
		b.sessionResolver = resolver
	}
}

// TelemetryBatcher asynchronously buffers high-throughput telemetry events,
// flushes them in batches to partitioned user_task_events, updates session counters,
// and broadcasts real-time timeline events to teachers.
type TelemetryBatcher struct {
	queue           chan EventRow
	queueCapacity   int
	batchSize       int
	flushInterval   time.Duration
	db              BatchDatabase
	hub             HubBroadcaster
	sessionResolver SessionAssignmentResolver

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	dropped uint64
	flushed uint64
}

// NewTelemetryBatcher creates a new TelemetryBatcher.
func NewTelemetryBatcher(db BatchDatabase, hub HubBroadcaster, opts ...BatcherOption) *TelemetryBatcher {
	b := &TelemetryBatcher{
		queueCapacity: DefaultBatcherCapacity,
		batchSize:     DefaultBatchSize,
		flushInterval: DefaultFlushInterval,
		db:            db,
		hub:           hub,
	}

	for _, opt := range opts {
		opt(b)
	}

	b.queue = make(chan EventRow, b.queueCapacity)
	return b
}

// Start spawns the background worker event loop.
func (b *TelemetryBatcher) Start(parentCtx context.Context) {
	b.ctx, b.cancel = context.WithCancel(parentCtx)
	b.wg.Add(1)
	go b.worker()
}

// Stop gracefully drains queued events, flushes remaining items, and halts worker.
func (b *TelemetryBatcher) Stop() {
	if b.cancel != nil {
		b.cancel()
	}
	b.wg.Wait()
}

// Enqueue puts a single event into the telemetry channel without blocking.
// Returns false if channel is saturated and event was dropped.
func (b *TelemetryBatcher) Enqueue(row EventRow) bool {
	if row.OccurredAt.IsZero() {
		row.OccurredAt = time.Now().UTC()
	}
	if len(row.Payload) == 0 {
		row.Payload = json.RawMessage("{}")
	}

	select {
	case b.queue <- row:
		return true
	default:
		b.mu.Lock()
		b.dropped++
		b.mu.Unlock()
		slog.Warn("TelemetryBatcher queue saturated, dropping event", "session_id", row.SessionID, "event", row.Event)
		return false
	}
}

// EnqueueBatch enqueues multiple telemetry events for a session.
func (b *TelemetryBatcher) EnqueueBatch(sessionID uuid.UUID, assignmentID uuid.UUID, userID uuid.UUID, events []TelemetryEventItem) {
	if assignmentID == uuid.Nil && b.sessionResolver != nil {
		assignmentID = b.sessionResolver(sessionID)
	}

	for _, item := range events {
		row := EventRow{
			SessionID:    sessionID,
			AssignmentID: assignmentID,
			UserID:       userID,
			TaskID:       item.TaskID,
			Event:        item.Event,
			Payload:      item.Payload,
			OccurredAt:   item.ClientTS,
		}
		b.Enqueue(row)
	}
}

// worker runs the 500ms ticker or 200-item batch flush event loop.
func (b *TelemetryBatcher) worker() {
	defer b.wg.Done()

	ticker := time.NewTicker(b.flushInterval)
	defer ticker.Stop()

	batch := make([]EventRow, 0, b.batchSize)

	for {
		select {
		case <-b.ctx.Done():
			// Drain remaining events in channel
			for {
				select {
				case row := <-b.queue:
					batch = append(batch, row)
					if len(batch) >= b.batchSize {
						b.flush(batch)
						batch = make([]EventRow, 0, b.batchSize)
					}
				default:
					if len(batch) > 0 {
						b.flush(batch)
					}
					return
				}
			}

		case row := <-b.queue:
			batch = append(batch, row)
			if len(batch) >= b.batchSize {
				b.flush(batch)
				batch = make([]EventRow, 0, b.batchSize)
				ticker.Reset(b.flushInterval)
			}

		case <-ticker.C:
			if len(batch) > 0 {
				b.flush(batch)
				batch = make([]EventRow, 0, b.batchSize)
			}
		}
	}
}

// flush persists events via pgx.Batch, increments session counters, and broadcasts deltas.
func (b *TelemetryBatcher) flush(batch []EventRow) {
	if len(batch) == 0 {
		return
	}

	// 1. Insert into database if configured
	if b.db != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		pgBatch := &pgx.Batch{}

		// Insert into partitioned user_task_events
		for _, row := range batch {
			pgBatch.Queue(
				`INSERT INTO user_task_events (session_id, assignment_id, user_id, task_id, event, payload, occurred_at) `+
					`VALUES ($1, $2, $3, $4, $5, $6, $7)`,
				row.SessionID, row.AssignmentID, row.UserID, row.TaskID, row.Event, row.Payload, row.OccurredAt,
			)
		}

		// Aggregate blur and paste counts per session
		blurCounts := make(map[uuid.UUID]int)
		pasteCounts := make(map[uuid.UUID]int)
		for _, row := range batch {
			switch row.Event {
			case "tab_blur":
				blurCounts[row.SessionID]++
			case "insert_in_ide":
				pasteCounts[row.SessionID]++
			}
		}

		for sessionID, blurs := range blurCounts {
			pastes := pasteCounts[sessionID]
			delete(pasteCounts, sessionID)
			pgBatch.Queue(
				`UPDATE assignment_sessions SET blur_count = blur_count + $1, paste_count = paste_count + $2 WHERE id = $3`,
				blurs, pastes, sessionID,
			)
		}
		for sessionID, pastes := range pasteCounts {
			pgBatch.Queue(
				`UPDATE assignment_sessions SET paste_count = paste_count + $1 WHERE id = $2`,
				pastes, sessionID,
			)
		}

		br := b.db.SendBatch(ctx, pgBatch)
		if err := br.Close(); err != nil {
			slog.Error("Failed to execute telemetry batch", "error", err, "count", len(batch))
		}
	}

	// 2. Broadcast timeline events & update presence in Hub
	if b.hub != nil {
		for _, row := range batch {
			if row.AssignmentID != uuid.Nil {
				b.hub.BroadcastTimelineEvent(row.AssignmentID, &MatrixTimelineEventPayload{
					AssignmentID: row.AssignmentID,
					UserID:       row.UserID,
					SessionID:    row.SessionID,
					Event: TimelineEventData{
						TaskID:     row.TaskID,
						Event:      row.Event,
						Payload:    row.Payload,
						OccurredAt: row.OccurredAt,
					},
				})
			}
			b.hub.UpdatePresenceFromEvent(row)
		}
	}

	b.mu.Lock()
	b.flushed += uint64(len(batch))
	b.mu.Unlock()
}

// Stats returns counts of queued, flushed, and dropped events.
func (b *TelemetryBatcher) Stats() (queued int, flushed uint64, dropped uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.queue), b.flushed, b.dropped
}
