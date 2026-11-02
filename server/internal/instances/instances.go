// Package instances moves chore instances through their lifecycle (docs/features/points.md,
// "Chore instance lifecycle"):
//
//	open ──complete──▶ submitted ──approve──▶ approved      (requires_approval = false: open ──complete──▶ approved)
//	submitted ──reject──▶ open                              (with a reason; history in chore_instance_rejections)
//	approved ──unapprove──▶ open                            (the credit is reversed)
//	missed ──excuse──▶ excused                              (the penalty is reversed)
//	open ──reassign──▶ open                                 (another kid; marked manually reassigned)
//
// open → missed belongs to day close (internal/dayclose), which updates the status itself. When a
// rejection or reversal reopens an instance whose close has passed already, day close misses it
// in the same transaction, with its penalty (dayclose.MissInTx; see missIfClosed). An open
// instance whose close has passed can't be completed either (ErrClosed): day close takes it.
//
// Each transition runs inside the caller's transaction, so callers that act on many instances
// (bulk approve, auto-approve) can call them from their own bus.InTx. A transition checks its
// source status with an atomic UPDATE … WHERE status = from, writes ledger entries only through
// internal/ledger, and emits one event. Ledger effects are once per (kind, kid, instance): to
// undo or re-apply one, the transition reverses the chain's tip (ledger.Tip) and never posts
// twice, so re-approving after a reversal restores the original credit: the current assignee's,
// or a shared instance's first split, which isn't re-split even if the points or the completer
// changed (see credit).
package instances

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"

	"github.com/voidrot/chorecast/server/internal/apierr"
	"github.com/voidrot/chorecast/server/internal/assign"
	"github.com/voidrot/chorecast/server/internal/caltime"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/dayclose"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
	"github.com/voidrot/chorecast/server/internal/events"
	"github.com/voidrot/chorecast/server/internal/evidence"
	"github.com/voidrot/chorecast/server/internal/horizon"
	"github.com/voidrot/chorecast/server/internal/household"
	"github.com/voidrot/chorecast/server/internal/ledger"
)

var (
	ErrNotFound     = apierr.New(http.StatusNotFound, apierr.CodeInstanceNotFound, "no such chore instance")
	ErrNotOpen      = apierr.New(http.StatusConflict, apierr.CodeInstanceNotOpen, "this chore isn't open")
	ErrNotSubmitted = apierr.New(http.StatusConflict, apierr.CodeInstanceNotSubmitted, "this chore isn't waiting for approval")
	ErrNotApproved  = apierr.New(http.StatusConflict, apierr.CodeInstanceNotApproved, "this chore isn't approved")
	ErrNotMissed    = apierr.New(http.StatusConflict, apierr.CodeInstanceNotMissed, "this chore wasn't missed")
	ErrPlanned      = apierr.New(http.StatusConflict, apierr.CodeInstancePlanned, "this chore is for a later day")
	ErrShared       = apierr.New(http.StatusConflict, apierr.CodeInstanceShared, "a shared chore can't be reassigned")
	ErrTaken        = apierr.New(http.StatusConflict, apierr.CodeInstanceTaken, "that kid already has this chore then")
	ErrClosed       = apierr.New(http.StatusConflict, apierr.CodeInstanceClosed, "this chore's day is over")
)

// Rejection reasons a parent can pick (chore_instance_rejections.reason_code).
const (
	ReasonNotDone         = "not_done"
	ReasonNotFinished     = "not_finished"
	ReasonNeedsRedo       = "needs_redo"
	ReasonEvidenceUnclear = "evidence_unclear"
)

// MaxTextLength is the longest rejection text or note, in characters.
const MaxTextLength = ledger.MaxNoteLength

// Default ledger notes for reversals the parent didn't explain.
const (
	noteReapproved = "Approved again"
	noteUnapproved = "Approval reversed"
	noteExcused    = "Missed chore excused"
)

// Change is every instance event's payload.
type Change struct {
	InstanceID int64   `json:"instanceId"`
	KidIDs     []int64 `json:"kidIds"` // the assignee, or a shared instance's members
	From       string  `json:"from"`
	To         string  `json:"to"`
	By         int64   `json:"by,omitempty"` // the acting user; 0 = the system
}

// Completed is emitted when a kid (or a parent for them) completes an instance. To is submitted,
// or approved when the chore doesn't require approval (no separate Approved event then).
type Completed struct {
	Change
	CompletedBy int64 `json:"completedBy"` // the kid who did it
}

