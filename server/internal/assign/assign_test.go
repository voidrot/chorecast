package assign

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/voidrot/chorecast/server/internal/caltime"
	"github.com/voidrot/chorecast/server/internal/schedule"
)

const (
	ann int64 = 1 // 12
	ben int64 = 2 // turns 10 on 2026-11-03
	cal int64 = 3 // 7
	dee int64 = 4 // archived
	eve int64 = 5 // birth date unknown
)

func d(t *testing.T, s string) caltime.Date {
	t.Helper()
	v, err := caltime.ParseDate(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func kids(t *testing.T) []Kid {
	return []Kid{
		{ID: cal, BirthDate: d(t, "2019-01-01")}, // deliberately not in id order
		{ID: ann, BirthDate: d(t, "2014-05-01")},
		{ID: ben, BirthDate: d(t, "2016-11-03")},
		{ID: dee, BirthDate: d(t, "2015-01-01"), Archived: true},
	}
}

func intp(v int) *int { return &v }

// days returns one daily occurrence per day from from, n days.
func days(t *testing.T, from string, n int) []schedule.ScheduleOccurrence {
	t.Helper()
	h, err := caltime.NewHousehold("America/Chicago", caltime.Midnight, time.Sunday)
	if err != nil {
		t.Fatal(err)
	}
	r := schedule.ScheduleRule{Type: schedule.TypeDaily, Interval: 1, StartDate: d(t, from)}
	return schedule.Expand(r, h, nil, d(t, from), d(t, from).AddDays(n-1), 0)
}

func names(ps []Planned) string {
	byID := map[int64]string{ann: "ann", ben: "ben", cal: "cal", dee: "dee", eve: "eve"}
	var parts []string
	for _, p := range ps {
		if p.KidID == 0 {
			var g []string
			for _, id := range p.Group {
				g = append(g, byID[id])
			}
			parts = append(parts, fmt.Sprintf("%s:[%s]", p.Date.String()[5:], strings.Join(g, "+")))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s:%s", p.Date.String()[5:], byID[p.KidID]))
	}
	return strings.Join(parts, " ")
}

func TestEligibility(t *testing.T) {
	p := &Planner{Kids: append(kids(t), Kid{ID: eve})}
	c := Chore{ID: 1, MinAge: intp(10), Overrides: map[int64]bool{cal: true}}
	tests := []struct {
		kid  int64
		day  string
		want bool
	}{
		{ann, "2026-11-02", true},
		{ben, "2026-11-02", false}, // 9 the day before his birthday
		{ben, "2026-11-03", true},  // 10 on it
		{cal, "2026-11-02", true},  // younger, but allowed by an override
		{dee, "2026-11-02", false}, // archived
		{eve, "2026-11-02", true},  // unknown birth date: not enforced
		{99, "2026-11-02", false},  // not a kid
	}
	for _, tt := range tests {
		if got := p.Eligible(c, tt.kid, d(t, tt.day)); got != tt.want {
			t.Errorf("Eligible(%d, %s) = %v, want %v", tt.kid, tt.day, got, tt.want)
		}
	}
	deny := Chore{ID: 1, Overrides: map[int64]bool{ann: false}}
	if p.Eligible(deny, ann, d(t, "2026-11-02")) {
		t.Error("a deny override must exclude the kid")
	}
	p.Away = func(kid int64, day caltime.Date) bool { return kid == ann }
	if p.Eligible(Chore{ID: 1}, ann, d(t, "2026-11-02")) {
		t.Error("an away kid must not be eligible")
	}
}

func TestFixedAndEveryone(t *testing.T) {
	p := &Planner{Kids: kids(t), DefaultMinutes: 10}
	fixed := Chore{ID: 1, Mode: ModeFixed, KidIDs: []int64{cal, dee, ben}, MinAge: intp(10), Overrides: map[int64]bool{cal: true}}
	got, _ := p.Plan(fixed, days(t, "2026-11-02", 2), State{})
	if want := "11-02:cal 11-03:cal 11-03:ben"; names(got) != want {
		t.Errorf("fixed = %s, want %s", names(got), want)
	}
	everyone := Chore{ID: 2, Mode: ModeEveryone, KidIDs: []int64{cal}, MinAge: intp(10)} // KidIDs ignored
	got, _ = p.Plan(everyone, days(t, "2026-11-02", 2), State{})
	if want := "11-02:ann 11-03:ann 11-03:ben"; names(got) != want {
		t.Errorf("everyone = %s, want %s", names(got), want)
	}
}

func TestRotationSkipsAndOwes(t *testing.T) {
	away := map[string]int64{"2026-11-02": ben}
	p := &Planner{Kids: kids(t), Away: func(kid int64, day caltime.Date) bool { return away[day.String()] == kid }}
	c := Chore{ID: 1, Mode: ModeRotation, KidIDs: []int64{ann, ben, cal}}

	got, st := p.Plan(c, days(t, "2026-11-01", 7), State{})
	// Ben is away on his turn: Cal goes, Ben takes the next occurrence, then the order resumes.
	if want := "11-01:ann 11-02:cal 11-03:ben 11-04:ann 11-05:ben 11-06:cal 11-07:ann"; names(got) != want {
		t.Errorf("rotation = %s\nwant       %s", names(got), want)
	}
	if st.Cursor != 1 || st.Owed != nil {
		t.Errorf("state = %+v", st)
	}

	// Nobody available: no instance, and nobody is owed a turn nobody took.
	all := &Planner{Kids: kids(t), Away: func(int64, caltime.Date) bool { return true }}
	got, st = all.Plan(c, days(t, "2026-11-01", 2), State{Cursor: 2})
	if len(got) != 0 || st.Cursor != 2 || st.Owed != nil {
		t.Errorf("all away = %s %+v", names(got), st)
	}

	// Kids who leave the rotation for good (archived, denied) are dropped from the owed list
	// and walked past without owing.
	gone := Chore{ID: 1, Mode: ModeRotation, KidIDs: []int64{dee, ann, ben}, Overrides: map[int64]bool{ben: false}}
	got, st = (&Planner{Kids: kids(t)}).Plan(gone, days(t, "2026-11-01", 2), State{Owed: []int64{dee, 42}})
	if names(got) != "11-01:ann 11-02:ann" || st.Owed != nil {
		t.Errorf("gone = %s %+v", names(got), st)
	}

	// A kid too young for the chore is skipped and owed; they take a turn once old enough.
	young := Chore{ID: 1, Mode: ModeRotation, KidIDs: []int64{ben, ann}, MinAge: intp(10)}
	got, _ = (&Planner{Kids: kids(t)}).Plan(young, days(t, "2026-11-01", 4), State{})
	if want := "11-01:ann 11-02:ann 11-03:ben 11-04:ben"; names(got) != want {
		t.Errorf("young = %s, want %s", names(got), want)
	}
}

func TestAutoPicksLowestLoad(t *testing.T) {
	ledger := &Ledger{Entries: []Entry{{KidID: ann, ChoreID: 9, Date: d(t, "2026-10-30"), Minutes: 30}}}
	p := &Planner{Kids: kids(t), DefaultMinutes: 10, Ledger: ledger}
	c := Chore{ID: 1, Mode: ModeAuto} // no estimate: counts as the default 10 minutes
	got, _ := p.Plan(c, days(t, "2026-11-01", 3), State{})
	// Day 1: Ben and Cal tie at 0 → lowest id. Day 2: Cal has less. Day 3: tie on load and picks → id.
	if want := "11-01:ben 11-02:cal 11-03:ben"; names(got) != want {
		t.Errorf("auto = %s, want %s", names(got), want)
	}
	for _, pl := range got {
		if !pl.Auto {
			t.Errorf("not marked auto: %+v", pl)
		}
	}
	if l := ledger.Load(ben, d(t, "2026-11-03")); l != 20 {
		t.Errorf("Ben's load = %d, want 20", l)
	}
	// Ann's 30 minutes on 10-30 leave the window after 11-05.
	if ledger.Load(ann, d(t, "2026-11-05")) != 30 || ledger.Load(ann, d(t, "2026-11-06")) != 0 {
		t.Error("load window is the 7 days ending on the date")
	}

	// Tie on load, broken by fewer recent auto picks of this chore.
	tie := &Planner{Kids: kids(t), Ledger: &Ledger{Entries: []Entry{
		{KidID: ben, ChoreID: 2, Date: d(t, "2026-11-01"), Minutes: 5, Auto: true},
		{KidID: cal, ChoreID: 7, Date: d(t, "2026-11-01"), Minutes: 5},
	}}}
	got, _ = tie.Plan(Chore{ID: 2, Mode: ModeAuto, KidIDs: []int64{ben, cal}, EstimatedMinutes: intp(5)}, days(t, "2026-11-02", 1), State{})
	if names(got) != "11-02:cal" {
		t.Errorf("tie-break on picks = %s", names(got))
	}
	// A pool's order breaks a full tie.
	pool := &Planner{Kids: kids(t)}
	got, _ = pool.Plan(Chore{ID: 3, Mode: ModeAuto, KidIDs: []int64{cal, ann}, EstimatedMinutes: intp(5)}, days(t, "2026-11-02", 1), State{})
	if names(got) != "11-02:cal" {
		t.Errorf("pool order = %s", names(got))
	}
}

func TestSharedGroups(t *testing.T) {
	ledger := &Ledger{}
	p := &Planner{Kids: kids(t), Ledger: ledger}
	c := Chore{ID: 1, Mode: ModeShared, KidIDs: []int64{ann, ben, dee}, MinAge: intp(10), EstimatedMinutes: intp(15)}
	got, _ := p.Plan(c, days(t, "2026-11-02", 2), State{})
	if want := "11-02:[ann] 11-03:[ann+ben]"; names(got) != want {
		t.Errorf("shared = %s, want %s", names(got), want)
	}
	// 15 minutes split between two members rounds up to 8 each.
	if a, b := ledger.Load(ann, d(t, "2026-11-03")), ledger.Load(ben, d(t, "2026-11-03")); a != 23 || b != 8 {
		t.Errorf("shared loads = %d, %d", a, b)
	}
	none := Chore{ID: 2, Mode: ModeShared, KidIDs: []int64{dee}}
	if got, _ := p.Plan(none, days(t, "2026-11-02", 1), State{}); len(got) != 0 {
		t.Errorf("no eligible members = %s", names(got))
	}
}

// TestDeterministic: regenerating gives identical assignments, and planning in two runs (resuming
// from the returned state and the kept ledger) matches planning in one.
func TestDeterministic(t *testing.T) {
	chores := []Chore{
		{ID: 1, Mode: ModeFixed, KidIDs: []int64{ann}, EstimatedMinutes: intp(20)},
		{ID: 2, Mode: ModeRotation, KidIDs: []int64{cal, ann, ben}, EstimatedMinutes: intp(10)},
		{ID: 3, Mode: ModeAuto, EstimatedMinutes: intp(15)},
		{ID: 4, Mode: ModeAuto, MinAge: intp(10)},
		{ID: 5, Mode: ModeShared, KidIDs: []int64{ann, ben, cal}},
		{ID: 6, Mode: ModeEveryone, MinAge: intp(8)},
	}
	away := func(kid int64, day caltime.Date) bool { return kid == cal && day.Day%3 == 0 }
	run := func(occ []schedule.ScheduleOccurrence, ledger *Ledger, states map[int64]State) map[int64][]Planned {
		p := &Planner{Kids: kids(t), DefaultMinutes: 10, Away: away, Ledger: ledger}
		out := map[int64][]Planned{}
		for _, c := range chores {
			got, st := p.Plan(c, occ, states[c.ID])
			states[c.ID] = st
			out[c.ID] = got
		}
		return out
	}
	occ := days(t, "2026-10-28", 14)
	first := run(occ, &Ledger{}, map[int64]State{})
	if second := run(occ, &Ledger{}, map[int64]State{}); !reflect.DeepEqual(first, second) {
		t.Fatal("regenerating gave different assignments")
	}

	// Two runs of 7 days, resuming from the returned states and the kept ledger.
	ledger, states := &Ledger{}, map[int64]State{}
	a, b := run(occ[:7], ledger, states), run(occ[7:], ledger, states)
	for _, c := range chores {
		split := append(a[c.ID], b[c.ID]...)
		if c.Mode == ModeAuto {
			// In one run the auto-pick also sees other chores' later days, so only the count matches.
			if len(split) != len(first[c.ID]) {
				t.Errorf("chore %d: %d instances split vs %d", c.ID, len(split), len(first[c.ID]))
			}
			continue
		}
		if names(split) != names(first[c.ID]) {
			t.Errorf("chore %d (%s) split run:\n%s\none run:\n%s", c.ID, c.Mode, names(split), names(first[c.ID]))
		}
	}
}

func TestSplitPoints(t *testing.T) {
	tests := []struct {
		points  int
		policy  string
		helpers []int64
		want    map[int64]int
	}{
		{10, PointsCompleterTakesAll, []int64{ben, cal}, map[int64]int{ann: 10}},
		{10, PointsSplitEvenly, nil, map[int64]int{ann: 10}},
		{10, PointsSplitEvenly, []int64{ben, cal}, map[int64]int{ann: 4, ben: 3, cal: 3}},
		{10, PointsSplitEvenly, []int64{ann, ben, ben}, map[int64]int{ann: 5, ben: 5}}, // duplicates and the completer ignored
		{1, PointsSplitEvenly, []int64{ben}, map[int64]int{ann: 1, ben: 0}},
	}
	for _, tt := range tests {
		if got := SplitPoints(tt.points, tt.policy, ann, tt.helpers); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("SplitPoints(%d, %s, %v) = %v, want %v", tt.points, tt.policy, tt.helpers, got, tt.want)
		}
	}
}
