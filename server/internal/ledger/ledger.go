// Package ledger is the append-only points ledger. A kid's balance is the SUM of their entries;
// nothing is edited or deleted, and mistakes are fixed with a reversal entry linked to the
// original. See docs/features/points.md ("Ledger model").
//
// Features that award or deduct points (approvals, day close, streaks) call Post and Reverse
// inside their own transaction, so the entry, their own state change and the EntryPosted event
// commit together:
//
//	err := bus.InTx(ctx, func(tx *events.Tx) error {
//		// ... tx.Q.MarkInstanceApproved(...)
//		_, err := ledger.Post(ctx, tx, clk, ledger.Posting{KidID: kid, Kind: ledger.KindChore, Amount: pts,
//			Source: ledger.Source{Kind: ledger.SourceChoreInstance, ID: instanceID}, LocalDate: day})
//		if errors.Is(err, ledger.ErrAlreadyPosted) {
//			return nil // a retry: the entry is already there, so keep this transaction
//		}
//		return err
//	})
//
// A source's entry and its reversals form a chain C → R1 → R2 …. To undo or re-apply a source's
// effect, call Tip and Reverse the tip only when inEffect is the state you want to change (true to
// undo, false to re-apply); never Post again for that source.
//
// ErrAlreadyReversed is not a retry signal like ErrAlreadyPosted. A retry in its own transaction
// can't run into its own earlier reversal (a rolled-back attempt left none, and a committed one
// shows in Tip), so the error means someone else reversed the entry. Treating it as done can leave
// the wrong state: a parent reverses C and then R1, and an un-approve job's Reverse(C) gets
// ErrAlreadyReversed while the credit stands. Re-read Tip instead.
package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"

	"github.com/voidrot/chorecast/server/internal/apierr"
	"github.com/voidrot/chorecast/server/internal/caltime"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
	"github.com/voidrot/chorecast/server/internal/events"
	"github.com/voidrot/chorecast/server/internal/household"
	"github.com/voidrot/chorecast/server/internal/paging"
)

// Entry kinds. New kinds (rewards, transfers, behavior) join with their features.
const (
	KindChore        = "chore"         // points for an approved chore instance (>= 0)
	KindChorePenalty = "chore_penalty" // a missed chore's penalty (<= 0)
	KindStreakBonus  = "streak_bonus"  // a streak milestone bonus (>= 0)
	KindAdjustment   = "adjustment"    // a parent's manual change (or a recurring adjustment); note required
	KindReversal     = "reversal"      // undoes one entry; written only by Reverse
)

// SourceChoreInstance is the source kind for entries about a chore instance (#25). Sources are
// free-form (kind, id) pairs with no foreign key; each feature names its own.
const SourceChoreInstance = "chore_instance"

const (
	// MaxAmount bounds a single entry so sums can't overflow.
	MaxAmount = 1_000_000
	// MaxNoteLength is the longest note, in characters.
	MaxNoteLength = 500
)

var (
	// ErrAlreadyReversed is returned by Reverse for an entry that has a reversal already. It
	// doesn't mean done: someone else reversed the entry, so re-read Tip and decide again.
	ErrAlreadyReversed = apierr.New(http.StatusConflict, apierr.CodeAlreadyReversed, "this entry has already been reversed")
	// ErrAlreadyPosted is returned by Post when an entry of the same kind, kid and source exists.
	// The returned PointsEntry is that existing entry, so at-least-once callers can carry on.
	ErrAlreadyPosted = apierr.New(http.StatusConflict, apierr.CodeAlreadyPosted, "an entry for this source has already been posted")
	// ErrEntryNotFound is returned for an unknown entry id (or one of another kid).
	ErrEntryNotFound = apierr.New(http.StatusNotFound, apierr.CodePointsEntryNotFound, "no such points entry")
)

// PointsEntry is a ledger entry as the API and callers see it.
type PointsEntry struct {
	ID              int64        `json:"id"`
	KidID           int64        `json:"kidId"`
	Kind            string       `json:"kind" enum:"chore,chore_penalty,streak_bonus,adjustment,reversal"`
	Amount          int64        `json:"amount" doc:"Signed points"`
	RequestedAmount *int64       `json:"requestedAmount,omitempty" doc:"The asked-for amount, when negative balances are off and the deduction was clamped to the balance"`
	BalanceAfter    int64        `json:"balanceAfter" doc:"The kid's balance including this entry and every earlier one"`
	LocalDate       caltime.Date `json:"localDate" format:"date" doc:"The kid's household-local day the entry belongs to"`
	SourceKind      string       `json:"sourceKind,omitempty" doc:"What the entry is for, e.g. chore_instance"`
	SourceID        int64        `json:"sourceId,omitempty"`
	ReversesID      int64        `json:"reversesId,omitempty" doc:"Reversals: the entry this one undoes"`
	ReversedByID    int64        `json:"reversedById,omitempty" doc:"The reversal that undid this entry, if any"`
	Note            string       `json:"note,omitempty"`
	CreatedBy       int64        `json:"createdBy,omitempty" doc:"The user who made the entry; absent = the system"`
	CreatedAt       time.Time    `json:"createdAt"`
}

