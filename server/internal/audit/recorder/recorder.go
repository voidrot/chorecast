// Package recorder writes audit log rows from the domain events that identify an audited action
// on their own: chore approvals (by a parent, or the system after the review timeout), rejections,
// un-approvals and excuses, and manual points adjustments. It is separate from package audit
// because the domain packages that write audit rows directly (ledger, household, ...) import audit,
// while this package imports them for their event types.
//
// Reversal entries are not recorded from points.entry_posted: an un-approval or excuse posts one
// too, and nothing on the event tells it from a parent's manual reversal, which ledger records in
// its own transaction instead.
package recorder

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/voidrot/chorecast/server/internal/audit"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/events"
	"github.com/voidrot/chorecast/server/internal/instances"
	"github.com/voidrot/chorecast/server/internal/jobs"
	"github.com/voidrot/chorecast/server/internal/ledger"
)

// Recorder turns events into audit rows.
type Recorder struct {
	db       *db.DB
	handlers map[string]events.Handler // by event type
}

func New(d *db.DB) *Recorder {
	r := &Recorder{db: d}
	r.handlers = map[string]events.Handler{
		instances.Approved{}.EventType():   r.approved,
		instances.Rejected{}.EventType():   r.rejected,
		instances.Unapproved{}.EventType(): r.unapproved,
		instances.Excused{}.EventType():    r.excused,
		ledger.EntryPosted{}.EventType():   r.entryPosted,
	}
	return r
}

// Register subscribes the recorder to the events it records.
func (r *Recorder) Register(bus *events.Bus) {
	for typ, h := range r.handlers {
		bus.Subscribe(typ, "audit."+typ, h, jobs.KindOptions{})
	}
}

// Handle records one delivered event; other event types are ignored. Delivering the same event
// again writes nothing.
func (r *Recorder) Handle(ctx context.Context, env events.Envelope) error {
	if h, ok := r.handlers[env.Type]; ok {
		return h(ctx, env)
	}
	return nil
}

func (r *Recorder) approved(ctx context.Context, env events.Envelope) error {
	ev, err := events.Decode[instances.Approved](env)
	if err != nil {
		return err
	}
	return r.recordInstance(ctx, env, audit.ActionChoreApproved, ev.Change, audit.AuditDetails{KidIDs: ev.KidIDs})
}

func (r *Recorder) unapproved(ctx context.Context, env events.Envelope) error {
	ev, err := events.Decode[instances.Unapproved](env)
	if err != nil {
		return err
	}
	return r.recordInstance(ctx, env, audit.ActionChoreUnapproved, ev.Change, audit.AuditDetails{KidIDs: ev.KidIDs})
}

func (r *Recorder) excused(ctx context.Context, env events.Envelope) error {
	ev, err := events.Decode[instances.Excused](env)
	if err != nil {
		return err
	}
	return r.recordInstance(ctx, env, audit.ActionChoreExcused, ev.Change, audit.AuditDetails{KidIDs: ev.KidIDs})
}

func (r *Recorder) rejected(ctx context.Context, env events.Envelope) error {
	ev, err := events.Decode[instances.Rejected](env)
	if err != nil {
		return err
	}
	return r.recordInstance(ctx, env, audit.ActionChoreRejected, ev.Change, audit.AuditDetails{
		KidIDs: ev.KidIDs, ReasonCode: ev.ReasonCode, ReasonText: ev.ReasonText,
	})
}

func (r *Recorder) recordInstance(ctx context.Context, env events.Envelope, action string, ch instances.Change, d audit.AuditDetails) error {
	// A deleted instance is still recorded, without its chore's name.
	name, err := r.db.GetInstanceChoreName(ctx, ch.InstanceID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("load instance %d: %w", ch.InstanceID, err)
	}
	return audit.RecordEvent(ctx, r.db.Queries, env, audit.Entry{
		Actor: audit.UserOrSystem(ch.By), Action: action, TargetKind: audit.TargetInstance, TargetID: ch.InstanceID,
		Summary: name, Details: d,
	})
}

// entryPosted records manual adjustments; other entries belong to actions recorded elsewhere or
// to nobody's decision (chore credits, penalties, streak bonuses).
func (r *Recorder) entryPosted(ctx context.Context, env events.Envelope) error {
	ev, err := events.Decode[ledger.EntryPosted](env)
	if err != nil || ev.Kind != ledger.KindAdjustment {
		return err
	}
	entry, err := r.db.GetPointsEntry(ctx, ev.EntryID)
	if err != nil {
		return fmt.Errorf("load points entry %d: %w", ev.EntryID, err)
	}
	kid, err := r.db.GetKidByID(ctx, ev.KidID)
	if err != nil {
		return fmt.Errorf("load kid %d: %w", ev.KidID, err)
	}
	return audit.RecordEvent(ctx, r.db.Queries, env, audit.Entry{
		Actor: audit.UserOrSystem(entry.CreatedBy.Int64), Action: audit.ActionPointsAdjusted,
		TargetKind: audit.TargetPointsEntry, TargetID: ev.EntryID, Summary: kid.DisplayName,
		Details: audit.AuditDetails{KidID: ev.KidID, Amount: &ev.Amount, Note: entry.Note},
	})
}
