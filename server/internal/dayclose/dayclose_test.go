package dayclose_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strconv"
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
	"github.com/voidrot/chorecast/server/internal/ledger"
	"github.com/voidrot/chorecast/server/internal/schedule"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

var chicago = func() *time.Location {
	loc, err := time.LoadLocation("America/Chicago")
	if err != nil {
		panic(err)
	}
	return loc
}()

// local is a wall time in the household's timezone, "2006-01-02 15:04".
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

// fixture: a household in Chicago whose day ends at 21:00 and whose weeks start on Sunday, at
// Wednesday 4 November 2026 noon, with Ann (household day end) and Ben (day ends at 19:00).
type fixture struct {
	t        *testing.T
	db       *db.DB
	clock    *clock.Fake
	gen      *horizon.Generator
	chores   *chores.Service
	bus      *events.Bus
	closer   *dayclose.Closer
	runner   *jobs.Runner
	sched    *jobs.Scheduler
	kids     *kids.Service
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
	f := &fixture{t: t, db: d, clock: clk, gen: horizon.New(d, clk), chores: chores.New(d, clk, nil), bus: bus,
		closer: dayclose.New(d, bus, clk), runner: runner, sched: jobs.NewScheduler(d.Queries, runner, clk, quiet)}
	f.closer.Register(f.runner, f.sched, quiet)
	f.run() // before setup: no schedules, no errors
	if err := d.CreateHousehold(t.Context(), sqlc.CreateHouseholdParams{
		Name: "Test", Timezone: "America/Chicago", DayEnd: "21:00", WeekStart: 0, CreatedAt: db.Timestamp(clk.Now()),
	}); err != nil {
		t.Fatal(err)
	}
	ks := kids.NewService(d, clk, nil)
	f.kids = ks
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

func (f *fixture) chore(in chores.ChoreInput) chores.Chore {
	f.t.Helper()
	c, err := f.chores.Create(f.t.Context(), in)
	if err != nil {
		f.t.Fatal(err)
	}
	return c
}

func daily(name string, penalty int) chores.ChoreInput {
	return chores.ChoreInput{Name: name, Penalty: penalty,
		Schedule: &schedule.ScheduleRule{Type: schedule.TypeDaily, StartDate: date("2026-11-01")}}
}

func (f *fixture) generate() {
	f.t.Helper()
	if _, err := f.gen.Generate(f.t.Context()); err != nil {
		f.t.Fatal(err)
	}
}

// run is one scheduler tick plus every job it made due, as the background loop does. A failed
// job fails the test.
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

func (f *fixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.db.SQL.ExecContext(f.t.Context(), query, args...); err != nil {
		f.t.Fatal(err)
	}
}

// instance finds the chore's instance for the kid (0 = shared) on the date.
func (f *fixture) instance(chore, kid int64, day string) horizon.ChoreInstance {
	f.t.Helper()
	got := f.instances(chore, kid, day)
	if len(got) != 1 {
		f.t.Fatalf("chore %d kid %d on %s: %d instances, want 1", chore, kid, day, len(got))
	}
	return got[0]
}

func (f *fixture) instances(chore, kid int64, day string) []horizon.ChoreInstance {
	f.t.Helper()
	all, err := f.gen.List(f.t.Context(), horizon.ListFilter{From: date("2026-10-01"), To: date("2026-12-31")})
	if err != nil {
		f.t.Fatal(err)
	}
	var out []horizon.ChoreInstance
	for _, i := range all {
		if i.ChoreID == chore && i.KidID == kid && i.Date.String() == day {
			out = append(out, i)
		}
	}
	return out
}

func (f *fixture) status(id int64) string {
	f.t.Helper()
	var s string
	if err := f.db.SQL.QueryRowContext(f.t.Context(), `SELECT status FROM chore_instances WHERE id = ?`, id).Scan(&s); err != nil {
		f.t.Fatal(err)
	}
	return s
}

type penalty struct {
	Instance  int64
	Amount    int64
	Requested int64 // 0 = not clamped
	Date      string
}

// penalties lists the kid's penalty entries in order.
func (f *fixture) penalties(kid int64) []penalty {
	f.t.Helper()
	rows, err := f.db.SQL.QueryContext(f.t.Context(), `SELECT source_id, amount, COALESCE(requested_amount, 0), local_date
		FROM points_entries WHERE kid_id = ? AND kind = 'chore_penalty' ORDER BY id`, kid)
	if err != nil {
		f.t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []penalty
	for rows.Next() {
		var p penalty
		if err := rows.Scan(&p.Instance, &p.Amount, &p.Requested, &p.Date); err != nil {
			f.t.Fatal(err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		f.t.Fatal(err)
	}
	return out
}

func (f *fixture) missedEvents() []dayclose.InstanceMissed {
	f.t.Helper()
	rows, err := f.db.SQL.QueryContext(f.t.Context(), `SELECT payload FROM events WHERE type = 'instance.missed' ORDER BY id`)
	if err != nil {
		f.t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []dayclose.InstanceMissed
	for rows.Next() {
		var raw string
		var ev dayclose.InstanceMissed
		if err := rows.Scan(&raw); err != nil {
			f.t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			f.t.Fatal(err)
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		f.t.Fatal(err)
	}
	return out
}

func (f *fixture) balance(kid int64) int64 {
	f.t.Helper()
	b, err := ledger.Balance(f.t.Context(), f.db.Queries, kid)
	if err != nil {
		f.t.Fatal(err)
	}
	return b
}

func wantPenalties(t *testing.T, who string, got, want []penalty) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("%s penalties = %+v, want %+v", who, got, want)
	}
}

// Each kid's day closes at their own day end, through the scheduler, and each missed instance
// is penalized exactly once however often the closes and the horizon run again.
func TestKidsDaysCloseAtTheirOwnDayEnd(t *testing.T) {
	f := newFixture(t)
	f.exec(`UPDATE household SET allow_negative_balance = 1`)
	c := f.chore(daily("Dishes", 2)) // everyone
	f.generate()
	f.run() // first tick: the schedules start from now
	ann, ben := f.instance(c.ID, f.ann, "2026-11-04"), f.instance(c.ID, f.ben, "2026-11-04")

	f.at("2026-11-04 18:59")
	if f.status(ben.ID) != horizon.StatusOpen {
		t.Fatal("Ben's day closed before his day end")
	}

	f.at("2026-11-04 19:00")
	if f.status(ben.ID) != horizon.StatusMissed || f.status(ann.ID) != horizon.StatusOpen {
		t.Fatalf("at 19:00: Ben %s, Ann %s; want missed, open", f.status(ben.ID), f.status(ann.ID))
	}
	wantPenalties(t, "Ben", f.penalties(f.ben), []penalty{{ben.ID, -2, 0, "2026-11-04"}})
	wantPenalties(t, "Ann", f.penalties(f.ann), nil)

	f.at("2026-11-04 21:00")
	f.generate() // the horizon also runs at the household day end
	if f.status(ann.ID) != horizon.StatusMissed {
		t.Fatalf("Ann at 21:00: %s, want missed", f.status(ann.ID))
	}
	if f.status(f.instance(c.ID, f.ann, "2026-11-05").ID) != horizon.StatusOpen {
		t.Fatal("Thursday closed early")
	}

	// Re-runs: the next tick, the same jobs again, and the horizon.
	f.run()
	for _, kid := range []int64{f.ann, f.ben} {
		if res, err := f.closer.CloseKidDay(t.Context(), kid, date("2026-11-04")); err != nil || res != (dayclose.Result{}) {
			t.Fatalf("re-run for kid %d: %+v, %v; want nothing", kid, res, err)
		}
	}
	f.generate()
	wantPenalties(t, "Ann", f.penalties(f.ann), []penalty{{ann.ID, -2, 0, "2026-11-04"}})
	wantPenalties(t, "Ben", f.penalties(f.ben), []penalty{{ben.ID, -2, 0, "2026-11-04"}})

	evs := f.missedEvents()
	want := []dayclose.InstanceMissed{
		{InstanceID: ben.ID, KidIDs: []int64{f.ben}, From: "open", To: "missed"},
		{InstanceID: ann.ID, KidIDs: []int64{f.ann}, From: "open", To: "missed"},
	}
	if !slices.EqualFunc(evs, want, func(a, b dayclose.InstanceMissed) bool {
		return a.InstanceID == b.InstanceID && slices.Equal(a.KidIDs, b.KidIDs) && a.From == b.From && a.To == b.To
	}) {
		t.Fatalf("events = %+v, want %+v", evs, want)
	}
}

// After downtime over several closes, the next run closes every day that didn't close, each
// with its own date.
func TestCatchesUpAfterDowntime(t *testing.T) {
	f := newFixture(t)
	f.exec(`UPDATE household SET allow_negative_balance = 1`)
	c := f.chore(daily("Dishes", 2))
	f.generate()
	f.run()

	f.at("2026-11-07 12:00") // down from Wednesday noon to Saturday noon
	for _, kid := range []int64{f.ann, f.ben} {
		var want []penalty
		for _, d := range []string{"2026-11-04", "2026-11-05", "2026-11-06"} {
			want = append(want, penalty{f.instance(c.ID, kid, d).ID, -2, 0, d})
		}
		wantPenalties(t, "kid", f.penalties(kid), want)
	}
	if f.status(f.instance(c.ID, f.ann, "2026-11-07").ID) != horizon.StatusOpen {
		t.Fatal("today closed early")
	}

	// One close also catches up on its own: reopen Ann's Wednesday and Thursday as if their closes
	// had never run (their penalties stand, so none is posted again).
	f.exec(`UPDATE chore_instances SET status = 'open' WHERE kid_id = ? AND occurrence_date IN ('2026-11-04', '2026-11-05')`, f.ann)
	res, err := f.closer.CloseKidDay(t.Context(), f.ann, date("2026-11-06"))
	if err != nil {
		t.Fatal(err)
	}
	if res != (dayclose.Result{Missed: 2}) {
		t.Fatalf("catch-up = %+v, want 2 missed, no new penalties", res)
	}
	if len(f.penalties(f.ann)) != 3 {
		t.Fatalf("Ann has %d penalties, want 3", len(f.penalties(f.ann)))
	}
}

// Submitted instances stay approvable, and other settled ones are left alone too.
func TestLeavesSettledInstances(t *testing.T) {
	f := newFixture(t)
	c := f.chore(daily("Dishes", 2))
	f.generate()
	f.run()
	submitted, approved := f.instance(c.ID, f.ann, "2026-11-04"), f.instance(c.ID, f.ben, "2026-11-04")
	f.exec(`UPDATE chore_instances SET status = 'submitted' WHERE id = ?`, submitted.ID)
	f.exec(`UPDATE chore_instances SET status = 'approved' WHERE id = ?`, approved.ID)

	f.at("2026-11-04 21:00")
	if f.status(submitted.ID) != horizon.StatusSubmitted || f.status(approved.ID) != horizon.StatusApproved {
		t.Fatalf("statuses %s, %s; want submitted, approved", f.status(submitted.ID), f.status(approved.ID))
	}
	if len(f.penalties(f.ann))+len(f.penalties(f.ben)) != 0 || len(f.missedEvents()) != 0 {
		t.Fatal("a settled instance was penalized")
	}
}

// Flexible instances close with the week, not the day, and each one left over is penalized,
// dated the week's last day.
func TestWeekClosePenalizesLeftoverFlexibleInstances(t *testing.T) {
	f := newFixture(t)
	f.exec(`UPDATE household SET allow_negative_balance = 1`)
	c := f.chore(chores.ChoreInput{Name: "Practice piano", Penalty: 3,
		Schedule:   &schedule.ScheduleRule{Type: schedule.TypeFlexibleWeekly, StartDate: date("2026-10-01"), TimesPerWeek: 3},
		Assignment: &chores.ChoreAssignment{Mode: assign.ModeFixed, KidIDs: []int64{f.ann}}})
	f.generate()
	f.run()
	week := f.instances(c.ID, f.ann, "2026-11-01") // dated at the week start
	if len(week) != 3 {
		t.Fatalf("%d flexible instances, want 3", len(week))
	}
	f.exec(`UPDATE chore_instances SET status = 'approved' WHERE id = ?`, week[0].ID)

	f.at("2026-11-06 21:00") // Friday: days close, the week doesn't
	if len(f.penalties(f.ann)) != 0 {
		t.Fatal("a day close penalized a flexible instance")
	}

	f.at("2026-11-07 21:00") // Saturday: the week closes
	f.generate()
	wantPenalties(t, "Ann", f.penalties(f.ann), []penalty{{week[1].ID, -3, 0, "2026-11-07"}, {week[2].ID, -3, 0, "2026-11-07"}})
	if f.status(week[0].ID) != horizon.StatusApproved {
		t.Fatal("the approved completion changed")
	}

	f.run()
	if res, err := f.closer.CloseKidWeek(t.Context(), f.ann, date("2026-11-07")); err != nil || res != (dayclose.Result{}) {
		t.Fatalf("re-run: %+v, %v", res, err)
	}
	if len(f.penalties(f.ann)) != 2 {
		t.Fatal("a re-run posted again")
	}
}

// A shared instance closes at the household day end, not at a member's earlier one, and its
// penalty goes to every member or to none.
func TestSharedInstancesCloseAtHouseholdDayEnd(t *testing.T) {
	f := newFixture(t)
	f.exec(`UPDATE household SET allow_negative_balance = 1`)
	group := []int64{f.ann, f.ben}
	all := f.chore(chores.ChoreInput{Name: "Tidy the playroom", Penalty: 4, Schedule: daily("x", 0).Schedule,
		Assignment: &chores.ChoreAssignment{Mode: assign.ModeShared, KidIDs: group, SharedPenalty: assign.PenaltyAll}})
	none := f.chore(chores.ChoreInput{Name: "Walk the dog", Penalty: 4, Schedule: daily("x", 0).Schedule,
		Assignment: &chores.ChoreAssignment{Mode: assign.ModeShared, KidIDs: group, SharedPenalty: assign.PenaltyNone}})
	f.generate()
	f.run()
	a, n := f.instance(all.ID, 0, "2026-11-04"), f.instance(none.ID, 0, "2026-11-04")

	f.at("2026-11-04 19:00") // Ben's day closes; the group's doesn't
	if f.status(a.ID) != horizon.StatusOpen {
		t.Fatal("a member's day close closed a shared instance")
	}

	f.at("2026-11-04 21:00")
	if f.status(a.ID) != horizon.StatusMissed || f.status(n.ID) != horizon.StatusMissed {
		t.Fatalf("statuses %s, %s; want missed", f.status(a.ID), f.status(n.ID))
	}
	for _, kid := range group {
		wantPenalties(t, "member", f.penalties(kid), []penalty{{a.ID, -4, 0, "2026-11-04"}})
	}
	for _, ev := range f.missedEvents() {
		if !slices.Equal(ev.KidIDs, group) {
			t.Fatalf("event %+v: want the group as kids", ev)
		}
	}

	f.run()
	if res, err := f.closer.CloseShared(t.Context(), date("2026-11-04")); err != nil || res != (dayclose.Result{}) {
		t.Fatalf("re-run: %+v, %v", res, err)
	}
	for _, kid := range group {
		if len(f.penalties(kid)) != 1 {
			t.Fatal("a re-run posted again")
		}
	}
}

// With negative balances off, the ledger clamps a penalty to what the kid has.
func TestPenaltyClampsToBalance(t *testing.T) {
	f := newFixture(t)
	if _, err := ledger.New(f.db, f.bus, f.clock).Adjust(t.Context(), f.ann, 3, "allowance", 0); err != nil {
		t.Fatal(err)
	}
	c := f.chore(daily("Dishes", 5))
	f.generate()
	f.run()

	f.at("2026-11-04 21:00")
	wantPenalties(t, "Ann", f.penalties(f.ann), []penalty{{f.instance(c.ID, f.ann, "2026-11-04").ID, -3, -5, "2026-11-04"}})
	wantPenalties(t, "Ben", f.penalties(f.ben), []penalty{{f.instance(c.ID, f.ben, "2026-11-04").ID, 0, -5, "2026-11-04"}})
	if f.balance(f.ann) != 0 || f.balance(f.ben) != 0 {
		t.Fatalf("balances %d, %d; want 0, 0", f.balance(f.ann), f.balance(f.ben))
	}
}

// A chore without a penalty is still missed, but posts nothing.
func TestZeroPenaltyPostsNothing(t *testing.T) {
	f := newFixture(t)
	c := f.chore(daily("Dishes", 0))
	f.generate()
	f.run()

	f.at("2026-11-04 21:00")
	inst := f.instance(c.ID, f.ann, "2026-11-04")
	if f.status(inst.ID) != horizon.StatusMissed {
		t.Fatalf("status %s, want missed", f.status(inst.ID))
	}
	if len(f.penalties(f.ann))+len(f.penalties(f.ben)) != 0 {
		t.Fatal("a zero penalty was posted")
	}
	if len(f.missedEvents()) != 2 {
		t.Fatalf("%d events, want 2", len(f.missedEvents()))
	}
}

// Close and regeneration at the same instant, in either order, leave the closed day's
// instances in place: the horizon keeps what's due, missed or not.
func TestHorizonKeepsClosedInstances(t *testing.T) {
	for _, closeFirst := range []bool{true, false} {
		f := newFixture(t)
		c := f.chore(daily("Dishes", 2))
		f.generate()
		f.run()
		inst := f.instance(c.ID, f.ann, "2026-11-04")

		f.clock.Set(local("2026-11-04 21:00"))
		if closeFirst {
			f.run()
			f.generate()
		} else {
			f.generate()
			f.run()
		}
		got := f.instance(c.ID, f.ann, "2026-11-04")
		if got.ID != inst.ID || got.Status != horizon.StatusMissed || len(f.penalties(f.ann)) != 1 {
			t.Fatalf("close first %v: instance %d %s with %d penalties; want %d missed with 1",
				closeFirst, got.ID, got.Status, len(f.penalties(f.ann)), inst.ID)
		}
	}
}

// An instance due after its kid's day end (a slot later than an early override) isn't missed
// before it's due; the next close takes it, dated its own day.
func TestSlotAfterKidDayEndClosesNextDay(t *testing.T) {
	f := newFixture(t)
	c := f.chore(chores.ChoreInput{Name: "Feed the cat", Penalty: 1, Slots: []string{"20:00"},
		Schedule:   daily("x", 0).Schedule,
		Assignment: &chores.ChoreAssignment{Mode: assign.ModeFixed, KidIDs: []int64{f.ben}}})
	f.generate()
	f.run()
	inst := f.instance(c.ID, f.ben, "2026-11-04")

	f.at("2026-11-04 19:00")
	if f.status(inst.ID) != horizon.StatusOpen {
		t.Fatal("missed before it was due")
	}
	// A late run of the same close still stops at Ben's day end, before the slot.
	f.clock.Set(local("2026-11-04 20:30"))
	if _, err := f.closer.CloseKidDay(t.Context(), f.ben, date("2026-11-04")); err != nil {
		t.Fatal(err)
	}
	if f.status(inst.ID) != horizon.StatusOpen {
		t.Fatal("a late close went past the kid's day end")
	}
	f.at("2026-11-05 19:00")
	if f.status(inst.ID) != horizon.StatusMissed {
		t.Fatal("not missed at the next close")
	}
	if p := f.penalties(f.ben); len(p) == 0 || p[0].Instance != inst.ID || p[0].Date != "2026-11-04" {
		t.Fatalf("penalties %+v: want the first for instance %d on 2026-11-04", p, inst.ID)
	}
}

// A close still ahead does nothing: Ben's day end moves later after his close was queued, so the
// queued close leaves his day open and the moved close takes it.
func TestDayEndMovedLaterAfterCloseWasQueued(t *testing.T) {
	f := newFixture(t)
	c := f.chore(daily("Dishes", 2))
	f.generate()
	f.run()
	inst := f.instance(c.ID, f.ben, "2026-11-04") // due at 19:00

	f.clock.Set(local("2026-11-04 19:00"))
	if _, err := f.sched.Tick(t.Context()); err != nil { // queued, not run yet
		t.Fatal(err)
	}
	f.exec(`UPDATE users SET day_end_override = '20:30' WHERE id = ?`, f.ben)
	f.clock.Set(local("2026-11-04 19:00").Add(5 * time.Second))
	f.run()
	if f.status(inst.ID) != horizon.StatusOpen || len(f.penalties(f.ben)) != 0 {
		t.Fatal("a queued close ran before Ben's moved day end")
	}

	f.at("2026-11-04 20:30")
	if f.status(inst.ID) != horizon.StatusMissed || len(f.penalties(f.ben)) != 1 {
		t.Fatalf("at the moved close: %s with %d penalties; want missed with 1", f.status(inst.ID), len(f.penalties(f.ben)))
	}
}

// MissInTx reads the assignee in its own transaction: an instance reassigned after a close
// listed it penalizes the new assignee. An instance that isn't open is left alone.
func TestMissInTxPenalizesTheCurrentAssignee(t *testing.T) {
	f := newFixture(t)
	f.exec(`UPDATE household SET allow_negative_balance = 1`)
	c := f.chore(chores.ChoreInput{Name: "Trash", Penalty: 2, Schedule: daily("x", 0).Schedule,
		Assignment: &chores.ChoreAssignment{Mode: assign.ModeFixed, KidIDs: []int64{f.ann}}})
	f.generate()
	inst := f.instance(c.ID, f.ann, "2026-11-04")
	f.exec(`UPDATE chore_instances SET kid_id = ?, manually_reassigned = 1 WHERE id = ?`, f.ben, inst.ID)

	miss := func() dayclose.Result {
		t.Helper()
		var res dayclose.Result
		if err := f.bus.InTx(t.Context(), func(tx *events.Tx) error {
			var err error
			res, err = dayclose.MissInTx(t.Context(), tx, f.clock, inst.ID)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return res
	}
	if res := miss(); res != (dayclose.Result{Missed: 1, Penalties: 1}) {
		t.Fatalf("miss = %+v", res)
	}
	wantPenalties(t, "Ben", f.penalties(f.ben), []penalty{{inst.ID, -2, 0, "2026-11-04"}})
	wantPenalties(t, "Ann", f.penalties(f.ann), nil)
	if evs := f.missedEvents(); len(evs) != 1 || !slices.Equal(evs[0].KidIDs, []int64{f.ben}) {
		t.Fatalf("events = %+v, want one for Ben", evs)
	}
	if res := miss(); res != (dayclose.Result{}) || len(f.missedEvents()) != 1 {
		t.Fatalf("second miss = %+v; want nothing", res)
	}
}

// A shared flexible instance closes at the household week close, with a penalty for each member
// dated the week's last day.
func TestSharedFlexibleClosesAtHouseholdWeekClose(t *testing.T) {
	f := newFixture(t)
	f.exec(`UPDATE household SET allow_negative_balance = 1`)
	group := []int64{f.ann, f.ben}
	c := f.chore(chores.ChoreInput{Name: "Wash the car", Penalty: 2,
		Schedule:   &schedule.ScheduleRule{Type: schedule.TypeFlexibleWeekly, StartDate: date("2026-10-01"), TimesPerWeek: 1},
		Assignment: &chores.ChoreAssignment{Mode: assign.ModeShared, KidIDs: group, SharedPenalty: assign.PenaltyAll}})
	f.generate()
	f.run()
	inst := f.instance(c.ID, 0, "2026-11-01")

	f.at("2026-11-06 21:00") // Friday's household close
	f.at("2026-11-07 19:00") // Ben's week closes; the group's doesn't
	if f.status(inst.ID) != horizon.StatusOpen {
		t.Fatal("closed before the household week close")
	}
	f.at("2026-11-07 21:00")
	if f.status(inst.ID) != horizon.StatusMissed {
		t.Fatalf("status %s, want missed", f.status(inst.ID))
	}
	for _, kid := range group {
		wantPenalties(t, "member", f.penalties(kid), []penalty{{inst.ID, -2, 0, "2026-11-07"}})
	}
}

// An archived kid's day still closes: an instance already due when they were archived is missed.
func TestArchivedKidsDaysStillClose(t *testing.T) {
	f := newFixture(t)
	f.exec(`UPDATE household SET allow_negative_balance = 1`)
	c := f.chore(chores.ChoreInput{Name: "Feed the fish", Penalty: 1, Slots: []string{"13:00"}, Schedule: daily("x", 0).Schedule,
		Assignment: &chores.ChoreAssignment{Mode: assign.ModeFixed, KidIDs: []int64{f.ann}}})
	f.generate()
	f.run()
	inst := f.instance(c.ID, f.ann, "2026-11-04")

	f.at("2026-11-04 14:00") // the 13:00 slot is due, so the horizon keeps it
	if _, err := f.kids.Archive(t.Context(), strconv.FormatInt(f.ann, 10)); err != nil {
		t.Fatal(err)
	}
	f.generate()

	f.at("2026-11-04 21:00")
	if f.status(inst.ID) != horizon.StatusMissed {
		t.Fatalf("status %s, want missed", f.status(inst.ID))
	}
	wantPenalties(t, "Ann", f.penalties(f.ann), []penalty{{inst.ID, -1, 0, "2026-11-04"}})
}

// ClosesAt agrees with the jobs: the kid's own day end, the household's for shared instances,
// the week close for flexible ones, and the next close for a slot after an early day end.
func TestClosesAt(t *testing.T) {
	f := newFixture(t)
	mine := f.chore(daily("Dishes", 0))
	shared := f.chore(chores.ChoreInput{Name: "Tidy", Schedule: daily("x", 0).Schedule,
		Assignment: &chores.ChoreAssignment{Mode: assign.ModeShared, KidIDs: []int64{f.ann, f.ben}}})
	flexible := f.chore(chores.ChoreInput{Name: "Piano",
		Schedule:   &schedule.ScheduleRule{Type: schedule.TypeFlexibleWeekly, StartDate: date("2026-10-01"), TimesPerWeek: 1},
		Assignment: &chores.ChoreAssignment{Mode: assign.ModeFixed, KidIDs: []int64{f.ben}}})
	late := f.chore(chores.ChoreInput{Name: "Cat", Slots: []string{"20:00"}, Schedule: daily("x", 0).Schedule,
		Assignment: &chores.ChoreAssignment{Mode: assign.ModeFixed, KidIDs: []int64{f.ben}}})
	f.generate()

	for _, tc := range []struct {
		name string
		inst horizon.ChoreInstance
		want string
	}{
		{"Ben's day", f.instance(mine.ID, f.ben, "2026-11-04"), "2026-11-04 19:00"},
		{"Ann's day", f.instance(mine.ID, f.ann, "2026-11-04"), "2026-11-04 21:00"},
		{"shared", f.instance(shared.ID, 0, "2026-11-04"), "2026-11-04 21:00"},
		{"flexible", f.instance(flexible.ID, f.ben, "2026-11-01"), "2026-11-07 19:00"},
		{"slot after day end", f.instance(late.ID, f.ben, "2026-11-04"), "2026-11-05 19:00"},
	} {
		row := sqlc.ChoreInstance{ID: tc.inst.ID}
		if err := f.db.SQL.QueryRowContext(t.Context(), `SELECT kid_id, occurrence_date, due_at, flexible
			FROM chore_instances WHERE id = ?`, tc.inst.ID).Scan(&row.KidID, &row.OccurrenceDate, &row.DueAt, &row.Flexible); err != nil {
			t.Fatal(err)
		}
		got, err := dayclose.ClosesAt(t.Context(), f.db.Queries, row)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Equal(local(tc.want)) {
			t.Errorf("%s: closes at %s, want %s", tc.name, got.In(chicago), tc.want)
		}
	}
}

// OpensAt (the evidence capture window's start) is the previous day's close for the instance's
// day end, or the close before a flexible instance's week began.
func TestOpensAt(t *testing.T) {
	f := newFixture(t)
	for _, tc := range []struct {
		name     string
		kid      int64
		date     string
		flexible bool
		want     string
	}{
		{"Ann's day", f.ann, "2026-11-04", false, "2026-11-03 21:00"},
		{"Ben's day (ends at 19:00)", f.ben, "2026-11-04", false, "2026-11-03 19:00"},
		{"shared", 0, "2026-11-04", false, "2026-11-03 21:00"},
		{"flexible, dated at the week start", f.ben, "2026-11-01", true, "2026-10-31 19:00"},
		{"flexible, any day of the week", f.ann, "2026-11-04", true, "2026-10-31 21:00"},
	} {
		inst := sqlc.ChoreInstance{OccurrenceDate: tc.date, Flexible: b2i(tc.flexible)}
		if tc.kid != 0 {
			inst.KidID = sql.NullInt64{Int64: tc.kid, Valid: true}
		}
		got, err := dayclose.OpensAt(t.Context(), f.db.Queries, inst)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Equal(local(tc.want)) {
			t.Errorf("%s: opens at %s, want %s", tc.name, got.In(chicago), tc.want)
		}
	}
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// The kid-day hook runs after the close has marked the day's misses, and a hook error fails the
// job, so it retries.
func TestAfterKidDayRunsAfterTheClose(t *testing.T) {
	f := newFixture(t)
	c := f.chore(daily("Dishes", 2))
	f.generate()
	f.run()
	inst := f.instance(c.ID, f.ben, "2026-11-04")
	var seen []string
	fail := true
	f.closer.AfterKidDay = func(_ context.Context, kid int64, day caltime.Date) error {
		if kid != f.ben || day != date("2026-11-04") {
			return nil
		}
		seen = append(seen, f.status(inst.ID))
		if fail {
			return errors.New("hook failed")
		}
		return nil
	}

	f.clock.Set(local("2026-11-04 19:00"))
	if _, err := f.sched.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runner.RunDue(t.Context()); err != nil {
		t.Fatal(err)
	}
	var failed int
	if err := f.db.SQL.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM jobs WHERE kind = ? AND last_error LIKE '%hook failed%'`,
		dayclose.KindKidDay).Scan(&failed); err != nil || failed != 1 {
		t.Fatalf("%d failed jobs, %v; want Ben's", failed, err)
	}
	fail = false
	f.clock.Advance(time.Hour) // past the retry backoff
	if _, err := f.runner.RunDue(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(seen, []string{horizon.StatusMissed, horizon.StatusMissed}) {
		t.Fatalf("hook saw %v, want missed on the first run and on the retry", seen)
	}
}
