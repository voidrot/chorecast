// Package streaks keeps each kid's daily streak and per-chore streaks, and posts the household's
// streak milestone bonuses. See docs/features/points.md (Streaks).
//
//   - A day is evaluated after the kid's day close and again after the household day end, once
//     shared instances and slots later than an early day end are due. It's counted when the kid
//     had at least one required instance that day (their own or a shared one they're a member
//     of; flexible ones belong to their week) and every one is approved, submitted or excused;
//     broken when one is still open or missed; none when there were none, which neither breaks
//     nor extends the streak.
//   - A counted day is final, so a later rejection doesn't break it. A broken or none day is
//     re-checked at each evaluation for RecheckDays, so a chore finished after an early day end,
//     or a miss a parent excuses, repairs it.
//   - Each evaluation recomputes the streak from the stored days and posts a streak_bonus entry
//     for every counted day on which the run reaches a milestone, unless the run was already paid
//     that milestone on another of its days (a repair joined two runs). Bonuses are never
//     reversed. Evaluating a day again, or out of order, changes nothing new.
//   - Per-chore streaks are display only, recomputed from the kid's instance history each time.
package streaks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/voidrot/chorecast/server/internal/caltime"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/dayclose"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
	"github.com/voidrot/chorecast/server/internal/events"
	"github.com/voidrot/chorecast/server/internal/horizon"
	"github.com/voidrot/chorecast/server/internal/household"
	"github.com/voidrot/chorecast/server/internal/ledger"
)

// RecheckDays is how many days, up to the one being evaluated, a day that didn't count is
// re-checked for: a late completion or an excuse within a week repairs the streak.
const RecheckDays = 7

// SourceMilestone is the ledger source of a streak bonus. Its id is the milestone day as
// YYYYMMDD times 10000 plus the milestone's days (2026-11-04, 7 days: 202611040007), so each
// (day, milestone) posts at most once, and a repair that lengthens a run can still post a
// longer milestone on a day that already has a shorter one.
const SourceMilestone = "streak_milestone"

// Day outcomes, as stored in streak_days.
const (
	outcomeCounted = "counted"
	outcomeBroken  = "broken"
	outcomeNone    = "none"
)

// KidStreaks are a kid's streaks as the API reads them.
type KidStreaks struct {
	Daily  DailyStreak   `json:"daily"`
	Chores []ChoreStreak `json:"chores" doc:"Recurring chores the kid has had, by chore id"`
}

// DailyStreak is the kid's run of days on which every required chore was done.
type DailyStreak struct {
	Current int          `json:"current" doc:"Counted days in a row up to the last evaluated day; days without required chores are skipped"`
	Best    int          `json:"best"`
	LastDay caltime.Date `json:"lastDay,omitzero" format:"date" doc:"The latest closed day evaluated; absent before the first"`
}

// ChoreStreak is the kid's run of done occurrences of one recurring chore (display only).
type ChoreStreak struct {
	ChoreID int64 `json:"choreId"`
	Current int   `json:"current" doc:"The kid's latest occurrences in a row that were approved, submitted or excused"`
	Best    int   `json:"best"`
}

// Service evaluates and reads streaks.
type Service struct {
	db    *db.DB
	bus   *events.Bus
	clock clock.Clock
}

func New(d *db.DB, bus *events.Bus, c clock.Clock) *Service {
	return &Service{db: d, bus: bus, clock: c}
}

// EvaluateKidDay evaluates the kid's day, which must have closed (otherwise it does nothing),
// re-checks the earlier days that didn't count, and updates the streaks and bonuses, all in one
// transaction. It's the dayclose.Closer AfterKidDay hook.
func (s *Service) EvaluateKidDay(ctx context.Context, kidID int64, day caltime.Date) error {
	return s.bus.InTx(ctx, func(tx *events.Tx) error { return evaluate(ctx, tx, s.clock, kidID, day) })
}

// EvaluateDay evaluates the day for every kid, archived ones included (they have no
// instances, so their days are none). It's the dayclose.Closer AfterShared hook: by the
// household day end every instance of the day is due, shared ones included. One kid's failure
// doesn't stop the others; the errors are joined.
func (s *Service) EvaluateDay(ctx context.Context, day caltime.Date) error {
	kids, err := s.db.ListKids(ctx, true)
	if err != nil {
		return fmt.Errorf("list kids: %w", err)
	}
	var errs []error
	for _, k := range kids {
		if err := s.EvaluateKidDay(ctx, k.ID, day); err != nil {
			errs = append(errs, fmt.Errorf("kid %d: %w", k.ID, err))
		}
	}
	return errors.Join(errs...)
}

