// Package dayclose closes days and weeks: open chore instances whose day (or flexible week) has
// closed become missed, and their chore's penalty is posted to the ledger. See
// docs/features/points.md (Chore instance lifecycle).
//
//   - A kid's day closes at their effective day end (KindKidDay), and their week at the day close
//     of the week's last day (KindKidWeek). Shared instances close at the household day end
//     (KindShared), which every member's day end is at or before.
//   - A close at instant T takes every open instance due by T, so one run also catches up on
//     earlier days that never closed (the server was down, a job failed). An instance due after
//     its kid's day end (a slot later than an early day-end override) waits for the next close.
//   - Each instance is closed in its own transaction: an atomic open → missed update, the
//     penalties and an InstanceMissed event. A re-run or a duplicate job finds nothing open, and a
//     penalty that was already posted counts as done.
//
// Close and horizon regeneration don't overlap: a close only touches instances due by now, and
// the horizon freezes every instance whose due time has passed.
package dayclose

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/voidrot/chorecast/server/internal/assign"
	"github.com/voidrot/chorecast/server/internal/caltime"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
	"github.com/voidrot/chorecast/server/internal/events"
	"github.com/voidrot/chorecast/server/internal/horizon"
	"github.com/voidrot/chorecast/server/internal/household"
	"github.com/voidrot/chorecast/server/internal/jobs"
	"github.com/voidrot/chorecast/server/internal/ledger"
)

// Job kinds. The kid kinds take a jobs.KidDayPayload, the shared kind a jobs.HouseholdDayPayload.
const (
	KindKidDay  = "dayclose.kid_day"
	KindKidWeek = "dayclose.kid_week"
	KindShared  = "dayclose.shared"
)

// InstanceMissed is emitted for each instance a close marks missed.
type InstanceMissed struct {
	InstanceID int64   `json:"instanceId"`
	KidIDs     []int64 `json:"kidIds"` // the assignee, or a shared instance's members
	From       string  `json:"from"`
	To         string  `json:"to"`
}

func (InstanceMissed) EventType() string { return "instance.missed" }

// Result counts what a close changed.
type Result struct {
	Missed    int // instances marked missed
	Penalties int // penalty entries posted
}

// Closer closes days and weeks.
type Closer struct {
	db    *db.DB
	bus   *events.Bus
	clock clock.Clock
}

func New(d *db.DB, bus *events.Bus, c clock.Clock) *Closer {
	return &Closer{db: d, bus: bus, clock: c}
}

// CloseKidDay closes the kid's non-flexible instances due by the close of day.
func (c *Closer) CloseKidDay(ctx context.Context, kidID int64, day caltime.Date) (Result, error) {
	return c.closeKid(ctx, kidID, day, false)
}

// CloseKidWeek closes the kid's flexible instances due by the close of the week containing day.
func (c *Closer) CloseKidWeek(ctx context.Context, kidID int64, day caltime.Date) (Result, error) {
	return c.closeKid(ctx, kidID, day, true)
}

func (c *Closer) closeKid(ctx context.Context, kidID int64, day caltime.Date, flexible bool) (Result, error) {
	cal, err := household.Calendar(ctx, c.db.Queries)
	if err != nil {
		return Result{}, err
	}
	kid, err := c.db.GetKidByID(ctx, kidID)
	if errors.Is(err, sql.ErrNoRows) {
		return Result{}, nil // deleted: their instances went with them
	}
	if err != nil {
		return Result{}, fmt.Errorf("load kid %d: %w", kidID, err)
	}
	end := dayEnd(cal, kid.DayEndOverride)
	at := cal.DayClose(day, end)
	if flexible {
		at = cal.WeekClose(day, end)
	}
	return c.close(ctx, cal, kidID, at, flexible)
}

