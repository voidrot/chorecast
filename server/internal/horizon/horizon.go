// Package horizon keeps chore instances materialized for the rolling horizon [today, today+7]:
// it expands each chore's schedule (internal/schedule), assigns the occurrences (internal/assign)
// and writes the result as chore_instances. See docs/features/schedules.md (Rolling horizon
// generation).
//
// Every run regenerates the whole household from the same inputs, so the result doesn't depend
// on what triggered it, and a second run changes nothing:
//
//   - An instance is frozen when it's touched (not open, completed before, or manually reassigned)
//     or its due time has passed (today's earlier slots, every earlier day). Frozen instances are
//     never changed.
//     Every other instance in the horizon is regenerable: it's replaced by the new plan, updated
//     in place when its identity (chore, date, slot, seq, kid) is unchanged so ids stay stable.
//   - An occurrence already due when it's planned (a slot earlier today, say on a new chore) gets
//     no instance. Nor does a slot added (or moved later) today for a kid who already has a kept
//     instance of the chore today: an edit takes full effect tomorrow.
//   - Without a slot, a kid's instance is due at the kid's own day (or week) close; a shared
//     instance at the household's.
//   - The fairness ledger starts with the frozen instances from a week before this week's start
//     (flexible instances are dated at the week start). Chores are planned by id, occurrences in
//     date order, one at a time; an occurrence a frozen instance already covers isn't planned again.
//     A manually reassigned instance covers the occurrence of the kid it was planned for
//     (reassigned_from) too, so fixed and everyone chores don't plan that kid's again.
//   - Rotation: each instance stores the state after its turn. chores.rotation_state is the state
//     after the chore's last instance dated before the horizon (before today, or before the week
//     start for flexible chores), and each run resumes from it. Walking the occurrences, it takes
//     the stored state of each frozen instance it passes in turn order (date, due time, seq), even
//     one whose slot the schedule no longer has. Planned instances never feed it, so regenerating
//     the forecast doesn't advance the rotation.
package horizon

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/voidrot/chorecast/server/internal/assign"
	"github.com/voidrot/chorecast/server/internal/caltime"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
	"github.com/voidrot/chorecast/server/internal/household"
	"github.com/voidrot/chorecast/server/internal/schedule"
)

// Days is how far ahead instances exist: [today, today+Days].
const Days = 7

// Instance statuses (chore_instances.status).
const (
	StatusOpen      = "open"
	StatusSubmitted = "submitted"
	StatusApproved  = "approved"
	StatusMissed    = "missed"
	StatusExcused   = "excused"
)

// Generator materializes the horizon. Runs are serialized.
type Generator struct {
	db    *db.DB
	clock clock.Clock
	mu    sync.Mutex
}

func New(d *db.DB, c clock.Clock) *Generator { return &Generator{db: d, clock: c} }

// Result counts what a run changed.
type Result struct {
	Inserted, Updated, Deleted int
}

// Today is the household's current day: the day whose close is still ahead. At the exact
// instant a day closes, the next day is today.
func Today(cal caltime.Household, now time.Time) caltime.Date {
	d, _ := cal.NextDayClose(now, cal.DayEnd)
	return d
}

type occKey struct {
	chore int64
	date  string
	slot  string
	seq   int64
}

type instKey struct {
	occKey
	kid int64 // 0 = shared
}

type existing struct {
	sqlc.ListChoreInstancesFromRow
	members []int64
}

func (e existing) key() instKey {
	return instKey{occKey{e.ChoreID, e.OccurrenceDate, e.Slot, e.Seq}, e.KidID.Int64}
}

// covers reports whether the instance stands for kid's planned occurrence: it's theirs, or it
// was reassigned away from them (the new kid does it in their place).
func (e existing) covers(kid int64) bool {
	return e.KidID.Int64 == kid || (e.ReassignedFrom.Valid && e.ReassignedFrom.Int64 == kid)
}

// untouched: nothing has happened to it yet. A reassignment (or swap) sets manually_reassigned,
// and a completion stays recorded after a rejection or an approval reversal sends it back to open.
// Submitted evidence touches it too (it comes with a completion, but is checked on its own).
func (e existing) untouched() bool {
	return e.Status == StatusOpen && e.ManuallyReassigned == 0 && !e.CompletedAt.Valid && !e.HasEvidence
}

type desired struct {
	key      instKey
	dueAt    string
	flexible int64
	auto     int64
	state    sql.NullString
	group    []int64
}