func evaluate(ctx context.Context, tx *events.Tx, clk clock.Clock, kidID int64, day caltime.Date) error {
	q := tx.Q
	cal, err := household.Calendar(ctx, q)
	if err != nil {
		return err
	}
	kid, err := q.GetKidByID(ctx, kidID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // deleted: their streaks went with them
	}
	if err != nil {
		return fmt.Errorf("load kid %d: %w", kidID, err)
	}
	if cal.DayClose(day, dayclose.KidDayEnd(cal, kid.DayEndOverride)).After(clk.Now()) {
		return nil // the day is still open (the day end moved later after the job was queued)
	}

	// Evaluate from the recheck window's start, or the day after the last evaluated one if
	// that's earlier (catching up after downtime), through day.
	from, lastDay := day.AddDays(-(RecheckDays - 1)), day
	state, err := q.GetKidStreak(ctx, kidID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return fmt.Errorf("load streak: %w", err)
	default:
		last, err := caltime.ParseDate(state.LastDay)
		if err != nil {
			return err
		}
		if next := last.AddDays(1); next.Before(from) {
			from = next
		}
		if last.After(lastDay) {
			lastDay = last // an older day evaluated late: the streak still runs to the latest
		}
	}
	if err := evaluateDays(ctx, q, kidID, from, day); err != nil {
		return err
	}

	h, err := q.GetHousehold(ctx)
	if err != nil {
		return fmt.Errorf("load household: %w", err)
	}
	milestones, err := household.Milestones(h)
	if err != nil {
		return err
	}
	bonus := make(map[int]int, len(milestones))
	for _, m := range milestones {
		bonus[m.Days] = m.Points
	}
	days, err := q.ListStreakDays(ctx, sqlc.ListStreakDaysParams{KidID: kidID})
	if err != nil {
		return fmt.Errorf("list streak days: %w", err)
	}
	// Walk the days into runs: each counted day's length so far and its run's first day, and
	// each run's last counted day.
	type countedDay struct {
		date, start caltime.Date
		run         int
	}
	var counted []countedDay
	ends := map[caltime.Date]caltime.Date{} // run start -> last counted day
	var run, best int
	var start, prev caltime.Date
	for _, r := range days {
		d, err := caltime.ParseDate(r.Day)
		if err != nil {
			return err
		}
		if !prev.IsZero() && d != prev.AddDays(1) {
			run = 0 // a day never evaluated breaks the run
		}
		prev = d
		switch r.Outcome {
		case outcomeBroken:
			run = 0
		case outcomeCounted:
			if run == 0 {
				start = d
			}
			run++
			best = max(best, run)
			counted = append(counted, countedDay{date: d, start: start, run: run})
			ends[start] = d
		}
	}
	for _, c := range counted {
		// Only runs through the days just evaluated can have changed.
		points, ok := bonus[c.run]
		if !ok || c.date.Before(from) {
			continue
		}
		// A run pays each milestone once: when a repair joins two runs, the later part may
		// already have been paid this milestone on another day.
		paid, err := q.StreakMilestonePaid(ctx, sqlc.StreakMilestonePaidParams{
			KidID: kidID, FirstID: MilestoneSource(c.start, c.run).ID, LastID: MilestoneSource(ends[c.start], c.run).ID,
			Days: int64(c.run),
		})
		if err != nil {
			return fmt.Errorf("streak bonus paid: %w", err)
		}
		if paid {
			continue
		}
		if err := postBonus(ctx, tx, clk, kidID, c.date, c.run, points); err != nil {
			return err
		}
	}
	if err := q.UpsertKidStreak(ctx, sqlc.UpsertKidStreakParams{
		KidID: kidID, Current: int64(run), Best: int64(best), LastDay: lastDay.String(),
	}); err != nil {
		return fmt.Errorf("save streak: %w", err)
	}
	return choreStreaks(ctx, q, kidID, lastDay)
}

