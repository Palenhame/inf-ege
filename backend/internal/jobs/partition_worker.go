package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"time"
)

var partitionRegex = regexp.MustCompile(`^user_task_events_(\d{4})_(\d{2})$`)

// MonthlyPartitionSpec represents the DDL parameters for a monthly partition.
type MonthlyPartitionSpec struct {
	TableName string
	FromDate  string
	ToDate    string
	Year      int
	Month     int
}

// PartitionMaintenanceJob creates future monthly partitions and detaches expired ones.
type PartitionMaintenanceJob struct {
	db              DB
	logger          *slog.Logger
	monthsAhead     int
	retentionMonths int
}

// NewPartitionMaintenanceJob creates a new PartitionMaintenanceJob.
func NewPartitionMaintenanceJob(db DB, logger *slog.Logger) *PartitionMaintenanceJob {
	if logger == nil {
		logger = slog.Default()
	}
	return &PartitionMaintenanceJob{
		db:              db,
		logger:          logger.With("job", "partition_maintenance"),
		monthsAhead:     3,  // current_month + 3 months
		retentionMonths: 12, // detach partitions older than 12 months
	}
}

// Name returns the job identifier.
func (j *PartitionMaintenanceJob) Name() string {
	return "partition_maintenance"
}

// Run executes monthly partition creation and detaches old partitions.
func (j *PartitionMaintenanceJob) Run(ctx context.Context) error {
	now := time.Now().UTC()

	// 1. Ensure monthly partitions exist for current_month through current_month + monthsAhead
	partitionsToCreate := GetMonthlyPartitionsToCreate(now, j.monthsAhead)
	for _, spec := range partitionsToCreate {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		query := fmt.Sprintf(
			"CREATE TABLE IF NOT EXISTS %s PARTITION OF user_task_events FOR VALUES FROM ('%s') TO ('%s')",
			spec.TableName,
			spec.FromDate,
			spec.ToDate,
		)

		if _, err := j.db.Exec(ctx, query); err != nil {
			return fmt.Errorf("failed to create partition %s: %w", spec.TableName, err)
		}
		j.logger.DebugContext(ctx, "verified/created monthly partition",
			"table", spec.TableName,
			"from", spec.FromDate,
			"to", spec.ToDate,
		)
	}

	// 2. Query existing partitions of user_task_events
	existingPartitions, err := j.queryExistingPartitions(ctx)
	if err != nil {
		return fmt.Errorf("failed to query existing partitions: %w", err)
	}

	// 3. Detach partitions older than retentionMonths
	toDetach := GetPartitionsToDetach(existingPartitions, now, j.retentionMonths)
	for _, partName := range toDetach {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Validate partition name matches strict pattern to prevent SQL injection
		if !partitionRegex.MatchString(partName) {
			j.logger.WarnContext(ctx, "skipping detachment of partition with unexpected name pattern", "table", partName)
			continue
		}

		query := fmt.Sprintf("ALTER TABLE user_task_events DETACH PARTITION %s", partName)
		if _, err := j.db.Exec(ctx, query); err != nil {
			j.logger.ErrorContext(ctx, "failed to detach partition", "table", partName, "error", err)
			continue
		}
		j.logger.InfoContext(ctx, "successfully detached expired partition", "table", partName)
	}

	return nil
}

// queryExistingPartitions retrieves all child partition table names of user_task_events.
func (j *PartitionMaintenanceJob) queryExistingPartitions(ctx context.Context) ([]string, error) {
	const query = `
		SELECT c.relname
		FROM pg_class c
		JOIN pg_inherits i ON i.inhrelid = c.oid
		JOIN pg_class p ON p.oid = i.inhparent
		WHERE p.relname = 'user_task_events';
	`
	rows, err := j.db.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var partitions []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		partitions = append(partitions, name)
	}
	return partitions, rows.Err()
}

// GetMonthlyPartitionsToCreate calculates partition specifications from now through now + monthsAhead.
func GetMonthlyPartitionsToCreate(now time.Time, monthsAhead int) []MonthlyPartitionSpec {
	nowUTC := now.UTC()
	startOfMonth := time.Date(nowUTC.Year(), nowUTC.Month(), 1, 0, 0, 0, 0, time.UTC)

	specs := make([]MonthlyPartitionSpec, 0, monthsAhead+1)
	for offset := 0; offset <= monthsAhead; offset++ {
		target := startOfMonth.AddDate(0, offset, 0)
		nextMonth := target.AddDate(0, 1, 0)

		specs = append(specs, MonthlyPartitionSpec{
			TableName: fmt.Sprintf("user_task_events_%04d_%02d", target.Year(), int(target.Month())),
			FromDate:  target.Format("2006-01-02"),
			ToDate:    nextMonth.Format("2006-01-02"),
			Year:      target.Year(),
			Month:     int(target.Month()),
		})
	}
	return specs
}

// GetPartitionsToDetach identifies partition names older than retentionMonths from now.
func GetPartitionsToDetach(existingPartitions []string, now time.Time, retentionMonths int) []string {
	nowUTC := now.UTC()
	cutoff := time.Date(nowUTC.Year(), nowUTC.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -retentionMonths, 0)

	var toDetach []string
	for _, partName := range existingPartitions {
		matches := partitionRegex.FindStringSubmatch(partName)
		if len(matches) != 3 {
			continue
		}

		year, errY := strconv.Atoi(matches[1])
		month, errM := strconv.Atoi(matches[2])
		if errY != nil || errM != nil || month < 1 || month > 12 {
			continue
		}

		partDate := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
		if partDate.Before(cutoff) {
			toDetach = append(toDetach, partName)
		}
	}
	return toDetach
}
