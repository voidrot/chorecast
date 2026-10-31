package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/jobs"
)

const (
	schedulerInterval = 5 * time.Second
	kindMaintenance   = "maintenance.daily"
	keepDoneJobs      = 7 * 24 * time.Hour
	keepFailedJobs    = 30 * 24 * time.Hour
)

// newJobs builds the job runner and scheduler with every job kind and recurring schedule.
// Feature work adds its kinds and providers here (day close, week close, horizon, ...).
func newJobs(store *db.DB, clk clock.Clock, log *slog.Logger) (*jobs.Runner, *jobs.Scheduler) {
	runner := jobs.NewRunner(store.Queries, clk, log)
	sched := jobs.NewScheduler(store.Queries, runner, clk, log)

	tokens := auth.NewStore(store.Queries, clk)
	runner.Register(kindMaintenance, func(ctx context.Context, _ jobs.Job) error {
		now := clk.Now()
		purgedJobs, err := runner.PurgeFinished(ctx, now.Add(-keepDoneJobs), now.Add(-keepFailedJobs))
		if err != nil {
			return err
		}
		purgedTokens, err := tokens.PurgeExpired(ctx)
		if err != nil {
			return err
		}
		log.Info("maintenance", "purged_jobs", purgedJobs, "purged_tokens", purgedTokens)
		return nil
	}, jobs.KindOptions{MaxAttempts: 3})
	sched.Add(jobs.Every("maintenance", kindMaintenance, 24*time.Hour))

	return runner, sched
}
