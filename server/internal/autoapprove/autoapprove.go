// Package autoapprove approves submitted chore instances nobody reviewed in time
// (docs/features/approvals-evidence.md, "Submission flow" step 5).
//
// The effective timeout is the chore's auto_approve_hours (NULL = the household default,
// 0 = never), else the household's auto_approve_hours (NULL = never). It counts from the latest
// submission (completed_at, which a resubmission after a rejection overwrites). The job reads
// both settings on every run, so a settings change applies from the next tick and a run after
// downtime catches up.
//
// Each instance is approved in its own transaction through instances.Approve with by = 0, so the
// credit is posted with created_by NULL (the system) and the note "auto-approved", and the usual
// instance.approved event (by 0) is emitted. That event is the audit hook until the audit log
// (#37) exists. Runs are idempotent: an instance that isn't submitted any more, or whose
// submission is newer than when it was listed, is skipped.
package autoapprove

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
	"github.com/voidrot/chorecast/server/internal/events"
	"github.com/voidrot/chorecast/server/internal/horizon"
	"github.com/voidrot/chorecast/server/internal/instances"
	"github.com/voidrot/chorecast/server/internal/jobs"
)

const (
	// Kind is the job kind; Interval is how often it runs.
	Kind     = "autoapprove.run"
	Interval = 5 * time.Minute

	// Note goes on the credit of an auto-approved instance.
	Note = "auto-approved"
)

// Service runs the auto-approve job.
type Service struct {
	db    *db.DB
	bus   *events.Bus
	clock clock.Clock
}

// New returns a Service.
func New(d *db.DB, bus *events.Bus, c clock.Clock) *Service {
	return &Service{db: d, bus: bus, clock: c}
}

// Register adds the job kind to runner and its recurring schedule to sched.
func (s *Service) Register(runner *jobs.Runner, sched *jobs.Scheduler, log *slog.Logger) {
	runner.Register(Kind, func(ctx context.Context, _ jobs.Job) error {
		n, err := s.Run(ctx)
		if n > 0 {
			log.Info("auto-approve", "approved", n)
		}
		return err
	}, jobs.KindOptions{MaxAttempts: 3})
	sched.Add(jobs.Every("autoapprove", Kind, Interval))
}

// Run approves every submitted instance that is past its timeout and returns how many it
// approved. One instance failing doesn't stop the rest; the errors are joined.
func (s *Service) Run(ctx context.Context) (int, error) {
	rows, err := s.db.ListSubmittedInstancesForAutoApprove(ctx)
	if err != nil {
		return 0, fmt.Errorf("list submitted instances: %w", err)
	}
	var approved int
	var errs []error
	for _, r := range rows {
		ok, err := s.approve(ctx, r.ID)
		if err != nil {
			errs = append(errs, fmt.Errorf("auto-approve instance %d: %w", r.ID, err))
		} else if ok {
			approved++
		}
	}
	return approved, errors.Join(errs...)
}

// approve re-reads the instance in its own transaction, so a rejection, resubmission or approval
// since the listing is honoured, and approves it when it is due. It reports whether it did.
func (s *Service) approve(ctx context.Context, id int64) (bool, error) {
	var approved bool
	err := s.bus.InTx(ctx, func(tx *events.Tx) error {
		approved = false
		row, err := tx.Q.GetChoreInstance(ctx, id)
		if err != nil {
			return fmt.Errorf("load instance: %w", err)
		}
		in := row.ChoreInstance
		if in.Status != horizon.StatusSubmitted || !in.CompletedAt.Valid {
			return nil
		}
		chore, err := tx.Q.GetChore(ctx, in.ChoreID)
		if err != nil {
			return fmt.Errorf("load chore: %w", err)
		}
		hh, err := tx.Q.GetHousehold(ctx)
		if err != nil {
			return fmt.Errorf("load household: %w", err)
		}
		submitted, err := db.ParseTimestamp(in.CompletedAt.String)
		if err != nil {
			return fmt.Errorf("parse completed_at: %w", err)
		}
		if !due(chore, hh, submitted, s.clock.Now()) {
			return nil
		}
		if hold, err := blocked(ctx, tx, in); err != nil || hold {
			return err
		}
		if err := instances.Approve(ctx, tx, s.clock, id, 0, Note); err != nil {
			if errors.Is(err, instances.ErrNotSubmitted) {
				return nil
			}
			return err
		}
		approved = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return approved, nil
}

// due reports whether a submission made at submitted has outlived the effective timeout at now.
func due(c sqlc.Chore, hh sqlc.Household, submitted, now time.Time) bool {
	hours := hh.AutoApproveHours // NULL = never
	if c.AutoApproveHours.Valid {
		hours = c.AutoApproveHours // 0 = never
	}
	if !hours.Valid || hours.Int64 <= 0 {
		return false
	}
	return !now.Before(submitted.Add(time.Duration(hours.Int64) * time.Hour))
}

// blocked is the seam for conditions that make a parent decide even after the timeout: an AI
// "not_yet" verdict and a near-duplicate photo flag (Phase 3). Nothing blocks today.
func blocked(context.Context, *events.Tx, sqlc.ChoreInstance) (bool, error) { return false, nil }