// PointsBalance is a kid's balance and what they can spend.
type PointsBalance struct {
	Balance   int64 `json:"balance" doc:"SUM of the kid's ledger entries"`
	Available int64 `json:"available" doc:"Balance minus points held by pending requests"`
}

// Source names what an entry is for. The zero Source means none.
type Source struct {
	Kind string
	ID   int64
}

// Posting is a new entry for Post.
type Posting struct {
	KidID  int64
	Kind   string // KindChore, KindChorePenalty, KindStreakBonus or KindAdjustment
	Amount int64
	// Source is required for chore, chore_penalty and streak_bonus entries; at most one entry per
	// (kind, kid, source) is ever posted.
	Source Source
	// LocalDate is the day the entry belongs to; zero means the kid's current day.
	LocalDate caltime.Date
	Note      string // required for adjustments
	CreatedBy int64  // the acting user; 0 = the system
}

// EntryPosted is emitted for every new entry, reversals included.
type EntryPosted struct {
	EntryID      int64        `json:"entryId"`
	KidID        int64        `json:"kidId"`
	Kind         string       `json:"kind"`
	Amount       int64        `json:"amount"`
	BalanceAfter int64        `json:"balanceAfter"`
	LocalDate    caltime.Date `json:"localDate"`
}

func (EntryPosted) EventType() string { return "points.entry_posted" }

// Post appends an entry inside the caller's transaction and emits EntryPosted. With negative
// balances off, a deduction is clamped so the balance doesn't drop below zero (an entry is
// still written, with RequestedAmount set, even when it clamps to 0). If an entry of the same
// kind, kid and source exists, Post returns it with ErrAlreadyPosted and writes nothing; callers
// must treat that as success and not return it from their InTx function, or their own state change
// rolls back.
func Post(ctx context.Context, tx *events.Tx, c clock.Clock, in Posting) (PointsEntry, error) {
	in.Note = strings.TrimSpace(in.Note)
	if err := validate(in); err != nil {
		return PointsEntry{}, err
	}
	kid, err := tx.Q.GetKidByID(ctx, in.KidID)
	if errors.Is(err, sql.ErrNoRows) {
		return PointsEntry{}, apierr.New(http.StatusNotFound, apierr.CodeKidNotFound, "no such kid")
	} else if err != nil {
		return PointsEntry{}, fmt.Errorf("load kid: %w", err)
	}
	if onceKind(in.Kind) {
		// The single connection serializes transactions, so check-then-insert can't race; the
		// unique index points_entries_once backs it up.
		existing, err := tx.Q.GetPointsEntryBySource(ctx, sqlc.GetPointsEntryBySourceParams{
			Kind: in.Kind, SourceKind: nullString(in.Source.Kind), SourceID: nullInt(in.Source.ID), KidID: in.KidID,
		})
		if err == nil {
			out, err := withBalance(ctx, tx.Q, existing)
			if err != nil {
				return PointsEntry{}, err
			}
			if out.ReversedByID, err = reversedBy(ctx, tx.Q, existing.ID); err != nil {
				return PointsEntry{}, err
			}
			return out, ErrAlreadyPosted
		} else if !errors.Is(err, sql.ErrNoRows) {
			return PointsEntry{}, fmt.Errorf("find entry by source: %w", err)
		}
	}

	amount, requested := in.Amount, sql.NullInt64{}
	if amount < 0 {
		h, err := tx.Q.GetHousehold(ctx)
		if err != nil {
			return PointsEntry{}, fmt.Errorf("load household: %w", err)
		}
		if h.AllowNegativeBalance == 0 {
			balance, err := tx.Q.KidPointsBalance(ctx, in.KidID)
			if err != nil {
				return PointsEntry{}, fmt.Errorf("balance: %w", err)
			}
			if floor := -max(balance, 0); amount < floor {
				requested = sql.NullInt64{Int64: amount, Valid: true}
				amount = floor
			}
		}
	}
	date := in.LocalDate
	if date.IsZero() {
		if date, err = today(ctx, tx.Q, c, kid); err != nil {
			return PointsEntry{}, err
		}
	}
	return insert(ctx, tx, c, sqlc.InsertPointsEntryParams{
		KidID: in.KidID, Kind: in.Kind, Amount: amount, RequestedAmount: requested, LocalDate: date.String(),
		SourceKind: nullString(in.Source.Kind), SourceID: nullInt(in.Source.ID), Note: in.Note,
		CreatedBy: nullInt(in.CreatedBy),
	})
}

