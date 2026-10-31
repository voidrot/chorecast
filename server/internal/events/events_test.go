package events

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
	"github.com/voidrot/chorecast/server/internal/jobs"
)

var (
	quiet = slog.New(slog.NewTextHandler(io.Discard, nil))
	t0    = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
)

type choreDone struct {
	KidID int64  `json:"kidId"`
	Chore string `json:"chore"`
}

func (choreDone) EventType() string { return "test.chore_done" }

type orphan struct{}

func (orphan) EventType() string { return "test.no_subscribers" }

type env struct {
	db     *db.DB
	clock  *clock.Fake
	runner *jobs.Runner
	bus    *Bus
}

func newEnv(t *testing.T, d *db.DB, c *clock.Fake) *env {
	t.Helper()
	r := jobs.NewRunner(d.Queries, c, quiet)
	return &env{db: d, clock: c, runner: r, bus: NewBus(d, r, c, quiet)}
}

func openDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// recorder records delivered events; it can fail the first n deliveries.
type recorder struct {
	mu   sync.Mutex
	got  []choreDone
	envs []Envelope
	fail int
}

func (r *recorder) handle(_ context.Context, e Envelope) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail > 0 {
		r.fail--
		return errors.New("subscriber down")
	}
	ev, err := Decode[choreDone](e)
	if err != nil {
		return err
	}
	r.got = append(r.got, ev)
	r.envs = append(r.envs, e)
	return nil
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.got)
}

