package horizon_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/voidrot/chorecast/server/internal/apierr"
	"github.com/voidrot/chorecast/server/internal/assign"
	"github.com/voidrot/chorecast/server/internal/caltime"
	"github.com/voidrot/chorecast/server/internal/chores"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
	"github.com/voidrot/chorecast/server/internal/horizon"
	"github.com/voidrot/chorecast/server/internal/household"
	"github.com/voidrot/chorecast/server/internal/kids"
	"github.com/voidrot/chorecast/server/internal/schedule"
)

// Wednesday 4 November 2026, 12:00 in Chicago (CST, after the 1 November fallback). The
// household's day ends at 21:00 and its weeks start on Sunday.
var wednesdayNoon = time.Date(2026, 11, 4, 18, 0, 0, 0, time.UTC)

type fixture struct {
	t      *testing.T
	db     *db.DB
	clock  *clock.Fake
	gen    *horizon.Generator
	chores *chores.Service
	kids   *kids.Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	d, err := db.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	clk := clock.NewFake(wednesdayNoon)
	f := &fixture{t: t, db: d, clock: clk, gen: horizon.New(d, clk), chores: chores.New(d, clk, nil), kids: kids.NewService(d, clk, nil)}
	if _, err := f.gen.Generate(t.Context()); err != nil { // before setup: nothing to do
		t.Fatalf("generate before setup: %v", err)
	}
	if err := d.CreateHousehold(t.Context(), sqlc.CreateHouseholdParams{
		Name: "Test", Timezone: "America/Chicago", DayEnd: "21:00", WeekStart: 0, CreatedAt: db.Timestamp(wednesdayNoon),
	}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) kid(name string) int64 {
	f.t.Helper()
	k, err := f.kids.Create(f.t.Context(), kids.KidInput{DisplayName: name, Color: "#2563eb"})
	if err != nil {
		f.t.Fatal(err)
	}
	return k.ID
}

func date(s string) caltime.Date {
	d, err := caltime.ParseDate(s)
	if err != nil {
		panic(err)
	}
	return d
}

func daily(start string) *schedule.ScheduleRule {
	return &schedule.ScheduleRule{Type: schedule.TypeDaily, StartDate: date(start)}
}

func (f *fixture) chore(in chores.ChoreInput) chores.Chore {
	f.t.Helper()
	c, err := f.chores.Create(f.t.Context(), in)
	if err != nil {
		f.t.Fatal(err)
	}
	return c
}

func (f *fixture) update(c chores.Chore) chores.Chore {
	f.t.Helper()
	out, err := f.chores.Update(f.t.Context(), c.ID, c.ChoreInput)
	if err != nil {
		f.t.Fatal(err)
	}
	return out
}

func (f *fixture) generate() horizon.Result {
	f.t.Helper()
	res, err := f.gen.Generate(f.t.Context())
	if err != nil {
		f.t.Fatal(err)
	}
	return res
}

func (f *fixture) list() []horizon.ChoreInstance {
	f.t.Helper()
	out, err := f.gen.List(f.t.Context(), horizon.ListFilter{From: date("2026-10-01"), To: date("2026-12-31")})
	if err != nil {
		f.t.Fatal(err)
	}
	return out
}

func (f *fixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.db.SQL.ExecContext(f.t.Context(), query, args...); err != nil {
		f.t.Fatal(err)
	}
}

// key names an instance for comparisons: date, slot, seq and assignee.
func key(i horizon.ChoreInstance) string {
	return fmt.Sprintf("%s %s #%d kid %d", i.Date, i.Slot, i.Seq, i.KidID)
}

func keys(is []horizon.ChoreInstance) []string {
	out := make([]string, 0, len(is))
	for _, i := range is {
		out = append(out, key(i))
	}
	slices.Sort(out)
	return out
}

func byKey(is []horizon.ChoreInstance) map[string]horizon.ChoreInstance {
	out := map[string]horizon.ChoreInstance{}
	for _, i := range is {
		out[key(i)] = i
	}
	return out
}

func TestGenerateFillsTheHorizon(t *testing.T) {
	f := newFixture(t)
	sam, ada := f.kid("Sam"), f.kid("Ada")
	f.chore(chores.ChoreInput{Name: "Dishes", Schedule: daily("2026-11-01")}) // everyone

	res := f.generate()
	got := f.list()
	if res.Inserted != 16 || len(got) != 16 {
		t.Fatalf("inserted %d, listed %d; want 8 days x 2 kids", res.Inserted, len(got))
	}
	cal, _ := caltime.NewHousehold("America/Chicago", caltime.MustTimeOfDay(21, 0), time.Sunday)
	for _, i := range got {
		if i.Date.Before(date("2026-11-04")) || i.Date.After(date("2026-11-11")) {
			t.Errorf("%s is outside [today, today+7]", key(i))
		}
		if i.KidID != sam && i.KidID != ada {
			t.Errorf("%s: unexpected kid", key(i))
		}
		if !i.DueAt.Equal(cal.DayClose(i.Date, cal.DayEnd)) || i.Status != horizon.StatusOpen {
			t.Errorf("%s: due %v status %s", key(i), i.DueAt, i.Status)
		}
		if i.Planned != i.Date.After(date("2026-11-04")) {
			t.Errorf("%s: planned = %v", key(i), i.Planned)
		}
	}

	// Idempotent: a second run changes nothing and keeps the same rows.
	if res := f.generate(); res != (horizon.Result{}) {
		t.Errorf("second run = %+v, want no changes", res)
	}
	again := f.list()
	for i := range got {
		if again[i].ID != got[i].ID {
			t.Fatalf("ids changed on re-run: %v -> %v", got[i], again[i])
		}
	}
}

func TestDisabledChoresAndArchivedKidsAreSkipped(t *testing.T) {
	f := newFixture(t)
	sam, ada := f.kid("Sam"), f.kid("Ada")
	c := f.chore(chores.ChoreInput{Name: "Dishes", Schedule: daily("2026-11-01")})
	f.chore(chores.ChoreInput{Name: "Unscheduled"})
	f.generate()

	if _, err := f.kids.Archive(t.Context(), "ada"); err != nil {
		t.Fatal(err)
	}
	f.generate()
	for _, i := range f.list() {
		if i.KidID != sam {
			t.Errorf("archived kid %d still has %s", ada, key(i))
		}
	}
	if n := len(f.list()); n != 8 {
		t.Errorf("%d instances, want Sam's 8", n)
	}

	c.Disabled = true
	f.update(c)
	f.generate()
	if n := len(f.list()); n != 0 {
		t.Errorf("disabled chore still has %d instances", n)
	}
}

func TestEditMidWeekKeepsPassedSlotsAndTouchedInstances(t *testing.T) {
	f := newFixture(t)
	f.kid("Sam")
	c := f.chore(chores.ChoreInput{Name: "Feed the dog", Slots: []string{"07:00", "18:00"}, Schedule: daily("2026-11-01")})

	// Created at noon: this morning's 07:00 slot had already passed, so it gets no instance.
	f.clock.Set(time.Date(2026, 11, 4, 6, 0, 0, 0, time.UTC)) // Wednesday 00:00 local
	f.generate()
	f.clock.Set(wednesdayNoon)
	before := byKey(f.list())
	morning, ok := before["2026-11-04 07:00 #0 kid 1"]
	if !ok {
		t.Fatalf("no 07:00 instance today: %v", keys(f.list()))
	}
	submitted := before["2026-11-06 07:00 #0 kid 1"]
	reassigned := before["2026-11-07 18:00 #0 kid 1"]
	f.exec("UPDATE chore_instances SET status = 'submitted' WHERE id = ?", submitted.ID)
	f.exec("UPDATE chore_instances SET manually_reassigned = 1 WHERE id = ?", reassigned.ID)

	c.Slots = []string{"07:00", "19:00"}
	f.update(c)
	f.generate()
	after := byKey(f.list())

	if got := after["2026-11-04 07:00 #0 kid 1"]; got.ID != morning.ID {
		t.Errorf("today's passed 07:00 instance was replaced: %+v", got)
	}
	if _, ok := after["2026-11-04 18:00 #0 kid 1"]; ok {
		t.Error("today's untouched 18:00 instance wasn't regenerated")
	}
	if _, ok := after["2026-11-04 19:00 #0 kid 1"]; ok {
		t.Error("today's new 19:00 slot was created; with a kept instance today the edit starts tomorrow")
	}
	if got := after["2026-11-06 07:00 #0 kid 1"]; got.ID != submitted.ID || got.Status != horizon.StatusSubmitted {
		t.Errorf("submitted instance not preserved: %+v", got)
	}
	if got := after["2026-11-07 18:00 #0 kid 1"]; got.ID != reassigned.ID || !got.ManuallyReassigned {
		t.Errorf("reassigned instance not preserved: %+v", got)
	}
	if _, ok := after["2026-11-07 19:00 #0 kid 1"]; !ok {
		t.Error("the reassigned day's new slot is missing")
	}
	// Untouched future instances whose identity didn't change keep their rows.
	if after["2026-11-08 07:00 #0 kid 1"].ID != before["2026-11-08 07:00 #0 kid 1"].ID {
		t.Error("an unchanged future instance got a new row")
	}
	if n := len(after); n != 2*8 {
		t.Errorf("%d instances, want today's kept 07:00, 7 days x 2 slots and the kept 18:00: %v", n, keys(f.list()))
	}
}

func TestTimezoneChangeRegeneratesDueTimes(t *testing.T) {
	f := newFixture(t)
	f.kid("Sam")
	f.chore(chores.ChoreInput{Name: "Dishes", Schedule: daily("2026-11-01")})
	// Wednesday 15:00 in Chicago: 22:00 in Berlin, after its 21:00 day end.
	f.clock.Set(time.Date(2026, 11, 4, 21, 0, 0, 0, time.UTC))
	f.generate()
	before := byKey(f.list())

	hs := household.New(f.db, nil)
	settings, err := hs.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	settings.Timezone = "Europe/Berlin"
	if _, err := hs.Update(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	f.generate()
	after := f.list()

	berlin, _ := caltime.NewHousehold("Europe/Berlin", caltime.MustTimeOfDay(21, 0), time.Sunday)
	if horizon.Today(berlin, f.clock.Now()) != date("2026-11-05") {
		t.Fatal("fixture: in Berlin it should already be Thursday")
	}
	for _, i := range after {
		if i.Date == date("2026-11-04") {
			// Wednesday is over in the new calendar; its instance isn't regenerated.
			if old := before[key(i)]; i.ID != old.ID || !i.DueAt.Equal(old.DueAt) {
				t.Errorf("Wednesday's instance changed: %+v -> %+v", old, i)
			}
			continue
		}
		if want := berlin.DayClose(i.Date, berlin.DayEnd); !i.DueAt.Equal(want) {
			t.Errorf("%s due %v, want Berlin day close %v", key(i), i.DueAt, want)
		}
		if old, ok := before[key(i)]; ok && old.ID != i.ID {
			t.Errorf("%s got a new row instead of a new due time", key(i))
		}
	}
	if _, ok := byKey(after)["2026-11-12  #0 kid 1"]; !ok {
		t.Errorf("the horizon didn't move to Berlin's today+7: %v", keys(after))
	}
}

func TestFlexibleWeeklyChores(t *testing.T) {
	f := newFixture(t)
	sam := f.kid("Sam")
	c := f.chore(chores.ChoreInput{Name: "Practice piano", Schedule: &schedule.ScheduleRule{
		Type: schedule.TypeFlexibleWeekly, StartDate: date("2026-10-01"), TimesPerWeek: 3,
	}})
	f.generate()

	cal, _ := caltime.NewHousehold("America/Chicago", caltime.MustTimeOfDay(21, 0), time.Sunday)
	got := f.list()
	want := []string{}
	for _, week := range []string{"2026-11-01", "2026-11-08"} { // this week (Sunday) and next
		for seq := range 3 {
			want = append(want, fmt.Sprintf("%s  #%d kid %d", week, seq, sam))
		}
	}
	if !slices.Equal(keys(got), want) {
		t.Fatalf("instances = %v, want %v", keys(got), want)
	}
	for _, i := range got {
		if !i.Flexible || !i.DueAt.Equal(cal.WeekClose(i.Date, cal.DayEnd)) {
			t.Errorf("%s: flexible %v due %v, want week close", key(i), i.Flexible, i.DueAt)
		}
	}

	// One completion this week is in; then the chore drops to twice a week.
	first, second := byKey(got)[want[0]], byKey(got)[want[1]]
	f.exec("UPDATE chore_instances SET status = 'approved' WHERE id = ?", first.ID)
	c.Schedule.TimesPerWeek = 2
	f.update(c)
	f.generate()
	after := byKey(f.list())
	if len(after) != 4 {
		t.Errorf("instances = %v, want 2 this week and 2 next", keys(f.list()))
	}
	if got := after[want[0]]; got.ID != first.ID || got.Status != horizon.StatusApproved {
		t.Errorf("the approved completion changed: %+v", got)
	}
	if got := after[want[1]]; got.ID != second.ID {
		t.Errorf("the untouched second completion got a new row: %+v", got)
	}
	if _, ok := after[want[2]]; ok {
		t.Error("third completion this week should be gone")
	}
}

func rotationState(t *testing.T, f *fixture, id int64) assign.State {
	t.Helper()
	c, err := f.db.GetChore(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	var st assign.State
	if err := json.Unmarshal([]byte(c.RotationState), &st); err != nil {
		t.Fatal(err)
	}
	return st
}

func assignees(is []horizon.ChoreInstance) map[string]int64 {
	out := map[string]int64{}
	for _, i := range is {
		out[i.Date.String()] = i.KidID
	}
	return out
}

func TestRotationDoesNotAdvanceOnRegeneration(t *testing.T) {
	f := newFixture(t)
	sam, ada, kim := f.kid("Sam"), f.kid("Ada"), f.kid("Kim")
	c := f.chore(chores.ChoreInput{Name: "Trash", Schedule: daily("2026-11-01"),
		Assignment: &chores.ChoreAssignment{Mode: assign.ModeRotation, KidIDs: []int64{sam, ada, kim}}})
	f.generate()
	forecast := assignees(f.list())
	order := []int64{sam, ada, kim}
	for n := range 8 {
		d := date("2026-11-04").AddDays(n).String()
		if forecast[d] != order[n%3] {
			t.Fatalf("day %s: kid %d, want %d (forecast %v)", d, forecast[d], order[n%3], forecast)
		}
	}

	// Re-running, or an edit that doesn't touch the rotation, keeps the same turns.
	f.generate()
	c.Name = "Take out the trash"
	f.update(c)
	f.generate()
	if got := assignees(f.list()); !mapsEqual(got, forecast) {
		t.Fatalf("regeneration moved the rotation: %v -> %v", forecast, got)
	}
	if st := rotationState(t, f, c.ID); st.Cursor != 0 {
		t.Errorf("chores.rotation_state advanced to %+v before any day passed", st)
	}

	// A day later the forecast holds: the rotation resumes from yesterday's kept turn.
	f.clock.Advance(24 * time.Hour)
	f.generate()
	got := assignees(f.list())
	for d, kid := range forecast {
		if got[d] != kid {
			t.Errorf("day %s: kid %d after a day passed, forecast said %d", d, got[d], kid)
		}
	}
	if got["2026-11-12"] != order[8%3] {
		t.Errorf("new day: kid %d, want %d", got["2026-11-12"], order[8%3])
	}
	if st := rotationState(t, f, c.ID); st.Cursor != 1 {
		t.Errorf("rotation state = %+v, want the state after Wednesday's turn (cursor 1)", st)
	}

	// Changing the order restarts the rotation from today with its first kid.
	c.Assignment.KidIDs = []int64{kim, sam, ada}
	f.update(c)
	f.generate()
	got = assignees(f.list())
	if got["2026-11-05"] != kim || got["2026-11-06"] != sam || got["2026-11-04"] != sam {
		t.Errorf("after reorder: %v", got)
	}
}

func mapsEqual(a, b map[string]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func TestSharedChoreIsOneInstanceForTheGroup(t *testing.T) {
	f := newFixture(t)
	sam, ada := f.kid("Sam"), f.kid("Ada")
	f.kid("Kim")
	f.chore(chores.ChoreInput{Name: "Tidy the playroom", Schedule: daily("2026-11-01"),
		Assignment: &chores.ChoreAssignment{Mode: assign.ModeShared, KidIDs: []int64{ada, sam}}})
	f.generate()
	got := f.list()
	if len(got) != 8 {
		t.Fatalf("%d instances, want one a day", len(got))
	}
	for _, i := range got {
		if i.KidID != 0 || !slices.Equal(i.GroupKidIDs, []int64{sam, ada}) {
			t.Errorf("%s: kid %d group %v", key(i), i.KidID, i.GroupKidIDs)
		}
	}
	mine, err := f.gen.List(t.Context(), horizon.ListFilter{KidID: sam, From: date("2026-11-04"), To: date("2026-11-11")})
	if err != nil || len(mine) != 8 {
		t.Errorf("a member's list has %d instances (err %v), want the shared ones", len(mine), err)
	}
	if res := f.generate(); res != (horizon.Result{}) {
		t.Errorf("re-run = %+v, want no changes", res)
	}
}

func TestAutoPickIsReproducible(t *testing.T) {
	f := newFixture(t)
	f.kid("Sam")
	f.kid("Ada")
	ten := 10
	for _, name := range []string{"Laundry", "Vacuum"} {
		f.chore(chores.ChoreInput{Name: name, EstimatedMinutes: &ten, Schedule: daily("2026-11-01"),
			Assignment: &chores.ChoreAssignment{Mode: assign.ModeAuto}})
	}
	f.generate()
	first := keys(f.list())
	load := map[int64]int{}
	for _, i := range f.list() {
		load[i.KidID]++
		if !i.AutoAssigned {
			t.Errorf("%s isn't marked auto-assigned", key(i))
		}
	}
	if len(load) != 2 || load[1] != load[2] {
		t.Errorf("auto-pick loads = %v, want an even split", load)
	}
	if res := f.generate(); res != (horizon.Result{}) {
		t.Errorf("re-run = %+v, want no changes", res)
	}
	if !slices.Equal(keys(f.list()), first) {
		t.Error("auto-pick changed on re-run")
	}
}

// Removing a slot whose turn is kept doesn't hand the next turn to the same kid.
func TestRotationResumesFromAKeptTurnTheScheduleDropped(t *testing.T) {
	f := newFixture(t)
	sam, ada := f.kid("Sam"), f.kid("Ada")
	c := f.chore(chores.ChoreInput{Name: "Walk the dog", Slots: []string{"07:00", "18:00"}, Schedule: daily("2026-11-01"),
		Assignment: &chores.ChoreAssignment{Mode: assign.ModeRotation, KidIDs: []int64{sam, ada}}})
	f.clock.Set(time.Date(2026, 11, 4, 6, 0, 0, 0, time.UTC)) // Wednesday 00:00 local
	f.generate()
	f.clock.Set(wednesdayNoon) // the 07:00 turn (Sam) is now kept
	c.Slots = []string{"18:00"}
	f.update(c)
	f.generate()

	turns := map[string]int64{}
	for _, i := range f.list() {
		turns[i.Date.String()+" "+i.Slot] = i.KidID
	}
	if turns["2026-11-04 07:00"] != sam || turns["2026-11-04 18:00"] != ada {
		t.Errorf("today: 07:00 kid %d, 18:00 kid %d; want Sam then Ada", turns["2026-11-04 07:00"], turns["2026-11-04 18:00"])
	}
	if turns["2026-11-05 18:00"] != sam || turns["2026-11-06 18:00"] != ada {
		t.Errorf("following days: %v", turns)
	}
}

// A flexible chore's pick weighs the week before its week start, so it doesn't change as the
// week goes on.
func TestFlexibleAutoPickIsStableThroughTheWeek(t *testing.T) {
	f := newFixture(t)
	sam, ada := f.kid("Sam"), f.kid("Ada")
	ten := 10
	other := f.chore(chores.ChoreInput{Name: "Old chore", EstimatedMinutes: &ten})
	f.exec(`INSERT INTO chore_instances (chore_id, kid_id, occurrence_date, due_at, status, created_at, updated_at)
		VALUES (?, ?, '2026-10-27', '2026-10-28T02:00:00.000Z', 'approved', '', '')`, other.ID, sam)
	f.chore(chores.ChoreInput{Name: "Clean the car", EstimatedMinutes: &ten, Schedule: &schedule.ScheduleRule{
		Type: schedule.TypeFlexibleWeekly, StartDate: date("2026-10-01"), TimesPerWeek: 1,
	}, Assignment: &chores.ChoreAssignment{Mode: assign.ModeAuto}})

	pick := func() int64 {
		t.Helper()
		for _, i := range f.list() {
			if i.Date == date("2026-11-01") && i.Flexible {
				return i.KidID
			}
		}
		t.Fatal("no flexible instance this week")
		return 0
	}
	f.clock.Set(time.Date(2026, 11, 1, 18, 0, 0, 0, time.UTC)) // Sunday noon
	f.generate()
	if got := pick(); got != ada {
		t.Fatalf("Sunday pick = kid %d, want Ada (Sam did 10 minutes on Tuesday)", got)
	}
	f.clock.Set(wednesdayNoon)
	if res := f.generate(); res != (horizon.Result{}) { // next week's instance exists since Sunday
		t.Errorf("Wednesday run = %+v, want no changes", res)
	}
	if got := pick(); got != ada {
		t.Errorf("Wednesday pick = kid %d, want the same as Sunday (Ada)", got)
	}
}

func maxInstanceID(t *testing.T, f *fixture) int64 {
	t.Helper()
	var id sql.NullInt64
	if err := f.db.SQL.QueryRowContext(t.Context(), "SELECT MAX(id) FROM chore_instances").Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id.Int64
}

func TestInstanceIDsAreNeverReused(t *testing.T) {
	f := newFixture(t)
	f.kid("Sam")
	a := f.chore(chores.ChoreInput{Name: "Dishes", Schedule: daily("2026-11-01")})
	f.generate()
	high := maxInstanceID(t, f)

	a.Disabled = true // regeneration deletes its open instances
	f.update(a)
	f.generate()
	f.chore(chores.ChoreInput{Name: "Laundry", Schedule: daily("2026-11-01")})
	f.generate()
	for _, i := range f.list() {
		if i.ID <= high {
			t.Errorf("instance %d reuses the id of a deleted one (max was %d)", i.ID, high)
		}
	}
	high = maxInstanceID(t, f)

	b := f.chore(chores.ChoreInput{Name: "Vacuum", Schedule: daily("2026-11-20")}) // no instances yet
	f.generate()
	if err := f.chores.Delete(t.Context(), b.ID); err != nil {
		t.Fatal(err)
	}
	b = f.chore(chores.ChoreInput{Name: "Vacuum", Schedule: daily("2026-11-01")})
	f.generate()
	for _, i := range f.list() {
		if i.ChoreID == b.ID && i.ID <= high {
			t.Errorf("instance %d reuses an old id after a chore delete (max was %d)", i.ID, high)
		}
	}
}

func TestDeleteChoreKeepsHistory(t *testing.T) {
	f := newFixture(t)
	f.kid("Sam")
	upcoming := f.chore(chores.ChoreInput{Name: "Dishes", Schedule: daily("2026-11-01")})
	done := f.chore(chores.ChoreInput{Name: "Laundry", Schedule: daily("2026-11-01")})
	past := f.chore(chores.ChoreInput{Name: "Feed the dog", Slots: []string{"07:00"}, Schedule: daily("2026-11-01")})
	f.clock.Set(time.Date(2026, 11, 4, 6, 0, 0, 0, time.UTC)) // Wednesday 00:00: 07:00 is still ahead
	f.generate()
	f.clock.Set(wednesdayNoon) // ... and now it has passed
	var approved int64
	for _, i := range f.list() {
		if i.ChoreID == done.ID {
			approved = i.ID
			break
		}
	}
	f.exec("UPDATE chore_instances SET status = 'approved' WHERE id = ?", approved)

	for name, c := range map[string]chores.Chore{"approved instance": done, "past-due instance": past} {
		err := f.chores.Delete(t.Context(), c.ID)
		var ae *apierr.Problem
		if !errors.As(err, &ae) || ae.Status != 409 || ae.Code != apierr.CodeChoreHasHistory {
			t.Errorf("%s: delete = %v, want 409 chore_has_history", name, err)
		}
	}
	if err := f.chores.Delete(t.Context(), upcoming.ID); err != nil {
		t.Fatalf("delete a chore with only upcoming instances: %v", err)
	}
	for _, i := range f.list() {
		if i.ChoreID == upcoming.ID {
			t.Errorf("deleted chore still has %s", key(i))
		}
	}
	if n := len(f.list()); n != 2*8 {
		t.Errorf("%d instances left, want the two kept chores' 16", n)
	}
}

// Moving today's passed slot later doesn't give the kid a second instance today.
func TestSlotMovedLaterTodayTakesEffectTomorrow(t *testing.T) {
	f := newFixture(t)
	f.kid("Sam")
	c := f.chore(chores.ChoreInput{Name: "Feed the cat", Slots: []string{"07:00"}, Schedule: daily("2026-11-01")})
	f.clock.Set(time.Date(2026, 11, 4, 6, 0, 0, 0, time.UTC))
	f.generate()
	f.clock.Set(wednesdayNoon)
	c.Slots = []string{"20:00"}
	f.update(c)
	f.generate()
	got := byKey(f.list())
	if _, ok := got["2026-11-04 07:00 #0 kid 1"]; !ok {
		t.Error("today's kept 07:00 is gone")
	}
	if _, ok := got["2026-11-04 20:00 #0 kid 1"]; ok {
		t.Error("the moved slot was added today too")
	}
	if _, ok := got["2026-11-05 20:00 #0 kid 1"]; !ok {
		t.Errorf("the moved slot doesn't start tomorrow: %v", keys(f.list()))
	}
}

// Day-close instances follow the kid's own day end; shared ones keep the household's.
func TestKidDayEndOverrideSetsDueTime(t *testing.T) {
	f := newFixture(t)
	sam, ada := f.kid("Sam"), f.kid("Ada")
	if _, err := f.kids.Update(t.Context(), "sam", kids.KidInput{DisplayName: "Sam", Color: "#2563eb", DayEndOverride: "19:30"}); err != nil {
		t.Fatal(err)
	}
	f.chore(chores.ChoreInput{Name: "Read", Schedule: daily("2026-11-01")})
	f.chore(chores.ChoreInput{Name: "Tidy up", Schedule: daily("2026-11-01"),
		Assignment: &chores.ChoreAssignment{Mode: assign.ModeShared, KidIDs: []int64{sam, ada}}})
	f.chore(chores.ChoreInput{Name: "Piano", Schedule: &schedule.ScheduleRule{
		Type: schedule.TypeFlexibleWeekly, StartDate: date("2026-10-01"), TimesPerWeek: 1,
	}, Assignment: &chores.ChoreAssignment{Mode: assign.ModeFixed, KidIDs: []int64{sam}}})
	f.generate()

	cal, _ := caltime.NewHousehold("America/Chicago", caltime.MustTimeOfDay(21, 0), time.Sunday)
	early := caltime.MustTimeOfDay(19, 30)
	for _, i := range f.list() {
		var want time.Time
		switch {
		case i.Flexible:
			want = cal.WeekClose(i.Date, early)
		case i.KidID == sam:
			want = cal.DayClose(i.Date, early)
		default: // Ada, and shared instances
			want = cal.DayClose(i.Date, cal.DayEnd)
		}
		if !i.DueAt.Equal(want) {
			t.Errorf("%s (%s): due %v, want %v", key(i), i.ChoreName, i.DueAt, want)
		}
	}
	// After Sam's 19:30 close, today's instance is kept as it is.
	var closed horizon.ChoreInstance
	for _, i := range f.list() {
		if i.KidID == sam && i.ChoreName == "Read" && i.Date == date("2026-11-04") {
			closed = i
		}
	}
	if closed.ID == 0 {
		t.Fatal("no instance for Sam's Wednesday")
	}
	f.clock.Set(time.Date(2026, 11, 5, 2, 0, 0, 0, time.UTC)) // Wednesday 20:00 local
	if res := f.generate(); res.Deleted != 0 {
		t.Errorf("a run after Sam's day closed = %+v; his closed day's instance must stay", res)
	}
	var after horizon.ChoreInstance
	ok := false
	for _, i := range f.list() {
		if i.ID == closed.ID {
			after, ok = i, true
		}
	}
	if !ok || after.ID != closed.ID || !after.DueAt.Equal(closed.DueAt) {
		t.Errorf("Sam's closed-day instance changed: %+v -> %+v", closed, after)
	}
}
