package caltime

import (
	"fmt"
)

const minutesPerDay = 24 * 60

// TimeOfDay is a wall-clock time with minute precision, from 00:00 to 24:00 inclusive.
// 24:00 is only meaningful as a day end ("the following midnight").
type TimeOfDay struct {
	minutes int
}

// Midnight is 00:00. As a day end it means the following midnight (see DayEndMinutes).
var Midnight = TimeOfDay{}

// NewTimeOfDay returns hour:minute, or an error if out of range. 24:00 is allowed.
func NewTimeOfDay(hour, minute int) (TimeOfDay, error) {
	if hour < 0 || hour > 24 || minute < 0 || minute > 59 || (hour == 24 && minute != 0) {
		return TimeOfDay{}, fmt.Errorf("invalid time of day %02d:%02d", hour, minute)
	}
	return TimeOfDay{hour*60 + minute}, nil
}

// MustTimeOfDay is NewTimeOfDay for constants and tests; it panics on invalid input.
func MustTimeOfDay(hour, minute int) TimeOfDay {
	t, err := NewTimeOfDay(hour, minute)
	if err != nil {
		panic(err)
	}
	return t
}

// ParseTimeOfDay parses HH:MM (00:00–24:00).
func ParseTimeOfDay(s string) (TimeOfDay, error) {
	if len(s) != 5 || s[2] != ':' || !isDigits(s[:2]) || !isDigits(s[3:]) {
		return TimeOfDay{}, fmt.Errorf("invalid time of day %q: want HH:MM", s)
	}
	h := int(s[0]-'0')*10 + int(s[1]-'0')
	m := int(s[3]-'0')*10 + int(s[4]-'0')
	return NewTimeOfDay(h, m)
}

func isDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func (t TimeOfDay) Hour() int   { return t.minutes / 60 }
func (t TimeOfDay) Minute() int { return t.minutes % 60 }

func (t TimeOfDay) String() string { return fmt.Sprintf("%02d:%02d", t.Hour(), t.Minute()) }

// DayEndMinutes returns t as minutes after the start of the day it ends, treating 00:00 as
// 24:00: a day end of midnight closes the day at the following midnight. Day ends are always
// within their own calendar day, in (00:00, 24:00].
func (t TimeOfDay) DayEndMinutes() int {
	if t.minutes == 0 {
		return minutesPerDay
	}
	return t.minutes
}

func (t TimeOfDay) MarshalText() ([]byte, error) { return []byte(t.String()), nil }

func (t *TimeOfDay) UnmarshalText(b []byte) error {
	p, err := ParseTimeOfDay(string(b))
	if err != nil {
		return err
	}
	*t = p
	return nil
}
