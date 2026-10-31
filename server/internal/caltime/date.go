// Package caltime holds household calendar time: civil dates, times of day, and the rules
// for when a household's (or kid's) day and week begin and end. All scheduling code goes
// through it so timezone and DST handling live in one place.
package caltime

import (
	"fmt"
	"time"
)

// Date is a civil calendar date with no time or zone.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// DateOf returns the date of t in t's own location.
func DateOf(t time.Time) Date {
	y, m, d := t.Date()
	return Date{y, m, d}
}

// ParseDate parses YYYY-MM-DD.
func ParseDate(s string) (Date, error) {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return Date{}, fmt.Errorf("invalid date %q: want YYYY-MM-DD", s)
	}
	return DateOf(t), nil
}

func (d Date) String() string { return fmt.Sprintf("%04d-%02d-%02d", d.Year, d.Month, d.Day) }

// noonUTC is a DST-free anchor for date arithmetic.
func (d Date) noonUTC() time.Time { return time.Date(d.Year, d.Month, d.Day, 12, 0, 0, 0, time.UTC) }

// AddDays returns the date n days later (or earlier for negative n).
func (d Date) AddDays(n int) Date { return DateOf(d.noonUTC().AddDate(0, 0, n)) }

// Weekday returns the day of the week.
func (d Date) Weekday() time.Weekday { return d.noonUTC().Weekday() }

// DaysUntil returns the number of days from d to o (negative if o is earlier).
func (d Date) DaysUntil(o Date) int { return int(o.noonUTC().Sub(d.noonUTC()).Hours() / 24) }

// Compare returns -1, 0 or +1 as d is before, equal to or after o.
func (d Date) Compare(o Date) int { return d.noonUTC().Compare(o.noonUTC()) }

func (d Date) Before(o Date) bool { return d.Compare(o) < 0 }
func (d Date) After(o Date) bool  { return d.Compare(o) > 0 }

func (d Date) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

func (d *Date) UnmarshalText(b []byte) error {
	p, err := ParseDate(string(b))
	if err != nil {
		return err
	}
	*d = p
	return nil
}