// Generate brings the horizon up to date. It does nothing before first-run setup.
func (g *Generator) Generate(ctx context.Context) (Result, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	tx, err := g.db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = tx.Rollback() }()
	q := g.db.WithTx(tx)
	res, err := g.generate(ctx, q)
	if err != nil {
		return Result{}, err
	}
	return res, tx.Commit()
}

// run is one generation's inputs, shared by every chore's plan.
type run struct {
	q       *sqlc.Queries
	planner *assign.Planner
	cal     caltime.Household
	dayEnds map[int64]caltime.TimeOfDay // kids' effective day ends
	now     time.Time
	today   caltime.Date
	end     caltime.Date
	// frozen instances by occurrence, and per chore in turn order (for rotation states).
	frozen        map[occKey][]existing
	frozenByChore map[int64][]existing
	// today: occurrences that had any instance, and kids (or, for shared, any member) with a frozen one.
	todayOccs  map[occKey]bool
	keptToday  map[int64]map[int64]bool // chore -> kid
	choreToday map[int64]bool           // chore has a frozen instance today
}

func (g *Generator) generate(ctx context.Context, q *sqlc.Queries) (Result, error) {
	hh, err := q.GetHousehold(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return Result{}, nil // not set up yet
	}
	if err != nil {
		return Result{}, err
	}
	cal, err := household.Calendar(ctx, q)
	if err != nil {
		return Result{}, err
	}
	now := g.clock.Now()
	today := Today(cal, now)
	r := &run{q: q, cal: cal, now: now, today: today, end: today.AddDays(Days),
		frozen: map[occKey][]existing{}, frozenByChore: map[int64][]existing{},
		todayOccs: map[occKey]bool{}, keptToday: map[int64]map[int64]bool{}, choreToday: map[int64]bool{}}
	// The ledger looks back a week from each date, and flexible instances are dated at the week
	// start, so history starts a week before this week's start.
	since := cal.WeekStartOf(today).AddDays(-(assign.LoadWindowDays - 1))

	if r.planner, r.dayEnds, err = newPlanner(ctx, q, cal, int(hh.DefaultChoreMinutes)); err != nil {
		return Result{}, err
	}
	rows, err := q.ListChoreInstancesFrom(ctx, since.String())
	if err != nil {
		return Result{}, err
	}
	memberRows, err := q.ListChoreInstanceMembersFrom(ctx, since.String())
	if err != nil {
		return Result{}, err
	}
	members := map[int64][]int64{}
	for _, m := range memberRows {
		members[m.InstanceID] = append(members[m.InstanceID], m.KidID)
	}

	nowTS := db.Timestamp(now)
	regen := map[instKey]existing{}
	for _, row := range rows {
		e := existing{ListChoreInstancesFromRow: row, members: members[row.ID]}
		isToday := e.OccurrenceDate == today.String()
		if isToday {
			r.todayOccs[e.key().occKey] = true
		}
		// Only the horizon is regenerated: today on, plus this week's flexible instances.
		inHorizon := e.Flexible == 1 || e.OccurrenceDate >= today.String()
		if e.untouched() && e.DueAt > nowTS && inHorizon {
			regen[e.key()] = e
			continue
		}
		r.frozen[e.key().occKey] = append(r.frozen[e.key().occKey], e)
		r.frozenByChore[e.ChoreID] = append(r.frozenByChore[e.ChoreID], e)
		r.planner.Ledger.Entries = append(r.planner.Ledger.Entries, ledgerEntries(e, r.planner.DefaultMinutes)...)
		if isToday {
			r.choreToday[e.ChoreID] = true
			if r.keptToday[e.ChoreID] == nil {
				r.keptToday[e.ChoreID] = map[int64]bool{}
			}
			for _, k := range append([]int64{e.KidID.Int64, e.ReassignedFrom.Int64}, e.members...) {
				r.keptToday[e.ChoreID][k] = true
			}
		}
	}
	for _, es := range r.frozenByChore {
		slices.SortFunc(es, func(a, b existing) int {
			return turnOrder(a.OccurrenceDate, a.DueAt, a.Seq, b.OccurrenceDate, b.DueAt, b.Seq)
		})
	}

	chores, err := q.ListChores(ctx)
	if err != nil {
		return Result{}, err
	}
	slices.SortFunc(chores, func(a, b sqlc.Chore) int { return int(a.ID - b.ID) })
	overrides, err := loadOverrides(ctx, q)
	if err != nil {
		return Result{}, err
	}

	var want []desired
	for _, c := range chores {
		ds, err := r.planChore(ctx, c, overrides[c.ID])
		if err != nil {
			return Result{}, err
		}
		want = append(want, ds...)
	}
	return apply(ctx, q, want, regen, nowTS)
}