func (Completed) EventType() string { return "instance.completed" }

// Approved is emitted when a submitted instance is approved and credited.
type Approved struct{ Change }

func (Approved) EventType() string { return "instance.approved" }

// Rejected is emitted when a submitted instance goes back to open with feedback.
type Rejected struct{ Change }

func (Rejected) EventType() string { return "instance.rejected" }

// Unapproved is emitted when a parent reverses an approval (approved → open).
type Unapproved struct{ Change }

func (Unapproved) EventType() string { return "instance.unapproved" }

// Excused is emitted when a parent excuses a missed instance.
type Excused struct{ Change }

func (Excused) EventType() string { return "instance.excused" }

// Reassigned is emitted when a parent moves an open instance to another kid (KidIDs).
type Reassigned struct {
	Change
	FromKidID int64 `json:"fromKidId"`
}

func (Reassigned) EventType() string { return "instance.reassigned" }

// Completion is who completed an instance, and the evidence they submitted with it.
type Completion struct {
	// KidID is the kid who did it. 0 = the assignee; a shared instance needs one of its members.
	KidID    int64
	By       int64 // the acting user (the kid, or a parent completing for them)
	Evidence evidence.Submission
}

// Reason is a parent's feedback on a rejection: a quick-pick Code, Text, or both.
type Reason struct {
	Code string
	Text string
}

// instance is an instance with what its transitions need.
type instance struct {
	sqlc.ChoreInstance
	chore   sqlc.Chore
	members []int64 // shared: the group; else empty
}

// kids are the instance's kids: the assignee, or a shared instance's members.
func (in instance) kids() []int64 {
	if in.KidID.Valid {
		return []int64{in.KidID.Int64}
	}
	return in.members
}

func (in instance) source() ledger.Source {
	return ledger.Source{Kind: ledger.SourceChoreInstance, ID: in.ID}
}

func (in instance) change(to string, by int64) Change {
	return Change{InstanceID: in.ID, KidIDs: in.kids(), From: in.Status, To: to, By: by}
}

func load(ctx context.Context, q *sqlc.Queries, id int64) (instance, error) {
	row, err := q.GetChoreInstance(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return instance{}, ErrNotFound
	} else if err != nil {
		return instance{}, fmt.Errorf("load instance: %w", err)
	}
	c, err := q.GetChore(ctx, row.ChoreInstance.ChoreID)
	if err != nil {
		return instance{}, fmt.Errorf("load chore: %w", err)
	}
	in := instance{ChoreInstance: row.ChoreInstance, chore: c}
	if !in.KidID.Valid {
		if in.members, err = q.ListChoreInstanceMembers(ctx, id); err != nil {
			return instance{}, fmt.Errorf("load members: %w", err)
		}
	}
	return in, nil
}

// move sets the instance's status from → to, or returns notFrom when it isn't in from.
func move(ctx context.Context, tx *events.Tx, c clock.Clock, in instance, from, to string, notFrom error) error {
	n, err := tx.Q.SetChoreInstanceStatus(ctx, sqlc.SetChoreInstanceStatusParams{
		ToStatus: to, UpdatedAt: db.Timestamp(c.Now()), ID: in.ID, FromStatus: from,
	})
	if err != nil {
		return fmt.Errorf("set instance status: %w", err)
	}
	if n == 0 {
		return notFrom
	}
	return nil
}

