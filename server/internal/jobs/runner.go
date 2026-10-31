// Package jobs runs background work in-process, backed by SQLite: a queue of one-off jobs
// with retries, and a scheduler that enqueues recurring work (day close, week close,
// maintenance) driven by the clock. Delivery is at-least-once, so handlers must be idempotent.
package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
)

const (
	DefaultQueue       = "default"
	defaultMaxAttempts = 5
	backoffBase        = 30 * time.Second
	backoffMax         = time.Hour
	pollInterval       = time.Second
)

// Job is a claimed job handed to a handler.
type Job struct {
	ID      int64
	Kind    string
	Payload json.RawMessage
	Attempt int // 1 on the first run
}

// Decode unmarshals the job's payload.
func Decode[T any](j Job) (T, error) {
	var v T
	err := json.Unmarshal(j.Payload, &v)
	return v, err
}

// Handler processes one job. Returning an error schedules a retry with backoff until the
// kind's max attempts is reached.
type Handler func(ctx context.Context, j Job) error

// KindOptions configures a job kind.
type KindOptions struct {
	Queue       string // default DefaultQueue
	MaxAttempts int    // default 5
}

type kind struct {
	handler Handler
	opts    KindOptions
}

// Runner owns the job queue and its workers.
type Runner struct {
	q      *sqlc.Queries
	clock  clock.Clock
	log    *slog.Logger
	kinds  map[string]kind
	queues map[string]int // queue -> worker count
	wake   chan struct{}
}

// NewRunner returns a Runner. Register kinds and set queue concurrency before Start.
func NewRunner(q *sqlc.Queries, c clock.Clock, log *slog.Logger) *Runner {
	return &Runner{
		q: q, clock: c, log: log,
		kinds:  map[string]kind{},
		queues: map[string]int{DefaultQueue: 1},
		wake:   make(chan struct{}, 1),
	}
}

// SetConcurrency sets how many jobs of a queue may run at once (e.g. media = 1).
func (r *Runner) SetConcurrency(queue string, workers int) { r.queues[queue] = max(workers, 1) }

// Register adds a job kind. It panics on duplicates (a programming error).
func (r *Runner) Register(name string, h Handler, opts KindOptions) {
	if _, dup := r.kinds[name]; dup {
		panic(fmt.Sprintf("jobs: kind %q registered twice", name))
	}
	if opts.Queue == "" {
		opts.Queue = DefaultQueue
	}
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = defaultMaxAttempts
	}
	if _, ok := r.queues[opts.Queue]; !ok {
		r.queues[opts.Queue] = 1
	}
	r.kinds[name] = kind{handler: h, opts: opts}
}

// EnqueueOptions are optional job settings.
type EnqueueOptions struct {
	Key   string    // idempotency key: a second enqueue with the same key is a no-op
	RunAt time.Time // zero = now
}

