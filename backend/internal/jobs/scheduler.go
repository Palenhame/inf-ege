package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// ScheduledJob holds a registered job and its schedule specification.
type ScheduledJob struct {
	Job  Job
	Spec ScheduleSpec
}

// SchedulerConfig provides configuration for the Scheduler.
type SchedulerConfig struct {
	Logger *slog.Logger
	Locker Locker
}

// Scheduler manages background periodic and daily jobs with distributed locking.
type Scheduler struct {
	logger *slog.Logger
	locker Locker

	mu     sync.RWMutex
	jobs   map[string]ScheduledJob
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewScheduler creates a new background job Scheduler.
func NewScheduler(cfg SchedulerConfig) *Scheduler {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("component", "scheduler")

	locker := cfg.Locker
	if locker == nil {
		locker = NoopLocker{}
	}

	return &Scheduler{
		logger: logger,
		locker: locker,
		jobs:   make(map[string]ScheduledJob),
	}
}

// Register registers a job with custom schedule specification.
func (s *Scheduler) Register(job Job, spec ScheduleSpec) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	name := job.Name()
	if _, exists := s.jobs[name]; exists {
		return fmt.Errorf("job %q is already registered", name)
	}

	if spec.LockTTL <= 0 {
		if spec.Kind == ScheduleInterval && spec.Interval > 0 {
			spec.LockTTL = spec.Interval
		} else {
			spec.LockTTL = 5 * time.Minute
		}
	}

	s.jobs[name] = ScheduledJob{
		Job:  job,
		Spec: spec,
	}
	s.logger.Info("registered background job",
		"name", name,
		"kind", spec.Kind,
		"interval", spec.Interval,
		"dailyHour", spec.DailyHour,
		"dailyMinute", spec.DailyMinute,
	)
	return nil
}

// RegisterInterval registers a job that runs periodically at the given interval.
func (s *Scheduler) RegisterInterval(job Job, interval time.Duration, lockTTL time.Duration) error {
	return s.Register(job, ScheduleSpec{
		Kind:     ScheduleInterval,
		Interval: interval,
		LockTTL:  lockTTL,
	})
}

// RegisterDaily registers a job that runs once per day at the given UTC hour and minute.
func (s *Scheduler) RegisterDaily(job Job, hourUTC, minuteUTC int, lockTTL time.Duration) error {
	if hourUTC < 0 || hourUTC > 23 || minuteUTC < 0 || minuteUTC > 59 {
		return fmt.Errorf("invalid daily UTC schedule: %02d:%02d", hourUTC, minuteUTC)
	}
	return s.Register(job, ScheduleSpec{
		Kind:        ScheduleDailyUTC,
		DailyHour:   hourUTC,
		DailyMinute: minuteUTC,
		LockTTL:     lockTTL,
	})
}

// RegisterDefaults registers the 4 core background jobs specified in Backend.md:
// 1. ExpireSessionsJob (every 15s)
// 2. PartitionMaintenanceJob (hourly)
// 3. RecalculateTaskStatsJob (daily at 03:00 UTC)
// 4. RecalculateTopicWeaknessScoreJob (daily at 03:30 UTC)
func (s *Scheduler) RegisterDefaults(db DB, rdb RedisClient, notifier SessionExpiredNotifier) error {
	expireSessions := NewExpireSessionsJob(db, notifier, s.logger)
	if err := s.RegisterInterval(expireSessions, 15*time.Second, 14*time.Second); err != nil {
		return fmt.Errorf("failed to register expire sessions job: %w", err)
	}

	partitionMaintenance := NewPartitionMaintenanceJob(db, s.logger)
	if err := s.RegisterInterval(partitionMaintenance, 1*time.Hour, 10*time.Minute); err != nil {
		return fmt.Errorf("failed to register partition maintenance job: %w", err)
	}

	taskStats := NewRecalculateTaskStatsJob(db, rdb, s.logger)
	if err := s.RegisterDaily(taskStats, 3, 0, 30*time.Minute); err != nil {
		return fmt.Errorf("failed to register task stats job: %w", err)
	}

	topicWeakness := NewRecalculateTopicWeaknessScoreJob(db, rdb, s.logger)
	if err := s.RegisterDaily(topicWeakness, 3, 30, 30*time.Minute); err != nil {
		return fmt.Errorf("failed to register topic weakness score job: %w", err)
	}

	return nil
}

