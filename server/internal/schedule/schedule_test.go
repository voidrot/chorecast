package schedule

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/voidrot/chorecast/server/internal/caltime"
)

func cal(t *testing.T, tz string, dayEnd string, weekStart time.Weekday) caltime.Household {
	t.Helper()
	de, err := caltime.ParseTimeOfDay(dayEnd)
	if err != nil {
		t.Fatal(err)
	}
	h, err := caltime.NewHousehold(tz, de, weekStart)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func d(t *testing.T, s string) caltime.Date {
	t.Helper()
	v, err := caltime.ParseDate(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func rule(t *testing.T, js string) ScheduleRule {
	t.Helper()
	var r ScheduleRule
	if err := json.Unmarshal([]byte(js), &r); err != nil {
		t.Fatalf("%s: %v", js, err)
	}
	r = Normalize(r)
	if errs := Validate(r, "schedule"); len(errs) > 0 {
		t.Fatalf("%s: unexpected validation errors %v", js, errs)
	}
	return r
}

func dates(occ []ScheduleOccurrence) string {
	var parts []string
	for _, o := range occ {
		parts = append(parts, o.Date.String())
	}
	return strings.Join(parts, " ")
}

// TestOccurrenceDates covers every rule type: which days a rule produces over a window.
func TestOccurrenceDates(t *testing.T) {
	mon := cal(t, "America/New_York", "00:00", time.Monday)
	sun := cal(t, "America/New_York", "00:00", time.Sunday)
	tests := []struct {
		name     string
		cal      caltime.Household
		rule     string
		from, to string
		want     string
	}{
		{"once", mon, `{"type":"once","date":"2026-11-08"}`, "2026-11-01", "2026-11-30", "2026-11-08"},
		{"once outside window", mon, `{"type":"once","date":"2026-12-08"}`, "2026-11-01", "2026-11-30", ""},
		{"daily", mon, `{"type":"daily","startDate":"2026-11-01"}`, "2026-10-30", "2026-11-03", "2026-11-01 2026-11-02 2026-11-03"},
		{"every 3 days anchored on start", mon, `{"type":"daily","interval":3,"startDate":"2026-11-02"}`,
			"2026-11-01", "2026-11-12", "2026-11-02 2026-11-05 2026-11-08 2026-11-11"},
		{"every 3 days, window after start keeps the anchor", mon, `{"type":"daily","interval":3,"startDate":"2026-11-02"}`,
			"2026-11-06", "2026-11-12", "2026-11-08 2026-11-11"},
		{"daily across DST change", mon, `{"type":"daily","interval":2,"startDate":"2026-10-30"}`,
			"2026-10-30", "2026-11-05", "2026-10-30 2026-11-01 2026-11-03 2026-11-05"},
		{"daily with end date", mon, `{"type":"daily","startDate":"2026-11-01","endDate":"2026-11-02"}`,
			"2026-11-01", "2026-11-05", "2026-11-01 2026-11-02"},
		{"weekly weekdays", mon, `{"type":"weekly","weekdays":["sat","mon"],"startDate":"2026-11-01"}`,
			"2026-11-01", "2026-11-10", "2026-11-02 2026-11-07 2026-11-09"},
		// Biweekly, start on Wednesday 2026-11-04. Monday weeks: weeks of 11-02, 11-16 are on.
		{"every 2 weeks, Monday week start", mon, `{"type":"weekly","interval":2,"weekdays":["sun","mon"],"startDate":"2026-11-04"}`,
			"2026-11-01", "2026-11-30", "2026-11-08 2026-11-16 2026-11-22 2026-11-30"},
		// Same rule with Sunday weeks: weeks of 11-01, 11-15, 11-29 are on, but 11-01 and 11-02 precede the start.
		{"every 2 weeks, Sunday week start", sun, `{"type":"weekly","interval":2,"weekdays":["sun","mon"],"startDate":"2026-11-04"}`,
			"2026-11-01", "2026-11-30", "2026-11-15 2026-11-16 2026-11-29 2026-11-30"},
		{"monthly by day", mon, `{"type":"monthly","monthDays":[15,1],"startDate":"2026-10-01"}`,
			"2026-10-01", "2026-11-30", "2026-10-01 2026-10-15 2026-11-01 2026-11-15"},
		{"day 31 skipped in short months", mon, `{"type":"monthly","monthDays":[31],"startDate":"2026-01-01"}`,
			"2026-01-01", "2026-07-31", "2026-01-31 2026-03-31 2026-05-31 2026-07-31"},
		{"last day of month incl. leap February", mon, `{"type":"monthly","monthDays":[-1],"startDate":"2028-01-01"}`,
			"2028-01-01", "2028-04-30", "2028-01-31 2028-02-29 2028-03-31 2028-04-30"},
		{"30 and last day coincide once", mon, `{"type":"monthly","monthDays":[30,-1],"startDate":"2026-04-01"}`,
			"2026-04-01", "2026-05-31", "2026-04-30 2026-05-30 2026-05-31"},
		{"every 3 months anchored on start month", mon, `{"type":"monthly","interval":3,"monthDays":[1],"startDate":"2026-11-20"}`,
			"2026-11-01", "2027-06-30", "2027-02-01 2027-05-01"},
		{"2nd Tuesday", mon, `{"type":"monthly","nthWeekday":{"n":2,"weekday":"tue"},"startDate":"2026-10-01"}`,
			"2026-10-01", "2026-12-31", "2026-10-13 2026-11-10 2026-12-08"},
		{"last Friday", mon, `{"type":"monthly","nthWeekday":{"n":-1,"weekday":"fri"},"startDate":"2026-10-01"}`,
			"2026-10-01", "2026-12-31", "2026-10-30 2026-11-27 2026-12-25"},
		{"5th Saturday skipped when missing", mon, `{"type":"monthly","nthWeekday":{"n":5,"weekday":"sat"},"startDate":"2026-08-01"}`,
			"2026-08-01", "2026-12-31", "2026-08-29 2026-10-31"},
		{"flexible: one week start per week", mon, `{"type":"flexible_weekly","timesPerWeek":1,"startDate":"2026-11-02"}`,
			"2026-11-01", "2026-11-17", "2026-11-02 2026-11-09 2026-11-16"},
		{"flexible first week starts at start date", mon, `{"type":"flexible_weekly","timesPerWeek":1,"startDate":"2026-11-04"}`,
			"2026-11-01", "2026-11-17", "2026-11-04 2026-11-09 2026-11-16"},
		{"flexible with Sunday weeks", sun, `{"type":"flexible_weekly","timesPerWeek":1,"startDate":"2026-11-04"}`,
			"2026-11-01", "2026-11-17", "2026-11-04 2026-11-08 2026-11-15"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dates(Expand(rule(t, tt.rule), tt.cal, nil, d(t, tt.from), d(t, tt.to), 0))
			if got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestDueTimesAcrossDST(t *testing.T) {
	ny := cal(t, "America/New_York", "00:00", time.Monday)
	slots := []caltime.TimeOfDay{caltime.MustTimeOfDay(2, 30), caltime.MustTimeOfDay(18, 0)}
	r := rule(t, `{"type":"daily","startDate":"2026-03-07"}`)
	occ := Expand(r, ny, slots, d(t, "2026-03-07"), d(t, "2026-03-08"), 0)
	want := []struct{ date, slot, due string }{
		{"2026-03-07", "02:30", "2026-03-07T07:30:00Z"},
		{"2026-03-07", "18:00", "2026-03-07T23:00:00Z"},
		{"2026-03-08", "02:30", "2026-03-08T07:00:00Z"}, // in the spring-forward gap: resolves to 03:00 EDT
		{"2026-03-08", "18:00", "2026-03-08T22:00:00Z"},
	}
	if len(occ) != len(want) {
		t.Fatalf("got %d occurrences, want %d", len(occ), len(want))
	}
	for i, w := range want {
		o := occ[i]
		if o.Date.String() != w.date || o.Slot != w.slot || o.DueAt.UTC().Format(time.RFC3339) != w.due {
			t.Errorf("[%d] = %s %s %s, want %s %s %s", i, o.Date, o.Slot, o.DueAt.UTC().Format(time.RFC3339), w.date, w.slot, w.due)
		}
	}

	// Without slots, due at the household day close; the fall-back day is 25 hours long.
	evening := cal(t, "America/New_York", "20:00", time.Monday)
	occ = Expand(r, evening, nil, d(t, "2026-10-31"), d(t, "2026-11-01"), 0)
	if got := occ[1].DueAt.Sub(occ[0].DueAt); got != 25*time.Hour {
		t.Errorf("gap between day closes across fall-back = %s, want 25h", got)
	}
	if got := occ[0].DueAt.UTC().Format(time.RFC3339); got != "2026-11-01T00:00:00Z" {
		t.Errorf("day close = %s, want 20:00 EDT", got)
	}
}

func TestFlexibleDueAtWeekClose(t *testing.T) {
	ny := cal(t, "America/New_York", "00:00", time.Monday)
	r := rule(t, `{"type":"flexible_weekly","timesPerWeek":3,"startDate":"2026-10-28"}`)
	slots := []caltime.TimeOfDay{caltime.MustTimeOfDay(7, 0)} // ignored for flexible chores
	occ := Expand(r, ny, slots, d(t, "2026-10-26"), d(t, "2026-11-01"), 0)
	if len(occ) != 3 {
		t.Fatalf("got %d occurrences, want 3", len(occ))
	}
	for _, o := range occ {
		// The week closes at the end of Sunday 2026-11-01 (EST after fall-back).
		if !o.Flexible || o.Slot != "" || o.Date.String() != "2026-10-28" || o.DueAt.UTC().Format(time.RFC3339) != "2026-11-02T05:00:00Z" {
			t.Errorf("occurrence = %+v", o)
		}
	}

	sun := cal(t, "America/New_York", "00:00", time.Sunday)
	occ = Expand(r, sun, nil, d(t, "2026-10-28"), d(t, "2026-10-28"), 0)
	if len(occ) != 3 || occ[0].DueAt.UTC().Format(time.RFC3339) != "2026-11-01T04:00:00Z" {
		t.Errorf("Sunday-week close = %v", occ)
	}
}

func TestNext(t *testing.T) {
	ny := cal(t, "America/New_York", "00:00", time.Monday)
	slots := []caltime.TimeOfDay{caltime.MustTimeOfDay(7, 0), caltime.MustTimeOfDay(18, 0)}

	occ := Next(rule(t, `{"type":"weekly","weekdays":["sat"],"startDate":"2026-01-01"}`), ny, slots, d(t, "2026-10-31"), 3)
	if got := dates(occ); got != "2026-10-31 2026-10-31 2026-11-07" {
		t.Errorf("weekly Next = %q", got)
	}
	if occ[1].Slot != "18:00" {
		t.Errorf("slot order = %v", occ)
	}

	// A start date in the future: the preview starts there.
	if got := dates(Next(rule(t, `{"type":"daily","interval":7,"startDate":"2026-12-25"}`), ny, nil, d(t, "2026-10-31"), 2)); got != "2026-12-25 2027-01-01" {
		t.Errorf("future start = %q", got)
	}
	// An ended rule and a past one-off have nothing upcoming.
	if occ := Next(rule(t, `{"type":"daily","startDate":"2026-01-01","endDate":"2026-02-01"}`), ny, nil, d(t, "2026-10-31"), 5); len(occ) != 0 {
		t.Errorf("ended rule = %v", occ)
	}
	if occ := Next(rule(t, `{"type":"once","date":"2026-10-30"}`), ny, nil, d(t, "2026-10-31"), 5); len(occ) != 0 {
		t.Errorf("past once = %v", occ)
	}
	// Mid-week, the current flexible week is still included.
	if got := dates(Next(rule(t, `{"type":"flexible_weekly","timesPerWeek":2,"startDate":"2026-10-01"}`), ny, nil, d(t, "2026-10-31"), 3)); got != "2026-10-26 2026-10-26 2026-11-02" {
		t.Errorf("flexible Next = %q", got)
	}
	// Yearly from February on the 31st never occurs; the bounded scan ends with nothing.
	if occ := Next(rule(t, `{"type":"monthly","interval":12,"monthDays":[31],"startDate":"2026-02-01"}`), ny, nil, d(t, "2026-10-31"), 1); len(occ) != 0 {
		t.Errorf("impossible rule = %v", occ)
	}
}

func TestNormalize(t *testing.T) {
	r := Normalize(ScheduleRule{
		Type: TypeWeekly, StartDate: caltime.Date{Year: 2026, Month: 11, Day: 1}, Weekdays: []string{"sat", "mon", "sat"},
		MonthDays: []int{3}, TimesPerWeek: 2, Date: caltime.Date{Year: 2026, Month: 1, Day: 1},
	})
	b, _ := json.Marshal(r)
	if got, want := string(b), `{"type":"weekly","startDate":"2026-11-01","interval":1,"weekdays":["mon","sat"]}`; got != want {
		t.Errorf("weekly = %s, want %s", got, want)
	}
	r = Normalize(ScheduleRule{Type: TypeMonthly, MonthDays: []int{-1, 15, 1, 15}, StartDate: caltime.Date{Year: 2026, Month: 11, Day: 1}})
	if b, _ := json.Marshal(r.MonthDays); string(b) != "[1,15,-1]" {
		t.Errorf("monthDays = %s", b)
	}
	r = Normalize(ScheduleRule{Type: TypeOnce, Date: caltime.Date{Year: 2026, Month: 11, Day: 8}, StartDate: caltime.Date{Year: 2026, Month: 1, Day: 1}, Interval: 3})
	if b, _ := json.Marshal(r); string(b) != `{"type":"once","date":"2026-11-08"}` {
		t.Errorf("once = %s", b)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name string
		rule string
		locs []string
	}{
		{"once without date", `{"type":"once"}`, []string{"s.date"}},
		{"missing start date", `{"type":"daily"}`, []string{"s.startDate"}},
		{"end before start", `{"type":"daily","startDate":"2026-11-02","endDate":"2026-11-01"}`, []string{"s.endDate"}},
		{"interval too large", `{"type":"daily","interval":100,"startDate":"2026-11-01"}`, []string{"s.interval"}},
		{"weekly without days", `{"type":"weekly","startDate":"2026-11-01"}`, []string{"s.weekdays"}},
		{"weekly unknown day", `{"type":"weekly","weekdays":["funday"],"startDate":"2026-11-01"}`, []string{"s.weekdays[0]"}},
		{"monthly needs a choice", `{"type":"monthly","startDate":"2026-11-01"}`, []string{"s.monthDays"}},
		{"monthly not both", `{"type":"monthly","monthDays":[1],"nthWeekday":{"n":1,"weekday":"mon"},"startDate":"2026-11-01"}`, []string{"s.nthWeekday"}},
		{"month day out of range", `{"type":"monthly","monthDays":[0,32],"startDate":"2026-11-01"}`, []string{"s.monthDays[0]", "s.monthDays[1]"}},
		{"nth out of range", `{"type":"monthly","nthWeekday":{"n":6,"weekday":"xyz"},"startDate":"2026-11-01"}`, []string{"s.nthWeekday.n", "s.nthWeekday.weekday"}},
		{"flexible needs a count", `{"type":"flexible_weekly","startDate":"2026-11-01"}`, []string{"s.timesPerWeek"}},
		{"flexible too many", `{"type":"flexible_weekly","timesPerWeek":15,"startDate":"2026-11-01"}`, []string{"s.timesPerWeek"}},
		{"unknown type", `{"type":"hourly","startDate":"2026-11-01"}`, []string{"s.type"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var r ScheduleRule
			if err := json.Unmarshal([]byte(tt.rule), &r); err != nil {
				t.Fatal(err)
			}
			errs := Validate(Normalize(r), "s")
			var locs []string
			for _, e := range errs {
				var detail *huma.ErrorDetail
				if errors.As(e, &detail) {
					locs = append(locs, detail.Location)
				}
			}
			if strings.Join(locs, ",") != strings.Join(tt.locs, ",") {
				t.Errorf("locations = %v, want %v", locs, tt.locs)
			}
		})
	}
}
