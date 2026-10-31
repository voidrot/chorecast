// Package schedule holds chore schedule rules: which days a chore occurs on. A rule is a small
// subset of iCal RRULE semantics stored as JSON on the chore (one-off, every N days, weekdays every
// N weeks, monthly by day or Nth weekday, and flexible "N times per week"). Expansion happens in
// the household calendar (caltime), so timezones and DST live in one place. The due times within
// a day (slots) belong to the chore. See docs/features/schedules.md.
package schedule

import (
	"fmt"
	"slices"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/voidrot/chorecast/server/internal/caltime"
)

// Rule types.
const (
	TypeOnce           = "once"
	TypeDaily          = "daily"
	TypeWeekly         = "weekly"
	TypeMonthly        = "monthly"
	TypeFlexibleWeekly = "flexible_weekly"
)

const (
	maxInterval     = 99
	maxTimesPerWeek = 14
)

// Weekdays are the rule's weekday names, Sunday first (index = time.Weekday).
var Weekdays = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

// NthWeekday is "the Nth <weekday> of the month"; N = -1 is the last one.
type NthWeekday struct {
	N       int    `json:"n" minimum:"-1" maximum:"5" doc:"1-5, or -1 for the last one in the month"`
	Weekday string `json:"weekday" enum:"sun,mon,tue,wed,thu,fri,sat"`
}

// ScheduleRule is a chore's schedule. Fields that don't apply to Type are dropped by Normalize.
type ScheduleRule struct {
	Type         string       `json:"type" enum:"once,daily,weekly,monthly,flexible_weekly"`
	StartDate    caltime.Date `json:"startDate,omitzero" format:"date" doc:"First day the rule applies; anchors intervals. Required except for once."`
	EndDate      caltime.Date `json:"endDate,omitzero" format:"date" doc:"Last day the rule applies (inclusive)"`
	Date         caltime.Date `json:"date,omitzero" format:"date" doc:"once: the day"`
	Interval     int          `json:"interval,omitempty" minimum:"0" maximum:"99" doc:"daily, weekly, monthly: every N days/weeks/months (default 1)"`
	Weekdays     []string     `json:"weekdays,omitempty" maxItems:"7" enum:"sun,mon,tue,wed,thu,fri,sat" doc:"weekly: the days of the week"`
	MonthDays    []int        `json:"monthDays,omitempty" maxItems:"32" minimum:"-1" maximum:"31" doc:"monthly: days of the month; -1 = last day. Days a month doesn't have (31 in a 30-day month) are skipped."`
	NthWeekday   *NthWeekday  `json:"nthWeekday,omitempty" doc:"monthly: the Nth weekday instead of monthDays"`
	TimesPerWeek int          `json:"timesPerWeek,omitempty" minimum:"0" maximum:"14" doc:"flexible_weekly: completions required per week, any day"`
}

// Normalize returns r with defaults filled in, fields that don't apply to its type cleared, and
// lists sorted and de-duplicated, so equal rules store identically.
func Normalize(r ScheduleRule) ScheduleRule {
	out := ScheduleRule{Type: r.Type, StartDate: r.StartDate, EndDate: r.EndDate}
	switch r.Type {
	case TypeOnce:
		out = ScheduleRule{Type: r.Type, Date: r.Date}
	case TypeDaily:
		out.Interval = max(r.Interval, 1)
	case TypeWeekly:
		out.Interval = max(r.Interval, 1)
		for _, w := range Weekdays { // canonical order, deduplicated
			if slices.Contains(r.Weekdays, w) {
				out.Weekdays = append(out.Weekdays, w)
			}
		}
		for _, w := range r.Weekdays { // keep unknown names so Validate can report them
			if !slices.Contains(Weekdays, w) {
				out.Weekdays = append(out.Weekdays, w)
			}
		}
	case TypeMonthly:
		out.Interval = max(r.Interval, 1)
		if r.NthWeekday != nil {
			n := *r.NthWeekday
			out.NthWeekday = &n
		}
		if len(r.MonthDays) > 0 {
			days := slices.Clone(r.MonthDays)
			slices.SortFunc(days, func(a, b int) int { // 1..31 then -1 (last)
				if a == -1 || b == -1 {
					return b - a
				}
				return a - b
			})
			out.MonthDays = slices.Compact(days)
		}
	case TypeFlexibleWeekly:
		out.TimesPerWeek = r.TimesPerWeek
	}
	return out
}

