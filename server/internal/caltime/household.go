package caltime

import (
	"errors"
	"fmt"
	"time"
)

// ErrDayEndAfterHousehold is returned when a kid's day-end override is later than the
// household day end. Household jobs (summaries, horizon generation) run at the household
// day end and rely on every kid's day having closed by then.
var ErrDayEndAfterHousehold = errors.New("kid day end must be at or before the household day end")

// Household holds a household's calendar settings.
type Household struct {
	Location  *time.Location
	DayEnd    TimeOfDay    // 00:00 means the following midnight
	WeekStart time.Weekday // first day of the week for weekly rules and week close
}

// NewHousehold loads the IANA timezone tz.
func NewHousehold(tz string, dayEnd TimeOfDay, weekStart time.Weekday) (Household, error) {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return Household{}, fmt.Errorf("unknown timezone %q: %w", tz, err)
	}
	if weekStart < time.Sunday || weekStart > time.Saturday {
		return Household{}, fmt.Errorf("invalid week start %d", weekStart)
	}
	return Household{Location: loc, DayEnd: dayEnd, WeekStart: weekStart}, nil
}

// EffectiveDayEnd returns a kid's day end: their override if set, else the household's.
func (h Household) EffectiveDayEnd(override *TimeOfDay) (TimeOfDay, error) {
	if override == nil {
		return h.DayEnd, nil
	}
	if override.DayEndMinutes() > h.DayEnd.DayEndMinutes() {
		return TimeOfDay{}, ErrDayEndAfterHousehold
	}
	return *override, nil
}

// CalendarDate is the household-local calendar date of instant t.
func (h Household) CalendarDate(t time.Time) Date { return DateOf(t.In(h.Location)) }

// At returns the instant of wall time tod on date d in the household timezone.
//
// DST rules: a wall time that occurs twice (clocks going back) resolves to the earlier
// instant; a wall time that doesn't exist (clocks going forward) resolves to the instant the
// gap ends. 24:00 is the following day's start.
func (h Household) At(d Date, tod TimeOfDay) time.Time {
	if tod.minutes == minutesPerDay {
		return h.StartOfDay(d.AddDays(1))
	}
	return resolveWall(d, tod, h.Location)
}

// StartOfDay is the first instant of date d. Usually local midnight, but in zones whose DST
// change happens at midnight it can be 01:00.
func (h Household) StartOfDay(d Date) time.Time { return resolveWall(d, Midnight, h.Location) }

// DayClose is the instant date d closes for the given day end (household or kid).
func (h Household) DayClose(d Date, dayEnd TimeOfDay) time.Time {
	if dayEnd.DayEndMinutes() == minutesPerDay {
		return h.StartOfDay(d.AddDays(1))
	}
	return h.At(d, dayEnd)
}

// DayContaining returns the day whose window contains t for the given day end. Day d covers
// (DayClose(d-1), DayClose(d)]. With an early day end, an evening instant after the close
// belongs to the next day.
func (h Household) DayContaining(t time.Time, dayEnd TimeOfDay) Date {
	d := h.CalendarDate(t)
	if t.After(h.DayClose(d, dayEnd)) {
		return d.AddDays(1)
	}
	if !t.After(h.DayClose(d.AddDays(-1), dayEnd)) {
		return d.AddDays(-1)
	}
	return d
}

// NextDayClose returns the first day close strictly after t, and the day it closes.
func (h Household) NextDayClose(t time.Time, dayEnd TimeOfDay) (Date, time.Time) {
	d := h.DayContaining(t, dayEnd)
	if c := h.DayClose(d, dayEnd); c.After(t) {
		return d, c
	}
	d = d.AddDays(1)
	return d, h.DayClose(d, dayEnd)
}

// WeekStartOf returns the first date of the week containing d.
func (h Household) WeekStartOf(d Date) Date {
	back := (int(d.Weekday()) - int(h.WeekStart) + 7) % 7
	return d.AddDays(-back)
}

// WeekClose is when the week containing d closes: the day close of its last day.
func (h Household) WeekClose(d Date, dayEnd TimeOfDay) time.Time {
	return h.DayClose(h.WeekStartOf(d).AddDays(6), dayEnd)
}

// resolveWall finds the instant of wall time (d, tod) in loc. time.Date leaves gap and
// overlap handling unspecified (in practice it resolves gap times backwards, which would
// close a day early), so scan the surrounding hours minute by minute: the earliest exact
// match wins; if none exists the time is in a gap and the first instant after it wins.
// DST shifts are whole minutes and at most 2 hours, so a ±3h window is enough.
func resolveWall(d Date, tod TimeOfDay, loc *time.Location) time.Time {
	want := time.Date(d.Year, d.Month, d.Day, tod.Hour(), tod.Minute(), 0, 0, time.UTC)
	base := time.Date(d.Year, d.Month, d.Day, tod.Hour(), tod.Minute(), 0, 0, loc)
	var after time.Time
	for c := base.Add(-3 * time.Hour); !c.After(base.Add(3 * time.Hour)); c = c.Add(time.Minute) {
		l := c.In(loc)
		wall := time.Date(l.Year(), l.Month(), l.Day(), l.Hour(), l.Minute(), 0, 0, time.UTC)
		switch {
		case wall.Equal(want):
			return c
		case wall.After(want) && after.IsZero():
			after = c
		}
	}
	if after.IsZero() {
		return base // unreachable for real zones; keep time.Date's answer
	}
	return after
}
