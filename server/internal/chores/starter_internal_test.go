package chores

import (
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/voidrot/chorecast/server/internal/caltime"
	"github.com/voidrot/chorecast/server/internal/schedule"
)

// keysUnique fails if keys repeats a value; names must also be distinct once matched by nameKey.
func keysUnique(t *testing.T, what string, keys []string, match func(string) string) {
	t.Helper()
	seen := map[string]bool{}
	for _, k := range keys {
		k = match(k)
		if seen[k] {
			t.Errorf("%s: %q repeats", what, k)
		}
		seen[k] = true
	}
}

func TestStarterPackIsValid(t *testing.T) {
	pack, err := loadStarterPack()
	if err != nil {
		t.Fatal(err)
	}
	same := func(s string) string { return s }

	var catKeys, locKeys, eqKeys, locNames, eqNames, choreKeys, choreNames []string
	for _, c := range pack.Categories {
		catKeys = append(catKeys, c.Key)
	}
	for _, l := range pack.Locations {
		locKeys, locNames = append(locKeys, l.Key), append(locNames, l.Name)
		checkLen(t, "location "+l.Key, l.Name, 1, 60)
		checkLen(t, "location "+l.Key+" description", l.Description, 0, 500)
		if l.Exists {
			t.Errorf("location %s: the file must not set exists", l.Key)
		}
	}
	for _, e := range pack.Equipment {
		eqKeys, eqNames = append(eqKeys, e.Key), append(eqNames, e.Name)
		checkLen(t, "equipment "+e.Key, e.Name, 1, 60)
		checkLen(t, "equipment "+e.Key+" description", e.Description, 0, 500)
		if e.Exists {
			t.Errorf("equipment %s: the file must not set exists", e.Key)
		}
	}
	keysUnique(t, "category keys", catKeys, same)
	keysUnique(t, "location keys", locKeys, same)
	keysUnique(t, "equipment keys", eqKeys, same)
	keysUnique(t, "location names", locNames, nameKey)
	keysUnique(t, "equipment names", eqNames, nameKey)

	recommended := 0
	for _, c := range pack.Chores {
		choreKeys, choreNames = append(choreKeys, c.Key), append(choreNames, c.Name)
		checkChore(t, c, catKeys, locKeys, eqKeys)
		if c.Exists {
			t.Errorf("chore %s: the file must not set exists", c.Key)
		}
		if c.Recommended {
			recommended++
			if c.Category == "pets" || c.Category == "outdoor" {
				t.Errorf("chore %s: pets and outdoor chores start unticked", c.Key)
			}
		}
	}
	keysUnique(t, "chore keys", choreKeys, same)
	keysUnique(t, "chore names", choreNames, nameKey)
	if recommended == 0 {
		t.Error("no recommended chore")
	}
}

func checkChore(t *testing.T, c StarterChore, categories, locations, equipment []string) {
	t.Helper()
	at := "chore " + c.Key
	if !slices.Contains(categories, c.Category) {
		t.Errorf("%s: unknown category %q", at, c.Category)
	}
	if c.Location != "" && !slices.Contains(locations, c.Location) {
		t.Errorf("%s: unknown location %q", at, c.Location)
	}
	keysUnique(t, at+" equipment", c.Equipment, func(s string) string { return s })
	for _, e := range c.Equipment {
		if !slices.Contains(equipment, e) {
			t.Errorf("%s: unknown equipment %q", at, e)
		}
	}
	if c.Assignment != "everyone" && c.Assignment != "auto" {
		t.Errorf("%s: assignment %q", at, c.Assignment)
	}

	checkLen(t, at+" name", c.Name, 1, 80)
	checkLen(t, at+" description", c.Description, 0, 2000)
	if c.Points < 0 || c.Points > 100000 {
		t.Errorf("%s: points %d", at, c.Points)
	}
	if m := c.EstimatedMinutes; m != nil && (*m < 1 || *m > 1440) {
		t.Errorf("%s: estimatedMinutes %d", at, *m)
	}
	if a := c.MinAge; a != nil && (*a < 1 || *a > 25) {
		t.Errorf("%s: minAge %d", at, *a)
	}
	if len(c.Steps) > maxSteps {
		t.Errorf("%s: %d steps", at, len(c.Steps))
	}
	for _, st := range c.Steps {
		checkLen(t, at+" step", st, 1, 200)
	}

	if c.Schedule.Type == "once" {
		t.Errorf("%s: a starter chore can't be a one-off", at)
	}
	for _, d := range c.Schedule.MonthDays {
		if d != -1 && (d < 1 || d > 28) {
			t.Errorf("%s: monthDay %d (use 1-28 or -1)", at, d)
		}
	}
	r := c.Schedule
	r.StartDate = caltime.Date{Year: 2026, Month: 11, Day: 1}
	for _, err := range schedule.Validate(schedule.Normalize(r), "schedule") {
		t.Errorf("%s: %v", at, err)
	}

	ev := c.Evidence
	switch {
	case ev.Photos < 0 || ev.Photos > 5:
		t.Errorf("%s: photos %d", at, ev.Photos)
	case ev.Video:
		t.Errorf("%s: video evidence isn't supported", at)
	case ev.BeforeAfter && ev.Photos < 2:
		t.Errorf("%s: beforeAfter needs 2 photos", at)
	case ev.Checklist && len(c.Steps) == 0:
		t.Errorf("%s: a checklist needs steps", at)
	}
}

func checkLen(t *testing.T, what, s string, lo, hi int) {
	t.Helper()
	if n := utf8.RuneCountInString(strings.TrimSpace(s)); n < lo || n > hi {
		t.Errorf("%s: length %d, want %d-%d", what, n, lo, hi)
	}
}