// evaluateDays stores the outcome of each day in [from, to] that hasn't counted yet.
func evaluateDays(ctx context.Context, q *sqlc.Queries, kidID int64, from, to caltime.Date) error {
	stored, err := q.ListStreakDays(ctx, sqlc.ListStreakDaysParams{KidID: kidID, Day: from.String()})
	if err != nil {
		return fmt.Errorf("list streak days: %w", err)
	}
	outcomes := make(map[string]string, len(stored))
	for _, r := range stored {
		outcomes[r.Day] = r.Outcome
	}
	tallies, err := q.TallyStreakDays(ctx, sqlc.TallyStreakDaysParams{FromDay: from.String(), ToDay: to.String(), Kid: kidID})
	if err != nil {
		return fmt.Errorf("tally days: %w", err)
	}
	done := make(map[string]bool, len(tallies))
	for _, t := range tallies {
		done[t.OccurrenceDate] = t.Done == t.Total
	}
	for d := from; !d.After(to); d = d.AddDays(1) {
		key := d.String()
		if outcomes[key] == outcomeCounted {
			continue
		}
		outcome := outcomeNone
		if allDone, ok := done[key]; ok {
			outcome = outcomeBroken
			if allDone {
				outcome = outcomeCounted
			}
		}
		if outcomes[key] == outcome {
			continue
		}
		if err := q.UpsertStreakDay(ctx, sqlc.UpsertStreakDayParams{KidID: kidID, Day: key, Outcome: outcome}); err != nil {
			return fmt.Errorf("save streak day %s: %w", key, err)
		}
	}
	return nil
}

// postBonus posts the milestone's bonus for the day; one already posted counts as done.
func postBonus(ctx context.Context, tx *events.Tx, clk clock.Clock, kidID int64, d caltime.Date, days, points int) error {
	_, err := ledger.Post(ctx, tx, clk, ledger.Posting{
		KidID: kidID, Kind: ledger.KindStreakBonus, Amount: int64(points),
		Source:    MilestoneSource(d, days),
		LocalDate: d,
		Note:      fmt.Sprintf("%d-day streak", days),
	})
	if err != nil && !errors.Is(err, ledger.ErrAlreadyPosted) {
		return fmt.Errorf("streak bonus %s (%d days): %w", d, days, err)
	}
	return nil
}

// MilestoneSource is the ledger source of the bonus for reaching a days-long streak on d.
func MilestoneSource(d caltime.Date, days int) ledger.Source {
	ymd := int64(d.Year)*10000 + int64(d.Month)*100 + int64(d.Day)
	return ledger.Source{Kind: SourceMilestone, ID: ymd*10000 + int64(days)}
}

// choreStreaks recomputes the kid's per-chore streaks from their instances dated up to the day.
// An instance still open counts as not done, like a missed one.
func choreStreaks(ctx context.Context, q *sqlc.Queries, kidID int64, to caltime.Date) error {
	history, err := q.ListChoreStreakHistory(ctx, sqlc.ListChoreStreakHistoryParams{ToDay: to.String(), Kid: kidID})
	if err != nil {
		return fmt.Errorf("chore history: %w", err)
	}
	if err := q.DeleteKidChoreStreaks(ctx, kidID); err != nil {
		return err
	}
	for i := 0; i < len(history); {
		chore := history[i].ChoreID
		var run, best int64
		for ; i < len(history) && history[i].ChoreID == chore; i++ {
			if done(history[i].Status) {
				run++
				best = max(best, run)
			} else {
				run = 0
			}
		}
		if err := q.InsertKidChoreStreak(ctx, sqlc.InsertKidChoreStreakParams{
			KidID: kidID, ChoreID: chore, Current: run, Best: best,
		}); err != nil {
			return fmt.Errorf("save chore %d streak: %w", chore, err)
		}
	}
	return nil
}

func done(status string) bool {
	return status == horizon.StatusApproved || status == horizon.StatusSubmitted || status == horizon.StatusExcused
}

// Get returns the kid's streaks as of their last evaluated day; zeros before the first.
func (s *Service) Get(ctx context.Context, kidID int64) (KidStreaks, error) {
	out := KidStreaks{Chores: []ChoreStreak{}}
	state, err := s.db.GetKidStreak(ctx, kidID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return KidStreaks{}, fmt.Errorf("load streak: %w", err)
	default:
		last, err := caltime.ParseDate(state.LastDay)
		if err != nil {
			return KidStreaks{}, err
		}
		out.Daily = DailyStreak{Current: int(state.Current), Best: int(state.Best), LastDay: last}
	}
	rows, err := s.db.ListKidChoreStreaks(ctx, kidID)
	if err != nil {
		return KidStreaks{}, fmt.Errorf("list chore streaks: %w", err)
	}
	for _, r := range rows {
		out.Chores = append(out.Chores, ChoreStreak{ChoreID: r.ChoreID, Current: int(r.Current), Best: int(r.Best)})
	}
	return out, nil
}
