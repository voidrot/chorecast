package instances_test

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/voidrot/chorecast/server/internal/apierr"
	"github.com/voidrot/chorecast/server/internal/assign"
	"github.com/voidrot/chorecast/server/internal/caltime"
	"github.com/voidrot/chorecast/server/internal/chores"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/dayclose"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
	"github.com/voidrot/chorecast/server/internal/events"
	"github.com/voidrot/chorecast/server/internal/horizon"
	"github.com/voidrot/chorecast/server/internal/instances"
	"github.com/voidrot/chorecast/server/internal/jobs"
	"github.com/voidrot/chorecast/server/internal/kids"
	"github.com/voidrot/chorecast/server/internal/ledger"
	"github.com/voidrot/chorecast/server/internal/schedule"
)

// Saturday 31 October 2026, 13:00 in Chicago: today is the 31st.
var saturday = time.Date(2026, 10, 31, 18, 0, 0, 0, time.UTC)

const today = "2026-10-31"

type fixture struct {
	t             *testing.T
	db            *db.DB
	clk           *clock.Fake
	bus           *events.Bus
	svc           *instances.Service
	chores        *chores.Service
	kids          *kids.Service
	sam, ada, kim int64
	mom           int64
}

// newFixture is a household in America/Chicago whose day ends at 21:00, with negative balances
// on, three kids and a parent.
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
	f := &fixture{t: t, db: d, clk: clk, bus: bus, svc: instances.New(d, bus, clk),
		chores: chores.New(d, clk, nil), kids: kids.NewService(d, clk, nil)}
	f.exec("UPDATE household SET allow_negative_balance = 1")
	for i, k := range []*int64{&f.sam, &f.ada, &f.kim} {
		kid, err := f.kids.Create(t.Context(), kids.KidInput{DisplayName: []string{"Sam", "Ada", "Kim"}[i], Color: "#2563eb"})
		if err != nil {
			t.Fatal(err)
		}
		*k = kid.ID
	}
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

