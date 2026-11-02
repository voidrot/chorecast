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
//     A close whose instant is still ahead (the day end moved later after the job was queued)
//     does nothing; the moved close is its own occurrence.
//   - Each instance is closed in its own transaction (MissInTx): an atomic open → missed update,
//     the penalties and an InstanceMissed event. A re-run or a duplicate job finds nothing open,
//     and a penalty that was already posted counts as done.
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

// InstanceMissed is emitted for each instance marked missed.
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

	// AfterKidDay and AfterShared, when set, run in the kid-day and shared close jobs after the
	// close, so work on the closed day (streaks) sees its misses. They also run when the close
	// did nothing. An error fails the job, which retries; the close itself is idempotent.
	AfterKidDay func(ctx context.Context, kidID int64, day caltime.Date) error
	AfterShared func(ctx context.Context, day caltime.Date) error
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
	return c.close(ctx, kidID, closeInstant(cal, day, flexible, KidDayEnd(cal, kid.DayEndOverride)), flexible)
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
		res, err := c.close(ctx, 0, at, flexible)
		total.Missed += res.Missed
		total.Penalties += res.Penalties
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// close marks missed every open instance of kid (0 = shared) due by at. A close still ahead does
// nothing: closing early would penalize a kid whose day is still open, and could touch an
// instance the horizon may still regenerate.
func (c *Closer) close(ctx context.Context, kid int64, at time.Time, flexible bool) (Result, error) {
	if at.After(c.clock.Now()) {
		return Result{}, nil
	}
	ids, err := c.db.ListDueOpenChoreInstanceIDs(ctx, sqlc.ListDueOpenChoreInstanceIDsParams{
		DueBy: db.Timestamp(at), Kid: kid, Flexible: b2i(flexible),
	})
	if err != nil {
		return Result{}, fmt.Errorf("list due instances: %w", err)
	}
	var total Result
	for _, id := range ids {
		var res Result
		err := c.bus.InTx(ctx, func(tx *events.Tx) error {
			var err error
			res, err = MissInTx(ctx, tx, c.clock, id)
			return err
		})
		if err != nil {
			return total, fmt.Errorf("close instance %d: %w", id, err)
		}
		total.Missed += res.Missed
		total.Penalties += res.Penalties
	}
	return total, nil
}

