package autoapprove_test

import (
	"database/sql"
	"log/slog"
	"testing"
	"time"

	"github.com/voidrot/chorecast/server/internal/autoapprove"
	"github.com/voidrot/chorecast/server/internal/chores"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
	"github.com/voidrot/chorecast/server/internal/events"
	"github.com/voidrot/chorecast/server/internal/horizon"
	"github.com/voidrot/chorecast/server/internal/instances"
	"github.com/voidrot/chorecast/server/internal/jobs"
	"github.com/voidrot/chorecast/server/internal/kids"
)

// Saturday 31 October 2026, 13:00 in Chicago; the household day ends at 21:00.
var saturday = time.Date(2026, 10, 31, 18, 0, 0, 0, time.UTC)

const today = "2026-10-31"

type fixture struct {
	t      *testing.T
	db     *db.DB
	clk    *clock.Fake
	inst   *instances.Service
	chores *chores.Service
	job    *autoapprove.Service
	sam    int64
	mom    int64
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	d, err := db.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	clk := clock.NewFake(saturday)
	quiet := slog.New(slog.DiscardHandler)
	bus := events.NewBus(d, jobs.NewRunner(d.Queries, clk, quiet), clk, quiet)
	if err := d.CreateHousehold(t.Context(), sqlc.CreateHouseholdParams{
		Name: "Test", Timezone: "America/Chicago", DayEnd: "21:00", WeekStart: 0, CreatedAt: db.Timestamp(clk.Now()),
	}); err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, db: d, clk: clk, inst: instances.New(d, bus, clk), chores: chores.New(d, clk, nil),
		job: autoapprove.New(d, bus, clk)}
	kid, err := kids.NewService(d, clk, nil).Create(t.Context(), kids.KidInput{DisplayName: "Sam", Color: "#2563eb"})
	if err != nil {
		t.Fatal(err)
	}
	f.sam = kid.ID
	mom, err := d.CreateUser(t.Context(), sqlc.CreateUserParams{DisplayName: "Mom", Role: "parent"})
	if err != nil {
		t.Fatal(err)
	}
	f.mom = mom.ID
	return f
}

func (f *fixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.db.SQL.ExecContext(f.t.Context(), query, args...); err != nil {
		f.t.Fatal(err)
	}
}

// household sets the household default timeout in hours; 0 = never (NULL).
func (f *fixture) household(hours int) {
	f.t.Helper()
	if hours == 0 {
		f.exec("UPDATE household SET auto_approve_hours = NULL")
		return
	}
	f.exec("UPDATE household SET auto_approve_hours = ?", hours)
}