// Enqueue adds a job. It returns the new job's id, or 0 and created=false when a job with the
// same idempotency key already exists.
func (r *Runner) Enqueue(ctx context.Context, kindName string, payload any, opts EnqueueOptions) (id int64, created bool, err error) {
	k, ok := r.kinds[kindName]
	if !ok {
		return 0, false, fmt.Errorf("jobs: unknown kind %q", kindName)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, false, fmt.Errorf("jobs: encode payload: %w", err)
	}
	now := r.clock.Now()
	runAt := opts.RunAt
	if runAt.IsZero() {
		runAt = now
	}
	id, err = r.q.EnqueueJob(ctx, sqlc.EnqueueJobParams{
		Queue:          k.opts.Queue,
		Kind:           kindName,
		Payload:        string(body),
		IdempotencyKey: sql.NullString{String: opts.Key, Valid: opts.Key != ""},
		RunAt:          db.Timestamp(runAt),
		MaxAttempts:    int64(k.opts.MaxAttempts),
		CreatedAt:      db.Timestamp(now),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil // duplicate key
	}
	if err != nil {
		return 0, false, fmt.Errorf("jobs: enqueue %s: %w", kindName, err)
	}
	r.notify()
	return id, true, nil
}

func (r *Runner) notify() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Recover requeues jobs left running by a previous process that stopped mid-run. Call once at
// startup, before Start.
func (r *Runner) Recover(ctx context.Context) error {
	n, err := r.q.RequeueRunningJobs(ctx)
	if err != nil {
		return fmt.Errorf("jobs: recover: %w", err)
	}
	if n > 0 {
		r.log.Warn("requeued interrupted jobs", "count", n)
	}
	return nil
}

// RunDue runs every due job in every queue, one at a time, until none are due. It returns the
// number of jobs run. Start uses the same path; tests call it directly with a fake clock.
func (r *Runner) RunDue(ctx context.Context) (int, error) {
	total := 0
	for {
		ran := 0
		for queue := range r.queues {
			ok, err := r.runOne(ctx, queue)
			if err != nil {
				return total, err
			}
			if ok {
				ran++
			}
		}
		if ran == 0 {
			return total, nil
		}
		total += ran
	}
}

// runOne claims and runs one due job from queue. It reports whether a job was run.
func (r *Runner) runOne(ctx context.Context, queue string) (bool, error) {
	now := r.clock.Now()
	row, err := r.q.ClaimJob(ctx, sqlc.ClaimJobParams{Now: sql.NullString{String: db.Timestamp(now), Valid: true}, Queue: queue})
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("jobs: claim: %w", err)
	}
	job := Job{ID: row.ID, Kind: row.Kind, Payload: json.RawMessage(row.Payload), Attempt: int(row.Attempts)}
	log := r.log.With("job", row.ID, "kind", row.Kind, "attempt", job.Attempt)

	// Bookkeeping after the handler returns must not be cancelled by shutdown, or a job that
	// finished would stay "running" and run again after restart.
	bg := context.WithoutCancel(ctx)
	k, ok := r.kinds[row.Kind]
	if !ok {
		return true, r.fail(bg, row.ID, "no handler registered for kind "+row.Kind)
	}
	runErr := safeRun(ctx, k.handler, job)
	if runErr == nil {
		return true, r.q.CompleteJob(bg, sqlc.CompleteJobParams{
			FinishedAt: sql.NullString{String: db.Timestamp(r.clock.Now()), Valid: true}, ID: row.ID,
		})
	}

	switch {
	case ctx.Err() != nil:
		// Shutting down: put it back without waiting for backoff.
		log.Info("job interrupted by shutdown; requeued")
		return true, r.q.RetryJob(bg, sqlc.RetryJobParams{
			RunAt: db.Timestamp(now), LastError: errString(runErr), ID: row.ID,
		})
	case job.Attempt >= int(row.MaxAttempts):
		log.Error("job failed permanently", "err", runErr)
		return true, r.fail(bg, row.ID, runErr.Error())
	default:
		delay := backoff(job.Attempt)
		log.Warn("job failed; will retry", "err", runErr, "in", delay)
		return true, r.q.RetryJob(bg, sqlc.RetryJobParams{
			RunAt: db.Timestamp(r.clock.Now().Add(delay)), LastError: errString(runErr), ID: row.ID,
		})
	}
}

func (r *Runner) fail(ctx context.Context, id int64, msg string) error {
	return r.q.FailJob(ctx, sqlc.FailJobParams{
		FinishedAt: sql.NullString{String: db.Timestamp(r.clock.Now()), Valid: true},
		LastError:  sql.NullString{String: msg, Valid: true},
		ID:         id,
	})
}

// safeRun turns a handler panic into an error so one bad job can't kill the worker.
func safeRun(ctx context.Context, h Handler, j Job) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
		}
	}()
	return h(ctx, j)
}

// backoff returns the delay before retry n+1: 30s, 1m, 2m, 4m, ... capped at 1h.
func backoff(attempt int) time.Duration {
	d := backoffBase
	for i := 1; i < attempt && d < backoffMax; i++ {
		d *= 2
	}
	return min(d, backoffMax)
}

func errString(err error) sql.NullString { return sql.NullString{String: err.Error(), Valid: true} }

// PurgeFinished deletes done jobs finished before doneBefore and failed jobs finished before
// failedBefore.
func (r *Runner) PurgeFinished(ctx context.Context, doneBefore, failedBefore time.Time) (int64, error) {
	return r.q.DeleteFinishedJobs(ctx, sqlc.DeleteFinishedJobsParams{
		DoneBefore:   sql.NullString{String: db.Timestamp(doneBefore), Valid: true},
		FailedBefore: sql.NullString{String: db.Timestamp(failedBefore), Valid: true},
	})
}

// Start runs workers for every queue until ctx is cancelled, then waits for running jobs to
// return. Call Recover first.
func (r *Runner) Start(ctx context.Context) {
	var wg sync.WaitGroup
	for queue, n := range r.queues {
		for range n {
			wg.Go(func() { r.work(ctx, queue) })
		}
	}
	wg.Wait()
}

func (r *Runner) work(ctx context.Context, queue string) {
	t := time.NewTicker(pollInterval)
	defer t.Stop()
	for {
		ran, err := r.runOne(ctx, queue)
		if err != nil {
			r.log.Error("job worker", "queue", queue, "err", err)
		}
		if ran && err == nil {
			continue // drain without waiting
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-r.wake:
			r.notify() // pass the wake-up on to other idle workers
		}
	}
}
