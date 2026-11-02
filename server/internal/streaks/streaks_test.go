package streaks_test

import (
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/voidrot/chorecast/server/internal/assign"
	"github.com/voidrot/chorecast/server/internal/caltime"
	"github.com/voidrot/chorecast/server/internal/chores"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/dayclose"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
	"github.com/voidrot/chorecast/server/internal/events"
	"github.com/voidrot/chorecast/server/internal/horizon"
	"github.com/voidrot/chorecast/server/internal/jobs"
	"github.com/voidrot/chorecast/server/internal/kids"
	"github.com/voidrot/chorecast/server/internal/schedule"
	"github.com/voidrot/chorecast/server/internal/streaks"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

var chicago = func() *time.Location {
	loc, err := time.LoadLocation("America/Chicago")
	if err != nil {
		panic(err)
	}
	return loc
}()

func local(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, chicago)
	if err != nil {
		panic(err)
	}
	return t
}

func date(s string) caltime.Date {
	d, err := caltime.ParseDate(s)
	if err != nil {
		panic(err)
	}
	return d
}

// fixture: a household in Chicago whose day ends at 21:00 (weeks start on Sunday), at Wednesday
// 4 November 2026 noon, with Ann (household day end) and Ben (day ends at 19:00). Day closes run
// through the real jobs, with the streak hooks wired as in production. Milestones: 2 days earn
// 5 points, 3 days 10.
type fixture struct {
	t        *testing.T
	db       *db.DB
	clock    *clock.Fake
	gen      *horizon.Generator
	chores   *chores.Service
	streaks  *streaks.Service
	runner   *jobs.Runner
	sched    *jobs.Scheduler
	ann, ben int64
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	d, err := db.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	clk := clock.NewFake(local("2026-11-04 12:00"))
	runner := jobs.NewRunner(d.Queries, clk, quiet)
	bus := events.NewBus(d, runner, clk, quiet)
	f := &fixture{t: t, db: d, clock: clk, gen: horizon.New(d, clk), chores: chores.New(d, clk, nil),
		streaks: streaks.New(d, bus, clk), runner: runner, sched: jobs.NewScheduler(d.Queries, runner, clk, quiet)}
	closer := dayclose.New(d, bus, clk)
	closer.AfterKidDay = f.streaks.EvaluateKidDay
	closer.AfterShared = f.streaks.EvaluateDay
	closer.Register(runner, f.sched, quiet)
	if err := d.CreateHousehold(t.Context(), sqlc.CreateHouseholdParams{
		Name: "Test", Timezone: "America/Chicago", DayEnd: "21:00", WeekStart: 0, CreatedAt: db.Timestamp(clk.Now()),
	}); err != nil {
		t.Fatal(err)
	}
	f.exec(`UPDATE household SET allow_negative_balance = 1,
		streak_milestones = '[{"days":2,"points":5},{"days":3,"points":10}]'`)
	ks := kids.NewService(d, clk, nil)
	for _, k := range []struct {
		id     *int64
		name   string
		dayEnd string
	}{{&f.ann, "Ann", ""}, {&f.ben, "Ben", "19:00"}} {
		kid, err := ks.Create(t.Context(), kids.KidInput{DisplayName: k.name, Color: "#2563eb", DayEndOverride: k.dayEnd})
		if err != nil {
			t.Fatal(err)
		}
		*k.id = kid.ID
	}
	return f
}

func (f *fixture) chore(name string, kid int64, slots ...string) chores.Chore {
	f.t.Helper()
	return f.choreWith(chores.ChoreInput{Name: name, Slots: slots, Penalty: 1,
		Schedule:   &schedule.ScheduleRule{Type: schedule.TypeDaily, StartDate: date("2026-11-01")},
		Assignment: &chores.ChoreAssignment{Mode: assign.ModeFixed, KidIDs: []int64{kid}}})
}

func (f *fixture) choreWith(in chores.ChoreInput) chores.Chore {
	f.t.Helper()
	c, err := f.chores.Create(f.t.Context(), in)
	if err != nil {
		f.t.Fatal(err)
	}
	return c
}

// start generates the horizon and runs the first tick, from which the close schedules start.
func (f *fixture) start() {
	f.t.Helper()
	if _, err := f.gen.Generate(f.t.Context()); err != nil {
		f.t.Fatal(err)
	}
	f.run()
}

