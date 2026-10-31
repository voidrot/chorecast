// Package clock is the only place allowed to read the wall clock. Everything else takes a
// Clock so tests can control time (a forbidigo lint rule enforces this).
package clock

import (
	"sync"
	"time"
)

// Clock reports the current instant.
type Clock interface {
	Now() time.Time
}

// Real reads the system clock.
type Real struct{}

func (Real) Now() time.Time { return time.Now() }

// Fake is a manually controlled Clock for tests. The zero value starts at the zero time;
// use NewFake to start somewhere meaningful. It is safe for concurrent use.
type Fake struct {
	mu  sync.Mutex
	now time.Time
}

// NewFake returns a Fake clock set to t.
func NewFake(t time.Time) *Fake { return &Fake{now: t} }

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// Set moves the clock to t (forwards or backwards).
func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = t
}

// Advance moves the clock forward by d and returns the new time.
func (f *Fake) Advance(d time.Duration) time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
	return f.now
}
