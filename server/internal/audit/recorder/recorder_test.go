package recorder_test

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/voidrot/chorecast/server/internal/audit"
	"github.com/voidrot/chorecast/server/internal/audit/recorder"
	"github.com/voidrot/chorecast/server/internal/chores"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/dayclose"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
	"github.com/voidrot/chorecast/server/internal/events"
	"github.com/voidrot/chorecast/server/internal/instances"
	"github.com/voidrot/chorecast/server/internal/jobs"
	"github.com/voidrot/chorecast/server/internal/kids"
	"github.com/voidrot/chorecast/server/internal/ledger"
)

// Saturday 31 October 2026, 13:00 in Chicago.
var saturday = time.Date(2026, 10, 31, 18, 0, 0, 0, time.UTC)

type fixture struct {
	t        *testing.T
	db       *db.DB
	clk      *clock.Fake
	bus      *events.Bus
	inst     *instances.Service
	ledger   *ledger.Service
	chores   *chores.Service
	rec      *recorder.Recorder
	sam, mom int64
}

// newFixture is a household with one kid (Sam) and one parent (Mom), the real instance
// transitions and ledger, and the recorder. Events stay in the outbox until deliver.
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
	f := &fixture{t: t, db: d, clk: clk, bus: bus, inst: instances.New(d, bus, clk), ledger: ledger.New(d, bus, clk),
		chores: chores.New(d, clk, nil), rec: recorder.New(d)}
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

// instance inserts an open instance of a new 5-point chore that needs approval, for Sam.
func (f *fixture) instance() int64 {
	f.t.Helper()
	c, err := f.chores.Create(f.t.Context(), chores.ChoreInput{Name: "Dishes", Points: 5, RequiresApproval: true})
	if err != nil {
		f.t.Fatal(err)
	}
	now := db.Timestamp(f.clk.Now())
	id, err := f.db.InsertChoreInstance(f.t.Context(), sqlc.InsertChoreInstanceParams{
		ChoreID: c.ID, KidID: sql.NullInt64{Int64: f.sam, Valid: true}, OccurrenceDate: "2026-10-31",
		DueAt: "2026-10-31T23:00:00.000Z", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return id
}

func (f *fixture) submitted() int64 {
	f.t.Helper()
	id := f.instance()
	if _, err := f.inst.Complete(f.t.Context(), id, instances.Completion{By: f.sam}); err != nil {
		f.t.Fatal(err)
	}
	return id
}

// deliver hands every event in the outbox to the recorder, as the bus would; the outbox is never
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
		if err := f.rec.Handle(f.t.Context(), env); err != nil {
			f.t.Fatalf("deliver %s: %v", r.Type, err)
		}
	}
}

type row struct {
	ActorKind  string
	ActorID    int64
	Action     string
	TargetKind string
	TargetID   int64
	Summary    string
	Details    audit.AuditDetails
}