// Reverse undoes entry entryID inside the caller's transaction: it appends a reversal with the
// negated amount and the original's source and local date, and emits EntryPosted. Dating it to
// the original means un-approving last week's chore corrects last week. Reversals are never
// clamped, so reversing a credit can take a balance below zero. A reversal can itself be
// reversed (e.g. un-excusing a missed chore). Reversing an entry twice returns
// ErrAlreadyReversed.
func Reverse(ctx context.Context, tx *events.Tx, c clock.Clock, entryID int64, note string, by int64) (PointsEntry, error) {
	note = strings.TrimSpace(note)
	if msg := noteProblem(note, true); msg != "" {
		return PointsEntry{}, apierr.New(http.StatusUnprocessableEntity, apierr.CodeValidationFailed, "validation failed",
			&huma.ErrorDetail{Location: "body.note", Message: msg, Value: note})
	}
	orig, err := tx.Q.GetPointsEntry(ctx, entryID)
	if errors.Is(err, sql.ErrNoRows) {
		return PointsEntry{}, ErrEntryNotFound
	} else if err != nil {
		return PointsEntry{}, fmt.Errorf("load entry: %w", err)
	}
	if rev, err := reversedBy(ctx, tx.Q, entryID); err != nil {
		return PointsEntry{}, err
	} else if rev != 0 {
		return PointsEntry{}, ErrAlreadyReversed
	}
	return insert(ctx, tx, c, sqlc.InsertPointsEntryParams{
		KidID: orig.KidID, Kind: KindReversal, Amount: -orig.Amount, LocalDate: orig.LocalDate,
		SourceKind: orig.SourceKind, SourceID: orig.SourceID, ReversesID: nullInt(entryID), Note: note,
		CreatedBy: nullInt(by),
	})
}

// Tip finds the kid's kind entry for src (kind is KindChore, KindChorePenalty or KindStreakBonus,
// which have at most one entry per source) and follows its reversals C → R1 → R2 … to the newest,
// unreversed one. found is false when nothing was posted for the source. inEffect reports whether
// the source's effect stands: true for an even number of reversals (C itself, or R2, …). Reverse
// the returned entry only when inEffect is the state to change: true to undo, false to re-apply.
func Tip(ctx context.Context, tx *events.Tx, kidID int64, kind string, src Source) (entry PointsEntry, inEffect bool, found bool, err error) {
	if !onceKind(kind) {
		return PointsEntry{}, false, false, fmt.Errorf("ledger.Tip: %q entries aren't once per source", kind)
	}
	cur, err := tx.Q.GetPointsEntryBySource(ctx, sqlc.GetPointsEntryBySourceParams{
		Kind: kind, SourceKind: nullString(src.Kind), SourceID: nullInt(src.ID), KidID: kidID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return PointsEntry{}, false, false, nil
	} else if err != nil {
		return PointsEntry{}, false, false, fmt.Errorf("find entry by source: %w", err)
	}
	inEffect = true
	for {
		next, err := tx.Q.GetPointsReversalOf(ctx, nullInt(cur.ID))
		if errors.Is(err, sql.ErrNoRows) {
			break
		} else if err != nil {
			return PointsEntry{}, false, false, fmt.Errorf("find reversal: %w", err)
		}
		cur, inEffect = next, !inEffect
	}
	entry, err = withBalance(ctx, tx.Q, cur)
	if err != nil {
		return PointsEntry{}, false, false, err
	}
	return entry, inEffect, true, nil
}

// Balance is the SUM of the kid's entries. q may be a transaction's queries.
func Balance(ctx context.Context, q *sqlc.Queries, kidID int64) (int64, error) {
	return q.KidPointsBalance(ctx, kidID)
}

// Available is what the kid can spend: the balance minus points held by pending reward
// redemptions and transfer requests. Holds arrive with rewards (Phase 2); subtract them here.
func Available(ctx context.Context, q *sqlc.Queries, kidID int64) (int64, error) {
	return Balance(ctx, q, kidID)
}

func insert(ctx context.Context, tx *events.Tx, c clock.Clock, p sqlc.InsertPointsEntryParams) (PointsEntry, error) {
	p.CreatedAt = db.Timestamp(c.Now())
	row, err := tx.Q.InsertPointsEntry(ctx, p)
	if err != nil {
		return PointsEntry{}, fmt.Errorf("insert points entry: %w", err)
	}
	out, err := withBalance(ctx, tx.Q, row)
	if err != nil {
		return PointsEntry{}, err
	}
	if err := tx.Emit(ctx, EntryPosted{
		EntryID: out.ID, KidID: out.KidID, Kind: out.Kind, Amount: out.Amount,
		BalanceAfter: out.BalanceAfter, LocalDate: out.LocalDate,
	}); err != nil {
		return PointsEntry{}, err
	}
	return out, nil
}

// reversedBy is the id of the reversal of entry id, or 0 if it has none.
func reversedBy(ctx context.Context, q *sqlc.Queries, id int64) (int64, error) {
	rev, err := q.GetPointsReversalOf(ctx, nullInt(id))
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	} else if err != nil {
		return 0, fmt.Errorf("find reversal: %w", err)
	}
	return rev.ID, nil
}

