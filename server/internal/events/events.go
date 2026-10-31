// Package events is the domain event outbox. Domain code emits events inside the same database
// transaction as the state change (Bus.InTx + Tx.Emit), so an event exists if and only if the
// change committed. A dispatcher then turns each event into one job per subscriber on the
// jobs runner, which provides retries, backoff and crash recovery. Delivery is at-least-once:
// subscribers must be idempotent (Envelope.ID identifies the event).
package events

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
	"github.com/voidrot/chorecast/server/internal/jobs"
)

const (
	dispatchBatch = 100
	pollInterval  = time.Second
	// KeepDispatched is how long dispatched events are kept before maintenance purges them.
	KeepDispatched = 30 * 24 * time.Hour
)

// Event is a typed domain event, e.g. assignment.submitted. EventType must be stable: it is
// stored and used to route to subscribers.
type Event interface {
	EventType() string
}

// Envelope is an event as delivered to a subscriber.
type Envelope struct {
	ID         int64           `json:"id"`
	Type       string          `json:"type"`
	OccurredAt time.Time       `json:"occurredAt"`
	Payload    json.RawMessage `json:"payload"`
}

// Decode unmarshals an envelope's payload into its typed event.
func Decode[T Event](env Envelope) (T, error) {
	var v T
	err := json.Unmarshal(env.Payload, &v)
	return v, err
}

// Handler processes one delivered event. Returning an error retries it with backoff.
type Handler func(ctx context.Context, env Envelope) error

type subscriber struct {
	name string
	kind string // jobs kind
}

// Bus emits and dispatches events.
type Bus struct {
	db     *db.DB
	runner *jobs.Runner
	clock  clock.Clock
	log    *slog.Logger
	subs   map[string][]subscriber // event type -> subscribers
	wake   chan struct{}
}

func NewBus(d *db.DB, r *jobs.Runner, c clock.Clock, log *slog.Logger) *Bus {
	return &Bus{db: d, runner: r, clock: c, log: log, subs: map[string][]subscriber{}, wake: make(chan struct{}, 1)}
}

// Subscribe registers handler for eventType under a unique, stable name (it becomes part of the
// job kind and idempotency key, so renaming it re-delivers in-flight events). Call before Start.
func (b *Bus) Subscribe(eventType, name string, h Handler, opts jobs.KindOptions) {
	kind := "event." + name
	b.runner.Register(kind, func(ctx context.Context, j jobs.Job) error {
		env, err := jobs.Decode[Envelope](j)
		if err != nil {
			return fmt.Errorf("decode envelope: %w", err)
		}
		return h(ctx, env)
	}, opts)
	b.subs[eventType] = append(b.subs[eventType], subscriber{name: name, kind: kind})
}

// Tx is a transaction that can emit events. Do all database work inside InTx through Q: the
// database has a single connection, so using any other Queries inside the transaction blocks.
type Tx struct {
	Q       *sqlc.Queries
	clock   clock.Clock
	emitted int
}

// Emit records ev in the transaction. It is delivered only if the transaction commits.
func (t *Tx) Emit(ctx context.Context, ev Event) error {
	payload, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("encode %s: %w", ev.EventType(), err)
	}
	if _, err := t.Q.InsertEvent(ctx, sqlc.InsertEventParams{
		Type: ev.EventType(), Payload: string(payload), OccurredAt: db.Timestamp(t.clock.Now()),
	}); err != nil {
		return fmt.Errorf("emit %s: %w", ev.EventType(), err)
	}
	t.emitted++
	return nil
}

// InTx runs fn in a transaction. If fn returns an error or panics, the transaction rolls back
// and none of its events are kept. After a commit that emitted events, the dispatcher is woken.
func (b *Bus) InTx(ctx context.Context, fn func(tx *Tx) error) (err error) {
	sqlTx, err := b.db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = sqlTx.Rollback()
		}
	}()
	tx := &Tx{Q: b.db.WithTx(sqlTx), clock: b.clock}
	if err := fn(tx); err != nil {
		return err
	}
	if err := sqlTx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	committed = true
	if tx.emitted > 0 {
		b.notify()
	}
	return nil
}

func (b *Bus) notify() {
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

// Dispatch fans out every undispatched event, oldest first, into one job per subscriber, and
// returns how many events it processed. Enqueueing is idempotent, so a crash between enqueueing
// and marking an event re-dispatches it without duplicate jobs.
func (b *Bus) Dispatch(ctx context.Context) (int, error) {
	total := 0
	for {
		rows, err := b.db.ListUndispatchedEvents(ctx, dispatchBatch)
		if err != nil {
			return total, fmt.Errorf("list events: %w", err)
		}
		for _, row := range rows {
			if err := b.dispatchOne(ctx, row); err != nil {
				return total, err
			}
			total++
		}
		if len(rows) < dispatchBatch {
			return total, nil
		}
	}
}

func (b *Bus) dispatchOne(ctx context.Context, row sqlc.Event) error {
	occurred, err := db.ParseTimestamp(row.OccurredAt)
	if err != nil {
		return fmt.Errorf("event %d: %w", row.ID, err)
	}
	env := Envelope{ID: row.ID, Type: row.Type, OccurredAt: occurred, Payload: json.RawMessage(row.Payload)}
	for _, s := range b.subs[row.Type] {
		key := "event:" + strconv.FormatInt(row.ID, 10) + ":" + s.name
		if _, _, err := b.runner.Enqueue(ctx, s.kind, env, jobs.EnqueueOptions{Key: key}); err != nil {
			return fmt.Errorf("event %d to %s: %w", row.ID, s.name, err)
		}
	}
	return b.db.MarkEventDispatched(ctx, sqlc.MarkEventDispatchedParams{
		DispatchedAt: sql.NullString{String: db.Timestamp(b.clock.Now()), Valid: true}, ID: row.ID,
	})
}

// Start dispatches until ctx is cancelled: immediately after a commit that emitted events, and
// every second to pick up anything left from before a restart.
func (b *Bus) Start(ctx context.Context) {
	t := time.NewTicker(pollInterval)
	defer t.Stop()
	for {
		if _, err := b.Dispatch(ctx); err != nil && ctx.Err() == nil {
			b.log.Error("event dispatch", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-b.wake:
		}
	}
}

// Purge deletes events dispatched before cutoff. Called by daily maintenance.
func (b *Bus) Purge(ctx context.Context, cutoff time.Time) (int64, error) {
	return b.db.DeleteDispatchedEventsBefore(ctx, sql.NullString{String: db.Timestamp(cutoff), Valid: true})
}