// Complete marks an open instance done by c.KidID: submitted, or approved and credited when the
// chore doesn't require approval. An instance dated after today can't be completed yet, nor one
// whose close has passed (dayclose.ClosesAt) though day close hasn't taken it yet: it's missed.
func Complete(ctx context.Context, tx *events.Tx, clk clock.Clock, id int64, c Completion) error {
	in, err := load(ctx, tx.Q, id)
	if err != nil {
		return err
	}
	if in.Status != horizon.StatusOpen {
		return ErrNotOpen
	}
	cal, err := household.Calendar(ctx, tx.Q)
	if err != nil {
		return err
	}
	if date, _ := caltime.ParseDate(in.OccurrenceDate); date.After(horizon.Today(cal, clk.Now())) {
		return ErrPlanned
	}
	closes, err := dayclose.ClosesAt(ctx, tx.Q, in.ChoreInstance)
	if err != nil {
		return fmt.Errorf("instance %d close: %w", in.ID, err)
	}
	if !closes.After(clk.Now()) {
		return ErrClosed
	}
	kid := c.KidID
	if kid == 0 && in.KidID.Valid {
		kid = in.KidID.Int64
	}
	if !slices.Contains(in.kids(), kid) {
		msg := "not one of this chore's kids"
		if kid == 0 {
			msg = "say which kid did this shared chore"
		}
		return apierr.New(http.StatusUnprocessableEntity, apierr.CodeValidationFailed, "validation failed",
			&huma.ErrorDetail{Location: "body.kidId", Message: msg, Value: c.KidID})
	}
	if err := checkEvidence(ctx, tx, clk, in, kid, c); err != nil {
		return err
	}
	to := horizon.StatusSubmitted
	if in.chore.RequiresApproval == 0 {
		to = horizon.StatusApproved
	}
	n, err := tx.Q.CompleteChoreInstance(ctx, sqlc.CompleteChoreInstanceParams{
		ToStatus: to, CompletedBy: sql.NullInt64{Int64: kid, Valid: true},
		CompletedAt: sql.NullString{String: db.Timestamp(clk.Now()), Valid: true}, ID: in.ID,
	})
	if err != nil {
		return fmt.Errorf("complete instance: %w", err)
	}
	if n == 0 {
		return ErrNotOpen
	}
	in.CompletedBy = sql.NullInt64{Int64: kid, Valid: true}
	if to == horizon.StatusApproved {
		if err := credit(ctx, tx, clk, in, c.By, ""); err != nil {
			return err
		}
		if err := evidence.Resolve(ctx, tx.Q, in.ID, clk.Now()); err != nil {
			return err
		}
	}
	return tx.Emit(ctx, Completed{Change: in.change(to, c.By), CompletedBy: kid})
}

// checkEvidence validates the completion's evidence against the chore's evidence rules and
// attaches it to the instance as a new submission, inside the completing transaction (see
// evidence.Attach). kid is the completer.
func checkEvidence(ctx context.Context, tx *events.Tx, clk clock.Clock, in instance, kid int64, c Completion) error {
	opens, err := dayclose.OpensAt(ctx, tx.Q, in.ChoreInstance)
	if err != nil {
		return fmt.Errorf("instance %d opens: %w", in.ID, err)
	}
	return evidence.Attach(ctx, tx.Q, clk, in.chore, evidence.Target{
		InstanceID: in.ID, KidID: kid, By: c.By, OpensAt: opens,
	}, c.Evidence)
}

// Approve approves a submitted instance and credits it. by is the parent, or 0 for the system
// (auto-approve); note goes on a new credit entry, e.g. "Auto-approved".
func Approve(ctx context.Context, tx *events.Tx, clk clock.Clock, id, by int64, note string) error {
	in, err := load(ctx, tx.Q, id)
	if err != nil {
		return err
	}
	if err := move(ctx, tx, clk, in, horizon.StatusSubmitted, horizon.StatusApproved, ErrNotSubmitted); err != nil {
		return err
	}
	if err := credit(ctx, tx, clk, in, by, note); err != nil {
		return err
	}
	if err := evidence.Resolve(ctx, tx.Q, in.ID, clk.Now()); err != nil {
		return err
	}
	return tx.Emit(ctx, Approved{in.change(horizon.StatusApproved, by)})
}

// Reject sends a submitted instance back to open with the parent's reason and records it in the
// instance's rejection history. If its close has passed already, it's missed at once (see
// missIfClosed).
func Reject(ctx context.Context, tx *events.Tx, clk clock.Clock, id, by int64, r Reason) error {
	r.Code, r.Text = strings.TrimSpace(r.Code), strings.TrimSpace(r.Text)
	if err := validateReason(r); err != nil {
		return err
	}
	in, err := load(ctx, tx.Q, id)
	if err != nil {
		return err
	}
	now := db.Timestamp(clk.Now())
	n, err := tx.Q.RejectChoreInstance(ctx, sqlc.RejectChoreInstanceParams{UpdatedAt: now, ID: in.ID})
	if err != nil {
		return fmt.Errorf("reject instance: %w", err)
	}
	if n == 0 {
		return ErrNotSubmitted
	}
	if err := tx.Q.InsertChoreInstanceRejection(ctx, sqlc.InsertChoreInstanceRejectionParams{
		InstanceID: in.ID, KidID: in.CompletedBy, ReasonCode: r.Code, ReasonText: r.Text,
		RejectedBy: sql.NullInt64{Int64: by, Valid: by != 0}, CreatedAt: now,
	}); err != nil {
		return fmt.Errorf("record rejection: %w", err)
	}
	if err := tx.Emit(ctx, Rejected{in.change(horizon.StatusOpen, by)}); err != nil {
		return err
	}
	return missIfClosed(ctx, tx, clk, in)
}