// Validate checks a normalized rule. Problems are reported at loc (e.g. "body.schedule").
func Validate(r ScheduleRule, loc string) []error {
	var details []error
	bad := func(field, msg string, v any) {
		l := loc
		if field != "" {
			l += "." + field
		}
		details = append(details, &huma.ErrorDetail{Location: l, Message: msg, Value: v})
	}
	switch r.Type {
	case TypeOnce:
		if r.Date.IsZero() {
			bad("date", "a one-off chore needs a date", nil)
		}
		return details
	case TypeDaily, TypeWeekly, TypeMonthly, TypeFlexibleWeekly:
	default:
		bad("type", "unknown schedule type", r.Type)
		return details
	}
	if r.StartDate.IsZero() {
		bad("startDate", "a start date is required", nil)
	}
	if !r.EndDate.IsZero() && !r.StartDate.IsZero() && r.EndDate.Before(r.StartDate) {
		bad("endDate", "the end date is before the start date", r.EndDate.String())
	}
	if r.Interval < 1 || r.Interval > maxInterval {
		if r.Type != TypeFlexibleWeekly {
			bad("interval", fmt.Sprintf("every 1 to %d", maxInterval), r.Interval)
		}
	}
	switch r.Type {
	case TypeWeekly:
		if len(r.Weekdays) == 0 {
			bad("weekdays", "pick at least one day of the week", nil)
		}
		for i, w := range r.Weekdays {
			if !slices.Contains(Weekdays, w) {
				bad(fmt.Sprintf("weekdays[%d]", i), "unknown weekday", w)
			}
		}
	case TypeMonthly:
		switch {
		case len(r.MonthDays) == 0 && r.NthWeekday == nil:
			bad("monthDays", "pick days of the month or an Nth weekday", nil)
		case len(r.MonthDays) > 0 && r.NthWeekday != nil:
			bad("nthWeekday", "use either days of the month or an Nth weekday, not both", nil)
		}
		for i, d := range r.MonthDays {
			if d != -1 && (d < 1 || d > 31) {
				bad(fmt.Sprintf("monthDays[%d]", i), "days are 1 to 31, or -1 for the last day", d)
			}
		}
		if n := r.NthWeekday; n != nil {
			if n.N != -1 && (n.N < 1 || n.N > 5) {
				bad("nthWeekday.n", "1 to 5, or -1 for the last one", n.N)
			}
			if !slices.Contains(Weekdays, n.Weekday) {
				bad("nthWeekday.weekday", "unknown weekday", n.Weekday)
			}
		}
	case TypeFlexibleWeekly:
		if r.TimesPerWeek < 1 || r.TimesPerWeek > maxTimesPerWeek {
			bad("timesPerWeek", fmt.Sprintf("1 to %d times a week", maxTimesPerWeek), r.TimesPerWeek)
		}
	}
	return details
}

// OccursOn reports whether the rule produces an occurrence on day d. For flexible_weekly that's
// the first active day of each week (week start, or the start date in the first week), when the
// week's instances are generated; they're all due at week close.
func OccursOn(r ScheduleRule, cal caltime.Household, d caltime.Date) bool {
	if r.Type == TypeOnce {
		return d == r.Date
	}
	if d.Before(r.StartDate) || (!r.EndDate.IsZero() && d.After(r.EndDate)) {
		return false
	}
	interval := max(r.Interval, 1)
	switch r.Type {
	case TypeDaily:
		return r.StartDate.DaysUntil(d)%interval == 0
	case TypeWeekly:
		weeks := cal.WeekStartOf(r.StartDate).DaysUntil(cal.WeekStartOf(d)) / 7
		return weeks%interval == 0 && slices.Contains(r.Weekdays, Weekdays[d.Weekday()])
	case TypeMonthly:
		months := (d.Year-r.StartDate.Year)*12 + int(d.Month) - int(r.StartDate.Month)
		if months%interval != 0 {
			return false
		}
		if n := r.NthWeekday; n != nil {
			return isNthWeekday(d, n.N, n.Weekday)
		}
		last := daysIn(d.Year, d.Month)
		for _, md := range r.MonthDays {
			if md == d.Day || (md == -1 && d.Day == last) {
				return true
			}
		}
		return false
	case TypeFlexibleWeekly:
		return d == firstActiveDay(r, cal, d)
	}
	return false
}