// turnOrder orders a chore's instances as the planner takes turns: by date, due time, then seq.
func turnOrder(aDate, aDue string, aSeq int64, bDate, bDue string, bSeq int64) int {
	if c := strings.Compare(aDate, bDate); c != 0 {
		return c
	}
	if c := strings.Compare(aDue, bDue); c != 0 {
		return c
	}
	return int(aSeq - bSeq)
}

// newPlanner loads every kid, archived too: the planner skips archived kids itself. It also
// returns each kid's effective day end.
func newPlanner(ctx context.Context, q *sqlc.Queries, cal caltime.Household, defaultMinutes int) (*assign.Planner, map[int64]caltime.TimeOfDay, error) {
	users, err := q.ListKids(ctx, true)
	if err != nil {
		return nil, nil, err
	}
	kids := make([]assign.Kid, 0, len(users))
	dayEnds := map[int64]caltime.TimeOfDay{}
	for _, u := range users {
		k := assign.Kid{ID: u.ID, Archived: u.ArchivedAt.Valid}
		if u.BirthDate.Valid {
			k.BirthDate, _ = caltime.ParseDate(u.BirthDate.String) // invalid = unknown, not age-gated
		}
		kids = append(kids, k)
		dayEnds[u.ID] = KidDayEnd(cal, u.DayEndOverride)
	}
	return &assign.Planner{Kids: kids, DefaultMinutes: defaultMinutes, Ledger: &assign.Ledger{}}, dayEnds, nil
}

func loadOverrides(ctx context.Context, q *sqlc.Queries) (map[int64]map[int64]bool, error) {
	rows, err := q.ListChoreEligibility(ctx)
	if err != nil {
		return nil, err
	}
	out := map[int64]map[int64]bool{}
	for _, r := range rows {
		if out[r.ChoreID] == nil {
			out[r.ChoreID] = map[int64]bool{}
		}
		out[r.ChoreID][r.KidID] = r.Allow == 1
	}
	return out, nil
}