func emit(t *testing.T, b *Bus, evs ...Event) {
	t.Helper()
	if err := b.InTx(t.Context(), func(tx *Tx) error {
		for _, ev := range evs {
			if err := tx.Emit(t.Context(), ev); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func dispatch(t *testing.T, b *Bus, want int) {
	t.Helper()
	n, err := b.Dispatch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if n != want {
		t.Fatalf("Dispatch processed %d events, want %d", n, want)
	}
}

func run(t *testing.T, r *jobs.Runner, want int) {
	t.Helper()
	n, err := r.RunDue(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if n != want {
		t.Fatalf("RunDue ran %d jobs, want %d", n, want)
	}
}

func countEvents(t *testing.T, d *db.DB) int {
	t.Helper()
	var n int
	if err := d.SQL.QueryRowContext(t.Context(), `SELECT count(*) FROM events`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Acceptance (#5): a rolled-back transaction emits no event, and neither its state change nor
// its event survive.
func TestRollbackEmitsNothing(t *testing.T) {
	e := newEnv(t, openDB(t), clock.NewFake(t0))
	rec := &recorder{}
	e.bus.Subscribe(choreDone{}.EventType(), "rec", rec.handle, jobs.KindOptions{})

	errBoom := errors.New("validation failed")
	err := e.bus.InTx(t.Context(), func(tx *Tx) error {
		if _, err := tx.Q.CreateUser(t.Context(), sqlc.CreateUserParams{DisplayName: "Sam", Role: "child"}); err != nil {
			return err
		}
		if err := tx.Emit(t.Context(), choreDone{KidID: 1, Chore: "dishes"}); err != nil {
			return err
		}
		return errBoom
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("InTx err = %v", err)
	}

	func() {
		defer func() { _ = recover() }()
		_ = e.bus.InTx(t.Context(), func(tx *Tx) error {
			_ = tx.Emit(t.Context(), choreDone{KidID: 2})
			panic("handler bug")
		})
	}()

	if n := countEvents(t, e.db); n != 0 {
		t.Errorf("%d events persisted after rollbacks", n)
	}
	if n, _ := e.db.CountUsers(t.Context()); n != 0 {
		t.Errorf("state change survived rollback: %d users", n)
	}
	dispatch(t, e.bus, 0)
	run(t, e.runner, 0)
}

func TestCommitDeliversExactlyOnce(t *testing.T) {
	e := newEnv(t, openDB(t), clock.NewFake(t0))
	rec := &recorder{}
	e.bus.Subscribe(choreDone{}.EventType(), "rec", rec.handle, jobs.KindOptions{})

	emit(t, e.bus, choreDone{KidID: 7, Chore: "dishes"})
	dispatch(t, e.bus, 1)
	run(t, e.runner, 1)
	if rec.count() != 1 || rec.got[0] != (choreDone{KidID: 7, Chore: "dishes"}) {
		t.Fatalf("delivered %+v", rec.got)
	}
	if env := rec.envs[0]; env.ID == 0 || env.Type != "test.chore_done" || !env.OccurredAt.Equal(t0) {
		t.Errorf("envelope = %+v", env)
	}
	dispatch(t, e.bus, 0)
	run(t, e.runner, 0)
}

// Acceptance (#5): a committed event is delivered even if the process dies before dispatching.
func TestDeliveredAfterCrashBeforeDispatch(t *testing.T) {
	d, c := openDB(t), clock.NewFake(t0)
	before := newEnv(t, d, c)
	emit(t, before.bus, choreDone{KidID: 1, Chore: "trash"})
	// ...process dies here: never dispatched.

	after := newEnv(t, d, c)
	rec := &recorder{}
	after.bus.Subscribe(choreDone{}.EventType(), "rec", rec.handle, jobs.KindOptions{})
	if err := after.runner.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	dispatch(t, after.bus, 1)
	run(t, after.runner, 1)
	if rec.count() != 1 {
		t.Errorf("delivered %d times, want 1", rec.count())
	}
}

// A crash after enqueueing a subscriber's job but before marking the event re-dispatches it
// without creating a second job.
func TestRedispatchAfterCrashMidDispatchIsNotDuplicated(t *testing.T) {
	e := newEnv(t, openDB(t), clock.NewFake(t0))
	rec := &recorder{}
	e.bus.Subscribe(choreDone{}.EventType(), "rec", rec.handle, jobs.KindOptions{})
	emit(t, e.bus, choreDone{KidID: 1})

	rows, err := e.db.ListUndispatchedEvents(t.Context(), 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %v, %v", rows, err)
	}
	ev := Envelope{ID: rows[0].ID, Type: rows[0].Type, Payload: []byte(rows[0].Payload)}
	if _, _, err := e.runner.Enqueue(t.Context(), "event.rec", ev, jobs.EnqueueOptions{Key: "event:1:rec"}); err != nil {
		t.Fatal(err)
	}

	dispatch(t, e.bus, 1) // still undispatched, so processed again
	run(t, e.runner, 1)   // but only one job exists
	if rec.count() != 1 {
		t.Errorf("delivered %d times, want 1", rec.count())
	}
}

func TestFanOutWithIndependentRetries(t *testing.T) {
	e := newEnv(t, openDB(t), clock.NewFake(t0))
	flaky, steady := &recorder{fail: 1}, &recorder{}
	e.bus.Subscribe(choreDone{}.EventType(), "flaky", flaky.handle, jobs.KindOptions{})
	e.bus.Subscribe(choreDone{}.EventType(), "steady", steady.handle, jobs.KindOptions{})

	emit(t, e.bus, choreDone{KidID: 1})
	dispatch(t, e.bus, 1)
	run(t, e.runner, 2) // flaky fails, steady succeeds
	if flaky.count() != 0 || steady.count() != 1 {
		t.Fatalf("after first run: flaky=%d steady=%d", flaky.count(), steady.count())
	}
	e.clock.Advance(30 * time.Second)
	run(t, e.runner, 1) // only flaky retries
	if flaky.count() != 1 || steady.count() != 1 {
		t.Errorf("after retry: flaky=%d steady=%d", flaky.count(), steady.count())
	}
}

func TestOrderAndUnsubscribedEvents(t *testing.T) {
	e := newEnv(t, openDB(t), clock.NewFake(t0))
	rec := &recorder{}
	e.bus.Subscribe(choreDone{}.EventType(), "rec", rec.handle, jobs.KindOptions{})

	emit(t, e.bus, choreDone{KidID: 1})
	emit(t, e.bus, orphan{}, choreDone{KidID: 2})
	emit(t, e.bus, choreDone{KidID: 3})
	dispatch(t, e.bus, 4) // the orphan event is marked dispatched too
	run(t, e.runner, 3)
	for i, want := range []int64{1, 2, 3} {
		if rec.got[i].KidID != want {
			t.Fatalf("delivery order = %+v", rec.got)
		}
	}

	e.clock.Advance(KeepDispatched)
	if n, err := e.bus.Purge(t.Context(), e.clock.Now().Add(-KeepDispatched+time.Second)); err != nil || n != 4 {
		t.Errorf("Purge = %d, %v", n, err)
	}
}

func TestStartDispatchesAfterCommit(t *testing.T) {
	d := openDB(t)
	r := jobs.NewRunner(d.Queries, clock.Real{}, quiet)
	bus := NewBus(d, r, clock.Real{}, quiet)
	delivered := make(chan choreDone, 1)
	bus.Subscribe(choreDone{}.EventType(), "chan", func(_ context.Context, e Envelope) error {
		ev, err := Decode[choreDone](e)
		delivered <- ev
		return err
	}, jobs.KindOptions{})

	ctx, cancel := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	wg.Go(func() { r.Start(ctx) })
	wg.Go(func() { bus.Start(ctx) })
	defer func() { cancel(); wg.Wait() }()

	emit(t, bus, choreDone{KidID: 9})
	select {
	case ev := <-delivered:
		if ev.KidID != 9 {
			t.Errorf("delivered %+v", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("event not delivered by the running dispatcher and workers")
	}
}