// rows is the whole audit log, oldest first.
func (f *fixture) rows() []row {
	f.t.Helper()
	rs, err := f.db.SQL.QueryContext(f.t.Context(),
		`SELECT actor_kind, COALESCE(actor_id, 0), action, target_kind, COALESCE(target_id, 0), summary, data FROM audit_log ORDER BY id`)
	if err != nil {
		f.t.Fatal(err)
	}
	defer func() { _ = rs.Close() }()
	var out []row
	for rs.Next() {
		var r row
		var data string
		if err := rs.Scan(&r.ActorKind, &r.ActorID, &r.Action, &r.TargetKind, &r.TargetID, &r.Summary, &data); err != nil {
			f.t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(data), &r.Details); err != nil {
			f.t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rs.Err(); err != nil {
		f.t.Fatal(err)
	}
	return out
}

func actions(rs []row) []string {
	out := []string{}
	for _, r := range rs {
		out = append(out, r.Action)
	}
	return out
}

func (f *fixture) want(want ...string) []row {
	f.t.Helper()
	rs := f.rows()
	if got := actions(rs); !reflect.DeepEqual(got, want) {
		f.t.Fatalf("audit log = %v, want %v", got, want)
	}
	return rs
}

// A parent's approval, rejection, un-approval and excuse each write one row naming the parent,
// the instance and its chore; delivering every event again writes nothing. The completion, the
// chore credit and the un-approval's reversal entry write none.
func TestReviewDecisions(t *testing.T) {
	f := newFixture(t)
	id := f.submitted()
	if _, err := f.inst.Reject(t.Context(), id, f.mom, instances.Reason{Code: instances.ReasonNeedsRedo, Text: "Pans too"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.inst.Complete(t.Context(), id, instances.Completion{By: f.sam}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.inst.Approve(t.Context(), id, f.mom); err != nil {
		t.Fatal(err)
	}
	if _, err := f.inst.Unapprove(t.Context(), id, f.mom, "not really done"); err != nil {
		t.Fatal(err)
	}
	f.deliver()
	f.deliver()

	rs := f.want(audit.ActionChoreRejected, audit.ActionChoreApproved, audit.ActionChoreUnapproved)
	for _, r := range rs {
		if r.ActorKind != audit.ActorParent || r.ActorID != f.mom || r.TargetKind != audit.TargetInstance || r.TargetID != id ||
			r.Summary != "Dishes" || !reflect.DeepEqual(r.Details.KidIDs, []int64{f.sam}) {
			t.Errorf("%s = %+v", r.Action, r)
		}
	}
	if d := rs[0].Details; d.ReasonCode != instances.ReasonNeedsRedo || d.ReasonText != "Pans too" {
		t.Errorf("rejection details = %+v", d)
	}
}

// The system's approval after the review timeout is recorded with the system as the actor.
func TestAutoApprovalIsTheSystems(t *testing.T) {
	f := newFixture(t)
	id := f.submitted()
	if err := f.bus.InTx(t.Context(), func(tx *events.Tx) error {
		return instances.Approve(t.Context(), tx, f.clk, id, 0, "auto-approved")
	}); err != nil {
		t.Fatal(err)
	}
	f.deliver()
	f.deliver()
	if r := f.want(audit.ActionChoreApproved)[0]; r.ActorKind != audit.ActorSystem || r.ActorID != 0 {
		t.Errorf("auto-approval = %+v", r)
	}
}

// Excusing a missed chore is one row by the parent; marking it missed (the day close) is none.
func TestExcuse(t *testing.T) {
	f := newFixture(t)
	id := f.instance()
	if err := f.bus.InTx(t.Context(), func(tx *events.Tx) error {
		_, err := dayclose.MissInTx(t.Context(), tx, f.clk, id)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.inst.Excuse(t.Context(), id, f.mom, ""); err != nil {
		t.Fatal(err)
	}
	f.deliver()
	f.deliver()
	if r := f.want(audit.ActionChoreExcused)[0]; r.ActorID != f.mom || r.TargetID != id {
		t.Errorf("excuse = %+v", r)
	}
}

// A manual adjustment is recorded from its event, with the amount and note; a manual reversal is
// recorded once, in its own transaction, and its event adds nothing.
func TestAdjustmentAndReversal(t *testing.T) {
	f := newFixture(t)
	adj, err := f.ledger.Adjust(t.Context(), f.sam, 20, "good week", f.mom)
	if err != nil {
		t.Fatal(err)
	}
	f.deliver()
	rs := f.want(audit.ActionPointsAdjusted)
	if r := rs[0]; r.ActorKind != audit.ActorParent || r.ActorID != f.mom || r.TargetKind != audit.TargetPointsEntry ||
		r.TargetID != adj.ID || r.Summary != "Sam" || r.Details.KidID != f.sam || r.Details.Amount == nil ||
		*r.Details.Amount != 20 || r.Details.Note != "good week" {
		t.Errorf("adjustment = %+v", r)
	}

	rev, err := f.ledger.ReverseKidEntry(t.Context(), f.sam, adj.ID, "counted twice", f.mom)
	if err != nil {
		t.Fatal(err)
	}
	f.deliver()
	f.deliver()
	rs = f.want(audit.ActionPointsAdjusted, audit.ActionPointsReversed)
	if r := rs[1]; r.ActorID != f.mom || r.TargetID != rev.ID || r.Details.ReversesID != adj.ID || r.Details.Amount == nil ||
		*r.Details.Amount != -20 || r.Details.Note != "counted twice" || r.Summary != "Sam" {
		t.Errorf("reversal = %+v", r)
	}
}

// Rows from events are dated when the event happened, not when it was delivered, and purged
// after audit.Keep.
func TestEventTimeAndPurge(t *testing.T) {
	f := newFixture(t)
	if _, err := f.ledger.Adjust(t.Context(), f.sam, 5, "old", f.mom); err != nil {
		t.Fatal(err)
	}
	f.clk.Advance(48 * time.Hour)
	f.deliver()
	var created string
	if err := f.db.SQL.QueryRowContext(t.Context(), `SELECT created_at FROM audit_log`).Scan(&created); err != nil {
		t.Fatal(err)
	}
	if created != db.Timestamp(saturday) {
		t.Errorf("created_at = %s, want the event's time %s", created, db.Timestamp(saturday))
	}

	if _, err := f.ledger.Adjust(t.Context(), f.sam, 5, "new", f.mom); err != nil {
		t.Fatal(err)
	}
	f.deliver()
	log := audit.New(f.db)
	f.clk.Set(saturday.Add(audit.Keep + time.Hour)) // past the first row's keep, within the second's
	if n, err := log.Purge(t.Context(), f.clk.Now().Add(-audit.Keep)); err != nil || n != 1 {
		t.Fatalf("purge = %d, %v; want 1", n, err)
	}
	if rs := f.want(audit.ActionPointsAdjusted); rs[0].Details.Note != "new" {
		t.Errorf("kept %+v", rs[0])
	}
}

// An event (its id and time) has one row, whatever is recorded for it again, even without a
// target; an id the events table reused later is another event.
func TestOneRowPerEvent(t *testing.T) {
	f := newFixture(t)
	record := func(env events.Envelope, e audit.Entry) {
		t.Helper()
		if err := audit.RecordEvent(t.Context(), f.db.Queries, env, e); err != nil {
			t.Fatal(err)
		}
	}
	env := events.Envelope{ID: 7, OccurredAt: saturday}
	record(env, audit.Entry{Actor: audit.System, Action: audit.ActionSettingsUpdated, TargetKind: audit.TargetHousehold})
	record(env, audit.Entry{Actor: audit.System, Action: audit.ActionSettingsUpdated, TargetKind: audit.TargetHousehold})
	record(env, audit.Entry{Actor: audit.System, Action: audit.ActionChoreApproved, TargetKind: audit.TargetInstance, TargetID: 3})
	f.want(audit.ActionSettingsUpdated)

	env.OccurredAt = saturday.Add(30 * 24 * time.Hour)
	record(env, audit.Entry{Actor: audit.System, Action: audit.ActionChoreApproved, TargetKind: audit.TargetInstance, TargetID: 3})
	f.want(audit.ActionSettingsUpdated, audit.ActionChoreApproved)
}
