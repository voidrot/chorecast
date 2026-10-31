package auth

import (
	"sync"
	"time"

	"github.com/voidrot/chorecast/server/internal/clock"
)

// Limiter counts failures per key (a username or client IP) in a fixed window and blocks a key
// that reaches the limit until its window ends. State is in memory: a restart clears it, which
// is acceptable for a household server.
type Limiter struct {
	mu     sync.Mutex
	clock  clock.Clock
	limit  int
	window time.Duration
	keys   map[string]*limitEntry
}

type limitEntry struct {
	failures int
	start    time.Time
}

func NewLimiter(c clock.Clock, limit int, window time.Duration) *Limiter {
	return &Limiter{clock: c, limit: limit, window: window, keys: map[string]*limitEntry{}}
}

// Blocked reports whether key is blocked, and if so for how long.
func (l *Limiter) Blocked(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entry(key)
	if e == nil || e.failures < l.limit {
		return false, 0
	}
	return true, e.start.Add(l.window).Sub(l.clock.Now())
}

// Fail records a failure for key.
func (l *Limiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entry(key)
	if e == nil {
		e = &limitEntry{start: l.clock.Now()}
		l.keys[key] = e
	}
	e.failures++
}

// Reset clears key after a success.
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.keys, key)
}

// entry returns key's live entry, dropping it (and any other expired ones, occasionally) once
// its window has passed.
func (l *Limiter) entry(key string) *limitEntry {
	now := l.clock.Now()
	if len(l.keys) > 1024 {
		for k, e := range l.keys {
			if !now.Before(e.start.Add(l.window)) {
				delete(l.keys, k)
			}
		}
	}
	e := l.keys[key]
	if e != nil && !now.Before(e.start.Add(l.window)) {
		delete(l.keys, key)
		return nil
	}
	return e
}