// withBalance converts a row and fills in BalanceAfter.
func withBalance(ctx context.Context, q *sqlc.Queries, row sqlc.PointsEntry) (PointsEntry, error) {
	bal, err := q.KidPointsBalanceThrough(ctx, sqlc.KidPointsBalanceThroughParams{KidID: row.KidID, ID: row.ID})
	if err != nil {
		return PointsEntry{}, fmt.Errorf("balance: %w", err)
	}
	out := toEntry(row)
	out.BalanceAfter = bal
	return out, nil
}

// today is the kid's current household-local day: the one whose window (up to the kid's
// effective day end) contains now.
func today(ctx context.Context, q *sqlc.Queries, c clock.Clock, kid sqlc.User) (caltime.Date, error) {
	cal, err := household.Calendar(ctx, q)
	if err != nil {
		return caltime.Date{}, err
	}
	var override *caltime.TimeOfDay
	if kid.DayEndOverride.Valid {
		t, err := caltime.ParseTimeOfDay(kid.DayEndOverride.String)
		if err != nil {
			return caltime.Date{}, fmt.Errorf("kid %d day end: %w", kid.ID, err)
		}
		override = &t
	}
	end, err := cal.EffectiveDayEnd(override)
	if err != nil {
		return caltime.Date{}, fmt.Errorf("kid %d day end: %w", kid.ID, err)
	}
	return cal.DayContaining(c.Now(), end), nil
}

func validate(in Posting) error {
	var details []error
	bad := func(loc, msg string, v any) {
		details = append(details, &huma.ErrorDetail{Location: "body." + loc, Message: msg, Value: v})
	}
	switch in.Kind {
	case KindChore, KindStreakBonus:
		if in.Amount < 0 {
			bad("amount", in.Kind+" entries can't be negative", in.Amount)
		}
	case KindChorePenalty:
		if in.Amount > 0 {
			bad("amount", "penalties can't be positive", in.Amount)
		}
	case KindAdjustment:
		if in.Amount == 0 {
			bad("amount", "an adjustment can't be zero", in.Amount)
		}
	case KindReversal:
		bad("kind", "write reversals with Reverse", in.Kind)
	default:
		bad("kind", "unknown entry kind", in.Kind)
	}
	if in.Amount > MaxAmount || in.Amount < -MaxAmount {
		bad("amount", fmt.Sprintf("at most %d points either way", MaxAmount), in.Amount)
	}
	switch {
	case onceKind(in.Kind):
		if in.Source.Kind == "" || in.Source.ID == 0 {
			bad("source", in.Kind+" entries need a source", in.Source)
		}
	case (in.Source.Kind == "") != (in.Source.ID == 0):
		bad("source", "a source needs both a kind and an id", in.Source)
	}
	if msg := noteProblem(in.Note, in.Kind == KindAdjustment); msg != "" {
		bad("note", msg, in.Note)
	}
	if len(details) > 0 {
		return apierr.New(http.StatusUnprocessableEntity, apierr.CodeValidationFailed, "validation failed", details...)
	}
	return nil
}

// onceKind reports whether at most one entry of kind may exist per kid and source.
func onceKind(kind string) bool {
	return kind == KindChore || kind == KindChorePenalty || kind == KindStreakBonus
}