// MissInTx moves an open instance to missed inside the caller's transaction, posts its chore's
// penalty and emits InstanceMissed. An instance that isn't open is left alone and gives a zero
// Result. The assignee, members and penalty are read in the transaction, so they're current.
//
// The penalty goes to the assignee, or for a shared instance to every member when the chore's
// shared_penalty is 'all' and to nobody when it's 'none'; a penalty of 0 posts nothing. It's
// dated the instance's day, or for a flexible instance the last day of its week.
//
// ErrAlreadyPosted counts as done: it means the instance's penalty was posted before (the
// instance was missed, then reopened), and it stands because no transition yet reopens an
// instance whose penalty was reversed (excused is final). A transition that does (excused →
// open, or reopening after a parent reversed the penalty entry) must make the miss re-apply the
// penalty with ledger.Tip and Reverse when inEffect is false; otherwise the instance ends up
// missed with no penalty in effect.
func MissInTx(ctx context.Context, tx *events.Tx, clk clock.Clock, instanceID int64) (Result, error) {
	inst, err := tx.Q.MarkChoreInstanceMissed(ctx, sqlc.MarkChoreInstanceMissedParams{
		UpdatedAt: db.Timestamp(clk.Now()), ID: instanceID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Result{}, nil // not open: someone else moved it first
	}
	if err != nil {
		return Result{}, err
	}
	res := Result{Missed: 1}
	kids := []int64{inst.KidID.Int64}
	if !inst.KidID.Valid {
		if kids, err = tx.Q.ListChoreInstanceMembers(ctx, instanceID); err != nil {
			return Result{}, err
		}
	}
	chore, err := tx.Q.GetChorePenalty(ctx, inst.ChoreID)
	if err != nil {
		return Result{}, fmt.Errorf("load chore %d: %w", inst.ChoreID, err)
	}
	if chore.Penalty > 0 && (inst.KidID.Valid || chore.SharedPenalty == assign.PenaltyAll) {
		date, err := caltime.ParseDate(inst.OccurrenceDate)
		if err != nil {
			return Result{}, err
		}
		if inst.Flexible == 1 {
			cal, err := household.Calendar(ctx, tx.Q)
			if err != nil {
				return Result{}, err
			}
			date = cal.WeekStartOf(date).AddDays(6) // a flexible miss belongs to the day its week closed
		}
		for _, k := range kids {
			_, err := ledger.Post(ctx, tx, clk, ledger.Posting{
				KidID: k, Kind: ledger.KindChorePenalty, Amount: -chore.Penalty,
				Source:    ledger.Source{Kind: ledger.SourceChoreInstance, ID: instanceID},
				LocalDate: date,
			})
			switch {
			case errors.Is(err, ledger.ErrAlreadyPosted):
			case err != nil:
				return Result{}, fmt.Errorf("penalty for kid %d: %w", k, err)
			default:
				res.Penalties++
			}
		}
	}
	if err := tx.Emit(ctx, InstanceMissed{
		InstanceID: instanceID, KidIDs: kids, From: horizon.StatusOpen, To: horizon.StatusMissed,
	}); err != nil {
		return Result{}, err
	}
	return res, nil
}

// ClosesAt is when a close takes the instance: the close of its day (its kid's effective day end,
// or the household's for a shared instance), or of its week when flexible. An instance due after
// that (a slot later than an early day end) is taken by the first close at or after its due time.
// Pass tx.Q inside a transaction.
func ClosesAt(ctx context.Context, q *sqlc.Queries, inst sqlc.ChoreInstance) (time.Time, error) {
	cal, err := household.Calendar(ctx, q)
	if err != nil {
		return time.Time{}, err
	}
	date, err := caltime.ParseDate(inst.OccurrenceDate)
	if err != nil {
		return time.Time{}, err
	}
	due, err := db.ParseTimestamp(inst.DueAt)
	if err != nil {
		return time.Time{}, err
	}
	end := cal.DayEnd
	if inst.KidID.Valid {
		kid, err := q.GetKidByID(ctx, inst.KidID.Int64)
		if err != nil {
			return time.Time{}, fmt.Errorf("load kid %d: %w", inst.KidID.Int64, err)
		}
		end = KidDayEnd(cal, kid.DayEndOverride)
	}
	flexible := inst.Flexible == 1
	at := closeInstant(cal, date, flexible, end)
	if !flexible && due.After(at) { // flexible instances are due at their week close
		_, at = cal.NextDayClose(due.Add(-time.Nanosecond), end) // the first close at or after due
	}
	return at, nil
}

// closeInstant is when day closes for dayEnd, or its week when flexible.
func closeInstant(cal caltime.Household, day caltime.Date, flexible bool, dayEnd caltime.TimeOfDay) time.Time {
	if flexible {
		return cal.WeekClose(day, dayEnd)
	}
	return cal.DayClose(day, dayEnd)
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
		res, err := c.CloseKidDay(ctx, p.KidID, p.Date)
		if err == nil && c.AfterKidDay != nil {
			err = c.AfterKidDay(ctx, p.KidID, p.Date)
		}
		return res, err
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
		res, err := c.CloseShared(ctx, p.Date)
		if err == nil && c.AfterShared != nil {
			err = c.AfterShared(ctx, p.Date)
		}
		return res, err
	}), jobs.KindOptions{})

	cal := func(ctx context.Context) (caltime.Household, error) { return household.Calendar(ctx, c.db.Queries) }
	kids := func(ctx context.Context) ([]jobs.Kid, error) { return Kids(ctx, c.db.Queries) }
	for _, p := range []jobs.Provider{
		jobs.KidDayCloses(KindKidDay, cal, kids),
		jobs.KidWeekCloses(KindKidWeek, cal, kids),
		jobs.HouseholdDayCloses("day_close:"+KindShared, KindShared, cal),
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
			end := KidDayEnd(cal, u.DayEndOverride)
			k.DayEnd = &end
		}
		out = append(out, k)
	}
	return out, nil
}

// KidDayEnd is a kid's effective day end. An unreadable override, or one after the household's,
// falls back to the household's, as in the horizon.
func KidDayEnd(cal caltime.Household, override sql.NullString) caltime.TimeOfDay {
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