func (f *fixture) chore(in chores.ChoreInput) sqlc.Chore {
	f.t.Helper()
	c, err := f.chores.Create(f.t.Context(), in)
	if err != nil {
		f.t.Fatal(err)
	}
	row, err := f.db.GetChore(f.t.Context(), c.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	return row
}

// instance inserts an open instance of chore on date: kid's, or shared by members when kid is 0.
func (f *fixture) instance(chore int64, date string, kid int64, members ...int64) int64 {
	f.t.Helper()
	now := db.Timestamp(f.clk.Now())
	id, err := f.db.InsertChoreInstance(f.t.Context(), sqlc.InsertChoreInstanceParams{
		ChoreID: chore, KidID: sql.NullInt64{Int64: kid, Valid: kid != 0}, OccurrenceDate: date,
		DueAt: date + "T23:00:00.000Z", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		f.t.Fatal(err)
	}
	for _, m := range members {
		if err := f.db.AddChoreInstanceMember(f.t.Context(), sqlc.AddChoreInstanceMemberParams{InstanceID: id, KidID: m}); err != nil {
			f.t.Fatal(err)
		}
	}
	return id
}

func (f *fixture) tx(fn func(tx *events.Tx) error) error {
	return f.bus.InTx(f.t.Context(), fn)
}

// miss closes the instance's day, as day close does.
func (f *fixture) miss(id int64) {
	f.t.Helper()
	if err := f.tx(func(tx *events.Tx) error {
		_, err := dayclose.MissInTx(f.t.Context(), tx, f.clk, id)
		return err
	}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) get(id int64) horizon.ChoreInstance {
	f.t.Helper()
	i, err := f.svc.Get(f.t.Context(), id)
	if err != nil {
		f.t.Fatal(err)
	}
	return i
}

func (f *fixture) balance(kid int64) int64 {
	f.t.Helper()
	b, err := ledger.Balance(f.t.Context(), f.db.Queries, kid)
	if err != nil {
		f.t.Fatal(err)
	}
	return b
}

// entries lists the instance's ledger entries as "kind kid amount".
func (f *fixture) entries(id int64) []string {
	f.t.Helper()
	rows, err := f.db.SQL.QueryContext(f.t.Context(),
		"SELECT kind, kid_id, amount, local_date FROM points_entries WHERE source_kind = 'chore_instance' AND source_id = ? ORDER BY id", id)
	if err != nil {
		f.t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var kind, date string
		var kid, amount int64
		if err := rows.Scan(&kind, &kid, &amount, &date); err != nil {
			f.t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%s %d %d %s", kind, kid, amount, date))
	}
	return out
}

func (f *fixture) eventTypes() []string {
	f.t.Helper()
	rows, err := f.db.SQL.QueryContext(f.t.Context(), "SELECT type FROM events WHERE type LIKE 'instance.%' ORDER BY id")
	if err != nil {
		f.t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			f.t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func wantErr(t *testing.T, what string, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Errorf("%s: err = %v, want %v", what, err, want)
	}
}

func wantValidation(t *testing.T, what string, err error) {
	t.Helper()
	var ae *apierr.Problem
	if !errors.As(err, &ae) || ae.Status != 422 || ae.Code != apierr.CodeValidationFailed {
		t.Errorf("%s: err = %v, want 422 validation_failed", what, err)
	}
}

// op is one transition, as the API's parent or kid would call it.
type op struct {
	name string
	run  func(f *fixture, id int64) error
}

func ops() []op {
	return []op{
		{"complete", func(f *fixture, id int64) error {
			_, err := f.svc.Complete(f.t.Context(), id, instances.Completion{By: f.sam})
			return err
		}},
		{"approve", func(f *fixture, id int64) error { _, err := f.svc.Approve(f.t.Context(), id, f.mom); return err }},
		{"reject", func(f *fixture, id int64) error {
			_, err := f.svc.Reject(f.t.Context(), id, f.mom, instances.Reason{Code: instances.ReasonNotDone})
			return err
		}},
		{"unapprove", func(f *fixture, id int64) error { _, err := f.svc.Unapprove(f.t.Context(), id, f.mom, ""); return err }},
		{"excuse", func(f *fixture, id int64) error { _, err := f.svc.Excuse(f.t.Context(), id, f.mom, ""); return err }},
		{"reassign", func(f *fixture, id int64) error {
			_, err := f.svc.Reassign(f.t.Context(), id, f.ada, f.mom)
			return err
		}},
	}
}

func TestInvalidTransitionsAreRejected(t *testing.T) {
	// For each status, the one transition allowed from it (if any); the rest get their guard's error.
	allowed := map[string][]string{
		horizon.StatusOpen:      {"complete", "reassign"},
		horizon.StatusSubmitted: {"approve", "reject"},
		horizon.StatusApproved:  {"unapprove"},
		horizon.StatusMissed:    {"excuse"},
		horizon.StatusExcused:   nil,
	}
	guard := map[string]error{
		"complete": instances.ErrNotOpen, "approve": instances.ErrNotSubmitted, "reject": instances.ErrNotSubmitted,
		"unapprove": instances.ErrNotApproved, "excuse": instances.ErrNotMissed, "reassign": instances.ErrNotOpen,
	}
	for status, ok := range allowed {
		for _, o := range ops() {
			t.Run(status+"/"+o.name, func(t *testing.T) {
				f := newFixture(t)
				c := f.chore(chores.ChoreInput{Name: "Dishes", Points: 5, Penalty: 2, RequiresApproval: true})
				id := f.instance(c.ID, today, f.sam)
				f.exec("UPDATE chore_instances SET status = ?, completed_by = ?, completed_at = ? WHERE id = ?",
					status, f.sam, db.Timestamp(f.clk.Now()), id)
				err := o.run(f, id)
				if slices.Contains(ok, o.name) {
					if err != nil {
						t.Fatalf("allowed transition failed: %v", err)
					}
					return
				}
				wantErr(t, o.name, err, guard[o.name])
				if got := f.get(id).Status; got != status {
					t.Errorf("status = %s after a refused %s, want %s", got, o.name, status)
				}
				if got := f.eventTypes(); len(got) != 0 {
					t.Errorf("a refused transition emitted %v", got)
				}
				if got := f.entries(id); len(got) != 0 {
					t.Errorf("a refused transition wrote %v", got)
				}
			})
		}
	}
}

func TestTransitionInputsAreValidated(t *testing.T) {
	f := newFixture(t)
	c := f.chore(chores.ChoreInput{Name: "Dishes", Points: 5, RequiresApproval: true})
	ctx := t.Context()

	for _, o := range ops() {
		wantErr(t, o.name+" unknown instance", o.run(f, 999), instances.ErrNotFound)
	}

	planned := f.instance(c.ID, "2026-11-01", f.sam)
	_, err := f.svc.Complete(ctx, planned, instances.Completion{By: f.sam})
	wantErr(t, "complete tomorrow's instance", err, instances.ErrPlanned)

	single := f.instance(c.ID, today, f.sam)
	_, err = f.svc.Complete(ctx, single, instances.Completion{KidID: f.ada, By: f.mom})
	wantValidation(t, "complete as a kid who isn't the assignee", err)

	shared := f.instance(c.ID, today, 0, f.sam, f.ada)
	_, err = f.svc.Complete(ctx, shared, instances.Completion{By: f.mom})
	wantValidation(t, "complete a shared instance without naming the kid", err)
	_, err = f.svc.Complete(ctx, shared, instances.Completion{KidID: f.kim, By: f.mom})
	wantValidation(t, "complete a shared instance as a non-member", err)
	_, err = f.svc.Reassign(ctx, shared, f.kim, f.mom)
	wantErr(t, "reassign a shared instance", err, instances.ErrShared)

	_, err = f.svc.Reassign(ctx, single, f.sam, f.mom)
	wantValidation(t, "reassign to the same kid", err)
	_, err = f.svc.Reassign(ctx, single, f.mom, f.mom)
	wantValidation(t, "reassign to a parent", err)
	if _, err := f.kids.Archive(ctx, fmt.Sprint(f.kim)); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.Reassign(ctx, single, f.kim, f.mom)
	wantValidation(t, "reassign to an archived kid", err)
	f.instance(c.ID, today, f.ada)
	_, err = f.svc.Reassign(ctx, single, f.ada, f.mom)
	wantErr(t, "reassign to a kid who has the same occurrence", err, instances.ErrTaken)

	if _, err := f.svc.Complete(ctx, single, instances.Completion{By: f.sam}); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.Reject(ctx, single, f.mom, instances.Reason{})
	wantValidation(t, "reject without a reason", err)
	_, err = f.svc.Reject(ctx, single, f.mom, instances.Reason{Code: "lazy"})
	wantValidation(t, "reject with an unknown reason", err)
	_, err = f.svc.Reject(ctx, single, f.mom, instances.Reason{Text: "   "})
	wantValidation(t, "reject with blank text", err)
	if got := f.get(single); got.Status != horizon.StatusSubmitted || got.RejectionCount != 0 {
		t.Errorf("after refused rejections: %+v", got)
	}
}

func TestCompleteThenApproveCreditsTheInstanceDay(t *testing.T) {
	f := newFixture(t)
	c := f.chore(chores.ChoreInput{Name: "Dishes", Points: 5, RequiresApproval: true})
	id := f.instance(c.ID, "2026-10-30", f.sam) // yesterday's, still open (day close hasn't run)

	got, err := f.svc.Complete(t.Context(), id, instances.Completion{By: f.sam})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != horizon.StatusSubmitted || got.CompletedBy != f.sam || !got.CompletedAt.Equal(saturday) {
		t.Errorf("completed: %+v", got)
	}
	if e := f.entries(id); len(e) != 0 {
		t.Errorf("a submission credited %v before approval", e)
	}
	f.clk.Advance(48 * time.Hour)
	if got, err = f.svc.Approve(t.Context(), id, f.mom); err != nil || got.Status != horizon.StatusApproved {
		t.Fatalf("approve: %+v, %v", got, err)
	}
	if e := f.entries(id); !slices.Equal(e, []string{fmt.Sprintf("chore %d 5 2026-10-30", f.sam)}) {
		t.Errorf("entries = %v, want the credit dated to the instance's day", e)
	}
	if got := f.eventTypes(); !slices.Equal(got, []string{"instance.completed", "instance.approved"}) {
		t.Errorf("events = %v", got)
	}
}

func TestCompleteWithoutApprovalCreditsAtOnce(t *testing.T) {
	f := newFixture(t)
	c := f.chore(chores.ChoreInput{Name: "Feed the cat", Points: 3})
	id := f.instance(c.ID, today, f.sam)
	got, err := f.svc.Complete(t.Context(), id, instances.Completion{By: f.mom}) // a parent, for the assignee
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != horizon.StatusApproved || got.CompletedBy != f.sam || f.balance(f.sam) != 3 {
		t.Errorf("got %+v, balance %d; want approved with 3 points", got, f.balance(f.sam))
	}
	if got := f.eventTypes(); !slices.Equal(got, []string{"instance.completed"}) {
		t.Errorf("events = %v, want one completion", got)
	}
	_, err = f.svc.Approve(t.Context(), id, f.mom)
	wantErr(t, "approve an auto-credited instance", err, instances.ErrNotSubmitted)
	if _, err := f.svc.Unapprove(t.Context(), id, f.mom, "Not really done"); err != nil {
		t.Fatal(err)
	}
	if f.balance(f.sam) != 0 {
		t.Errorf("balance after reversal = %d", f.balance(f.sam))
	}
}

func TestReapprovalRestoresTheCreditWithoutPostingAgain(t *testing.T) {
	f := newFixture(t)
	c := f.chore(chores.ChoreInput{Name: "Dishes", Points: 5, RequiresApproval: true})
	id := f.instance(c.ID, today, f.sam)
	ctx := t.Context()
	for range 2 {
		if _, err := f.svc.Complete(ctx, id, instances.Completion{By: f.sam}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.svc.Approve(ctx, id, f.mom); err != nil {
			t.Fatal(err)
		}
		if f.balance(f.sam) != 5 {
			t.Fatalf("balance after approval = %d", f.balance(f.sam))
		}
		if _, err := f.svc.Unapprove(ctx, id, f.mom, ""); err != nil {
			t.Fatal(err)
		}
		if f.balance(f.sam) != 0 {
			t.Fatalf("balance after reversal = %d", f.balance(f.sam))
		}
	}
	want := []string{"chore", "reversal", "reversal", "reversal"}
	var kinds []string
	for _, e := range f.entries(id) {
		var k string
		_, _ = fmt.Sscan(e, &k)
		kinds = append(kinds, k)
	}
	if !slices.Equal(kinds, want) {
		t.Errorf("entries = %v, want one credit and its reversal chain", f.entries(id))
	}
	// The completion stays recorded on the reopened instance, so the horizon treats it as touched.
	if got := f.get(id); got.Status != horizon.StatusOpen || got.CompletedBy != f.sam {
		t.Errorf("reopened: %+v", got)
	}
}

func TestRejectKeepsHistory(t *testing.T) {
	f := newFixture(t)
	c := f.chore(chores.ChoreInput{Name: "Dishes", Points: 5, RequiresApproval: true})
	id := f.instance(c.ID, today, f.sam)
	ctx := t.Context()
	reasons := []instances.Reason{{Code: instances.ReasonNotFinished}, {Code: instances.ReasonNeedsRedo, Text: "  Pots too  "}}
	for _, r := range reasons {
		if _, err := f.svc.Complete(ctx, id, instances.Completion{By: f.sam}); err != nil {
			t.Fatal(err)
		}
		f.clk.Advance(time.Minute)
		if _, err := f.svc.Reject(ctx, id, f.mom, r); err != nil {
			t.Fatal(err)
		}
	}
	got := f.get(id)
	if got.Status != horizon.StatusOpen || got.RejectionCount != 2 || got.LastRejection == nil {
		t.Fatalf("after two rejections: %+v", got)
	}
	want := horizon.ChoreInstanceRejection{Reason: instances.ReasonNeedsRedo, Text: "Pots too", KidID: f.sam, RejectedBy: f.mom,
		RejectedAt: saturday.Add(2 * time.Minute)}
	if *got.LastRejection != want {
		t.Errorf("last rejection = %+v, want %+v", *got.LastRejection, want)
	}
	var n int
	if err := f.db.SQL.QueryRowContext(ctx, "SELECT COUNT(*) FROM chore_instance_rejections WHERE instance_id = ?", id).Scan(&n); err != nil || n != 2 {
		t.Errorf("rejection rows = %d (%v), want 2", n, err)
	}
	if e := f.entries(id); len(e) != 0 {
		t.Errorf("rejections wrote %v", e)
	}
	listed, err := horizon.New(f.db, f.clk).List(ctx, horizon.ListFilter{From: date(today), To: date(today)})
	if err != nil || len(listed) != 1 || listed[0].LastRejection == nil || *listed[0].LastRejection != want {
		t.Errorf("list: %+v, %v", listed, err)
	}
}

func TestSharedCompletion(t *testing.T) {
	for _, tc := range []struct {
		points, penalty string
		credit          map[string]int64 // by kid name
		penalized       bool
	}{
		{assign.PointsCompleterTakesAll, assign.PenaltyAll, map[string]int64{"ada": 10}, true},
		{assign.PointsSplitEvenly, assign.PenaltyNone, map[string]int64{"ada": 4, "sam": 3, "kim": 3}, false},
	} {
		t.Run(tc.points, func(t *testing.T) {
			f := newFixture(t)
			names := map[string]int64{"sam": f.sam, "ada": f.ada, "kim": f.kim}
			c := f.chore(chores.ChoreInput{Name: "Tidy up", Points: 10, Penalty: 2, RequiresApproval: true,
				Assignment: &chores.ChoreAssignment{Mode: assign.ModeShared, KidIDs: []int64{f.sam, f.ada, f.kim},
					SharedPoints: tc.points, SharedPenalty: tc.penalty}})
			id := f.instance(c.ID, today, 0, f.sam, f.ada, f.kim)
			if _, err := f.svc.Complete(t.Context(), id, instances.Completion{KidID: f.ada, By: f.mom}); err != nil {
				t.Fatal(err)
			}
			if _, err := f.svc.Approve(t.Context(), id, f.mom); err != nil {
				t.Fatal(err)
			}
			for name, kid := range names {
				if got := f.balance(kid); got != tc.credit[name] {
					t.Errorf("%s balance = %d, want %d", name, got, tc.credit[name])
				}
			}
			if _, err := f.svc.Unapprove(t.Context(), id, f.mom, ""); err != nil {
				t.Fatal(err)
			}
			f.miss(id)
			wantPenalty := int64(0)
			if tc.penalized {
				wantPenalty = -2
			}
			for name, kid := range names {
				if got := f.balance(kid); got != wantPenalty {
					t.Errorf("%s balance after the miss = %d, want %d", name, got, wantPenalty)
				}
			}
			if got, err := f.svc.Excuse(t.Context(), id, f.mom, ""); err != nil || got.Status != horizon.StatusExcused {
				t.Fatalf("excuse: %+v, %v", got, err)
			}
			for name, kid := range names {
				if got := f.balance(kid); got != 0 {
					t.Errorf("%s balance after the excuse = %d, want 0", name, got)
				}
			}
		})
	}
}

func TestEventsCarryTheTransition(t *testing.T) {
	f := newFixture(t)
	c := f.chore(chores.ChoreInput{Name: "Dishes", Points: 5, RequiresApproval: true})
	id := f.instance(c.ID, today, f.sam)
	if _, err := f.svc.Reassign(t.Context(), id, f.ada, f.mom); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Complete(t.Context(), id, instances.Completion{By: f.ada}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Reject(t.Context(), id, f.mom, instances.Reason{Text: "Again"}); err != nil {
		t.Fatal(err)
	}
	rows, err := f.db.SQL.QueryContext(t.Context(), "SELECT type, payload FROM events WHERE type LIKE 'instance.%' ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var got []string
	for rows.Next() {
		var typ, payload string
		if err := rows.Scan(&typ, &payload); err != nil {
			t.Fatal(err)
		}
		got = append(got, typ+" "+payload)
	}
	want := []string{
		fmt.Sprintf(`instance.reassigned {"instanceId":%d,"kidIds":[%d],"from":"open","to":"open","by":%d,"fromKidId":%d}`, id, f.ada, f.mom, f.sam),
		fmt.Sprintf(`instance.completed {"instanceId":%d,"kidIds":[%d],"from":"open","to":"submitted","by":%d,"completedBy":%d}`, id, f.ada, f.ada, f.ada),
		fmt.Sprintf(`instance.rejected {"instanceId":%d,"kidIds":[%d],"from":"submitted","to":"open","by":%d}`, id, f.ada, f.mom),
	}
	if !slices.Equal(got, want) {
		t.Errorf("events:\n got %q\nwant %q", got, want)
	}
}

// instanceDue inserts an open instance of chore due at the given instant.
func (f *fixture) instanceDue(chore int64, date string, due time.Time, kid int64, members ...int64) int64 {
	f.t.Helper()
	now := db.Timestamp(f.clk.Now())
	id, err := f.db.InsertChoreInstance(f.t.Context(), sqlc.InsertChoreInstanceParams{
		ChoreID: chore, KidID: sql.NullInt64{Int64: kid, Valid: kid != 0}, OccurrenceDate: date,
		Slot: due.Format("15:04"), DueAt: db.Timestamp(due), CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		f.t.Fatal(err)
	}
	for _, m := range members {
		if err := f.db.AddChoreInstanceMember(f.t.Context(), sqlc.AddChoreInstanceMemberParams{InstanceID: id, KidID: m}); err != nil {
			f.t.Fatal(err)
		}
	}
	return id
}

// TestReopeningAfterTheDayClosedMisses: day close takes every open instance due by the close, so
// a rejection or reversal that reopens an instance whose close has passed hands it to day close
// in the same transaction: it's missed, with its penalty, instead of open.
func TestReopeningAfterTheDayClosedMisses(t *testing.T) {
	local := func(day, hhmm string) time.Time {
		at, err := time.ParseInLocation("2006-01-02 15:04", day+" "+hhmm, time.FixedZone("CDT", -5*3600))
		if err != nil {
			t.Fatal(err)
		}
		return at
	}
	for _, reopen := range []string{"reject", "unapprove"} {
		t.Run(reopen, func(t *testing.T) {
			f := newFixture(t) // now: Saturday 13:00 in Chicago; Ada's day ends at 12:00
			f.exec("UPDATE users SET day_end_override = '12:00' WHERE id = ?", f.ada)
			c := f.chore(chores.ChoreInput{Name: "Dishes", Points: 5, Penalty: 2, RequiresApproval: true})
			for _, tc := range []struct {
				what      string
				id        int64
				completer int64
				missed    bool
			}{
				{"yesterday's, due at Sam's day close", f.instanceDue(c.ID, "2026-10-30", local("2026-10-30", "21:00"), f.sam), f.sam, true},
				{"today's 10:00 slot: Sam's day is still open", f.instanceDue(c.ID, today, local(today, "10:00"), f.sam), f.sam, false},
				{"today's 11:00 slot: Ada's day closed at 12:00", f.instanceDue(c.ID, today, local(today, "11:00"), f.ada), f.ada, true},
				{"a slot after Ada's day end waits for tomorrow's close", f.instanceDue(c.ID, today, local(today, "12:30"), f.ada), f.ada, false},
				{"shared, today's 11:00 slot: shared chores close at the household day end",
					f.instanceDue(c.ID, today, local(today, "11:00"), 0, f.sam, f.ada), f.ada, false},
			} {
				if _, err := f.svc.Complete(t.Context(), tc.id, instances.Completion{KidID: tc.completer, By: f.mom}); err != nil {
					t.Fatalf("%s: complete: %v", tc.what, err)
				}
				var got horizon.ChoreInstance
				var err error
				if reopen == "reject" {
					got, err = f.svc.Reject(t.Context(), tc.id, f.mom, instances.Reason{Code: instances.ReasonNotDone})
				} else {
					if _, err := f.svc.Approve(t.Context(), tc.id, f.mom); err != nil {
						t.Fatal(err)
					}
					got, err = f.svc.Unapprove(t.Context(), tc.id, f.mom, "")
				}
				want, penalty := horizon.StatusOpen, 0
				if tc.missed {
					want, penalty = horizon.StatusMissed, 1
				}
				if err != nil || got.Status != want {
					t.Errorf("%s: status %s (%v), want %s", tc.what, got.Status, err, want)
				}
				penalties := 0
				for _, e := range f.entries(tc.id) {
					if strings.HasPrefix(e, ledger.KindChorePenalty+" ") {
						penalties++
					}
				}
				if penalties != penalty {
					t.Errorf("%s: %d penalties, want %d: %v", tc.what, penalties, penalty, f.entries(tc.id))
				}
			}
			if a, b := f.balance(f.sam), f.balance(f.ada); a != -2 || b != -2 {
				t.Errorf("balances %d and %d, want each kid's one missed penalty (-2)", a, b)
			}
			// Each late reopen is its transition's event, then day close's, in one transaction.
			transition := map[string]string{"reject": "instance.rejected", "unapprove": "instance.unapproved"}[reopen]
			events, pairs := f.eventTypes(), 0
			for i := 1; i < len(events); i++ {
				if events[i] == "instance.missed" && events[i-1] == transition {
					pairs++
				}
			}
			if pairs != 2 {
				t.Errorf("%d %s → instance.missed pairs in %v, want 2", pairs, transition, events)
			}
		})
	}
}

// TestHorizonKeepsTouchedInstances: a rejected (or reversed) completion and a reassignment leave
// an open instance that regeneration must keep, with its id and history.
func TestHorizonKeepsTouchedInstances(t *testing.T) {
	f := newFixture(t)
	gen := horizon.New(f.db, f.clk)
	c, err := f.chores.Create(t.Context(), chores.ChoreInput{Name: "Dishes", Points: 5, RequiresApproval: true,
		Schedule:   &schedule.ScheduleRule{Type: schedule.TypeDaily, StartDate: date(today)},
		Assignment: &chores.ChoreAssignment{Mode: assign.ModeFixed, KidIDs: []int64{f.sam}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gen.Generate(t.Context()); err != nil {
		t.Fatal(err)
	}
	list := func() map[int64]horizon.ChoreInstance {
		is, err := gen.List(t.Context(), horizon.ListFilter{From: date(today), To: date("2026-11-07")})
		if err != nil {
			t.Fatal(err)
		}
		out := map[int64]horizon.ChoreInstance{}
		for _, i := range is {
			out[i.ID] = i
		}
		return out
	}
	byDate := map[string]int64{}
	for id, i := range list() {
		byDate[i.Date.String()] = id
	}
	rejected, reassigned, untouched := byDate[today], byDate["2026-11-02"], byDate["2026-11-01"]
	if _, err := f.svc.Complete(t.Context(), rejected, instances.Completion{By: f.sam}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Reject(t.Context(), rejected, f.mom, instances.Reason{Code: instances.ReasonNotDone}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Reassign(t.Context(), reassigned, f.ada, f.mom); err != nil {
		t.Fatal(err)
	}
	// The chore moves to Kim: Sam's untouched instances are replaced by Kim's.
	c.Assignment = &chores.ChoreAssignment{Mode: assign.ModeFixed, KidIDs: []int64{f.kim}}
	if _, err := f.chores.Update(t.Context(), c.ID, c.ChoreInput); err != nil {
		t.Fatal(err)
	}
	if _, err := gen.Generate(t.Context()); err != nil {
		t.Fatal(err)
	}
	after := list()
	if got := after[rejected]; got.KidID != f.sam || got.RejectionCount != 1 || got.CompletedBy != f.sam {
		t.Errorf("rejected instance after regeneration: %+v", got)
	}
	if got := after[reassigned]; got.KidID != f.ada || !got.ManuallyReassigned {
		t.Errorf("reassigned instance after regeneration: %+v", got)
	}
	if got, ok := after[untouched]; ok && got.KidID != f.kim {
		t.Errorf("an untouched instance wasn't regenerated: %+v", got)
	}
}

// TestReassignDoesNotDuplicateOnRegeneration: a reassigned instance stands for the occurrence of
// the kid it was planned for, so regenerating (any chore or kid edit) doesn't plan that kid's
// again, in any mode and across a second reassignment.
func TestReassignDoesNotDuplicateOnRegeneration(t *testing.T) {
	for _, tc := range []struct {
		mode    string
		kids    func(f *fixture) []int64
		perDate int
	}{
		{assign.ModeFixed, func(f *fixture) []int64 { return []int64{f.sam} }, 1},
		{assign.ModeEveryone, nil, 2}, // Kim is excluded, so there's a kid to hand Sam's to
		{assign.ModeRotation, func(f *fixture) []int64 { return []int64{f.sam, f.ada, f.kim} }, 1},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			f := newFixture(t)
			gen := horizon.New(f.db, f.clk)
			in := chores.ChoreInput{Name: "Dishes", Points: 5,
				Schedule:   &schedule.ScheduleRule{Type: schedule.TypeDaily, StartDate: date(today)},
				Assignment: &chores.ChoreAssignment{Mode: tc.mode}}
			if tc.kids != nil {
				in.Assignment.KidIDs = tc.kids(f)
			} else {
				in.Eligibility = []chores.ChoreEligibility{{KidID: f.kim, Allow: false}}
			}
			c, err := f.chores.Create(t.Context(), in)
			if err != nil {
				t.Fatal(err)
			}
			byDate := func() map[string][]horizon.ChoreInstance {
				if _, err := gen.Generate(t.Context()); err != nil {
					t.Fatal(err)
				}
				is, err := gen.List(t.Context(), horizon.ListFilter{From: date(today), To: date("2026-11-07")})
				if err != nil {
					t.Fatal(err)
				}
				out := map[string][]horizon.ChoreInstance{}
				for _, i := range is {
					if i.ChoreID == c.ID {
						out[i.Date.String()] = append(out[i.Date.String()], i)
					}
				}
				return out
			}
			// reassign moves the day's reassigned instance (at first, its first one) to a kid
			// without one that day.
			reassign := func(day []horizon.ChoreInstance) int64 {
				t.Helper()
				target := day[0]
				for _, i := range day {
					if i.ManuallyReassigned {
						target = i
					}
				}
				for _, k := range []int64{f.sam, f.ada, f.kim} {
					if !slices.ContainsFunc(day, func(i horizon.ChoreInstance) bool { return i.KidID == k }) {
						if _, err := f.svc.Reassign(t.Context(), target.ID, k, f.mom); err != nil {
							t.Fatal(err)
						}
						return k
					}
				}
				t.Fatal("no kid to reassign to")
				return 0
			}
			const day = "2026-11-02"
			for round := range 2 { // a second reassignment still covers the original kid
				to := reassign(byDate()[day])
				got := byDate()
				for d := date(today); !d.After(date("2026-11-07")); d = d.AddDays(1) {
					if n := len(got[d.String()]); n != tc.perDate {
						t.Errorf("round %d: %s has %d instances, want %d: %+v", round, d, n, tc.perDate, got[d.String()])
					}
				}
				if !slices.ContainsFunc(got[day], func(i horizon.ChoreInstance) bool { return i.KidID == to && i.ManuallyReassigned }) {
					t.Errorf("round %d: the reassigned instance is gone: %+v", round, got[day])
				}
			}
		})
	}
}

// TestReassignMovesTheDueTimeToTheNewKid: an instance without a slot is due at its kid's own day
// (or week) close, so a reassignment moves it to the new kid's; a slot keeps its time.
func TestReassignMovesTheDueTimeToTheNewKid(t *testing.T) {
	f := newFixture(t) // the household day (and Sam's) ends at 21:00 in Chicago
	f.exec("UPDATE users SET day_end_override = '19:00' WHERE id = ?", f.ada)
	c := f.chore(chores.ChoreInput{Name: "Dishes", Points: 5})
	ctx := t.Context()
	at := func(s string) time.Time {
		t.Helper()
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}

	daily := f.instance(c.ID, "2026-11-02", f.sam)    // Monday, CST (UTC-6)
	flexible := f.instance(c.ID, "2026-10-25", f.sam) // this week's (it starts on Sunday the 25th)
	f.exec("UPDATE chore_instances SET flexible = 1 WHERE id = ?", flexible)
	slotted := f.instanceDue(c.ID, "2026-11-02", at("2026-11-02T14:00:00Z"), f.sam) // 08:00
	for _, tc := range []struct {
		what string
		id   int64
		kid  int64
		want string
	}{
		{"a day's instance, to Ada (19:00)", daily, f.ada, "2026-11-03T01:00:00Z"},
		{"and back to Sam (21:00)", daily, f.sam, "2026-11-03T03:00:00Z"},
		{"a flexible instance, to Ada: her week close (Saturday 19:00 CDT)", flexible, f.ada, "2026-11-01T00:00:00Z"},
		{"a slot keeps its time", slotted, f.ada, "2026-11-02T14:00:00Z"},
	} {
		got, err := f.svc.Reassign(ctx, tc.id, tc.kid, f.mom)
		if err != nil {
			t.Fatalf("%s: %v", tc.what, err)
		}
		if !got.DueAt.Equal(at(tc.want)) {
			t.Errorf("%s: due %s, want %s", tc.what, got.DueAt.UTC().Format(time.RFC3339), tc.want)
		}
	}
}

// TestLedgerStaysConsistent drives random transitions over single and shared instances and checks
// after every step that each kid's balance is the sum of the effects in force for their
// instances, that a credit is in force exactly while an instance is approved and a penalty
// exactly while it's missed, and that nothing is ever posted twice for a source.
func TestLedgerStaysConsistent(t *testing.T) {
	for seed := range uint64(3) {
		for _, allowNegative := range []bool{true, false} {
			t.Run(fmt.Sprintf("seed %d negative %v", seed, allowNegative), func(t *testing.T) {
				runLedgerModel(t, seed, allowNegative)
			})
		}
	}
}

type modelInstance struct {
	id        int64
	chore     sqlc.Chore
	kid       int64   // 0 = shared
	members   []int64 // shared
	status    string
	completer int64
	credited  map[int64]int64 // each kid's first credit amount: re-approval restores it
	pastDay   bool            // its day has closed: a rejection or reversal makes it missed
	penalty   bool            // missed by day close, with its penalty
}

// reopen is a rejection or reversal: back to open, or missed with its penalty once its day closed.
func (m *modelInstance) reopen() {
	m.status = horizon.StatusOpen
	if m.pastDay {
		m.status, m.penalty = horizon.StatusMissed, true
	}
}

func (m *modelInstance) kids() []int64 {
	if m.kid != 0 {
		return []int64{m.kid}
	}
	return m.members
}

// credit is who has a credit in force while approved, and how much.
func (m *modelInstance) credit() map[int64]int64 {
	out := map[int64]int64{}
	shares := map[int64]int{}
	if m.kid != 0 {
		shares[m.kid] = int(m.chore.Points)
	} else {
		shares = assign.SplitPoints(int(m.chore.Points), m.chore.SharedPoints, m.completer, m.members)
	}
	for k, pts := range shares {
		if pts == 0 {
			continue
		}
		if _, ok := m.credited[k]; !ok {
			m.credited[k] = int64(pts)
		}
		out[k] = m.credited[k]
	}
	return out
}

// penalized is who has a penalty in force while missed.
func (m *modelInstance) penalized() []int64 {
	switch {
	case m.chore.Penalty == 0 || !m.penalty:
		return nil
	case m.kid != 0:
		return []int64{m.kid}
	case m.chore.SharedPenalty == assign.PenaltyAll:
		return m.members
	}
	return nil
}

func runLedgerModel(t *testing.T, seed uint64, allowNegative bool) {
	f := newFixture(t)
	if !allowNegative {
		f.exec("UPDATE household SET allow_negative_balance = 0")
	}
	all := []int64{f.sam, f.ada, f.kim}
	shared := func(points, penalty string) *chores.ChoreAssignment {
		return &chores.ChoreAssignment{Mode: assign.ModeShared, KidIDs: all, SharedPoints: points, SharedPenalty: penalty}
	}
	dishes := f.chore(chores.ChoreInput{Name: "Dishes", Points: 5, Penalty: 3, RequiresApproval: true})
	cat := f.chore(chores.ChoreInput{Name: "Feed the cat", Points: 4, Penalty: 2})
	tidy := f.chore(chores.ChoreInput{Name: "Tidy", Points: 6, Penalty: 2, RequiresApproval: true,
		Assignment: shared(assign.PointsCompleterTakesAll, assign.PenaltyAll)})
	yard := f.chore(chores.ChoreInput{Name: "Yard", Points: 10, Penalty: 1, RequiresApproval: true,
		Assignment: shared(assign.PointsSplitEvenly, assign.PenaltyNone)})
	var model []*modelInstance
	add := func(c sqlc.Chore, date string, kid int64) {
		m := &modelInstance{chore: c, kid: kid, status: horizon.StatusOpen, credited: map[int64]int64{}, pastDay: date != today}
		if kid == 0 {
			m.members = all
		}
		m.id = f.instance(c.ID, date, kid, m.members...)
		model = append(model, m)
	}
	for i, date := range []string{"2026-10-29", "2026-10-30", today} {
		add(dishes, date, all[i])
		add(cat, date, all[(i+1)%3])
		add(tidy, date, 0)
		add(yard, date, 0)
	}

	rng := rand.New(rand.NewPCG(seed, 25)) //nolint:gosec // seeded on purpose: failures must reproduce
	ctx := t.Context()
	for step := range 150 {
		m := model[rng.IntN(len(model))]
		kid := all[rng.IntN(len(all))]
		var err, want error
		var what string
		switch rng.IntN(7) {
		case 0, 1: // complete, by a random kid
			what = fmt.Sprintf("complete by %d", kid)
			_, err = f.svc.Complete(ctx, m.id, instances.Completion{KidID: kid, By: kid})
			switch {
			case m.status != horizon.StatusOpen:
				want = instances.ErrNotOpen
			case !slices.Contains(m.kids(), kid):
				want = errValidation
			default:
				m.completer, m.status = kid, horizon.StatusSubmitted
				if m.chore.RequiresApproval == 0 {
					m.status = horizon.StatusApproved
				}
			}
		case 2:
			what = "approve"
			_, err = f.svc.Approve(ctx, m.id, f.mom)
			if want = instances.ErrNotSubmitted; m.status == horizon.StatusSubmitted {
				want, m.status = nil, horizon.StatusApproved
			}
		case 3:
			what = "reject"
			_, err = f.svc.Reject(ctx, m.id, f.mom, instances.Reason{Code: instances.ReasonNeedsRedo})
			if want = instances.ErrNotSubmitted; m.status == horizon.StatusSubmitted {
				want = nil
				m.reopen()
			}
		case 4:
			what = "unapprove"
			_, err = f.svc.Unapprove(ctx, m.id, f.mom, "")
			if want = instances.ErrNotApproved; m.status == horizon.StatusApproved {
				want = nil
				m.reopen()
			}
		case 5:
			if rng.IntN(2) == 0 {
				what = "miss"
				f.miss(m.id)
				if m.status == horizon.StatusOpen {
					m.status, m.penalty = horizon.StatusMissed, true
				}
			} else {
				what = "excuse"
				_, err = f.svc.Excuse(ctx, m.id, f.mom, "")
				if want = instances.ErrNotMissed; m.status == horizon.StatusMissed {
					want, m.status = nil, horizon.StatusExcused
				}
			}
		case 6:
			what = fmt.Sprintf("reassign to %d", kid)
			_, err = f.svc.Reassign(ctx, m.id, kid, f.mom)
			switch {
			case m.status != horizon.StatusOpen:
				want = instances.ErrNotOpen
			case m.kid == 0:
				want = instances.ErrShared
			case m.kid == kid:
				want = errValidation
			default:
				m.kid = kid
			}
		}
		label := fmt.Sprintf("step %d: %s instance %d", step, what, m.id)
		switch {
		case errors.Is(want, errValidation):
			wantValidation(t, label, err)
		case want != nil:
			wantErr(t, label, err, want)
		case err != nil:
			t.Fatalf("%s: %v", label, err)
		}
		checkLedger(t, f, label, model, allowNegative)
		if t.Failed() {
			t.FailNow()
		}
	}
}

var errValidation = errors.New("validation_failed")

func checkLedger(t *testing.T, f *fixture, label string, model []*modelInstance, allowNegative bool) {
	t.Helper()
	sums := map[int64]int64{}
	err := f.tx(func(tx *events.Tx) error {
		for _, m := range model {
			got, err := tx.Q.GetChoreInstance(t.Context(), m.id)
			if err != nil {
				return err
			}
			if got.ChoreInstance.Status != m.status || got.ChoreInstance.KidID.Int64 != m.kid {
				t.Errorf("%s: instance %d is %s for kid %d, model says %s for %d", label, m.id,
					got.ChoreInstance.Status, got.ChoreInstance.KidID.Int64, m.status, m.kid)
			}
			wantCredit := map[int64]int64{}
			if m.status == horizon.StatusApproved {
				wantCredit = m.credit()
			}
			var wantPenalty []int64
			if m.status == horizon.StatusMissed {
				wantPenalty = m.penalized()
			}
			src := ledger.Source{Kind: ledger.SourceChoreInstance, ID: m.id}
			for _, kid := range []int64{f.sam, f.ada, f.kim} {
				for _, kind := range []string{ledger.KindChore, ledger.KindChorePenalty} {
					tip, inEffect, _, err := ledger.Tip(t.Context(), tx, kid, kind, src)
					if err != nil {
						return err
					}
					if inEffect {
						sums[kid] += tip.Amount // an even reversal chain's tip has the original's amount
					}
					switch kind {
					case ledger.KindChore:
						want, should := wantCredit[kid]
						if inEffect != should || (should && tip.Amount != want) {
							t.Errorf("%s: instance %d kid %d credit in effect %v (%d), want %v (%d)", label, m.id, kid, inEffect, tip.Amount, should, want)
						}
					case ledger.KindChorePenalty:
						should := slices.Contains(wantPenalty, kid)
						if inEffect != should || (should && allowNegative && tip.Amount != -m.chore.Penalty) {
							t.Errorf("%s: instance %d kid %d penalty in effect %v (%d), want %v", label, m.id, kid, inEffect, tip.Amount, should)
						}
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("%s: %v", label, err)
	}
	for _, kid := range []int64{f.sam, f.ada, f.kim} {
		if got := f.balance(kid); got != sums[kid] {
			t.Errorf("%s: kid %d balance %d, but the effects in force sum to %d", label, kid, got, sums[kid])
		}
	}
	var dupes, strays int
	if err := f.db.SQL.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM (SELECT 1 FROM points_entries
		WHERE kind IN ('chore', 'chore_penalty') GROUP BY kind, source_kind, source_id, kid_id HAVING COUNT(*) > 1)`).Scan(&dupes); err != nil {
		t.Fatal(err)
	}
	if err := f.db.SQL.QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM points_entries WHERE kind NOT IN ('chore', 'chore_penalty', 'reversal') OR source_kind IS NOT 'chore_instance'`).Scan(&strays); err != nil {
		t.Fatal(err)
	}
	if dupes != 0 || strays != 0 {
		t.Errorf("%s: %d sources posted twice, %d unexpected entries", label, dupes, strays)
	}
}

func date(s string) caltime.Date {
	d, err := caltime.ParseDate(s)
	if err != nil {
		panic(err)
	}
	return d
}
