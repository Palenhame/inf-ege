package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

const taskStatsCheckpointKey = "jobs:checkpoint:task_stats"

// RecalculateTaskStatsJob recalculates performance and usage metrics for tasks.
type RecalculateTaskStatsJob struct {
	db              DB
	redis           RedisClient
	logger          *slog.Logger
	defaultLookback time.Duration
}

// NewRecalculateTaskStatsJob creates a new RecalculateTaskStatsJob.
func NewRecalculateTaskStatsJob(db DB, redis RedisClient, logger *slog.Logger) *RecalculateTaskStatsJob {
	if logger == nil {
		logger = slog.Default()
	}
	return &RecalculateTaskStatsJob{
		db:              db,
		redis:           redis,
		logger:          logger.With("job", "recalculate_task_stats"),
		defaultLookback: 25 * time.Hour,
	}
}

// Name returns the job identifier.
func (j *RecalculateTaskStatsJob) Name() string {
	return "recalculate_task_stats"
}

const recalculateTaskStatsQuery = `
WITH changed AS (
    SELECT id AS task_id
    FROM tasks
    WHERE deleted_at IS NULL AND $2 = true
    UNION
    SELECT DISTINCT task_id FROM (
        SELECT task_id FROM user_tasks WHERE updated_at >= $1
        UNION
        SELECT task_id FROM user_assignment_tasks WHERE updated_at >= $1
    ) active_tasks
    WHERE $2 = false
),
bank_stats AS (
    SELECT
        ut.task_id,
        round(count(*) filter (where ut.is_correct = true)::numeric / nullif(count(*), 0) * 100, 2) AS solve_rate,
        round(avg(ut.total_attempts)::numeric, 2) AS avg_attempts,
        count(distinct ut.user_id)::int AS bank_stats_count
    FROM user_tasks ut
    JOIN changed c ON c.task_id = ut.task_id
    GROUP BY ut.task_id
),
test_stats AS (
    SELECT
        uat.task_id,
        round(avg(uat.time_spent_sec))::int AS avg_solve_time,
        count(distinct uat.user_id)::int AS test_stats_count
    FROM user_assignment_tasks uat
    JOIN changed c ON c.task_id = uat.task_id
    GROUP BY uat.task_id
),
merged AS (
    SELECT
        c.task_id,
        b.solve_rate,
        b.avg_attempts,
        coalesce(b.bank_stats_count, 0) AS bank_stats_count,
        t.avg_solve_time,
        coalesce(t.test_stats_count, 0) AS test_stats_count
    FROM changed c
    LEFT JOIN bank_stats b ON b.task_id = c.task_id
    LEFT JOIN test_stats t ON t.task_id = c.task_id
)
UPDATE tasks
SET solve_rate = m.solve_rate,
    avg_attempts = m.avg_attempts,
    bank_stats_count = m.bank_stats_count,
    avg_solve_time = m.avg_solve_time,
    test_stats_count = m.test_stats_count,
    updated_at = now()
FROM merged m
WHERE tasks.id = m.task_id;
`

// Run executes task stats recalculation incrementally.
func (j *RecalculateTaskStatsJob) Run(ctx context.Context) error {
	return j.RunWithOptions(ctx, false)
}

// RunWithOptions executes task stats recalculation with optional full table scan.
func (j *RecalculateTaskStatsJob) RunWithOptions(ctx context.Context, fullRecalculate bool) error {
	now := time.Now().UTC()
	since := now.Add(-j.defaultLookback)

	if !fullRecalculate && j.redis != nil {
		if val, err := j.redis.Get(ctx, taskStatsCheckpointKey).Result(); err == nil && val != "" {
			if parsed, parseErr := time.Parse(time.RFC3339, val); parseErr == nil {
				since = parsed
			}
		}
	}

	j.logger.InfoContext(ctx, "starting task stats recalculation",
		"since", since.Format(time.RFC3339),
		"full", fullRecalculate,
	)

	tag, err := j.db.Exec(ctx, recalculateTaskStatsQuery, since, fullRecalculate)
	if err != nil {
		return fmt.Errorf("failed to recalculate task stats: %w", err)
	}

	rowsUpdated := tag.RowsAffected()
	j.logger.InfoContext(ctx, "task stats recalculation completed", "rows_updated", rowsUpdated)

	if j.redis != nil {
		if err := j.redis.Set(ctx, taskStatsCheckpointKey, now.Format(time.RFC3339), 30*24*time.Hour).Err(); err != nil {
			j.logger.WarnContext(ctx, "failed to update checkpoint in Redis", "error", err)
		}
	}

	return nil
}
