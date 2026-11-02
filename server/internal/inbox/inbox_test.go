package inbox_test

import (
	"database/sql"
	"log/slog"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/voidrot/chorecast/server/internal/caltime"
	"github.com/voidrot/chorecast/server/internal/chores"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/dayclose"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
	"github.com/voidrot/chorecast/server/internal/events"
	"github.com/voidrot/chorecast/server/internal/inbox"
	"github.com/voidrot/chorecast/server/internal/instances"
	"github.com/voidrot/chorecast/server/internal/jobs"
	"github.com/voidrot/chorecast/server/internal/kids"
	"github.com/voidrot/chorecast/server/internal/ledger"
	"github.com/voidrot/chorecast/server/internal/paging"
	"github.com/voidrot/chorecast/server/internal/streaks"
)

// Saturday 31 October 2026, 13:00 in Chicago; the household day ends at 21:00.
var saturday = time.Date(2026, 10, 31, 18, 0, 0, 0, time.UTC)

const today = "2026-10-31"

type fixture struct {
	t             *testing.T
	db            *db.DB
	clk           *clock.Fake
	bus           *events.Bus
	inst          *instances.Service
	chores        *chores.Service
	inbox         *inbox.Service
	sam, ada, kim int64
	mom, dad      int64
}

// newFixture is a household with three kids and two parents (and a removed third), the real
// instance transitions and the inbox service. Events stay in the outbox until deliver.
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
	f := &fixture{t: t, db: d, clk: clk, bus: bus, inst: instances.New(d, bus, clk), chores: chores.New(d, clk, nil),
		inbox: inbox.New(d, clk)}
	f.exec("UPDATE household SET allow_negative_balance = 1")
	for i, k := range []*int64{&f.sam, &f.ada, &f.kim} {
		kid, err := kids.NewService(d, clk, nil).Create(t.Context(), kids.KidInput{DisplayName: []string{"Sam", "Ada", "Kim"}[i], Color: "#2563eb"})
		if err != nil {
			t.Fatal(err)
		}
		*k = kid.ID
	}
	for _, p := range []struct {
		id   *int64
		name string
	}{{&f.mom, "Mom"}, {&f.dad, "Dad"}, {new(int64), "Gran"}} {
		u, err := d.CreateUser(t.Context(), sqlc.CreateUserParams{DisplayName: p.name, Role: "parent"})
		if err != nil {
			t.Fatal(err)
		}
		*p.id = u.ID
		if p.name == "Gran" {
			f.exec("UPDATE users SET removed_at = ? WHERE id = ?", db.Timestamp(clk.Now()), u.ID)
		}
	}
	return f
}

func (f *fixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.db.SQL.ExecContext(f.t.Context(), query, args...); err != nil {
		f.t.Fatal(err)
	}
}