// Start initiates the scheduling loops for all registered jobs in background goroutines.
// It returns immediately and runs until ctx is cancelled or Stop() is called.
func (s *Scheduler) Start(ctx context.Context) {
	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		s.logger.Warn("scheduler is already running")
		return
	}

	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	jobsList := make([]ScheduledJob, 0, len(s.jobs))
	for _, j := range s.jobs {
		jobsList = append(jobsList, j)
	}
	s.mu.Unlock()

	s.logger.Info("starting scheduler", "job_count", len(jobsList))

	for _, sj := range jobsList {
		s.wg.Add(1)
		go func(entry ScheduledJob) {
			defer s.wg.Done()
			s.runLoop(runCtx, entry)
		}(sj)
	}
}

// Stop signals all job loops to terminate and waits for running executions to finish.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	cancel := s.cancel
	s.cancel = nil
	s.mu.Unlock()

	if cancel != nil {
		s.logger.Info("stopping scheduler...")
		cancel()
		s.wg.Wait()
		s.logger.Info("scheduler stopped cleanly")
	}
}

// RunJobNow triggers a registered job immediately without waiting for its next scheduled run.
func (s *Scheduler) RunJobNow(ctx context.Context, name string) error {
	s.mu.RLock()
	sj, exists := s.jobs[name]
	s.mu.RUnlock()

	if !exists {
		return fmt.Errorf("job %q not found", name)
	}

	return s.executeWithLock(ctx, sj.Job, sj.Spec.LockTTL)
}

// runLoop executes the timing loop for a single scheduled job.
func (s *Scheduler) runLoop(ctx context.Context, sj ScheduledJob) {
	jobName := sj.Job.Name()
	s.logger.Debug("starting job loop", "job", jobName)

	switch sj.Spec.Kind {
	case ScheduleInterval:
		ticker := time.NewTicker(sj.Spec.Interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				s.logger.Debug("job loop stopping", "job", jobName)
				return
			case <-ticker.C:
				if err := s.executeWithLock(ctx, sj.Job, sj.Spec.LockTTL); err != nil {
					s.logger.ErrorContext(ctx, "job execution error", "job", jobName, "error", err)
				}
			}
		}

	case ScheduleDailyUTC:
		for {
			now := time.Now().UTC()
			nextRun := NextDailyTime(now, sj.Spec.DailyHour, sj.Spec.DailyMinute)
			waitDuration := nextRun.Sub(now)

			s.logger.Debug("scheduled next daily run",
				"job", jobName,
				"next_run", nextRun.Format(time.RFC3339),
				"wait", waitDuration.Round(time.Second),
			)

			timer := time.NewTimer(waitDuration)
			select {
			case <-ctx.Done():
				timer.Stop()
				s.logger.Debug("job loop stopping", "job", jobName)
				return
			case <-timer.C:
				if err := s.executeWithLock(ctx, sj.Job, sj.Spec.LockTTL); err != nil {
					s.logger.ErrorContext(ctx, "daily job execution error", "job", jobName, "error", err)
				}
			}
		}
	}
}

// executeWithLock acquires a distributed lock and executes the job.
func (s *Scheduler) executeWithLock(ctx context.Context, job Job, lockTTL time.Duration) error {
	jobName := job.Name()

	unlock, acquired, err := s.locker.Acquire(ctx, jobName, lockTTL)
	if err != nil {
		return fmt.Errorf("failed to acquire lock for job %s: %w", jobName, err)
	}
	if !acquired {
		s.logger.Debug("job skipped: lock held by another instance", "job", jobName)
		return nil
	}
	defer unlock()

	start := time.Now()
	s.logger.Info("running job", "job", jobName)

	execErr := job.Run(ctx)
	duration := time.Since(start)

	if execErr != nil {
		s.logger.Error("job failed", "job", jobName, "duration", duration, "error", execErr)
		return execErr
	}

	s.logger.Info("job completed successfully", "job", jobName, "duration", duration)
	return nil
}

// NextDailyTime returns the next upcoming time matching hour and minute in UTC after `after`.
func NextDailyTime(after time.Time, hour, minute int) time.Time {
	afterUTC := after.UTC()
	target := time.Date(
		afterUTC.Year(),
		afterUTC.Month(),
		afterUTC.Day(),
		hour,
		minute,
		0,
		0,
		time.UTC,
	)

	if !target.After(afterUTC) {
		target = target.AddDate(0, 0, 1)
	}

	return target
}