// submitted is an instance of a new 5-point chore, completed by Sam now. override is the chore's
// auto_approve_hours: nil = household default, 0 = never.
func (f *fixture) submitted(override *int) int64 {
	f.t.Helper()
	c, err := f.chores.Create(f.t.Context(), chores.ChoreInput{Name: "Dishes", Points: 5, RequiresApproval: true, AutoApproveHours: override})
	if err != nil {
		f.t.Fatal(err)
	}
	now := db.Timestamp(f.clk.Now())
	id, err := f.db.InsertChoreInstance(f.t.Context(), sqlc.InsertChoreInstanceParams{
		ChoreID: c.ID, KidID: sql.NullInt64{Int64: f.sam, Valid: true}, OccurrenceDate: today,
		DueAt: today + "T23:00:00.000Z", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		f.t.Fatal(err)
	}
	f.complete(id)
	return id
}

func (f *fixture) complete(id int64) {
	f.t.Helper()
	if _, err := f.inst.Complete(f.t.Context(), id, instances.Completion{By: f.sam}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) status(id int64) string {
	f.t.Helper()
	i, err := f.inst.Get(f.t.Context(), id)
	if err != nil {
		f.t.Fatal(err)
	}
	return i.Status
}

// run advances the clock by d, runs the job and returns how many it approved.
func (f *fixture) run(d time.Duration) int {
	f.t.Helper()
	f.clk.Advance(d)
	n, err := f.job.Run(f.t.Context())
	if err != nil {
		f.t.Fatal(err)
	}
	return n
}

func ptr(n int) *int { return &n }

func TestHouseholdDefaultTimeout(t *testing.T) {
	f := newFixture(t)
	f.household(2)
	id := f.submitted(nil)

	if n := f.run(time.Hour); n != 0 || f.status(id) != horizon.StatusSubmitted {
		t.Fatalf("1h old: approved %d, status %s", n, f.status(id))
	}
	if n := f.run(2 * time.Hour); n != 1 || f.status(id) != horizon.StatusApproved {
		t.Fatalf("3h old: approved %d, status %s", n, f.status(id))
	}
}

func TestChoreOverrideBeatsTheHouseholdDefault(t *testing.T) {
	f := newFixture(t)
	f.household(4)
	shorter := f.submitted(ptr(1))
	longer := f.submitted(ptr(6))
	dflt := f.submitted(nil)

	if n := f.run(2 * time.Hour); n != 1 || f.status(shorter) != horizon.StatusApproved {
		t.Fatalf("at 2h: approved %d, shorter %s", n, f.status(shorter))
	}
	if f.status(longer) != horizon.StatusSubmitted || f.status(dflt) != horizon.StatusSubmitted {
		t.Errorf("at 2h: longer %s, default %s, want both still submitted", f.status(longer), f.status(dflt))
	}
	f.run(2 * time.Hour) // 4h
	if f.status(dflt) != horizon.StatusApproved || f.status(longer) != horizon.StatusSubmitted {
		t.Errorf("at 4h: default %s, longer %s", f.status(dflt), f.status(longer))
	}
	f.run(2 * time.Hour) // 6h
	if f.status(longer) != horizon.StatusApproved {
		t.Errorf("at 6h: longer %s", f.status(longer))
	}
}

func TestNeverIsHonouredAtBothLevels(t *testing.T) {
	f := newFixture(t)
	f.household(1)
	choreNever := f.submitted(ptr(0)) // chore never beats a household timeout
	dflt := f.submitted(nil)
	if n := f.run(1000 * time.Hour); n != 1 || f.status(choreNever) != horizon.StatusSubmitted || f.status(dflt) != horizon.StatusApproved {
		t.Errorf("chore never: approved %d, never %s, default %s", n, f.status(choreNever), f.status(dflt))
	}

	g := newFixture(t)
	g.household(0) // household never; a chore override still applies
	hhNever := g.submitted(nil)
	timed := g.submitted(ptr(1))
	if n := g.run(1000 * time.Hour); n != 1 || g.status(hhNever) != horizon.StatusSubmitted || g.status(timed) != horizon.StatusApproved {
		t.Errorf("household never: approved %d, default %s, timed %s", n, g.status(hhNever), g.status(timed))
	}
}

func TestResubmissionRestartsTheClock(t *testing.T) {
	f := newFixture(t)
	f.household(2)
	id := f.submitted(nil)

	f.clk.Advance(time.Hour)
	if _, err := f.inst.Reject(t.Context(), id, f.mom, instances.Reason{Code: instances.ReasonNotDone}); err != nil {
		t.Fatal(err)
	}
	f.complete(id) // resubmitted 1h after the first submission
	if n := f.run(90 * time.Minute); n != 0 || f.status(id) != horizon.StatusSubmitted {
		t.Fatalf("2.5h after the first, 1.5h after the second: approved %d, status %s", n, f.status(id))
	}
	if n := f.run(time.Hour); n != 1 || f.status(id) != horizon.StatusApproved {
		t.Fatalf("2.5h after the second: approved %d, status %s", n, f.status(id))
	}
}

func TestApprovalPostsOneSystemCredit(t *testing.T) {
	f := newFixture(t)
	f.household(1)
	id := f.submitted(nil)

	if n := f.run(2 * time.Hour); n != 1 {
		t.Fatalf("approved %d", n)
	}
	if n := f.run(time.Hour); n != 0 {
		t.Errorf("a re-run approved %d", n)
	}
	rows, err := f.db.SQL.QueryContext(t.Context(),
		"SELECT kind, kid_id, amount, note, created_by FROM points_entries WHERE source_kind = 'chore_instance' AND source_id = ?", id)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var n int
	for rows.Next() {
		var kind, note string
		var kid, amount int64
		var by sql.NullInt64
		if err := rows.Scan(&kind, &kid, &amount, &note, &by); err != nil {
			t.Fatal(err)
		}
		n++
		if kind != "chore" || kid != f.sam || amount != 5 || note != autoapprove.Note || by.Valid {
			t.Errorf("entry = %s kid %d %d %q by %v, want a 5-point chore credit for Sam, noted, by the system", kind, kid, amount, note, by)
		}
	}
	if n != 1 {
		t.Errorf("%d entries, want 1", n)
	}
	var events int
	if err := f.db.SQL.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM events WHERE type = 'instance.approved'").Scan(&events); err != nil || events != 1 {
		t.Errorf("instance.approved events = %d, %v", events, err)
	}
}

func TestSkipsInstancesApprovedByAParentMeanwhile(t *testing.T) {
	f := newFixture(t)
	f.household(1)
	id := f.submitted(nil)
	f.clk.Advance(2 * time.Hour)
	if _, err := f.inst.Approve(t.Context(), id, f.mom); err != nil {
		t.Fatal(err)
	}
	if n, err := f.job.Run(t.Context()); err != nil || n != 0 {
		t.Errorf("run = %d, %v; want a no-op", n, err)
	}
	var credits int
	if err := f.db.SQL.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM points_entries WHERE kind = 'chore'").Scan(&credits); err != nil || credits != 1 {
		t.Errorf("credits = %d, %v", credits, err)
	}
}

func TestCatchesUpAfterDowntime(t *testing.T) {
	f := newFixture(t)
	f.household(2)
	a := f.submitted(nil)
	f.clk.Advance(time.Hour)
	b := f.submitted(nil)

	// The server was down for a week: both are long overdue and approved in one run.
	if n := f.run(7 * 24 * time.Hour); n != 2 || f.status(a) != horizon.StatusApproved || f.status(b) != horizon.StatusApproved {
		t.Errorf("approved %d, a %s, b %s", n, f.status(a), f.status(b))
	}
}

func TestSettingChangeAppliesOnTheNextRun(t *testing.T) {
	f := newFixture(t)
	f.household(0)
	id := f.submitted(nil)
	if n := f.run(3 * time.Hour); n != 0 {
		t.Fatalf("approved %d under never", n)
	}
	f.household(2)
	if n := f.run(0); n != 1 || f.status(id) != horizon.StatusApproved {
		t.Errorf("after enabling a 2h default: approved %d, status %s", n, f.status(id))
	}
}
