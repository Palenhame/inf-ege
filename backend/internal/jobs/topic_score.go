package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"
)

const topicScoreCheckpointKey = "jobs:checkpoint:topic_weakness_score"

// RecalculateTopicWeaknessScoreJob computes topic weakness scores in user_topic_stats.
type RecalculateTopicWeaknessScoreJob struct {
	db              DB
	redis           RedisClient
	logger          *slog.Logger
	defaultLookback time.Duration
}

// NewRecalculateTopicWeaknessScoreJob creates a new RecalculateTopicWeaknessScoreJob.
func NewRecalculateTopicWeaknessScoreJob(db DB, redis RedisClient, logger *slog.Logger) *RecalculateTopicWeaknessScoreJob {
	if logger == nil {
		logger = slog.Default()
	}
	return &RecalculateTopicWeaknessScoreJob{
		db:              db,
		redis:           redis,
		logger:          logger.With("job", "recalculate_topic_weakness_score"),
		defaultLookback: 25 * time.Hour,
	}
}

// Name returns the job identifier.
func (j *RecalculateTopicWeaknessScoreJob) Name() string {
	return "recalculate_topic_weakness_score"
}

// Run executes the topic weakness score recalculation.
func (j *RecalculateTopicWeaknessScoreJob) Run(ctx context.Context) error {
	return j.RunWithOptions(ctx, false)
}

// RunWithOptions recalculates topic weakness scores with optional full table update.
func (j *RecalculateTopicWeaknessScoreJob) RunWithOptions(ctx context.Context, fullRecalculate bool) error {
	now := time.Now().UTC()
	since := now.Add(-j.defaultLookback)

	if !fullRecalculate && j.redis != nil {
		if val, err := j.redis.Get(ctx, topicScoreCheckpointKey).Result(); err == nil && val != "" {
			if parsed, parseErr := time.Parse(time.RFC3339, val); parseErr == nil {
				since = parsed
			}
		}
	}

	// 1. Calculate normalizer over all user_topic_stats with attempted > 0
	const normalizerQuery = `
		SELECT coalesce(max((100.0 - success_rate) * ln(attempted + 1)), 1.0)
		FROM user_topic_stats
		WHERE attempted > 0;
	`
	var normalizer float64
	if err := j.db.QueryRow(ctx, normalizerQuery).Scan(&normalizer); err != nil {
		return fmt.Errorf("failed to query topic score normalizer: %w", err)
	}

	if normalizer <= 0 {
		normalizer = 1.0
	}

	j.logger.InfoContext(ctx, "calculated topic weakness normalizer",
		"normalizer", normalizer,
		"since", since.Format(time.RFC3339),
		"full", fullRecalculate,
	)

	// 2. Update user_topic_stats with logarithmic score formula:
	// round(least((100.0 - success_rate) * ln(attempted + 1) / nullif(normalizer, 0) * 100, 100), 2)
	const updateQuery = `
		UPDATE user_topic_stats
		SET score = round(least((100.0 - success_rate) * ln(attempted + 1) / nullif($1, 0) * 100.0, 100.0), 2),
		    stats_updated_at = now(),
		    updated_at = now()
		WHERE ($2 = true OR stats_updated_at >= $3);
	`

	tag, err := j.db.Exec(ctx, updateQuery, normalizer, fullRecalculate, since)
	if err != nil {
		return fmt.Errorf("failed to update topic weakness scores: %w", err)
	}

	rowsUpdated := tag.RowsAffected()
	j.logger.InfoContext(ctx, "recalculated topic weakness scores",
		"rows_updated", rowsUpdated,
		"normalizer", normalizer,
	)

	if j.redis != nil {
		if err := j.redis.Set(ctx, topicScoreCheckpointKey, now.Format(time.RFC3339), 30*24*time.Hour).Err(); err != nil {
			j.logger.WarnContext(ctx, "failed to update checkpoint in Redis", "error", err)
		}
	}

	return nil
}

// CalculateWeaknessScore evaluates the mathematical formula in Go for testing and local calculations.
// formula: round(least((100.0 - success_rate) * ln(attempted + 1) / nullif(normalizer, 0) * 100, 100), 2)
func CalculateWeaknessScore(successRate float64, attempted int, normalizer float64) float64 {
	if attempted <= 0 {
		return 0.0
	}
	if normalizer <= 0 {
		normalizer = 1.0
	}

	raw := (100.0 - successRate) * math.Log(float64(attempted+1)) / normalizer * 100.0
	capped := math.Min(raw, 100.0)
	if capped < 0 {
		capped = 0
	}
	return math.Round(capped*100.0) / 100.0
}