// CloseShared closes the shared instances, flexible ones included, due by the household close
// of day.
func (c *Closer) CloseShared(ctx context.Context, day caltime.Date) (Result, error) {
	cal, err := household.Calendar(ctx, c.db.Queries)
	if err != nil {
		return Result{}, err
	}
	at := cal.DayClose(day, cal.DayEnd)
	var total Result
	for _, flexible := range []bool{false, true} {
		res, err := c.close(ctx, cal, 0, at, flexible)
		total.Missed += res.Missed
		total.Penalties += res.Penalties
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// close marks missed every open instance of kid (0 = shared) due by at. at is capped at now, so
// a close never touches an instance the horizon may still regenerate.
func (c *Closer) close(ctx context.Context, cal caltime.Household, kid int64, at time.Time, flexible bool) (Result, error) {
	now := c.clock.Now()
	if at.After(now) {
		at = now
	}
	due, err := c.db.ListDueOpenChoreInstances(ctx, sqlc.ListDueOpenChoreInstancesParams{
		DueBy: db.Timestamp(at), Kid: kid, Flexible: b2i(flexible),
	})
	if err != nil {
		return Result{}, fmt.Errorf("list due instances: %w", err)
	}
	var total Result
	for _, inst := range due {
		res, err := c.miss(ctx, cal, inst)
		if err != nil {
			return total, fmt.Errorf("close instance %d: %w", inst.ID, err)
		}
		total.Missed += res.Missed
		total.Penalties += res.Penalties
	}
	return total, nil
}

// miss marks one instance missed and posts its penalties, unless it's no longer open.
func (c *Closer) miss(ctx context.Context, cal caltime.Household, inst sqlc.ListDueOpenChoreInstancesRow) (Result, error) {
	date, err := caltime.ParseDate(inst.OccurrenceDate)
	if err != nil {
		return Result{}, err
	}
	if inst.Flexible == 1 {
		date = cal.WeekStartOf(date).AddDays(6) // a flexible miss belongs to the day its week closed
	}
	var res Result
	err = c.bus.InTx(ctx, func(tx *events.Tx) error {
		res = Result{}
		n, err := tx.Q.MarkChoreInstanceMissed(ctx, sqlc.MarkChoreInstanceMissedParams{
			UpdatedAt: db.Timestamp(c.clock.Now()), ID: inst.ID,
		})
		if err != nil || n == 0 {
			return err // n == 0: someone else moved it first
		}
		res.Missed = 1
		kids := []int64{inst.KidID.Int64}
		if !inst.KidID.Valid {
			if kids, err = tx.Q.ListChoreInstanceMembers(ctx, inst.ID); err != nil {
				return err
			}
		}
		if inst.Penalty > 0 && (inst.KidID.Valid || inst.SharedPenalty == assign.PenaltyAll) {
			for _, k := range kids {
				_, err := ledger.Post(ctx, tx, c.clock, ledger.Posting{
					KidID: k, Kind: ledger.KindChorePenalty, Amount: -inst.Penalty,
					Source:    ledger.Source{Kind: ledger.SourceChoreInstance, ID: inst.ID},
					LocalDate: date,
				})
				switch {
				case errors.Is(err, ledger.ErrAlreadyPosted):
				case err != nil:
					return fmt.Errorf("penalty for kid %d: %w", k, err)
				default:
					res.Penalties++
				}
			}
		}
		return tx.Emit(ctx, InstanceMissed{
			InstanceID: inst.ID, KidIDs: kids, From: horizon.StatusOpen, To: horizon.StatusMissed,
		})
	})
	if err != nil {
		return Result{}, err
	}
	return res, nil
}

// Register adds the close job kinds to runner and their schedules to sched: each kid's day and
// week closes, and the household day end for shared instances.
func (c *Closer) Register(runner *jobs.Runner, sched *jobs.Scheduler, log *slog.Logger) {
	handle := func(name string, run func(context.Context, jobs.Job) (Result, error)) jobs.Handler {
		return func(ctx context.Context, j jobs.Job) error {
			res, err := run(ctx, j)
			if err != nil {
				return err
			}
			log.Info(name, "job", j.ID, "missed", res.Missed, "penalties", res.Penalties)
			return nil
		}
	}
	runner.Register(KindKidDay, handle("day close", func(ctx context.Context, j jobs.Job) (Result, error) {
		p, err := jobs.Decode[jobs.KidDayPayload](j)
		if err != nil {
			return Result{}, err
		}
		return c.CloseKidDay(ctx, p.KidID, p.Date)
	}), jobs.KindOptions{})
	runner.Register(KindKidWeek, handle("week close", func(ctx context.Context, j jobs.Job) (Result, error) {
		p, err := jobs.Decode[jobs.KidDayPayload](j)
		if err != nil {
			return Result{}, err
		}
		return c.CloseKidWeek(ctx, p.KidID, p.Date)
	}), jobs.KindOptions{})
	runner.Register(KindShared, handle("shared close", func(ctx context.Context, j jobs.Job) (Result, error) {
		p, err := jobs.Decode[jobs.HouseholdDayPayload](j)
		if err != nil {
			return Result{}, err
		}
		return c.CloseShared(ctx, p.Date)
	}), jobs.KindOptions{})

	cal := func(ctx context.Context) (caltime.Household, error) { return household.Calendar(ctx, c.db.Queries) }
	kids := func(ctx context.Context) ([]jobs.Kid, error) { return Kids(ctx, c.db.Queries) }
	for _, p := range []jobs.Provider{
		jobs.KidDayCloses(KindKidDay, cal, kids),
		jobs.KidWeekCloses(KindKidWeek, cal, kids),
		jobs.HouseholdDayCloses("day_close:shared", KindShared, cal),
	} {
		sched.Add(func(ctx context.Context) ([]jobs.Recurring, error) {
			if _, err := c.db.GetHousehold(ctx); errors.Is(err, sql.ErrNoRows) {
				return nil, nil // before first-run setup there's no calendar yet
			}
			return p(ctx)
		})
	}
}

// Kids lists every kid with their day-end override, for the scheduler's per-kid closes. Archived
// kids are included: their remaining instances still close.
func Kids(ctx context.Context, q *sqlc.Queries) ([]jobs.Kid, error) {
	cal, err := household.Calendar(ctx, q)
	if err != nil {
		return nil, err
	}
	users, err := q.ListKids(ctx, true)
	if err != nil {
		return nil, err
	}
	out := make([]jobs.Kid, 0, len(users))
	for _, u := range users {
		k := jobs.Kid{ID: u.ID}
		if u.DayEndOverride.Valid {
			end := dayEnd(cal, u.DayEndOverride)
			k.DayEnd = &end
		}
		out = append(out, k)
	}
	return out, nil
}

// dayEnd is a kid's effective day end. An unreadable override, or one after the household's,
// falls back to the household's, as in the horizon.
func dayEnd(cal caltime.Household, override sql.NullString) caltime.TimeOfDay {
	if !override.Valid {
		return cal.DayEnd
	}
	o, err := caltime.ParseTimeOfDay(override.String)
	if err != nil {
		return cal.DayEnd
	}
	end, err := cal.EffectiveDayEnd(&o)
	if err != nil {
		return cal.DayEnd
	}
	return end
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