func validateReason(r Reason) error {
	var details []error
	switch r.Code {
	case "", ReasonNotDone, ReasonNotFinished, ReasonNeedsRedo, ReasonEvidenceUnclear:
	default:
		details = append(details, &huma.ErrorDetail{Location: "body.reason", Message: "unknown reason", Value: r.Code})
	}
	if utf8.RuneCountInString(r.Text) > MaxTextLength {
		details = append(details, &huma.ErrorDetail{Location: "body.text", Message: fmt.Sprintf("at most %d characters", MaxTextLength), Value: r.Text})
	}
	if r.Code == "" && r.Text == "" {
		details = append(details, &huma.ErrorDetail{Location: "body.reason", Message: "pick a reason or write one", Value: r.Code})
	}
	if len(details) > 0 {
		return apierr.New(http.StatusUnprocessableEntity, apierr.CodeValidationFailed, "validation failed", details...)
	}
	return nil
}

// Unapprove reverses an approval: the instance goes back to open (and is missed at once if its
// close has passed; see missIfClosed) and every credit still in effect is reversed. note explains
// it on the reversal entries (empty = a default).
func Unapprove(ctx context.Context, tx *events.Tx, clk clock.Clock, id, by int64, note string) error {
	in, err := load(ctx, tx.Q, id)
	if err != nil {
		return err
	}
	if err := move(ctx, tx, clk, in, horizon.StatusApproved, horizon.StatusOpen, ErrNotApproved); err != nil {
		return err
	}
	// Every credit in effect for it: the current kids', and any other an earlier approval left.
	kids, err := ledger.SourceKids(ctx, tx, ledger.KindChore, in.source())
	if err != nil {
		return err
	}
	if err := undo(ctx, tx, clk, in, ledger.KindChore, kids, by, cmp.Or(strings.TrimSpace(note), noteUnapproved)); err != nil {
		return err
	}
	if err := evidence.Reopen(ctx, tx.Q, in.ID); err != nil {
		return err
	}
	if err := tx.Emit(ctx, Unapproved{in.change(horizon.StatusOpen, by)}); err != nil {
		return err
	}
	return missIfClosed(ctx, tx, clk, in)
}

// Excuse excuses a missed instance and reverses every penalty still in effect for it (each member
// of a shared instance). note explains it on the reversal entries (empty = a default).
func Excuse(ctx context.Context, tx *events.Tx, clk clock.Clock, id, by int64, note string) error {
	in, err := load(ctx, tx.Q, id)
	if err != nil {
		return err
	}
	if err := move(ctx, tx, clk, in, horizon.StatusMissed, horizon.StatusExcused, ErrNotMissed); err != nil {
		return err
	}
	if err := undo(ctx, tx, clk, in, ledger.KindChorePenalty, in.kids(), by, cmp.Or(strings.TrimSpace(note), noteExcused)); err != nil {
		return err
	}
	if err := evidence.Resolve(ctx, tx.Q, in.ID, clk.Now()); err != nil {
		return err
	}
	return tx.Emit(ctx, Excused{in.change(horizon.StatusExcused, by)})
}

