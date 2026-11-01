// Package horizon keeps chore instances materialized for the rolling horizon [today, today+7]:
// it expands each chore's schedule (internal/schedule), assigns the occurrences (internal/assign)
// and writes the result as chore_instances. See docs/features/schedules.md (Rolling horizon
// generation).
//
// Every run regenerates the whole household from the same inputs, so the result doesn't depend
// on what triggered it, and a second run changes nothing:
//
//   - An instance is frozen when it's touched (not open, or manually reassigned) or its due time
//     has passed (today's earlier slots, every earlier day). Frozen instances are never changed.
//     Every other instance in the horizon is regenerable: it's replaced by the new plan, updated
//     in place when its identity (chore, date, slot, seq, kid) is unchanged so ids stay stable.
//   - An occurrence already due when it's planned (a slot earlier today, say on a new chore) gets
//     no instance.
//   - The fairness ledger starts with the frozen instances of the past week and the horizon.
//     Chores are planned by id, occurrences in date order, one at a time; an occurrence a frozen
//     instance already covers isn't planned again.
//   - Rotation: each instance stores the state after its turn. chores.rotation_state is the state
//     after the chore's last instance dated before the horizon (before today, or before the week
//     start for flexible chores), and each run resumes from it, taking the stored state of frozen
//     instances it passes. Planned instances never feed it, so regenerating the forecast doesn't
//     advance the rotation.
package horizon

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
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

// untouched: nothing has happened to it yet. Evidence (#27) and swaps also touch an instance;
// a swap sets manually_reassigned.
func (e existing) untouched() bool { return e.Status == StatusOpen && e.ManuallyReassigned == 0 }

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
	end := today.AddDays(Days)
	// The ledger looks back a week from each date; flexible instances are dated at the week start.
	since := today.AddDays(-(assign.LoadWindowDays - 1))

	planner, err := newPlanner(ctx, q, int(hh.DefaultChoreMinutes))
	if err != nil {
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
	frozen := map[occKey][]existing{}
	for _, r := range rows {
		e := existing{ListChoreInstancesFromRow: r, members: members[r.ID]}
		// Only the horizon is regenerated: today on, plus this week's flexible instances.
		inHorizon := e.Flexible == 1 || e.OccurrenceDate >= today.String()
		if e.untouched() && e.DueAt > nowTS && inHorizon {
			regen[e.key()] = e
			continue
		}
		frozen[e.key().occKey] = append(frozen[e.key().occKey], e)
		planner.Ledger.Entries = append(planner.Ledger.Entries, ledgerEntries(e, planner.DefaultMinutes)...)
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
		ds, err := planChore(ctx, q, planner, cal, c, overrides[c.ID], now, today, end, frozen)
		if err != nil {
			return Result{}, err
		}
		want = append(want, ds...)
	}
	return apply(ctx, q, want, regen, nowTS)
}

// newPlanner loads every kid, archived too: the planner skips archived kids itself.
func newPlanner(ctx context.Context, q *sqlc.Queries, defaultMinutes int) (*assign.Planner, error) {
	users, err := q.ListKids(ctx, true)
	if err != nil {
		return nil, err
	}
	kids := make([]assign.Kid, 0, len(users))
	for _, u := range users {
		k := assign.Kid{ID: u.ID, Archived: u.ArchivedAt.Valid}
		if u.BirthDate.Valid {
			k.BirthDate, _ = caltime.ParseDate(u.BirthDate.String) // invalid = unknown, not age-gated
		}
		kids = append(kids, k)
	}
	return &assign.Planner{Kids: kids, DefaultMinutes: defaultMinutes, Ledger: &assign.Ledger{}}, nil
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

// planChore plans one chore's occurrences in the horizon. Disabled and unscheduled chores get
// none, so their regenerable instances are removed.
func planChore(ctx context.Context, q *sqlc.Queries, p *assign.Planner, cal caltime.Household, c sqlc.Chore,
	overrides map[int64]bool, now time.Time, today, end caltime.Date, frozen map[occKey][]existing,
) ([]desired, error) {
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
	from := today
	if rule.Type == schedule.TypeFlexibleWeekly {
		from = cal.WeekStartOf(today) // this week's completions are dated at its first active day
	}

	ac := assign.Chore{ID: c.ID, MinAge: optInt(c.MinAge), EstimatedMinutes: optInt(c.EstimatedMinutes),
		Overrides: overrides, Mode: c.AssignMode}
	_ = json.Unmarshal([]byte(c.AssignKids), &ac.KidIDs)
	rotation := c.AssignMode == assign.ModeRotation

	var st assign.State
	if rotation {
		var err error
		if st, err = resumeRotation(ctx, q, c, from); err != nil {
			return nil, err
		}
	}
	// One instance per occurrence (rotation, auto, shared) or one per kid (fixed, everyone).
	single := rotation || c.AssignMode == assign.ModeAuto || c.AssignMode == assign.ModeShared

	var out []desired
	seqs := map[[2]string]int64{}
	for _, o := range schedule.Expand(rule, cal, slots, from, end, 0) {
		dk := [2]string{o.Date.String(), o.Slot}
		ok := occKey{c.ID, dk[0], dk[1], seqs[dk]}
		seqs[dk]++
		kept := frozen[ok]
		if single && len(kept) > 0 {
			for _, k := range kept { // the kept turn stands; continue from its state
				if s, valid := parseState(k.RotationState); rotation && valid {
					st = s
				}
			}
			continue
		}
		if !o.DueAt.After(now) {
			continue // already due (an earlier slot today): too late to create, and nobody's turn
		}
		base := len(p.Ledger.Entries)
		planned, next := p.Plan(ac, []schedule.ScheduleOccurrence{o}, st)
		st = next
		if len(kept) > 0 {
			// fixed/everyone: kids who already have a kept instance here don't get a second one.
			// Plan added one ledger entry per instance, in order; drop those of the duplicates.
			added := p.Ledger.Entries[base:]
			var keepPlanned []assign.Planned
			var keepEntries []assign.Entry
			for i, pl := range planned {
				if slices.ContainsFunc(kept, func(k existing) bool { return k.KidID.Int64 == pl.KidID }) {
					continue
				}
				keepPlanned = append(keepPlanned, pl)
				if i < len(added) {
					keepEntries = append(keepEntries, added[i])
				}
			}
			p.Ledger.Entries = append(p.Ledger.Entries[:base], keepEntries...)
			planned = keepPlanned
		}
		for _, pl := range planned {
			d := desired{
				key:   instKey{ok, pl.KidID},
				dueAt: db.Timestamp(pl.DueAt), flexible: b2i(pl.Flexible), auto: b2i(pl.Auto),
			}
			if pl.Group != nil {
				d.group = slices.Sorted(slices.Values(pl.Group)) // stored and compared by kid id
			}
			if rotation {
				b, _ := json.Marshal(st)
				d.state = sql.NullString{String: string(b), Valid: true}
			}
			out = append(out, d)
		}
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
