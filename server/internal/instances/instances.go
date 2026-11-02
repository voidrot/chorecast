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
// in the same transaction, with its penalty (dayclose.MissInTx; see missIfClosed).
//
// Each transition runs inside the caller's transaction, so callers that act on many instances
// (bulk approve, auto-approve) can call them from their own bus.InTx. A transition checks its
// source status with an atomic UPDATE … WHERE status = from, writes ledger entries only through
// internal/ledger, and emits one event. Ledger effects are once per (kind, kid, instance): to
// undo or re-apply one, the transition reverses the chain's tip (ledger.Tip) and never posts
// twice, so re-approving after a reversal restores each kid's original credit even if the
// chore's points changed in between.
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

// Completion is who completed an instance.
type Completion struct {
	// KidID is the kid who did it. 0 = the assignee; a shared instance needs one of its members.
	KidID int64
	By    int64 // the acting user (the kid, or a parent completing for them)
	// #27 adds the submitted evidence here; see checkEvidence.
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
// chore doesn't require approval. An instance dated after today can't be completed yet.
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
	if err := checkEvidence(ctx, tx, in, c); err != nil {
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
	}
	return tx.Emit(ctx, Completed{Change: in.change(to, c.By), CompletedBy: kid})
}

// checkEvidence is where #27 validates the completion's evidence against the chore's evidence
// rules (and attaches it to the instance), inside the completing transaction. Until then a
// completion carries no evidence and every completion passes.
func checkEvidence(context.Context, *events.Tx, instance, Completion) error { return nil }

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
	// Credit goes only to the instance's kids; the completer is listed too in case the kid was
	// reassigned since an earlier approval.
	kids := in.kids()
	if in.CompletedBy.Valid && !slices.Contains(kids, in.CompletedBy.Int64) {
		kids = append(slices.Clone(kids), in.CompletedBy.Int64)
	}
	if err := undo(ctx, tx, clk, in, ledger.KindChore, kids, by, cmp.Or(strings.TrimSpace(note), noteUnapproved)); err != nil {
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
	return tx.Emit(ctx, Excused{in.change(horizon.StatusExcused, by)})
}

// Reassign moves an open, single-kid instance to kidID and marks it manually reassigned, so the
// horizon keeps it as it is.
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
	n, err := tx.Q.ReassignChoreInstance(ctx, sqlc.ReassignChoreInstanceParams{
		KidID: sql.NullInt64{Int64: kidID, Valid: true}, UpdatedAt: db.Timestamp(clk.Now()), ID: in.ID,
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

// Shares is how an instance's points are credited: all to the assignee, or for a shared instance
// to the completer or split evenly across the members (remainder to the completer).
func shares(in instance) map[int64]int {
	pts := int(in.chore.Points)
	if in.KidID.Valid {
		return map[int64]int{in.KidID.Int64: pts}
	}
	return assign.SplitPoints(pts, in.chore.SharedPoints, in.CompletedBy.Int64, in.members)
}

// credit puts each kid's share in effect: a first approval posts it, a re-approval reverses the
// reversal of the earlier credit (restoring its amount), and a credit in effect stays. The
// entries are dated to the instance's day, like its missed penalty.
func credit(ctx context.Context, tx *events.Tx, clk clock.Clock, in instance, by int64, note string) error {
	date, err := caltime.ParseDate(in.OccurrenceDate)
	if err != nil {
		return fmt.Errorf("instance %d date: %w", in.ID, err)
	}
	split := shares(in)
	kids := make([]int64, 0, len(split))
	for k := range split {
		kids = append(kids, k)
	}
	slices.Sort(kids) // a stable entry order
	for _, kid := range kids {
		pts := split[kid]
		if pts == 0 {
			continue
		}
		tip, inEffect, found, err := ledger.Tip(ctx, tx, kid, ledger.KindChore, in.source())
		switch {
		case err != nil:
			return err
		case !found:
			_, err = ledger.Post(ctx, tx, clk, ledger.Posting{
				KidID: kid, Kind: ledger.KindChore, Amount: int64(pts), Source: in.source(), LocalDate: date,
				Note: note, CreatedBy: by,
			})
			if err != nil && !errors.Is(err, ledger.ErrAlreadyPosted) {
				return err
			}
		case !inEffect:
			if _, err := ledger.Reverse(ctx, tx, clk, tip.ID, cmp.Or(strings.TrimSpace(note), noteReapproved), by); err != nil {
				return err
			}
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
