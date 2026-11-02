package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/voidrot/chorecast/server/internal/caltime"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
)

// maxCatchUp bounds how many missed occurrences of one schedule a single tick enqueues, so a
// server that was off for months doesn't flood the queue in one go. Later ticks continue.
const maxCatchUp = 64

// Occurrence is one firing of a recurring schedule.
type Occurrence struct {
	At      time.Time
	Key     string // stable per occurrence, e.g. the local date; part of the idempotency key
	Payload any
}

// Recurring is a schedule that enqueues Kind at each occurrence.
type Recurring struct {
	Name string // unique and stable, e.g. "day_close:dayclose.kid_day:kid:42"; also the cursor key
	Kind string
	// Next returns the first occurrence strictly after t.
	Next func(t time.Time) Occurrence
}

// Provider lists recurring schedules. It is called on every tick, so schedules that depend on
// data (one per kid) pick up changes automatically.
type Provider func(ctx context.Context) ([]Recurring, error)

// Scheduler enqueues recurring jobs. Each schedule has a cursor holding the last occurrence it
// enqueued; a tick enqueues every occurrence after the cursor that is due, so runs missed while
// the server was down are caught up on the next tick. A new schedule starts from now, without
// backfilling the past.
type Scheduler struct {
	q         *sqlc.Queries
	runner    *Runner
	clock     clock.Clock
	log       *slog.Logger
	providers []Provider
}

func NewScheduler(q *sqlc.Queries, r *Runner, c clock.Clock, log *slog.Logger) *Scheduler {
	return &Scheduler{q: q, runner: r, clock: c, log: log}
}

// Add registers a provider.
func (s *Scheduler) Add(p Provider) { s.providers = append(s.providers, p) }

// Tick enqueues every due occurrence and returns how many jobs it created.
func (s *Scheduler) Tick(ctx context.Context) (int, error) {
	now := s.clock.Now()
	created := 0
	for _, p := range s.providers {
		schedules, err := p(ctx)
		if err != nil {
			return created, fmt.Errorf("jobs: schedule provider: %w", err)
		}
		for _, rec := range schedules {
			n, err := s.tickOne(ctx, rec, now)
			created += n
			if err != nil {
				return created, err
			}
		}
	}
	return created, nil
}

func (s *Scheduler) tickOne(ctx context.Context, rec Recurring, now time.Time) (int, error) {
	raw, err := s.q.GetScheduleCursor(ctx, rec.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, s.setCursor(ctx, rec.Name, now)
	}
	if err != nil {
		return 0, fmt.Errorf("jobs: cursor %s: %w", rec.Name, err)
	}
	last, err := db.ParseTimestamp(raw)
	if err != nil {
		return 0, fmt.Errorf("jobs: cursor %s: %w", rec.Name, err)
	}

	created := 0
	for range maxCatchUp {
		occ := rec.Next(last)
		if occ.At.After(now) {
			break
		}
		// Enqueue before moving the cursor: a crash in between re-enqueues the same key on the
		// next tick, which is a no-op, rather than losing the occurrence.
		_, ok, err := s.runner.Enqueue(ctx, rec.Kind, occ.Payload, EnqueueOptions{
			Key: rec.Name + ":" + occ.Key, RunAt: occ.At,
		})
		if err != nil {
			return created, err
		}
		if ok {
			created++
		}
		if err := s.setCursor(ctx, rec.Name, occ.At); err != nil {
			return created, err
		}
		last = occ.At
	}
	return created, nil
}

func (s *Scheduler) setCursor(ctx context.Context, name string, t time.Time) error {
	return s.q.SetScheduleCursor(ctx, sqlc.SetScheduleCursorParams{Name: name, LastFiredAt: db.Timestamp(t)})
}