// Reassign moves an open, single-kid instance to kidID and marks it manually reassigned, so the
// horizon keeps it as it is and counts it as the original kid's occurrence. An instance without a
// slot becomes due at the new kid's own day (or week) close. It can't go to a kid whose close for
// it has passed already (ErrClosed).
func Reassign(ctx context.Context, tx *events.Tx, clk clock.Clock, id, kidID, by int64) error {
	in, err := load(ctx, tx.Q, id)
	if err != nil {
		return err
	}
	if in.Status != horizon.StatusOpen {
		return ErrNotOpen
	}
	if !in.KidID.Valid {
		return ErrShared
	}
	bad := func(msg string) error {
		return apierr.New(http.StatusUnprocessableEntity, apierr.CodeValidationFailed, "validation failed",
			&huma.ErrorDetail{Location: "body.kidId", Message: msg, Value: kidID})
	}
	if kidID == in.KidID.Int64 {
		return bad("the chore is already this kid's")
	}
	k, err := tx.Q.GetKidByID(ctx, kidID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && k.ArchivedAt.Valid) {
		return bad("no such kid")
	} else if err != nil {
		return fmt.Errorf("load kid: %w", err)
	}
	due := in.DueAt
	if in.Slot == "" {
		cal, err := household.Calendar(ctx, tx.Q)
		if err != nil {
			return err
		}
		date, err := caltime.ParseDate(in.OccurrenceDate)
		if err != nil {
			return fmt.Errorf("instance %d date: %w", in.ID, err)
		}
		due = db.Timestamp(horizon.SlotlessDueAt(cal, date, in.Flexible == 1, horizon.KidDayEnd(cal, k.DayEndOverride)))
	}
	// The new kid's close mustn't have passed: they couldn't complete it, and day close would
	// miss it at their next close.
	moved := in.ChoreInstance
	moved.KidID, moved.DueAt = sql.NullInt64{Int64: kidID, Valid: true}, due
	closes, err := dayclose.ClosesAt(ctx, tx.Q, moved)
	if err != nil {
		return fmt.Errorf("instance %d close: %w", in.ID, err)
	}
	if !closes.After(clk.Now()) {
		return ErrClosed
	}
	n, err := tx.Q.ReassignChoreInstance(ctx, sqlc.ReassignChoreInstanceParams{
		KidID: sql.NullInt64{Int64: kidID, Valid: true}, DueAt: due, UpdatedAt: db.Timestamp(clk.Now()), ID: in.ID,
	})
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return ErrTaken
	} else if err != nil {
		return fmt.Errorf("reassign instance: %w", err)
	}
	if n == 0 {
		return ErrNotOpen
	}
	ch := in.change(horizon.StatusOpen, by)
	ch.KidIDs = []int64{kidID}
	return tx.Emit(ctx, Reassigned{Change: ch, FromKidID: in.KidID.Int64})
}

// missIfClosed hands a reopened instance to day close when the close that takes it has passed
// already (dayclose.ClosesAt): the kid can't redo that day, so it's missed now, with its penalty,
// in the same transaction, instead of waiting open for the next close.
func missIfClosed(ctx context.Context, tx *events.Tx, clk clock.Clock, in instance) error {
	closes, err := dayclose.ClosesAt(ctx, tx.Q, in.ChoreInstance)
	if err != nil {
		return fmt.Errorf("instance %d close: %w", in.ID, err)
	}
	if closes.After(clk.Now()) {
		return nil
	}
	_, err = dayclose.MissInTx(ctx, tx, clk, in.ID)
	return err
}

// soleEarner is the one kid an instance credits in full: its assignee, or the completer of a
// shared instance whose completer takes all. ok is false for a split.
func soleEarner(in instance) (int64, bool) {
	switch {
	case in.KidID.Valid:
		return in.KidID.Int64, true
	case in.chore.SharedPoints != assign.PointsSplitEvenly:
		return in.CompletedBy.Int64, in.CompletedBy.Valid
	}
	return 0, false
}

// Shares is how an instance's points are credited: all to the assignee, or for a shared instance
// to the completer or split evenly across the members (remainder to the completer).
func shares(in instance) map[int64]int {
	pts := int(in.chore.Points)
	if in.KidID.Valid {
		return map[int64]int{in.KidID.Int64: pts}
	}
	return assign.SplitPoints(pts, in.chore.SharedPoints, in.CompletedBy.Int64, in.members)
}

// credit puts the instance's credit in effect. The entries are dated to the instance's day, like
// its missed penalty.
//
// A single-kid instance is credited to its current assignee only: their earlier credit is
// restored (reversing a reversal tip, with its original amount), or else the current points are
// posted. A kid it was reassigned away from keeps their reversed credit, so nobody is paid for
// another kid's work.
//
// A shared instance whose completer takes all works the same way for its current completer: an
// earlier completer's credit stays reversed.
//
// A shared instance split evenly keeps its group (it can't be reassigned), so the first approval
// splits the points (shares) and posts each share above 0, and a re-approval restores every
// earlier credit, each to its kid with its amount, and posts nothing new: it doesn't re-split,
// even if the points or the completer changed, so the total stays what was first credited.
func credit(ctx context.Context, tx *events.Tx, clk clock.Clock, in instance, by int64, note string) error {
	date, err := caltime.ParseDate(in.OccurrenceDate)
	if err != nil {
		return fmt.Errorf("instance %d date: %w", in.ID, err)
	}
	restore := func(kid int64) (found bool, err error) {
		tip, inEffect, found, err := ledger.Tip(ctx, tx, kid, ledger.KindChore, in.source())
		if err != nil || !found || inEffect {
			return found, err
		}
		_, err = ledger.Reverse(ctx, tx, clk, tip.ID, cmp.Or(strings.TrimSpace(note), noteReapproved), by)
		return true, err
	}
	post := func(kid int64, pts int64) error {
		_, err := ledger.Post(ctx, tx, clk, ledger.Posting{
			KidID: kid, Kind: ledger.KindChore, Amount: pts, Source: in.source(), LocalDate: date,
			Note: note, CreatedBy: by,
		})
		return err
	}

	// One kid gets it all: the assignee, or a shared instance's current completer.
	if one, ok := soleEarner(in); ok {
		found, err := restore(one)
		if err != nil || found || in.chore.Points == 0 {
			return err
		}
		return post(one, in.chore.Points)
	}

	credited, err := ledger.SourceKids(ctx, tx, ledger.KindChore, in.source())
	if err != nil {
		return err
	}
	if len(credited) > 0 {
		for _, kid := range credited {
			if _, err := restore(kid); err != nil {
				return err
			}
		}
		return nil
	}
	split := shares(in)
	kids := make([]int64, 0, len(split))
	for k, pts := range split {
		if pts > 0 {
			kids = append(kids, k)
		}
	}
	slices.Sort(kids) // a stable entry order
	for _, kid := range kids {
		if err := post(kid, int64(split[kid])); err != nil {
			return err
		}
	}
	return nil
}