// run is one scheduler tick plus every job it made due. A failed job fails the test.
func (f *fixture) run() {
	f.t.Helper()
	if _, err := f.sched.Tick(f.t.Context()); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.runner.RunDue(f.t.Context()); err != nil {
		f.t.Fatal(err)
	}
	var lastError string
	err := f.db.SQL.QueryRowContext(f.t.Context(), `SELECT last_error FROM jobs WHERE last_error IS NOT NULL LIMIT 1`).Scan(&lastError)
	if err == nil {
		f.t.Fatalf("a job failed: %s", lastError)
	}
}

// at moves the clock to a local wall time and runs what came due.
func (f *fixture) at(s string) {
	f.t.Helper()
	f.clock.Set(local(s))
	f.run()
}

// endDay runs the day's closes (Ben's at 19:00, Ann's and the household's at 21:00) and the
// horizon, which runs at the household day end too.
func (f *fixture) endDay(day string) {
	f.t.Helper()
	f.at(day + " 19:00")
	f.at(day + " 21:00")
	if _, err := f.gen.Generate(f.t.Context()); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.db.SQL.ExecContext(f.t.Context(), query, args...); err != nil {
		f.t.Fatal(err)
	}
}

// set gives the chore's instance for the kid (0 = shared) on the day a status, as the instance
// transitions would.
func (f *fixture) set(chore, kid int64, day, status string) {
	f.t.Helper()
	res, err := f.db.SQL.ExecContext(f.t.Context(), `UPDATE chore_instances SET status = ?
		WHERE chore_id = ? AND COALESCE(kid_id, 0) = ? AND occurrence_date = ?`, status, chore, kid, day)
	if err != nil {
		f.t.Fatal(err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		f.t.Fatalf("chore %d kid %d on %s: %d instances, want 1", chore, kid, day, n)
	}
}

func (f *fixture) get(kid int64) streaks.KidStreaks {
	f.t.Helper()
	s, err := f.streaks.Get(f.t.Context(), kid)
	if err != nil {
		f.t.Fatal(err)
	}
	return s
}

func (f *fixture) wantDaily(who string, kid int64, current, best int, lastDay string) {
	f.t.Helper()
	got := f.get(kid).Daily
	want := streaks.DailyStreak{Current: current, Best: best}
	if lastDay != "" {
		want.LastDay = date(lastDay)
	}
	if got != want {
		f.t.Fatalf("%s's daily streak = %+v, want %+v", who, got, want)
	}
}

type bonus struct {
	Date   string
	Amount int64
	Note   string
}

func (f *fixture) bonuses(kid int64) []bonus {
	f.t.Helper()
	rows, err := f.db.SQL.QueryContext(f.t.Context(), `SELECT local_date, amount, note FROM points_entries
		WHERE kid_id = ? AND kind = 'streak_bonus' ORDER BY local_date, amount`, kid)
	if err != nil {
		f.t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []bonus
	for rows.Next() {
		var b bonus
		if err := rows.Scan(&b.Date, &b.Amount, &b.Note); err != nil {
			f.t.Fatal(err)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		f.t.Fatal(err)
	}
	return out
}

func (f *fixture) wantBonuses(who string, kid int64, want ...bonus) {
	f.t.Helper()
	if got := f.bonuses(kid); !slices.Equal(got, want) {
		f.t.Fatalf("%s's bonuses = %+v, want %+v", who, got, want)
	}
}

// A streak grows by one per day whose chores are all done, posts each milestone on the day it's
// reached, and breaks on a missed chore; a new run earns the milestones again.
func TestDailyStreakBuildsAndBreaksOnAMiss(t *testing.T) {
	f := newFixture(t)
	c := f.chore("Dishes", f.ann)
	f.start()
	f.wantDaily("Ann", f.ann, 0, 0, "")

	for _, d := range []string{"2026-11-04", "2026-11-05", "2026-11-06"} {
		f.set(c.ID, f.ann, d, horizon.StatusApproved)
		f.endDay(d)
	}
	f.wantDaily("Ann", f.ann, 3, 3, "2026-11-06")
	f.wantBonuses("Ann", f.ann, bonus{"2026-11-05", 5, "2-day streak"}, bonus{"2026-11-06", 10, "3-day streak"})

	f.endDay("2026-11-07") // not done: missed at the close
	f.wantDaily("Ann", f.ann, 0, 3, "2026-11-07")

	for _, d := range []string{"2026-11-08", "2026-11-09"} {
		f.set(c.ID, f.ann, d, horizon.StatusSubmitted) // submitted counts too
		f.endDay(d)
	}
	f.wantDaily("Ann", f.ann, 2, 3, "2026-11-09")
	f.wantBonuses("Ann", f.ann, bonus{"2026-11-05", 5, "2-day streak"}, bonus{"2026-11-06", 10, "3-day streak"},
		bonus{"2026-11-09", 5, "2-day streak"})
	f.wantDaily("Ben", f.ben, 0, 0, "2026-11-09") // no chores: evaluated, but nothing to count
}

// A day without required chores neither extends nor breaks the streak.
func TestDayWithoutChoresNeitherBreaksNorExtends(t *testing.T) {
	f := newFixture(t)
	c := f.choreWith(chores.ChoreInput{Name: "Piano", // not on Thursdays
		Schedule:   &schedule.ScheduleRule{Type: schedule.TypeWeekly, StartDate: date("2026-11-01"), Weekdays: []string{"wed", "fri", "sat"}},
		Assignment: &chores.ChoreAssignment{Mode: assign.ModeFixed, KidIDs: []int64{f.ann}}})
	f.start()

	f.set(c.ID, f.ann, "2026-11-04", horizon.StatusApproved)
	f.endDay("2026-11-04")
	f.endDay("2026-11-05")
	f.wantDaily("Ann", f.ann, 1, 1, "2026-11-05")
	f.wantBonuses("Ann", f.ann)

	f.set(c.ID, f.ann, "2026-11-06", horizon.StatusApproved)
	f.endDay("2026-11-06")
	f.wantDaily("Ann", f.ann, 2, 2, "2026-11-06")
	f.wantBonuses("Ann", f.ann, bonus{"2026-11-06", 5, "2-day streak"})
}

// Milestones post once per run however often a day is evaluated, including when the server
// catches up on several closes after downtime and when jobs repeat or run out of order.
func TestMilestonesPostOnceOnReevaluationAndCatchUp(t *testing.T) {
	f := newFixture(t)
	c := f.chore("Dishes", f.ann)
	f.start()
	for _, d := range []string{"2026-11-04", "2026-11-05", "2026-11-06"} {
		f.set(c.ID, f.ann, d, horizon.StatusApproved)
	}

	f.at("2026-11-07 12:00") // down from Wednesday noon to Saturday noon
	f.wantDaily("Ann", f.ann, 3, 3, "2026-11-06")
	want := []bonus{{"2026-11-05", 5, "2-day streak"}, {"2026-11-06", 10, "3-day streak"}}
	f.wantBonuses("Ann", f.ann, want...)

	// Again, in reverse order, through both hooks, and the scheduler's next tick.
	for _, d := range []string{"2026-11-06", "2026-11-05", "2026-11-04"} {
		if err := f.streaks.EvaluateKidDay(t.Context(), f.ann, date(d)); err != nil {
			t.Fatal(err)
		}
		if err := f.streaks.EvaluateDay(t.Context(), date(d)); err != nil {
			t.Fatal(err)
		}
	}
	f.run()
	f.wantDaily("Ann", f.ann, 3, 3, "2026-11-06")
	f.wantBonuses("Ann", f.ann, want...)

	// A day whose close is still ahead isn't evaluated.
	if err := f.streaks.EvaluateKidDay(t.Context(), f.ann, date("2026-11-07")); err != nil {
		t.Fatal(err)
	}
	f.wantDaily("Ann", f.ann, 3, 3, "2026-11-06")
}

// Each kid's day is evaluated at their own day end.
func TestKidsWithDifferentDayEnds(t *testing.T) {
	f := newFixture(t)
	ann, ben := f.chore("Dishes", f.ann), f.chore("Trash", f.ben)
	f.start()
	f.set(ann.ID, f.ann, "2026-11-04", horizon.StatusApproved)
	f.set(ben.ID, f.ben, "2026-11-04", horizon.StatusApproved)

	f.at("2026-11-04 18:59")
	f.wantDaily("Ben", f.ben, 0, 0, "")
	f.at("2026-11-04 19:00")
	f.wantDaily("Ben", f.ben, 1, 1, "2026-11-04")
	f.wantDaily("Ann", f.ann, 0, 0, "")
	f.at("2026-11-04 21:00")
	f.wantDaily("Ann", f.ann, 1, 1, "2026-11-04")
}

// A missed chore a parent excuses later repairs the day it broke, at the kid's next evaluation;
// the run's milestones post then.
func TestExcusedDaysCount(t *testing.T) {
	f := newFixture(t)
	c := f.chore("Dishes", f.ann)
	f.start()

	f.set(c.ID, f.ann, "2026-11-04", horizon.StatusApproved)
	f.endDay("2026-11-04")
	f.endDay("2026-11-05") // missed
	f.wantDaily("Ann", f.ann, 0, 1, "2026-11-05")

	f.set(c.ID, f.ann, "2026-11-05", horizon.StatusExcused) // the parent excuses it the next morning
	f.set(c.ID, f.ann, "2026-11-06", horizon.StatusApproved)
	f.endDay("2026-11-06")
	f.wantDaily("Ann", f.ann, 3, 3, "2026-11-06")
	f.wantBonuses("Ann", f.ann, bonus{"2026-11-05", 5, "2-day streak"}, bonus{"2026-11-06", 10, "3-day streak"})
}

// A repair that lengthens a run posts the longer milestone even on a day that already earned a
// shorter one, but doesn't pay the joined run the shorter one again.
func TestRepairPostsLongerMilestoneOnTheSameDay(t *testing.T) {
	f := newFixture(t)
	c := f.chore("Dishes", f.ann)
	f.start()

	f.endDay("2026-11-04") // missed
	f.endDay("2026-11-05") // missed
	for _, d := range []string{"2026-11-06", "2026-11-07"} {
		f.set(c.ID, f.ann, d, horizon.StatusApproved)
		f.endDay(d)
	}
	f.wantBonuses("Ann", f.ann, bonus{"2026-11-07", 5, "2-day streak"})

	f.set(c.ID, f.ann, "2026-11-05", horizon.StatusExcused)
	f.set(c.ID, f.ann, "2026-11-08", horizon.StatusApproved)
	f.endDay("2026-11-08")
	f.wantDaily("Ann", f.ann, 4, 4, "2026-11-08")
	f.wantBonuses("Ann", f.ann, bonus{"2026-11-07", 5, "2-day streak"}, bonus{"2026-11-07", 10, "3-day streak"})
}

// One run is paid each milestone once: when an excuse joins a run to the day before it, the
// joined run reaches the milestone earlier, on a day that wasn't paid, and still isn't paid again.
func TestJoinedRunIsPaidEachMilestoneOnce(t *testing.T) {
	f := newFixture(t)
	f.exec(`UPDATE household SET streak_milestones = '[{"days":2,"points":5}]'`)
	c := f.chore("Dishes", f.ann)
	f.start()

	f.endDay("2026-11-04") // missed
	f.endDay("2026-11-05") // missed
	for _, d := range []string{"2026-11-06", "2026-11-07"} {
		f.set(c.ID, f.ann, d, horizon.StatusApproved)
		f.endDay(d)
	}
	f.wantBonuses("Ann", f.ann, bonus{"2026-11-07", 5, "2-day streak"})

	f.set(c.ID, f.ann, "2026-11-05", horizon.StatusExcused) // the run is now 05..07, reaching 2 days on 06
	f.set(c.ID, f.ann, "2026-11-08", horizon.StatusApproved)
	f.endDay("2026-11-08")
	f.wantDaily("Ann", f.ann, 4, 4, "2026-11-08")
	f.wantBonuses("Ann", f.ann, bonus{"2026-11-07", 5, "2-day streak"})

	// A new run after a break earns it again.
	f.endDay("2026-11-09") // missed
	for _, d := range []string{"2026-11-10", "2026-11-11"} {
		f.set(c.ID, f.ann, d, horizon.StatusApproved)
		f.endDay(d)
	}
	f.wantBonuses("Ann", f.ann, bonus{"2026-11-07", 5, "2-day streak"}, bonus{"2026-11-11", 5, "2-day streak"})
}

// At the household day end, one kid's failed evaluation doesn't stop the others'.
func TestEvaluateDayContinuesPastAKidsError(t *testing.T) {
	f := newFixture(t)
	c := f.chore("Trash", f.ben)
	f.start()
	f.exec(`INSERT INTO kid_streaks (kid_id, current, best, last_day) VALUES (?, 0, 0, 'garbage')`, f.ann)
	f.set(c.ID, f.ben, "2026-11-04", horizon.StatusApproved)
	f.clock.Set(local("2026-11-04 21:00"))

	err := f.streaks.EvaluateDay(t.Context(), date("2026-11-04"))
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("kid %d:", f.ann)) {
		t.Fatalf("err = %v, want Ann's", err)
	}
	f.wantDaily("Ben", f.ben, 1, 1, "2026-11-04")
}

// A day that counted stays counted when a submitted chore is later rejected and missed.
func TestCountedDayStaysCounted(t *testing.T) {
	f := newFixture(t)
	c := f.chore("Dishes", f.ann)
	f.start()

	f.set(c.ID, f.ann, "2026-11-04", horizon.StatusSubmitted)
	f.endDay("2026-11-04")
	f.set(c.ID, f.ann, "2026-11-04", horizon.StatusMissed) // rejected after the close, so missed
	f.set(c.ID, f.ann, "2026-11-05", horizon.StatusApproved)
	f.endDay("2026-11-05")
	f.wantDaily("Ann", f.ann, 2, 2, "2026-11-05")
}

// A chore due after the kid's early day end, and a shared chore, are still open at the kid's
// close, so the day doesn't count then; at the household day end, once both are due, it does.
func TestLateChoresSettleAtHouseholdDayEnd(t *testing.T) {
	f := newFixture(t)
	late := f.chore("Feed the cat", f.ben, "20:00")
	shared := f.choreWith(chores.ChoreInput{Name: "Tidy up", Schedule: &schedule.ScheduleRule{Type: schedule.TypeDaily, StartDate: date("2026-11-01")},
		Assignment: &chores.ChoreAssignment{Mode: assign.ModeShared, KidIDs: []int64{f.ann, f.ben}}})
	f.start()

	f.at("2026-11-04 19:00")
	f.wantDaily("Ben", f.ben, 0, 0, "2026-11-04")

	f.set(late.ID, f.ben, "2026-11-04", horizon.StatusApproved) // at 20:00
	f.set(shared.ID, 0, "2026-11-04", horizon.StatusApproved)   // Ann did it
	f.at("2026-11-04 21:00")
	f.wantDaily("Ben", f.ben, 1, 1, "2026-11-04")
	f.wantDaily("Ann", f.ann, 1, 1, "2026-11-04")
}

// Per-chore streaks count each recurring chore's occurrences in a row that were done; a one-off
// chore has none.
func TestPerChoreStreaks(t *testing.T) {
	f := newFixture(t)
	dishes, trash := f.chore("Dishes", f.ann), f.chore("Trash", f.ann)
	once := f.choreWith(chores.ChoreInput{Name: "Wash the car", Schedule: &schedule.ScheduleRule{Type: schedule.TypeOnce, Date: date("2026-11-04")},
		Assignment: &chores.ChoreAssignment{Mode: assign.ModeFixed, KidIDs: []int64{f.ann}}})
	f.start()

	for _, d := range []string{"2026-11-04", "2026-11-05", "2026-11-06"} {
		f.set(dishes.ID, f.ann, d, horizon.StatusApproved)
	}
	f.set(trash.ID, f.ann, "2026-11-04", horizon.StatusApproved)
	f.set(trash.ID, f.ann, "2026-11-05", horizon.StatusApproved)
	f.set(once.ID, f.ann, "2026-11-04", horizon.StatusApproved)
	for _, d := range []string{"2026-11-04", "2026-11-05", "2026-11-06"} { // the trash is missed on Friday
		f.endDay(d)
	}

	got := f.get(f.ann).Chores
	want := []streaks.ChoreStreak{{ChoreID: dishes.ID, Current: 3, Best: 3}, {ChoreID: trash.ID, Current: 0, Best: 2}}
	if !slices.Equal(got, want) {
		t.Fatalf("chore streaks = %+v, want %+v", got, want)
	}
	if got := f.get(f.ben).Chores; len(got) != 0 {
		t.Fatalf("Ben's chore streaks = %+v, want none", got)
	}
}

func TestMilestoneSource(t *testing.T) {
	if got := streaks.MilestoneSource(date("2026-11-04"), 7); got.Kind != streaks.SourceMilestone || got.ID != 202611040007 {
		t.Fatalf("source = %+v", got)
	}
}
