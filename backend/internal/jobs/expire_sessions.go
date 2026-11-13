package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// SessionExpiredEvent contains information about a session that has timed out.
type SessionExpiredEvent struct {
	SessionID      uuid.UUID
	AssignmentID   uuid.UUID
	UserID         uuid.UUID
	PrimaryScore   int16
	SecondaryScore *int16
	FinishedAt     time.Time
}

// SessionExpiredNotifier receives notifications when sessions expire.
type SessionExpiredNotifier interface {
	NotifySessionExpired(ctx context.Context, event SessionExpiredEvent) error
}

// ExpireSessionsJob finds overdue assignment sessions and transitions them to 'expired'.
type ExpireSessionsJob struct {
	db       DB
	notifier SessionExpiredNotifier
	logger   *slog.Logger
	batchSize int
}

// NewExpireSessionsJob creates a new ExpireSessionsJob.
func NewExpireSessionsJob(db DB, notifier SessionExpiredNotifier, logger *slog.Logger) *ExpireSessionsJob {
	if logger == nil {
		logger = slog.Default()
	}
	return &ExpireSessionsJob{
		db:        db,
		notifier:  notifier,
		logger:    logger.With("job", "expire_sessions"),
		batchSize: 100,
	}
}

// Name returns the job identifier.
func (j *ExpireSessionsJob) Name() string {
	return "expire_sessions"
}

const expireSessionsQuery = `
WITH expired_sessions AS (
    SELECT id
    FROM assignment_sessions
    WHERE status = 'in_progress'
      AND expires_at IS NOT NULL
      AND expires_at <= now()
    ORDER BY expires_at ASC
    LIMIT $1
    FOR UPDATE SKIP LOCKED
),
scores AS (
    SELECT
        es.id AS session_id,
        coalesce(sum(uat.earned_score), 0)::smallint AS primary_score
    FROM expired_sessions es
    LEFT JOIN user_assignment_tasks uat ON uat.session_id = es.id
    GROUP BY es.id
),
scored AS (
    SELECT
        s.session_id,
        s.primary_score,
        scale.secondary_score
    FROM scores s
    LEFT JOIN exam_score_scale scale ON scale.primary_score = least(s.primary_score, 29::smallint)
)
UPDATE assignment_sessions s
SET status = 'expired',
    primary_score = sc.primary_score,
    secondary_score = coalesce(sc.secondary_score, least(round((sc.primary_score::numeric * 100.0) / 29.0)::smallint, 100::smallint)),
    finished_at = now()
FROM scored sc
WHERE s.id = sc.session_id
RETURNING s.id, s.assignment_id, s.user_id, s.primary_score, s.secondary_score, s.finished_at;
`

// Run executes the session expiration check and updates overdue sessions.
func (j *ExpireSessionsJob) Run(ctx context.Context) error {
	totalExpired := 0

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		rows, err := j.db.Query(ctx, expireSessionsQuery, j.batchSize)
		if err != nil {
			return fmt.Errorf("failed to query and expire sessions: %w", err)
		}

		type expiredRow struct {
			SessionID      uuid.UUID
			AssignmentID   uuid.UUID
			UserID         uuid.UUID
			PrimaryScore   int16
			SecondaryScore *int16
			FinishedAt     time.Time
		}

		var batch []expiredRow
		for rows.Next() {
			var r expiredRow
			if err := rows.Scan(
				&r.SessionID,
				&r.AssignmentID,
				&r.UserID,
				&r.PrimaryScore,
				&r.SecondaryScore,
				&r.FinishedAt,
			); err != nil {
				rows.Close()
				return fmt.Errorf("failed to scan expired session: %w", err)
			}
			batch = append(batch, r)
		}
		rows.Close()

		if err := rows.Err(); err != nil {
			return fmt.Errorf("error during expired session iteration: %w", err)
		}

		count := len(batch)
		if count == 0 {
			break
		}

		totalExpired += count
		j.logger.InfoContext(ctx, "expired batch of sessions", "count", count)

		if j.notifier != nil {
			for _, r := range batch {
				event := SessionExpiredEvent{
					SessionID:      r.SessionID,
					AssignmentID:   r.AssignmentID,
					UserID:         r.UserID,
					PrimaryScore:   r.PrimaryScore,
					SecondaryScore: r.SecondaryScore,
					FinishedAt:     r.FinishedAt,
				}
				if notifyErr := j.notifier.NotifySessionExpired(ctx, event); notifyErr != nil {
					j.logger.WarnContext(ctx, "failed to send session expired notification",
						"session_id", r.SessionID,
						"error", notifyErr,
					)
				}
			}
		}

		// If fewer rows than batchSize were returned, all expired sessions have been drained
		if count < j.batchSize {
			break
		}
	}

	if totalExpired > 0 {
		j.logger.InfoContext(ctx, "finished expiring sessions", "total_expired", totalExpired)
	}
	return nil
}
