package jobs

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/voidrot/chorecast/server/internal/caltime"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type env struct {
	q     *sqlc.Queries
	clock *clock.Fake
}

func newEnv(t *testing.T, start time.Time) *env {
	t.Helper()
	d, err := db.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return &env{q: d.Queries, clock: clock.NewFake(start)}
}

func (e *env) runner() *Runner { return NewRunner(e.q, e.clock, quiet) }

func (e *env) job(t *testing.T, key string) sqlc.Job {
	t.Helper()
	j, err := e.q.GetJobByKey(t.Context(), sql.NullString{String: key, Valid: true})
	if err != nil {
		t.Fatal(err)
	}
	return j
}

// recorder is a handler that records the payloads it was called with.
type recorder struct {
	mu    sync.Mutex
	calls []KidDayPayload
	fail  int // fail this many calls before succeeding
}

func (r *recorder) handle(_ context.Context, j Job) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail > 0 {
		r.fail--
		return errors.New("boom")
	}
	p, err := Decode[KidDayPayload](j)
	if err != nil {
		return err
	}
	r.calls = append(r.calls, p)
	return nil
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

var t0 = time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)

func mustRun(t *testing.T, r *Runner, want int) {
	t.Helper()
	n, err := r.RunDue(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if n != want {
		t.Fatalf("RunDue ran %d jobs, want %d", n, want)
	}
}

func TestEnqueueRunAndIdempotency(t *testing.T) {
	e := newEnv(t, t0)
	r := e.runner()
	rec := &recorder{}
	r.Register("k", rec.handle, KindOptions{})

	payload := KidDayPayload{KidID: 7, Date: caltime.Date{Year: 2026, Month: 3, Day: 8}}
	id, created, err := r.Enqueue(t.Context(), "k", payload, EnqueueOptions{Key: "once"})
	if err != nil || !created || id == 0 {
		t.Fatalf("Enqueue = %d %v %v", id, created, err)
	}
	if _, created, err := r.Enqueue(t.Context(), "k", payload, EnqueueOptions{Key: "once"}); created || err != nil {
		t.Fatalf("duplicate Enqueue created=%v err=%v", created, err)
	}
	mustRun(t, r, 1)
	if rec.count() != 1 || rec.calls[0] != payload {
		t.Fatalf("calls = %+v", rec.calls)
	}
	if j := e.job(t, "once"); j.Status != "done" || j.Attempts != 1 || !j.FinishedAt.Valid {
		t.Errorf("job row = %+v", j)
	}
	mustRun(t, r, 0)

	if _, _, err := r.Enqueue(t.Context(), "nope", nil, EnqueueOptions{}); err == nil {
		t.Error("Enqueue of an unregistered kind should fail")
	}
}

func TestRunAtInTheFuture(t *testing.T) {
	e := newEnv(t, t0)
	r := e.runner()
	rec := &recorder{}
	r.Register("k", rec.handle, KindOptions{})
	_, _, _ = r.Enqueue(t.Context(), "k", KidDayPayload{}, EnqueueOptions{RunAt: t0.Add(time.Hour)})

	mustRun(t, r, 0)
	e.clock.Advance(59 * time.Minute)
	mustRun(t, r, 0)
	e.clock.Advance(time.Minute)
	mustRun(t, r, 1)
}

func TestRetriesWithBackoffThenSucceeds(t *testing.T) {
	e := newEnv(t, t0)
	r := e.runner()
	rec := &recorder{fail: 2}
	r.Register("k", rec.handle, KindOptions{})
	_, _, _ = r.Enqueue(t.Context(), "k", KidDayPayload{}, EnqueueOptions{Key: "flaky"})

	mustRun(t, r, 1) // attempt 1 fails -> retry in 30s
	if j := e.job(t, "flaky"); j.Status != "queued" || j.LastError.String != "boom" || j.RunAt != db.Timestamp(t0.Add(30*time.Second)) {
		t.Fatalf("after first failure: %+v", j)
	}
	e.clock.Advance(29 * time.Second)
	mustRun(t, r, 0)
	e.clock.Advance(time.Second)
	mustRun(t, r, 1) // attempt 2 fails -> retry in 1m
	e.clock.Advance(time.Minute)
	mustRun(t, r, 1) // attempt 3 succeeds
	if j := e.job(t, "flaky"); j.Status != "done" || j.Attempts != 3 || j.LastError.Valid {
		t.Errorf("final: %+v", j)
	}
}

func TestPermanentFailureAndPanics(t *testing.T) {
	e := newEnv(t, t0)
	r := e.runner()
	r.Register("panics", func(context.Context, Job) error { panic("kaboom") }, KindOptions{MaxAttempts: 2})
	_, _, _ = r.Enqueue(t.Context(), "panics", nil, EnqueueOptions{Key: "p"})

	mustRun(t, r, 1)
	e.clock.Advance(time.Hour)
	mustRun(t, r, 1)
	j := e.job(t, "p")
	if j.Status != "failed" || j.Attempts != 2 || j.LastError.String != "panic: kaboom" {
		t.Errorf("job = %+v", j)
	}
	e.clock.Advance(time.Hour)
	mustRun(t, r, 0)
}

func TestBackoff(t *testing.T) {
	want := []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute}
	for i, w := range want {
		if got := backoff(i + 1); got != w {
			t.Errorf("backoff(%d) = %s, want %s", i+1, got, w)
		}
	}
	if got := backoff(20); got != time.Hour {
		t.Errorf("backoff(20) = %s, want cap 1h", got)
	}
}

