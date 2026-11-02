package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/caltime"
	"github.com/voidrot/chorecast/server/internal/chores"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/devices"
	"github.com/voidrot/chorecast/server/internal/events"
	"github.com/voidrot/chorecast/server/internal/horizon"
	"github.com/voidrot/chorecast/server/internal/household"
	"github.com/voidrot/chorecast/server/internal/idempotency"
	"github.com/voidrot/chorecast/server/internal/jobs"
	"github.com/voidrot/chorecast/server/internal/kids"
	"github.com/voidrot/chorecast/server/internal/parents"
)

const (
	schedulerInterval = 5 * time.Second
	kindMaintenance   = "maintenance.daily"
	kindHorizon       = "horizon.generate" // also enqueued once at startup (serve)
	keepDoneJobs      = 7 * 24 * time.Hour
	keepFailedJobs    = 30 * 24 * time.Hour
)

// newBackground builds the job runner, scheduler and event bus with every job kind, recurring
// schedule and event subscriber. Feature work adds its kinds, providers and subscribers here
// (day close, week close, horizon, notifications, ...).
func newBackground(store *db.DB, clk clock.Clock, log *slog.Logger) (*jobs.Runner, *jobs.Scheduler, *events.Bus) {
	runner := jobs.NewRunner(store.Queries, clk, log)
	sched := jobs.NewScheduler(store.Queries, runner, clk, log)
	bus := events.NewBus(store, runner, clk, log)

	tokens := auth.NewStore(store.Queries, clk)
	parentsSvc := parents.New(store, clk)
	pairing := devices.New(store, clk)
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
		purgedKeys, err := idempotency.Purge(ctx, store.Queries, now)
		if err != nil {
			return err
		}
		purgedEvents, err := bus.Purge(ctx, now.Add(-events.KeepDispatched))
		if err != nil {
			return err
		}
		purgedInvites, err := parentsSvc.PurgeExpiredInvites(ctx)
		if err != nil {
			return err
		}
		purgedCodes, err := pairing.PurgeExpiredCodes(ctx)
		if err != nil {
			return err
		}
		log.Info("maintenance", "purged_jobs", purgedJobs, "purged_tokens", purgedTokens,
			"purged_idempotency_keys", purgedKeys, "purged_events", purgedEvents, "purged_invites", purgedInvites,
			"purged_pairing_codes", purgedCodes)
		return nil
	}, jobs.KindOptions{MaxAttempts: 3})
	sched.Add(jobs.Every("maintenance", kindMaintenance, 24*time.Hour))

	// The rolling horizon: regenerated at each household day end, at startup, and whenever the
	// calendar, a chore or a kid changes. Every run regenerates everything, so one handler serves
	// all triggers and repeats are harmless.
	gen := horizon.New(store, clk)
	generate := func(ctx context.Context) error {
		res, err := gen.Generate(ctx)
		if err != nil {
			return err
		}
		log.Info("horizon", "inserted", res.Inserted, "updated", res.Updated, "deleted", res.Deleted)
		return nil
	}
	runner.Register(kindHorizon, func(ctx context.Context, _ jobs.Job) error { return generate(ctx) }, jobs.KindOptions{})
	dayEnds := jobs.HouseholdDayCloses("horizon", kindHorizon, func(ctx context.Context) (caltime.Household, error) {
		return household.Calendar(ctx, store.Queries)
	})
	sched.Add(func(ctx context.Context) ([]jobs.Recurring, error) {
		if _, err := store.GetHousehold(ctx); errors.Is(err, sql.ErrNoRows) {
			return nil, nil // before first-run setup there's no calendar yet
		}
		return dayEnds(ctx)
	})
	for _, ev := range []events.Event{household.CalendarChanged{}, chores.ChoreChanged{}, kids.KidChanged{}} {
		bus.Subscribe(ev.EventType(), "horizon."+ev.EventType(), func(ctx context.Context, _ events.Envelope) error {
			return generate(ctx)
		}, jobs.KindOptions{})
	}

	return runner, sched, bus
}
