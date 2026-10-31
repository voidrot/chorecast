package clock

import (
	"testing"
	"time"
)

func TestFake(t *testing.T) {
	start := time.Date(2026, 3, 8, 6, 0, 0, 0, time.UTC)
	f := NewFake(start)
	if !f.Now().Equal(start) {
		t.Fatalf("Now = %s, want %s", f.Now(), start)
	}
	if got := f.Advance(90 * time.Minute); !got.Equal(start.Add(90 * time.Minute)) {
		t.Errorf("Advance = %s", got)
	}
	f.Set(start)
	if !f.Now().Equal(start) {
		t.Errorf("Set: Now = %s", f.Now())
	}
}

func TestRealIsAClock(t *testing.T) {
	var c Clock = Real{}
	if c.Now().IsZero() {
		t.Error("Real.Now returned the zero time")
	}
}