// A process that dies mid-job leaves it "running"; the next process requeues it and runs it
// exactly once more, with no duplicate row.
func TestRecoverAfterCrashMidRun(t *testing.T) {
	e := newEnv(t, t0)
	first := e.runner()
	first.Register("k", func(context.Context, Job) error { return nil }, KindOptions{})
	_, _, _ = first.Enqueue(t.Context(), "k", KidDayPayload{KidID: 1}, EnqueueOptions{Key: "crash"})

	// Simulate the crash: claim the job, then never finish it.
	if _, err := e.q.ClaimJob(t.Context(), sqlc.ClaimJobParams{
		Now: sql.NullString{String: db.Timestamp(t0), Valid: true}, Queue: DefaultQueue,
	}); err != nil {
		t.Fatal(err)
	}
	if j := e.job(t, "crash"); j.Status != "running" {
		t.Fatalf("status = %s, want running", j.Status)
	}

	second := e.runner()
	rec := &recorder{}
	second.Register("k", rec.handle, KindOptions{})
	mustRun(t, second, 0) // still running: not claimable until recovered
	if err := second.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	mustRun(t, second, 1)
	mustRun(t, second, 0)
	if rec.count() != 1 {
		t.Errorf("handler ran %d times, want 1", rec.count())
	}
	if _, created, _ := second.Enqueue(t.Context(), "k", KidDayPayload{KidID: 1}, EnqueueOptions{Key: "crash"}); created {
		t.Error("re-enqueue after recovery created a duplicate")
	}
}

func TestPurgeFinished(t *testing.T) {
	e := newEnv(t, t0)
	r := e.runner()
	r.Register("ok", func(context.Context, Job) error { return nil }, KindOptions{})
	r.Register("bad", func(context.Context, Job) error { return errors.New("x") }, KindOptions{MaxAttempts: 1})
	_, _, _ = r.Enqueue(t.Context(), "ok", nil, EnqueueOptions{Key: "a"})
	_, _, _ = r.Enqueue(t.Context(), "bad", nil, EnqueueOptions{Key: "b"})
	mustRun(t, r, 2)

	n, err := r.PurgeFinished(t.Context(), t0.Add(time.Second), t0)
	if err != nil || n != 1 {
		t.Fatalf("PurgeFinished = %d, %v (want only the done job)", n, err)
	}
	if j := e.job(t, "b"); j.Status != "failed" {
		t.Errorf("failed job purged too early: %+v", j)
	}
}

func TestStartRunsJobsUntilCancelled(t *testing.T) {
	e := newEnv(t, t0)
	r := NewRunner(e.q, clock.Real{}, quiet)
	r.SetConcurrency("media", 1)
	done := make(chan struct{})
	r.Register("k", func(context.Context, Job) error { close(done); return nil }, KindOptions{Queue: "media"})

	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan struct{})
	go func() { r.Start(ctx); close(stopped) }()
	if _, _, err := r.Enqueue(t.Context(), "k", nil, EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not run the job")
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after cancel")
	}
}

// --- scheduler -----------------------------------------------------------------------------

func nyHousehold(t *testing.T) func(context.Context) (caltime.Household, error) {
	t.Helper()
	h, err := caltime.NewHousehold("America/New_York", caltime.Midnight, time.Monday)
	if err != nil {
		t.Fatal(err)
	}
	return func(context.Context) (caltime.Household, error) { return h, nil }
}

