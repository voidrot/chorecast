// Package assign decides who gets each occurrence of a chore: fixed kids, everyone eligible, a
// rotation, a fair auto-pick, or a shared instance for a group. It's pure and deterministic: given
// the same kids, chores, occurrences, starting state and ledger, it produces the same plan, so
// regenerating the horizon is reproducible. The horizon generator (#23) persists the plan as
// instances and the returned state. See docs/features/schedules.md (Assignment modes).
package assign

import (
	"slices"

	"github.com/voidrot/chorecast/server/internal/caltime"
	"github.com/voidrot/chorecast/server/internal/schedule"
)

// Modes.
const (
	ModeFixed    = "fixed"
	ModeEveryone = "everyone"
	ModeRotation = "rotation"
	ModeAuto     = "auto"
	ModeShared   = "shared"
)

// Shared-chore policies.
const (
	PointsCompleterTakesAll = "completer_takes_all"
	PointsSplitEvenly       = "split_evenly"
	PenaltyAll              = "all"
	PenaltyNone             = "none"
)

// LoadWindowDays is the fairness window: the 7 days ending on the instance date.
const LoadWindowDays = 7

// Kid is what assignment needs to know about a kid.
type Kid struct {
	ID        int64
	BirthDate caltime.Date // zero = unknown; the minimum age then isn't enforced
	Archived  bool
}

// AgeOn is the kid's age in whole years on d, or -1 when the birth date is unknown.
func (k Kid) AgeOn(d caltime.Date) int {
	if k.BirthDate.IsZero() {
		return -1
	}
	age := d.Year - k.BirthDate.Year
	if d.Month < k.BirthDate.Month || (d.Month == k.BirthDate.Month && d.Day < k.BirthDate.Day) {
		age--
	}
	return age
}

// Chore is what assignment needs to know about a chore.
type Chore struct {
	ID               int64
	EstimatedMinutes *int
	MinAge           *int
	Overrides        map[int64]bool // per-kid minimum-age exceptions: true = allow anyway, false = never
	Mode             string
	KidIDs           []int64 // fixed: the kids; rotation: the order; auto: the pool (empty = all); shared: the group
}

// State carries a rotation between generation runs.
type State struct {
	Cursor int     `json:"cursor,omitempty"` // index into KidIDs of the next kid in turn
	Owed   []int64 `json:"owed,omitempty"`   // kids skipped while ineligible; they take the next turn they can
}

// Planned is one instance to create for an occurrence.
type Planned struct {
	schedule.ScheduleOccurrence
	KidID int64   // the assignee; 0 for a shared instance
	Group []int64 // shared: the members who may complete it
	Auto  bool    // picked by the fair auto-pick
}

// Entry is one instance in the fairness ledger.
type Entry struct {
	KidID   int64
	ChoreID int64
	Date    caltime.Date
	Minutes int
	Auto    bool
}

// Ledger is the instances the fair auto-pick weighs: those already kept for other chores and
// earlier days, plus what this run plans (Plan appends to it, so later chores see earlier picks).
type Ledger struct {
	Entries []Entry
}

func inWindow(e, d caltime.Date) bool {
	n := e.DaysUntil(d)
	return n >= 0 && n < LoadWindowDays
}

// Load is the kid's minutes over the window ending on d.
func (l *Ledger) Load(kid int64, d caltime.Date) int {
	sum := 0
	for _, e := range l.Entries {
		if e.KidID == kid && inWindow(e.Date, d) {
			sum += e.Minutes
		}
	}
	return sum
}

// AutoPicks counts the kid's auto picks of the chore over the window ending on d.
func (l *Ledger) AutoPicks(chore, kid int64, d caltime.Date) int {
	n := 0
	for _, e := range l.Entries {
		if e.Auto && e.ChoreID == chore && e.KidID == kid && inWindow(e.Date, d) {
			n++
		}
	}
	return n
}

// Planner plans instances for the household's kids.
type Planner struct {
	Kids           []Kid
	DefaultMinutes int // load of a chore without an estimate (household setting)
	// Away reports a kid who is away (or the household paused) on a day: skipped, and owed a
	// rotation turn. Absences arrive in Phase 2; nil means nobody is away.
	Away   func(kid int64, d caltime.Date) bool
	Ledger *Ledger
}

func (p *Planner) kid(id int64) (Kid, bool) {
	i := slices.IndexFunc(p.Kids, func(k Kid) bool { return k.ID == id })
	if i < 0 {
		return Kid{}, false
	}
	return p.Kids[i], true
}

// excluded reports kids who never get the chore: unknown, archived, or denied by an override.
func (p *Planner) excluded(c Chore, id int64) bool {
	k, ok := p.kid(id)
	if !ok || k.Archived {
		return true
	}
	allow, has := c.Overrides[id]
	return has && !allow
}

// Eligible reports whether the kid can get the chore on d: not excluded, not away, and old enough
// on that day unless an override allows them.
func (p *Planner) Eligible(c Chore, id int64, d caltime.Date) bool {
	if p.excluded(c, id) {
		return false
	}
	if p.Away != nil && p.Away(id, d) {
		return false
	}
	if c.MinAge == nil || c.Overrides[id] {
		return true
	}
	k, _ := p.kid(id)
	age := k.AgeOn(d)
	return age < 0 || age >= *c.MinAge
}