// Start ticks every interval until ctx is cancelled.
func (s *Scheduler) Start(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if _, err := s.Tick(ctx); err != nil && ctx.Err() == nil {
			s.log.Error("scheduler tick", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// --- schedule factories --------------------------------------------------------------------

// Every returns a provider for a fixed-interval schedule (e.g. daily maintenance).
func Every(name, kindName string, interval time.Duration) Provider {
	rec := Recurring{Name: name, Kind: kindName, Next: func(t time.Time) Occurrence {
		at := t.Add(interval)
		return Occurrence{At: at, Key: db.Timestamp(at)}
	}}
	return func(context.Context) ([]Recurring, error) { return []Recurring{rec}, nil }
}

// Kid is the scheduling view of a kid: who, and their day-end override (nil = household's).
type Kid struct {
	ID     int64
	DayEnd *caltime.TimeOfDay
}

// KidDayPayload is the payload of per-kid day-close and week-close jobs.
type KidDayPayload struct {
	KidID int64        `json:"kid_id"`
	Date  caltime.Date `json:"date"` // the day (or last day of the week) that closed
}

// KidDayCloses returns a provider with one schedule per kid that fires at each of the kid's
// day closes (their effective day end in the household timezone).
func KidDayCloses(kindName string, household func(context.Context) (caltime.Household, error), kids func(context.Context) ([]Kid, error)) Provider {
	return kidSchedules("day_close", kindName, household, kids, func(h caltime.Household, dayEnd caltime.TimeOfDay, t time.Time) (caltime.Date, time.Time) {
		return h.NextDayClose(t, dayEnd)
	})
}

// KidWeekCloses returns a provider with one schedule per kid that fires when each week closes
// for that kid (the day close of the last day of the week).
func KidWeekCloses(kindName string, household func(context.Context) (caltime.Household, error), kids func(context.Context) ([]Kid, error)) Provider {
	return kidSchedules("week_close", kindName, household, kids, func(h caltime.Household, dayEnd caltime.TimeOfDay, t time.Time) (caltime.Date, time.Time) {
		d, _ := h.NextDayClose(t, dayEnd)
		last := h.WeekStartOf(d).AddDays(6)
		return last, h.DayClose(last, dayEnd)
	})
}

func kidSchedules(prefix, kindName string,
	household func(context.Context) (caltime.Household, error),
	kids func(context.Context) ([]Kid, error),
	next func(caltime.Household, caltime.TimeOfDay, time.Time) (caltime.Date, time.Time),
) Provider {
	return func(ctx context.Context) ([]Recurring, error) {
		h, err := household(ctx)
		if err != nil {
			return nil, err
		}
		ks, err := kids(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]Recurring, 0, len(ks))
		for _, k := range ks {
			dayEnd, err := h.EffectiveDayEnd(k.DayEnd)
			if err != nil {
				return nil, fmt.Errorf("kid %d: %w", k.ID, err)
			}
			kidID := k.ID
			out = append(out, Recurring{
				// The kind is in the name, so two features scheduling the same kid's closes keep
				// their own cursors and job keys.
				Name: fmt.Sprintf("%s:%s:kid:%d", prefix, kindName, kidID),
				Kind: kindName,
				Next: func(t time.Time) Occurrence {
					d, at := next(h, dayEnd, t)
					return Occurrence{At: at, Key: closeKey(d, at), Payload: KidDayPayload{KidID: kidID, Date: d}}
				},
			})
		}
		return out, nil
	}
}

// HouseholdDayPayload is the payload of household day-end jobs.
type HouseholdDayPayload struct {
	Date caltime.Date `json:"date"`
}

// HouseholdDayCloses returns a provider that fires at each household day end (e.g. horizon
// generation and the daily summary, after every kid's day has closed).
func HouseholdDayCloses(name, kindName string, household func(context.Context) (caltime.Household, error)) Provider {
	return func(ctx context.Context) ([]Recurring, error) {
		h, err := household(ctx)
		if err != nil {
			return nil, err
		}
		return []Recurring{{Name: name, Kind: kindName, Next: func(t time.Time) Occurrence {
			d, at := h.NextDayClose(t, h.DayEnd)
			return Occurrence{At: at, Key: closeKey(d, at), Payload: HouseholdDayPayload{Date: d}}
		}}}, nil
	}
}

// closeKey identifies a close by its day and instant: a day end moved later after the day's
// close already fired gives a new occurrence on the same date, which must still run.
func closeKey(d caltime.Date, at time.Time) string { return d.String() + "@" + db.Timestamp(at) }
