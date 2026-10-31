package caltime

import (
	"errors"
	"testing"
	"time"
)

func household(t *testing.T, tz string, dayEnd string, weekStart time.Weekday) Household {
	t.Helper()
	de, err := ParseTimeOfDay(dayEnd)
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHousehold(tz, de, weekStart)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func date(t *testing.T, s string) Date {
	t.Helper()
	d, err := ParseDate(s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func utc(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestParseTimeOfDay(t *testing.T) {
	for _, s := range []string{"00:00", "07:05", "23:59", "24:00"} {
		v, err := ParseTimeOfDay(s)
		if err != nil || v.String() != s {
			t.Errorf("ParseTimeOfDay(%q) = %v, %v", s, v, err)
		}
	}
	for _, s := range []string{"", "7:30", "07:60", "24:01", "25:00", "+1:30", "ab:cd", "07-30", "07:300"} {
		if _, err := ParseTimeOfDay(s); err == nil {
			t.Errorf("ParseTimeOfDay(%q) succeeded, want error", s)
		}
	}
	if Midnight.DayEndMinutes() != 24*60 || MustTimeOfDay(24, 0).DayEndMinutes() != 24*60 {
		t.Error("00:00 and 24:00 must both mean the following midnight as a day end")
	}
}

func TestDateArithmetic(t *testing.T) {
	tests := []struct {
		from string
		n    int
		want string
	}{
		{"2028-02-28", 1, "2028-02-29"},
		{"2027-02-28", 1, "2027-03-01"},
		{"2027-12-31", 1, "2028-01-01"},
		{"2026-03-08", -1, "2026-03-07"}, // across a US DST change
		{"2026-11-01", 7, "2026-11-08"},
	}
	for _, tt := range tests {
		if got := date(t, tt.from).AddDays(tt.n); got != date(t, tt.want) {
			t.Errorf("%s + %d = %s, want %s", tt.from, tt.n, got, tt.want)
		}
	}
	if n := date(t, "2026-03-01").DaysUntil(date(t, "2026-04-01")); n != 31 {
		t.Errorf("DaysUntil = %d, want 31", n)
	}
	if !date(t, "2026-01-01").Before(date(t, "2026-01-02")) {
		t.Error("Before")
	}
	if _, err := ParseDate("2026-02-30"); err == nil {
		t.Error("ParseDate accepted 2026-02-30")
	}
	var d Date
	if err := d.UnmarshalText([]byte("2026-10-31")); err != nil || d.Weekday() != time.Saturday {
		t.Errorf("UnmarshalText: %v %v", d, err)
	}
}

func TestAtResolvesDSTGapsAndOverlaps(t *testing.T) {
	tests := []struct {
		name string
		tz   string
		date string
		tod  string
		want string
	}{
		{"normal evening", "America/New_York", "2026-06-01", "20:00", "2026-06-02T00:00:00Z"},
		{"gap resolves to gap end", "America/New_York", "2026-03-08", "02:30", "2026-03-08T07:00:00Z"},
		{"gap start resolves to gap end", "America/New_York", "2026-03-08", "02:00", "2026-03-08T07:00:00Z"},
		{"time right after gap", "America/New_York", "2026-03-08", "03:00", "2026-03-08T07:00:00Z"},
		{"overlap resolves to earlier", "America/New_York", "2026-11-01", "01:30", "2026-11-01T05:30:00Z"},
		{"overlap start resolves to earlier", "America/New_York", "2026-11-01", "01:00", "2026-11-01T05:00:00Z"},
		{"half-hour DST gap", "Australia/Lord_Howe", "2026-10-04", "02:15", "2026-10-03T15:30:00Z"},
		{"overlap at local midnight", "America/Santiago", "2026-04-04", "23:30", "2026-04-05T02:30:00Z"},
		{"quarter-hour offset", "Asia/Kathmandu", "2026-06-01", "21:00", "2026-06-01T15:15:00Z"},
		{"24:00 is next day start", "America/New_York", "2026-06-01", "24:00", "2026-06-02T04:00:00Z"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := household(t, tt.tz, "00:00", time.Monday)
			tod, _ := ParseTimeOfDay(tt.tod)
			if got := h.At(date(t, tt.date), tod); !got.Equal(utc(t, tt.want)) {
				t.Errorf("At(%s %s) = %s, want %s", tt.date, tt.tod, got.UTC().Format(time.RFC3339), tt.want)
			}
		})
	}
}

func TestStartOfDayWhenMidnightIsSkipped(t *testing.T) {
	// Chile springs forward at local midnight: 2026-09-06 starts at 01:00 -03.
	h := household(t, "America/Santiago", "00:00", time.Monday)
	if got, want := h.StartOfDay(date(t, "2026-09-06")), utc(t, "2026-09-06T04:00:00Z"); !got.Equal(want) {
		t.Errorf("StartOfDay = %s, want %s", got.UTC(), want)
	}
	// Day after the fall-back at local midnight starts at 00:00 -04.
	if got, want := h.StartOfDay(date(t, "2026-04-05")), utc(t, "2026-04-05T04:00:00Z"); !got.Equal(want) {
		t.Errorf("StartOfDay = %s, want %s", got.UTC(), want)
	}
}

func TestDayCloseAndDayLengthsAcrossDST(t *testing.T) {
	h := household(t, "America/New_York", "00:00", time.Monday)
	dayLength := func(d string) time.Duration {
		dd := date(t, d)
		return h.DayClose(dd, h.DayEnd).Sub(h.DayClose(dd.AddDays(-1), h.DayEnd))
	}
	if l := dayLength("2026-03-08"); l != 23*time.Hour {
		t.Errorf("spring-forward day length = %s, want 23h", l)
	}
	if l := dayLength("2026-11-01"); l != 25*time.Hour {
		t.Errorf("fall-back day length = %s, want 25h", l)
	}
	if l := dayLength("2026-06-01"); l != 24*time.Hour {
		t.Errorf("normal day length = %s, want 24h", l)
	}

	kid := MustTimeOfDay(20, 0)
	if got, want := h.DayClose(date(t, "2026-03-08"), kid), utc(t, "2026-03-09T00:00:00Z"); !got.Equal(want) {
		t.Errorf("kid DayClose = %s, want %s (20:00 EDT)", got.UTC(), want)
	}
	if got, want := h.DayClose(date(t, "2026-03-07"), kid), utc(t, "2026-03-08T01:00:00Z"); !got.Equal(want) {
		t.Errorf("kid DayClose = %s, want %s (20:00 EST)", got.UTC(), want)
	}
}

func TestDayContaining(t *testing.T) {
	h := household(t, "America/New_York", "00:00", time.Monday)
	kid := MustTimeOfDay(20, 0)
	tests := []struct {
		at     string
		dayEnd TimeOfDay
		want   string
	}{
		{"2026-03-08T23:59:00Z", kid, "2026-03-08"},      // 19:59 EDT
		{"2026-03-09T00:00:00Z", kid, "2026-03-08"},      // exactly at close: still that day
		{"2026-03-09T00:01:00Z", kid, "2026-03-09"},      // 20:01 EDT: next day's window
		{"2026-03-09T04:00:00Z", Midnight, "2026-03-08"}, // local midnight with a midnight day end
		{"2026-03-09T04:01:00Z", Midnight, "2026-03-09"}, // just after
		{"2026-11-01T05:30:00Z", Midnight, "2026-11-01"}, // first 01:30 (EDT)
		{"2026-11-01T06:30:00Z", Midnight, "2026-11-01"}, // second 01:30 (EST)
		{"2026-11-02T04:59:00Z", Midnight, "2026-11-01"}, // 23:59 EST on the 25h day
	}
	for _, tt := range tests {
		if got := h.DayContaining(utc(t, tt.at), tt.dayEnd); got != date(t, tt.want) {
			t.Errorf("DayContaining(%s, %s) = %s, want %s", tt.at, tt.dayEnd, got, tt.want)
		}
	}
}

func TestNextDayClose(t *testing.T) {
	h := household(t, "America/New_York", "00:00", time.Monday)
	kid := MustTimeOfDay(20, 0)

	d, c := h.NextDayClose(utc(t, "2026-03-08T12:00:00Z"), kid)
	if d != date(t, "2026-03-08") || !c.Equal(utc(t, "2026-03-09T00:00:00Z")) {
		t.Errorf("NextDayClose midday = %s %s", d, c.UTC())
	}
	// Exactly at a close, the next close is the following day's.
	d, c = h.NextDayClose(utc(t, "2026-03-09T00:00:00Z"), kid)
	if d != date(t, "2026-03-09") || !c.Equal(utc(t, "2026-03-10T00:00:00Z")) {
		t.Errorf("NextDayClose at close = %s %s", d, c.UTC())
	}
}

func TestWeeks(t *testing.T) {
	sat := date(t, "2026-10-31")
	mon := household(t, "America/New_York", "00:00", time.Monday)
	sun := household(t, "America/New_York", "00:00", time.Sunday)
	if got := mon.WeekStartOf(sat); got != date(t, "2026-10-26") {
		t.Errorf("Monday week start = %s", got)
	}
	if got := sun.WeekStartOf(sat); got != date(t, "2026-10-25") {
		t.Errorf("Sunday week start = %s", got)
	}
	if got := mon.WeekStartOf(date(t, "2026-10-26")); got != date(t, "2026-10-26") {
		t.Errorf("week start of a Monday = %s", got)
	}
	// Week of 2026-10-26 closes at the end of Sunday 2026-11-01, the 25-hour fall-back day.
	if got, want := mon.WeekClose(date(t, "2026-10-28"), Midnight), utc(t, "2026-11-02T05:00:00Z"); !got.Equal(want) {
		t.Errorf("WeekClose = %s, want %s", got.UTC(), want)
	}
}

func TestEffectiveDayEnd(t *testing.T) {
	early := MustTimeOfDay(20, 0)
	late := MustTimeOfDay(21, 30)

	h := household(t, "UTC", "00:00", time.Monday)
	if got, err := h.EffectiveDayEnd(nil); err != nil || got != Midnight {
		t.Errorf("no override = %s, %v", got, err)
	}
	if got, err := h.EffectiveDayEnd(&early); err != nil || got != early {
		t.Errorf("earlier override = %s, %v", got, err)
	}
	mid := Midnight
	if got, err := h.EffectiveDayEnd(&mid); err != nil || got != mid {
		t.Errorf("equal override = %s, %v", got, err)
	}

	h9 := household(t, "UTC", "21:00", time.Monday)
	for _, o := range []TimeOfDay{late, Midnight} {
		if _, err := h9.EffectiveDayEnd(&o); !errors.Is(err, ErrDayEndAfterHousehold) {
			t.Errorf("override %s after 21:00: err = %v", o, err)
		}
	}
}

func TestNewHouseholdRejectsUnknownZone(t *testing.T) {
	if _, err := NewHousehold("Mars/Olympus_Mons", Midnight, time.Monday); err == nil {
		t.Error("expected error for unknown timezone")
	}
}