func (p *Planner) minutes(c Chore) int {
	if c.EstimatedMinutes != nil {
		return *c.EstimatedMinutes
	}
	return max(p.DefaultMinutes, 1)
}

// sortedKidIDs are all kids by id: the stable order for everyone and auto without a pool.
func (p *Planner) sortedKidIDs() []int64 {
	ids := make([]int64, 0, len(p.Kids))
	for _, k := range p.Kids {
		ids = append(ids, k.ID)
	}
	slices.Sort(ids)
	return ids
}

// Plan assigns the chore's occurrences, in order, starting from st. It returns the instances
// and the state to resume from. Occurrences nobody is eligible for produce no instance.
func (p *Planner) Plan(c Chore, occ []schedule.ScheduleOccurrence, st State) ([]Planned, State) {
	if p.Ledger == nil {
		p.Ledger = &Ledger{}
	}
	st = State{Cursor: st.Cursor, Owed: slices.Clone(st.Owed)}
	var out []Planned
	single := func(o schedule.ScheduleOccurrence, kid int64, auto bool) {
		out = append(out, Planned{ScheduleOccurrence: o, KidID: kid, Auto: auto})
		p.Ledger.Entries = append(p.Ledger.Entries, Entry{KidID: kid, ChoreID: c.ID, Date: o.Date, Minutes: p.minutes(c), Auto: auto})
	}
	for _, o := range occ {
		switch c.Mode {
		case ModeFixed, ModeEveryone:
			ids := c.KidIDs
			if c.Mode == ModeEveryone {
				ids = p.sortedKidIDs()
			}
			for _, id := range ids {
				if p.Eligible(c, id, o.Date) {
					single(o, id, false)
				}
			}
		case ModeRotation:
			if id, ok := p.rotate(c, o.Date, &st); ok {
				single(o, id, false)
			}
		case ModeAuto:
			if id, ok := p.pick(c, o.Date); ok {
				single(o, id, true)
			}
		case ModeShared:
			var group []int64
			for _, id := range c.KidIDs {
				if p.Eligible(c, id, o.Date) {
					group = append(group, id)
				}
			}
			if len(group) == 0 {
				continue
			}
			out = append(out, Planned{ScheduleOccurrence: o, Group: group})
			share := (p.minutes(c) + len(group) - 1) / len(group) // the work is split among members
			for _, id := range group {
				p.Ledger.Entries = append(p.Ledger.Entries, Entry{KidID: id, ChoreID: c.ID, Date: o.Date, Minutes: share})
			}
		}
	}
	if len(st.Owed) == 0 {
		st.Owed = nil
	}
	return out, st
}

// rotate takes the first eligible owed kid; otherwise it walks from the cursor to the first
// eligible kid, adding kids skipped while temporarily ineligible to the owed list.
func (p *Planner) rotate(c Chore, d caltime.Date, st *State) (int64, bool) {
	n := len(c.KidIDs)
	if n == 0 {
		return 0, false
	}
	st.Owed = slices.DeleteFunc(st.Owed, func(id int64) bool {
		return !slices.Contains(c.KidIDs, id) || p.excluded(c, id) // left the rotation for good
	})
	for i, id := range st.Owed {
		if p.Eligible(c, id, d) {
			st.Owed = slices.Delete(st.Owed, i, i+1)
			return id, true
		}
	}
	before := slices.Clone(st.Owed)
	cursor := ((st.Cursor % n) + n) % n
	for i := range n {
		id := c.KidIDs[(cursor+i)%n]
		if p.Eligible(c, id, d) {
			st.Cursor = (cursor + i + 1) % n
			return id, true
		}
		if !p.excluded(c, id) && !slices.Contains(st.Owed, id) {
			st.Owed = append(st.Owed, id)
		}
	}
	// Nobody can take it: drop what this walk added, so nobody is owed a turn nobody took.
	st.Owed = before
	return 0, false
}

// pick is the fair auto-pick: the eligible kid with the lowest load over the window, then the
// fewest auto picks of this chore in it, then the pool order (kid id when the pool is everyone).
func (p *Planner) pick(c Chore, d caltime.Date) (int64, bool) {
	pool := c.KidIDs
	if len(pool) == 0 {
		pool = p.sortedKidIDs()
	}
	best, bestLoad, bestPicks, found := int64(0), 0, 0, false
	for _, id := range pool {
		if !p.Eligible(c, id, d) {
			continue
		}
		load, picks := p.Ledger.Load(id, d), p.Ledger.AutoPicks(c.ID, id, d)
		if !found || load < bestLoad || (load == bestLoad && picks < bestPicks) {
			best, bestLoad, bestPicks, found = id, load, picks, true
		}
	}
	return best, found
}

// SplitPoints divides a shared chore's points between the completer and the helpers they tagged:
// all to the completer, or evenly with the remainder to the completer.
func SplitPoints(points int, policy string, completer int64, helpers []int64) map[int64]int {
	out := map[int64]int{completer: points}
	var others []int64
	for _, h := range helpers {
		if h != completer && !slices.Contains(others, h) {
			others = append(others, h)
		}
	}
	if policy != PointsSplitEvenly || len(others) == 0 {
		return out
	}
	each := points / (len(others) + 1)
	out[completer] = points - each*len(others)
	for _, h := range others {
		out[h] = each
	}
	return out
}