func mustTick(t *testing.T, s *Scheduler, want int) {
	t.Helper()
	n, err := s.Tick(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if n != want {
		t.Fatalf("Tick created %d jobs, want %d", n, want)
	}
}

// Acceptance (#4): advancing the fake clock past a kid's day close in another timezone runs the
// day-close job exactly once; downtime is caught up after a restart.
func TestKidDayCloseRunsExactlyOnceAndCatchesUp(t *testing.T) {
	e := newEnv(t, t0) // 2026-03-08 08:00 EDT, the spring-forward day
	r := e.runner()
	rec := &recorder{}
	r.Register("day_close", rec.handle, KindOptions{})
	eight := caltime.MustTimeOfDay(20, 0)
	kids := []Kid{{ID: 1, DayEnd: &eight}}
	newSched := func() *Scheduler {
		s := NewScheduler(e.q, r, e.clock, quiet)
		s.Add(KidDayCloses("day_close", nyHousehold(t), func(context.Context) ([]Kid, error) { return kids, nil }))
		return s
	}
	s := newSched()

	mustTick(t, s, 0)                                          // first sight of the schedule: cursor starts now, no backfill
	e.clock.Set(time.Date(2026, 3, 8, 23, 59, 0, 0, time.UTC)) // 19:59 EDT
	mustTick(t, s, 0)
	mustRun(t, r, 0)

	e.clock.Set(time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC)) // 20:00 EDT: the close
	mustTick(t, s, 1)
	mustRun(t, r, 1)
	want := KidDayPayload{KidID: 1, Date: caltime.Date{Year: 2026, Month: 3, Day: 8}}
	if rec.calls[0] != want {
		t.Fatalf("payload = %+v, want %+v", rec.calls[0], want)
	}
	if j := e.job(t, "day_close:kid:1:2026-03-08"); j.RunAt != "2026-03-09T00:00:00.000Z" {
		t.Errorf("run_at = %s", j.RunAt)
	}
	for range 3 { // later ticks and runs do nothing more
		e.clock.Advance(10 * time.Minute)
		mustTick(t, s, 0)
		mustRun(t, r, 0)
	}

	// Server down for three days, then a fresh scheduler (restart) catches up in order.
	e.clock.Set(time.Date(2026, 3, 12, 1, 0, 0, 0, time.UTC)) // 21:00 EDT on the 11th
	mustTick(t, newSched(), 3)
	mustRun(t, r, 3)
	got := []caltime.Date{rec.calls[1].Date, rec.calls[2].Date, rec.calls[3].Date}
	wantDates := []caltime.Date{{Year: 2026, Month: 3, Day: 9}, {Year: 2026, Month: 3, Day: 10}, {Year: 2026, Month: 3, Day: 11}}
	for i := range got {
		if got[i] != wantDates[i] {
			t.Errorf("catch-up order = %v, want %v", got, wantDates)
			break
		}
	}
}

func TestDayEndChangeAndNewKid(t *testing.T) {
	e := newEnv(t, time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC))
	r := e.runner()
	rec := &recorder{}
	r.Register("day_close", rec.handle, KindOptions{})
	eight := caltime.MustTimeOfDay(20, 0)
	kids := []Kid{{ID: 1, DayEnd: &eight}}
	s := NewScheduler(e.q, r, e.clock, quiet)
	s.Add(KidDayCloses("day_close", nyHousehold(t), func(context.Context) ([]Kid, error) { return kids, nil }))
	mustTick(t, s, 0)

	// Kid 1's day end moves to 19:00 before today's close: it now fires at 19:00 EDT (23:00Z).
	seven := caltime.MustTimeOfDay(19, 0)
	kids[0].DayEnd = &seven
	e.clock.Set(time.Date(2026, 6, 1, 23, 0, 0, 0, time.UTC))
	mustTick(t, s, 1)

	// A kid added later starts from now: yesterday's closes aren't backfilled.
	kids = append(kids, Kid{ID: 2})
	mustTick(t, s, 0)
	e.clock.Set(time.Date(2026, 6, 2, 4, 0, 0, 0, time.UTC)) // midnight EDT: kid 2's first close
	mustTick(t, s, 1)
	if j := e.job(t, "day_close:kid:2:2026-06-01"); j.Status != "queued" {
		t.Errorf("kid 2 job = %+v", j)
	}
}

func TestKidWeekCloseAndHouseholdSchedules(t *testing.T) {
	e := newEnv(t, time.Date(2026, 10, 28, 16, 0, 0, 0, time.UTC)) // Wednesday
	r := e.runner()
	r.Register("week_close", func(context.Context, Job) error { return nil }, KindOptions{})
	r.Register("horizon", func(context.Context, Job) error { return nil }, KindOptions{})
	r.Register("maintenance", func(context.Context, Job) error { return nil }, KindOptions{})
	s := NewScheduler(e.q, r, e.clock, quiet)
	s.Add(KidWeekCloses("week_close", nyHousehold(t), func(context.Context) ([]Kid, error) { return []Kid{{ID: 3}}, nil }))
	s.Add(HouseholdDayCloses("horizon", "horizon", nyHousehold(t)))
	s.Add(Every("maintenance", "maintenance", 24*time.Hour))
	mustTick(t, s, 0)

	// Sunday 2026-11-01 closes at midnight EST after the 25-hour fall-back day.
	e.clock.Set(time.Date(2026, 11, 2, 5, 0, 0, 0, time.UTC))
	// week close (1) + household day closes Wed..Sun (5) + maintenance (4 full days elapsed)
	mustTick(t, s, 1+5+4)
	if j := e.job(t, "week_close:kid:3:2026-11-01"); j.RunAt != "2026-11-02T05:00:00.000Z" {
		t.Errorf("week close run_at = %s", j.RunAt)
	}
	e.clock.Advance(time.Hour)
	mustTick(t, s, 0)
}