// noteProblem describes what's wrong with a trimmed note, or returns "".
func noteProblem(note string, required bool) string {
	switch {
	case required && note == "":
		return "a note is required"
	case utf8.RuneCountInString(note) > MaxNoteLength:
		return fmt.Sprintf("at most %d characters", MaxNoteLength)
	}
	return ""
}

func toEntry(r sqlc.PointsEntry) PointsEntry {
	e := PointsEntry{
		ID: r.ID, KidID: r.KidID, Kind: r.Kind, Amount: r.Amount, SourceKind: r.SourceKind.String,
		SourceID: r.SourceID.Int64, ReversesID: r.ReversesID.Int64, Note: r.Note, CreatedBy: r.CreatedBy.Int64,
	}
	if r.RequestedAmount.Valid {
		v := r.RequestedAmount.Int64
		e.RequestedAmount = &v
	}
	e.LocalDate, _ = caltime.ParseDate(r.LocalDate)
	e.CreatedAt, _ = db.ParseTimestamp(r.CreatedAt)
	return e
}

func nullString(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }
func nullInt(n int64) sql.NullInt64      { return sql.NullInt64{Int64: n, Valid: n != 0} }

// Service is the ledger for the API: reads, and parent adjustments and reversals in their own
// transaction. bus must be non-nil for Adjust and ReverseKidEntry.
type Service struct {
	db    *db.DB
	bus   *events.Bus
	clock clock.Clock
}

func New(d *db.DB, bus *events.Bus, c clock.Clock) *Service {
	return &Service{db: d, bus: bus, clock: c}
}

// Balance returns the kid's balance and available points, read in one transaction so a write
// can't land between the two.
func (s *Service) Balance(ctx context.Context, kidID int64) (PointsBalance, error) {
	tx, err := s.db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return PointsBalance{}, err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.db.WithTx(tx)
	bal, err := Balance(ctx, q, kidID)
	if err != nil {
		return PointsBalance{}, err
	}
	avail, err := Available(ctx, q, kidID)
	if err != nil {
		return PointsBalance{}, err
	}
	return PointsBalance{Balance: bal, Available: avail}, nil
}

// List returns a page of the kid's entries, newest first, each with its running balance.
func (s *Service) List(ctx context.Context, kidID int64, p paging.Params) (paging.Page[PointsEntry], error) {
	before, first, err := paging.Decode[int64](p.Cursor)
	if err != nil {
		return paging.Page[PointsEntry]{}, err
	}
	if first {
		before = 1<<63 - 1
	}
	size := p.Size()
	rows, err := s.db.ListKidPointsEntries(ctx, sqlc.ListKidPointsEntriesParams{KidID: kidID, ID: before, Limit: int64(size) + 1})
	if err != nil {
		return paging.Page[PointsEntry]{}, err
	}
	items := make([]PointsEntry, 0, len(rows))
	if len(rows) > 0 {
		// Walk down from the balance through the newest row. Later inserts only add newer ids,
		// so they can't change it.
		bal, err := s.db.KidPointsBalanceThrough(ctx, sqlc.KidPointsBalanceThroughParams{KidID: kidID, ID: rows[0].PointsEntry.ID})
		if err != nil {
			return paging.Page[PointsEntry]{}, err
		}
		for _, r := range rows {
			e := toEntry(r.PointsEntry)
			e.ReversedByID = r.ReversedBy.Int64
			e.BalanceAfter = bal
			bal -= e.Amount
			items = append(items, e)
		}
	}
	return paging.Build(items, size, func(e PointsEntry) int64 { return e.ID })
}

// Adjust adds (or, negative, subtracts) points for a kid with a required note.
func (s *Service) Adjust(ctx context.Context, kidID, amount int64, note string, by int64) (PointsEntry, error) {
	var out PointsEntry
	err := s.bus.InTx(ctx, func(tx *events.Tx) error {
		var err error
		out, err = Post(ctx, tx, s.clock, Posting{KidID: kidID, Kind: KindAdjustment, Amount: amount, Note: note, CreatedBy: by})
		return err
	})
	return out, err
}

// ReverseKidEntry reverses one of the kid's entries; an entry of another kid is not found.
func (s *Service) ReverseKidEntry(ctx context.Context, kidID, entryID int64, note string, by int64) (PointsEntry, error) {
	var out PointsEntry
	err := s.bus.InTx(ctx, func(tx *events.Tx) error {
		e, err := tx.Q.GetPointsEntry(ctx, entryID)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && e.KidID != kidID) {
			return ErrEntryNotFound
		} else if err != nil {
			return err
		}
		out, err = Reverse(ctx, tx, s.clock, entryID, note, by)
		return err
	})
	return out, err
}