// undo reverses each kid's kind entry for the instance where its effect is still in force.
func undo(ctx context.Context, tx *events.Tx, clk clock.Clock, in instance, kind string, kids []int64, by int64, note string) error {
	for _, kid := range kids {
		tip, inEffect, found, err := ledger.Tip(ctx, tx, kid, kind, in.source())
		if err != nil {
			return err
		}
		if !found || !inEffect {
			continue
		}
		if _, err := ledger.Reverse(ctx, tx, clk, tip.ID, note, by); err != nil {
			return err
		}
	}
	return nil
}

// Service runs the transitions for the API, each in its own transaction, and returns the
// updated instance.
type Service struct {
	db    *db.DB
	bus   *events.Bus
	clock clock.Clock
}

func New(d *db.DB, bus *events.Bus, c clock.Clock) *Service {
	return &Service{db: d, bus: bus, clock: c}
}

// Get returns one instance.
func (s *Service) Get(ctx context.Context, id int64) (horizon.ChoreInstance, error) {
	out, err := horizon.Instance(ctx, s.db.Queries, s.clock.Now(), id)
	if errors.Is(err, sql.ErrNoRows) {
		return horizon.ChoreInstance{}, ErrNotFound
	}
	return out, err
}

func (s *Service) Complete(ctx context.Context, id int64, c Completion) (horizon.ChoreInstance, error) {
	return s.run(ctx, id, func(tx *events.Tx) error { return Complete(ctx, tx, s.clock, id, c) })
}

func (s *Service) Approve(ctx context.Context, id, by int64) (horizon.ChoreInstance, error) {
	return s.run(ctx, id, func(tx *events.Tx) error { return Approve(ctx, tx, s.clock, id, by, "") })
}

func (s *Service) Reject(ctx context.Context, id, by int64, r Reason) (horizon.ChoreInstance, error) {
	return s.run(ctx, id, func(tx *events.Tx) error { return Reject(ctx, tx, s.clock, id, by, r) })
}

func (s *Service) Unapprove(ctx context.Context, id, by int64, note string) (horizon.ChoreInstance, error) {
	return s.run(ctx, id, func(tx *events.Tx) error { return Unapprove(ctx, tx, s.clock, id, by, note) })
}

func (s *Service) Excuse(ctx context.Context, id, by int64, note string) (horizon.ChoreInstance, error) {
	return s.run(ctx, id, func(tx *events.Tx) error { return Excuse(ctx, tx, s.clock, id, by, note) })
}

func (s *Service) Reassign(ctx context.Context, id, kidID, by int64) (horizon.ChoreInstance, error) {
	return s.run(ctx, id, func(tx *events.Tx) error { return Reassign(ctx, tx, s.clock, id, kidID, by) })
}

// run applies fn in one transaction and reads the instance back in it.
func (s *Service) run(ctx context.Context, id int64, fn func(tx *events.Tx) error) (horizon.ChoreInstance, error) {
	var out horizon.ChoreInstance
	err := s.bus.InTx(ctx, func(tx *events.Tx) error {
		if err := fn(tx); err != nil {
			return err
		}
		var err error
		out, err = horizon.Instance(ctx, tx.Q, s.clock.Now(), id)
		return err
	})
	return out, err
}