// instance inserts an open instance of a new 5-point chore: Sam's, or shared by members when
// kid is 0.
func (f *fixture) instance(approval bool, kid int64, members ...int64) int64 {
	f.t.Helper()
	c, err := f.chores.Create(f.t.Context(), chores.ChoreInput{Name: "Dishes", Points: 5, RequiresApproval: approval})
	if err != nil {
		f.t.Fatal(err)
	}
	now := db.Timestamp(f.clk.Now())
	id, err := f.db.InsertChoreInstance(f.t.Context(), sqlc.InsertChoreInstanceParams{
		ChoreID: c.ID, KidID: sql.NullInt64{Int64: kid, Valid: kid != 0}, OccurrenceDate: today,
		DueAt: today + "T23:00:00.000Z", CreatedAt: now, UpdatedAt: now,
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

func (f *fixture) complete(id, kid, by int64) {
	f.t.Helper()
	if _, err := f.inst.Complete(f.t.Context(), id, instances.Completion{KidID: kid, By: by}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) miss(id int64) {
	f.t.Helper()
	err := f.bus.InTx(f.t.Context(), func(tx *events.Tx) error {
		_, err := dayclose.MissInTx(f.t.Context(), tx, f.clk, id)
		return err
	})
	if err != nil {
		f.t.Fatal(err)
	}
}

// deliver hands every event in the outbox to the inbox, as the bus would; the outbox is never
// marked dispatched, so each call redelivers all of them.
func (f *fixture) deliver() {
	f.t.Helper()
	rows, err := f.db.ListUndispatchedEvents(f.t.Context(), 1000)
	if err != nil {
		f.t.Fatal(err)
	}
	for _, r := range rows {
		occurred, err := db.ParseTimestamp(r.OccurredAt)
		if err != nil {
			f.t.Fatal(err)
		}
		env := events.Envelope{ID: r.ID, Type: r.Type, OccurredAt: occurred, Payload: []byte(r.Payload)}
		if err := f.inbox.Handle(f.t.Context(), env); err != nil {
			f.t.Fatalf("deliver %s: %v", r.Type, err)
		}
	}
}

// got is the recipient's inbox, oldest first, as kind plus the details that vary.
func (f *fixture) got(user int64) []inbox.InboxItem {
	f.t.Helper()
	page, err := f.inbox.List(f.t.Context(), user, paging.Params{Limit: 100})
	if err != nil {
		f.t.Fatal(err)
	}
	slices.Reverse(page.Items)
	return page.Items
}

func kinds(items []inbox.InboxItem) []string {
	out := []string{}
	for _, i := range items {
		out = append(out, i.Kind)
	}
	return out
}

func (f *fixture) wantKinds(who string, user int64, want ...string) []inbox.InboxItem {
	f.t.Helper()
	items := f.got(user)
	if want == nil {
		want = []string{}
	}
	if got := kinds(items); !reflect.DeepEqual(got, want) {
		f.t.Fatalf("%s's inbox = %v, want %v", who, got, want)
	}
	return items
}

// A submission notifies every parent in the household, once, naming the kid and the chore; the
// kid hears back when it is approved or rejected (with the reason), and again after a
// resubmission. Redelivering all the events writes nothing new.
func TestSubmissionAndReview(t *testing.T) {
	f := newFixture(t)
	id := f.instance(true, f.sam)

	f.complete(id, 0, f.sam)
	f.deliver()
	for who, parent := range map[string]int64{"Mom": f.mom, "Dad": f.dad} {
		items := f.wantKinds(who, parent, inbox.KindChoreSubmitted)
		if it := items[0]; it.KidID != f.sam || it.RefKind != inbox.RefInstance || it.RefID != id || it.Data.ChoreName != "Dishes" {
			t.Errorf("%s's notification = %+v", who, it)
		}
	}
	f.wantKinds("Sam", f.sam)

	if _, err := f.inst.Reject(t.Context(), id, f.mom, instances.Reason{Code: instances.ReasonNeedsRedo, Text: "Pans too"}); err != nil {
		t.Fatal(err)
	}
	f.complete(id, 0, f.sam)
	if _, err := f.inst.Reject(t.Context(), id, f.mom, instances.Reason{Text: "Still wet"}); err != nil {
		t.Fatal(err)
	}
	f.complete(id, 0, f.sam)
	if _, err := f.inst.Approve(t.Context(), id, f.mom); err != nil {
		t.Fatal(err)
	}
	f.deliver()
	f.deliver() // redelivered: nothing new

	items := f.wantKinds("Sam", f.sam, inbox.KindChoreRejected, inbox.KindChoreRejected, inbox.KindChoreApproved)
	if d := items[0].Data; d.ReasonCode != instances.ReasonNeedsRedo || d.ReasonText != "Pans too" || d.ChoreName != "Dishes" {
		t.Errorf("first rejection = %+v", d)
	}
	if d := items[1].Data; d.ReasonCode != "" || d.ReasonText != "Still wet" {
		t.Errorf("second rejection = %+v", d)
	}
	if items[2].Data.Auto {
		t.Error("a parent's approval is marked auto")
	}
	// Each resubmission is its own event, so the parents got three notices; Mom acted on none of them.
	f.wantKinds("Dad", f.dad, inbox.KindChoreSubmitted, inbox.KindChoreSubmitted, inbox.KindChoreSubmitted)
	f.wantKinds("Mom", f.mom, inbox.KindChoreSubmitted, inbox.KindChoreSubmitted, inbox.KindChoreSubmitted)
}

// The parent who completes a chore for a kid isn't told about their own submission.
func TestParentCompletingForKid(t *testing.T) {
	f := newFixture(t)
	f.complete(f.instance(true, f.sam), 0, f.mom)
	f.deliver()
	f.wantKinds("Mom", f.mom)
	f.wantKinds("Dad", f.dad, inbox.KindChoreSubmitted)
}

// A chore that needs no approval is approved at once: nobody has anything to review and the kid
// just did it.
func TestInstantApprovalNotifiesNobody(t *testing.T) {
	f := newFixture(t)
	f.complete(f.instance(false, f.sam), 0, f.sam)
	f.deliver()
	for who, user := range map[string]int64{"Mom": f.mom, "Dad": f.dad, "Sam": f.sam} {
		f.wantKinds(who, user)
	}
}

// An approval by the system (by = 0) tells the kid and is marked auto; parents hear nothing.
func TestAutoApproval(t *testing.T) {
	f := newFixture(t)
	id := f.instance(true, f.sam)
	f.complete(id, 0, f.sam)
	err := f.bus.InTx(t.Context(), func(tx *events.Tx) error {
		return instances.Approve(t.Context(), tx, f.clk, id, 0, "auto-approved")
	})
	if err != nil {
		t.Fatal(err)
	}
	f.deliver()
	items := f.wantKinds("Sam", f.sam, inbox.KindChoreApproved)
	if !items[0].Data.Auto {
		t.Errorf("system approval = %+v, want auto", items[0].Data)
	}
	f.wantKinds("Dad", f.dad, inbox.KindChoreSubmitted) // the submission only
}

// Day close marks a chore missed and a parent can excuse it; both tell the kid.
func TestMissedAndExcused(t *testing.T) {
	f := newFixture(t)
	id := f.instance(true, f.sam)
	f.miss(id)
	if _, err := f.inst.Excuse(t.Context(), id, f.mom, ""); err != nil {
		t.Fatal(err)
	}
	f.deliver()
	f.deliver()
	items := f.wantKinds("Sam", f.sam, inbox.KindChoreMissed, inbox.KindChoreExcused)
	if items[0].Data.ChoreName != "Dishes" || items[0].RefID != id {
		t.Errorf("missed = %+v", items[0])
	}
	f.wantKinds("Dad", f.dad)
}

// A shared chore's event names all its members: each gets exactly one, and a kid outside the group none.
func TestSharedChoreNotifiesEachMember(t *testing.T) {
	f := newFixture(t)
	id := f.instance(true, 0, f.sam, f.ada)
	f.complete(id, f.sam, f.sam)
	if _, err := f.inst.Approve(t.Context(), id, f.mom); err != nil {
		t.Fatal(err)
	}
	f.miss(f.instance(true, 0, f.sam, f.ada))
	f.deliver()
	f.deliver()
	for who, kid := range map[string]int64{"Sam": f.sam, "Ada": f.ada} {
		items := f.wantKinds(who, kid, inbox.KindChoreApproved, inbox.KindChoreMissed)
		if items[0].KidID != kid || items[0].RefID != id {
			t.Errorf("%s's approval = %+v", who, items[0])
		}
	}
	f.wantKinds("Kim", f.kim)
	items := f.wantKinds("Dad", f.dad, inbox.KindChoreSubmitted)
	if items[0].KidID != f.sam {
		t.Errorf("the submission is about %d, want Sam (%d), who did it", items[0].KidID, f.sam)
	}
}

// An archived kid has no inbox.
func TestArchivedKidGetsNothing(t *testing.T) {
	f := newFixture(t)
	id := f.instance(true, f.sam)
	f.exec("UPDATE users SET archived_at = ? WHERE id = ?", db.Timestamp(f.clk.Now()), f.sam)
	f.miss(id)
	f.deliver()
	f.wantKinds("Sam", f.sam)
}

// A streak bonus tells the kid the streak length and points; other ledger entries don't notify.
func TestStreakMilestone(t *testing.T) {
	f := newFixture(t)
	post := func(p ledger.Posting) {
		t.Helper()
		err := f.bus.InTx(t.Context(), func(tx *events.Tx) error {
			_, err := ledger.Post(t.Context(), tx, f.clk, p)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	day := caltime.Date{Year: 2026, Month: 10, Day: 30}
	post(ledger.Posting{KidID: f.sam, Kind: ledger.KindAdjustment, Amount: 4, Note: "Extra help"})
	post(ledger.Posting{KidID: f.sam, Kind: ledger.KindStreakBonus, Amount: 15, Source: streaks.MilestoneSource(day, 7), LocalDate: day})
	f.deliver()
	f.deliver()
	items := f.wantKinds("Sam", f.sam, inbox.KindStreakMilestone)
	it := items[0]
	if it.Data.Days != 7 || it.Data.Points != 15 || it.RefKind != inbox.RefPointsEntry || it.KidID != f.sam {
		t.Errorf("streak notification = %+v", it)
	}
	f.wantKinds("Dad", f.dad)
}

// Maintenance purges notifications older than the cutoff, read or not.
func TestPurge(t *testing.T) {
	f := newFixture(t)
	f.miss(f.instance(true, f.sam))
	f.deliver()
	f.clk.Advance(inbox.Keep + time.Hour)
	f.miss(f.instance(true, f.sam))
	f.deliver()
	f.wantKinds("Sam", f.sam, inbox.KindChoreMissed, inbox.KindChoreMissed)
	if err := f.inbox.MarkAllRead(t.Context(), f.sam); err != nil {
		t.Fatal(err)
	}

	n, err := f.inbox.Purge(t.Context(), f.clk.Now().Add(-inbox.Keep))
	if err != nil || n != 1 {
		t.Fatalf("purged %d, %v; want 1", n, err)
	}
	items := f.wantKinds("Sam", f.sam, inbox.KindChoreMissed)
	if !items[0].CreatedAt.After(saturday.Add(inbox.Keep)) {
		t.Errorf("kept the old one: %v", items[0].CreatedAt)
	}
}