// ledgerEntries is a kept instance's weight in the fair auto-pick, as assign.Planner counts it.
func ledgerEntries(e existing, defaultMinutes int) []assign.Entry {
	d, err := caltime.ParseDate(e.OccurrenceDate)
	if err != nil {
		return nil
	}
	minutes := max(defaultMinutes, 1)
	if e.EstimatedMinutes.Valid {
		minutes = int(e.EstimatedMinutes.Int64)
	}
	if !e.KidID.Valid {
		if len(e.members) == 0 {
			return nil
		}
		share := (minutes + len(e.members) - 1) / len(e.members)
		out := make([]assign.Entry, 0, len(e.members))
		for _, id := range e.members {
			out = append(out, assign.Entry{KidID: id, ChoreID: e.ChoreID, Date: d, Minutes: share})
		}
		return out
	}
	return []assign.Entry{{KidID: e.KidID.Int64, ChoreID: e.ChoreID, Date: d, Minutes: minutes, Auto: e.AutoAssigned == 1}}
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// KidDayEnd is a kid's effective day end: their day_end_override when it's valid, else the
// household's.
func KidDayEnd(cal caltime.Household, override sql.NullString) caltime.TimeOfDay {
	if override.Valid {
		if o, err := caltime.ParseTimeOfDay(override.String); err == nil {
			if end, err := cal.EffectiveDayEnd(&o); err == nil {
				return end
			}
		}
	}
	return cal.DayEnd
}

// SlotlessDueAt is when a kid's instance without a slot is due: at the close of its day for a
// kid whose day ends at end, or of its week when flexible (date is then the week's first day).
func SlotlessDueAt(cal caltime.Household, date caltime.Date, flexible bool, end caltime.TimeOfDay) time.Time {
	if flexible {
		return cal.WeekClose(date, end)
	}
	return cal.DayClose(date, end)
}

// dueAt is when an assigned occurrence is due. Without a slot, a kid's instance closes with the
// kid's own day (or week); shared instances use the household day end.
func (r *run) dueAt(o schedule.ScheduleOccurrence, kid int64) time.Time {
	end, ok := r.dayEnds[kid]
	if o.Slot != "" || kid == 0 || !ok {
		return o.DueAt
	}
	return SlotlessDueAt(r.cal, o.Date, o.Flexible, end)
}

// planChore plans one chore's occurrences in the horizon. Disabled and unscheduled chores get
// none, so their regenerable instances are removed.
func (r *run) planChore(ctx context.Context, c sqlc.Chore, overrides map[int64]bool) ([]desired, error) {
	if c.Disabled == 1 || !c.Schedule.Valid {
		return nil, nil
	}
	var rule schedule.ScheduleRule
	if err := json.Unmarshal([]byte(c.Schedule.String), &rule); err != nil {
		return nil, nil // unreadable rule: treat as unscheduled
	}
	var slotNames []string
	_ = json.Unmarshal([]byte(c.Slots), &slotNames)
	slots := make([]caltime.TimeOfDay, 0, len(slotNames))
	for _, s := range slotNames {
		if t, err := caltime.ParseTimeOfDay(s); err == nil {
			slots = append(slots, t)
		}
	}
	from := r.today
	if rule.Type == schedule.TypeFlexibleWeekly {
		from = r.cal.WeekStartOf(r.today) // this week's completions are dated at its first active day
	}

	ac := assign.Chore{ID: c.ID, MinAge: optInt(c.MinAge), EstimatedMinutes: optInt(c.EstimatedMinutes),
		Overrides: overrides, Mode: c.AssignMode}
	_ = json.Unmarshal([]byte(c.AssignKids), &ac.KidIDs)
	rotation := c.AssignMode == assign.ModeRotation
	// One instance per occurrence (rotation, auto, shared) or one per kid (fixed, everyone).
	single := rotation || c.AssignMode == assign.ModeAuto || c.AssignMode == assign.ModeShared

	var st assign.State
	// The chore's frozen instances in the horizon, in turn order: the rotation takes each one's
	// state as the walk passes it, whether or not the schedule still produces its occurrence.
	var kept []existing
	if rotation {
		var err error
		if st, err = resumeRotation(ctx, r.q, c, from); err != nil {
			return nil, err
		}
		for _, e := range r.frozenByChore[c.ID] {
			if e.OccurrenceDate >= from.String() {
				kept = append(kept, e)
			}
		}
	}
	passKept := func(date, due string, seq int64) {
		for len(kept) > 0 && turnOrder(kept[0].OccurrenceDate, kept[0].DueAt, kept[0].Seq, date, due, seq) <= 0 {
			if s, ok := parseState(kept[0].RotationState); ok {
				st = s
			}
			kept = kept[1:]
		}
	}

	var out []desired
	seqs := map[[2]string]int64{}
	for _, o := range schedule.Expand(rule, r.cal, slots, from, r.end, 0) {
		dk := [2]string{o.Date.String(), o.Slot}
		ok := occKey{c.ID, dk[0], dk[1], seqs[dk]}
		seqs[dk]++
		passKept(ok.date, db.Timestamp(o.DueAt), ok.seq)
		frozenHere := r.frozen[ok]
		// A slot added today, or moved later today, for a chore already done or due today: the
		// edit takes full effect tomorrow.
		newToday := o.Date == r.today && !r.todayOccs[ok]
		if single && (len(frozenHere) > 0 || !o.DueAt.After(r.now) || (newToday && r.choreToday[c.ID])) {
			continue
		}
		if !o.DueAt.After(r.now) {
			continue // already due (an earlier slot today): too late to create
		}
		base := len(r.planner.Ledger.Entries)
		planned, next := r.planner.Plan(ac, []schedule.ScheduleOccurrence{o}, st)
		added := slices.Clone(r.planner.Ledger.Entries[base:])
		var keepEntries []assign.Entry
		var ds []desired
		for i, pl := range planned {
			due := r.dueAt(o, pl.KidID)
			drop := !due.After(r.now) // the kid's own day already closed
			if !single {
				// fixed/everyone: no second instance for a kid with a kept one here (or whose
				// one was reassigned to another kid), and no slot added today for a kid with a
				// kept instance today.
				drop = drop || slices.ContainsFunc(frozenHere, func(k existing) bool { return k.covers(pl.KidID) }) ||
					(newToday && r.keptToday[c.ID][pl.KidID])
			}
			if drop {
				continue
			}
			if !single && i < len(added) {
				keepEntries = append(keepEntries, added[i]) // Plan adds one entry per instance, in order
			}
			d := desired{key: instKey{ok, pl.KidID}, dueAt: db.Timestamp(due), flexible: b2i(pl.Flexible), auto: b2i(pl.Auto)}
			if pl.Group != nil {
				d.group = slices.Sorted(slices.Values(pl.Group)) // stored and compared by kid id
			}
			if rotation {
				b, _ := json.Marshal(next)
				d.state = sql.NullString{String: string(b), Valid: true}
			}
			ds = append(ds, d)
		}
		if single {
			keepEntries = nil
			if len(ds) > 0 {
				keepEntries = added
				st = next
			} // else nobody got it: no turn taken, no load
		} else {
			st = next
		}
		r.planner.Ledger.Entries = append(r.planner.Ledger.Entries[:base], keepEntries...)
		out = append(out, ds...)
	}
	return out, nil
}

// resumeRotation returns the state after the chore's last instance dated before from, and
// persists it as chores.rotation_state. Without such an instance it's the stored state.
func resumeRotation(ctx context.Context, q *sqlc.Queries, c sqlc.Chore, from caltime.Date) (assign.State, error) {
	raw, err := q.LatestChoreRotationStateBefore(ctx, sqlc.LatestChoreRotationStateBeforeParams{
		ChoreID: c.ID, OccurrenceDate: from.String(),
	})
	switch {
	case errors.Is(err, sql.ErrNoRows):
		raw = sql.NullString{String: c.RotationState, Valid: true}
	case err != nil:
		return assign.State{}, err
	}
	st, _ := parseState(raw)
	b, _ := json.Marshal(st)
	if string(b) != c.RotationState {
		if err := q.SetChoreRotationState(ctx, sqlc.SetChoreRotationStateParams{RotationState: string(b), ID: c.ID}); err != nil {
			return assign.State{}, err
		}
	}
	return st, nil
}

func parseState(raw sql.NullString) (assign.State, bool) {
	var st assign.State
	if !raw.Valid || json.Unmarshal([]byte(raw.String), &st) != nil {
		return assign.State{}, false
	}
	return st, true
}

func optInt(v sql.NullInt64) *int {
	if !v.Valid {
		return nil
	}
	i := int(v.Int64)
	return &i
}

// apply writes the plan: regenerable instances with the same identity are updated in place, new
// ones inserted, and the rest deleted.
func apply(ctx context.Context, q *sqlc.Queries, want []desired, regen map[instKey]existing, now string) (Result, error) {
	var res Result
	for _, d := range want {
		if e, ok := regen[d.key]; ok {
			delete(regen, d.key)
			planChanged := e.DueAt != d.dueAt || e.Flexible != d.flexible || e.AutoAssigned != d.auto || e.RotationState != d.state
			membersChanged := !slices.Equal(e.members, d.group)
			if planChanged {
				if err := q.UpdateChoreInstancePlan(ctx, sqlc.UpdateChoreInstancePlanParams{
					DueAt: d.dueAt, Flexible: d.flexible, AutoAssigned: d.auto, RotationState: d.state, UpdatedAt: now, ID: e.ID,
				}); err != nil {
					return res, err
				}
			}
			if membersChanged {
				if err := q.DeleteChoreInstanceMembers(ctx, e.ID); err != nil {
					return res, err
				}
				if err := addMembers(ctx, q, e.ID, d.group); err != nil {
					return res, err
				}
			}
			if planChanged || membersChanged {
				res.Updated++
			}
			continue
		}
		id, err := q.InsertChoreInstance(ctx, sqlc.InsertChoreInstanceParams{
			ChoreID: d.key.chore, KidID: sql.NullInt64{Int64: d.key.kid, Valid: d.key.kid != 0},
			OccurrenceDate: d.key.date, Slot: d.key.slot, Seq: d.key.seq, DueAt: d.dueAt, Flexible: d.flexible,
			AutoAssigned: d.auto, RotationState: d.state, CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			return res, err
		}
		if err := addMembers(ctx, q, id, d.group); err != nil {
			return res, err
		}
		res.Inserted++
	}
	// Delete in a stable order (map iteration isn't).
	ids := make([]int64, 0, len(regen))
	for _, e := range regen {
		ids = append(ids, e.ID)
	}
	slices.Sort(ids)
	for _, id := range ids {
		if err := q.DeleteChoreInstance(ctx, id); err != nil {
			return res, err
		}
		res.Deleted++
	}
	return res, nil
}

func addMembers(ctx context.Context, q *sqlc.Queries, id int64, group []int64) error {
	for _, k := range group {
		if err := q.AddChoreInstanceMember(ctx, sqlc.AddChoreInstanceMemberParams{InstanceID: id, KidID: k}); err != nil {
			return err
		}
	}
	return nil
}