// firstActiveDay is the first day of d's week the rule applies on.
func firstActiveDay(r ScheduleRule, cal caltime.Household, d caltime.Date) caltime.Date {
	ws := cal.WeekStartOf(d)
	if ws.Before(r.StartDate) {
		return r.StartDate
	}
	return ws
}

func daysIn(y int, m time.Month) int { return time.Date(y, m+1, 0, 12, 0, 0, 0, time.UTC).Day() }

func isNthWeekday(d caltime.Date, n int, weekday string) bool {
	if Weekdays[d.Weekday()] != weekday {
		return false
	}
	if n == -1 {
		return d.Day+7 > daysIn(d.Year, d.Month)
	}
	return (d.Day-1)/7+1 == n
}

// ScheduleOccurrence is one due instance of a chore produced by its rule and slots.
type ScheduleOccurrence struct {
	Date     caltime.Date `json:"date" format:"date" doc:"The day it belongs to (flexible: the week's first active day)"`
	Slot     string       `json:"slot,omitempty" doc:"HH:MM due time; absent = due at day close (or week close when flexible)"`
	DueAt    time.Time    `json:"dueAt"`
	Flexible bool         `json:"flexible,omitempty" doc:"One of a flexible chore's completions for the week"`
}

// Expand returns the occurrences on days [from, to] in order. A day yields one occurrence per
// slot (or one due at the household day close without slots); a flexible week yields
// TimesPerWeek occurrences due at the week close. limit > 0 caps the result.
func Expand(r ScheduleRule, cal caltime.Household, slots []caltime.TimeOfDay, from, to caltime.Date, limit int) []ScheduleOccurrence {
	var out []ScheduleOccurrence
	full := func() bool { return limit > 0 && len(out) >= limit }
	for d := from; !d.After(to) && !full(); d = d.AddDays(1) {
		if !OccursOn(r, cal, d) {
			continue
		}
		if r.Type == TypeFlexibleWeekly {
			due := cal.WeekClose(d, cal.DayEnd)
			for i := 0; i < r.TimesPerWeek && !full(); i++ {
				out = append(out, ScheduleOccurrence{Date: d, DueAt: due, Flexible: true})
			}
			continue
		}
		if len(slots) == 0 {
			out = append(out, ScheduleOccurrence{Date: d, DueAt: cal.DayClose(d, cal.DayEnd)})
			continue
		}
		for _, s := range slots {
			if full() {
				break
			}
			out = append(out, ScheduleOccurrence{Date: d, Slot: s.String(), DueAt: cal.At(d, s)})
		}
	}
	return out
}

// searchYears bounds Next's scan: a valid rule always repeats within a few years (Feb 29
// aside, which isn't expressible), so this only ends scans of rules that have finished.
const searchYears = 10

// Next returns up to n occurrences on or after day from. The current flexible week is included
// even when from is mid-week.
func Next(r ScheduleRule, cal caltime.Household, slots []caltime.TimeOfDay, from caltime.Date, n int) []ScheduleOccurrence {
	if r.Type == TypeFlexibleWeekly {
		from = firstActiveDay(r, cal, from)
	}
	to := from.AddDays(366 * searchYears)
	if r.Type == TypeOnce {
		if r.Date.Before(from) {
			return nil
		}
		from, to = r.Date, r.Date
	} else {
		if from.Before(r.StartDate) {
			from = r.StartDate
		}
		if !r.EndDate.IsZero() && r.EndDate.Before(to) {
			to = r.EndDate
		}
	}
	return Expand(r, cal, slots, from, to, n)
}
