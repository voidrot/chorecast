package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/voidrot/chorecast/server/internal/audit"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/config"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
	"github.com/voidrot/chorecast/server/internal/household"
	"github.com/voidrot/chorecast/server/internal/jobs"
)

const (
	// Kind is the scheduled backup's job kind. It has a queue of its own, so a long backup never
	// holds up other jobs.
	Kind  = "backup.scheduled"
	queue = "backup"
	// staleAfter is how late a scheduled backup may run. Older occurrences (caught up after the
	// server was off) are skipped: the newest one already backs up the same data.
	staleAfter = 24 * time.Hour
)

// Scheduler takes the scheduled backups: daily, cfg.AfterDayEnd after the household day end,
// into <dataDir>/backups, keeping the newest cfg.Keep.
type Scheduler struct {
	db      *db.DB
	clock   clock.Clock
	dataDir string
	cfg     config.BackupConfig
}

// NewScheduler returns a Scheduler for the database in dataDir.
func NewScheduler(d *db.DB, c clock.Clock, dataDir string, cfg config.BackupConfig) *Scheduler {
	return &Scheduler{db: d, clock: c, dataDir: dataDir, cfg: cfg}
}

type jobPayload struct {
	At time.Time `json:"at"` // the occurrence; names the file, so a retry finds its own backup
}

// Register adds the job kind to runner and its daily schedule to sched.
func (s *Scheduler) Register(runner *jobs.Runner, sched *jobs.Scheduler, log *slog.Logger) {
	runner.Register(Kind, func(ctx context.Context, j jobs.Job) error {
		p, err := jobs.Decode[jobPayload](j)
		if err != nil {
			return err
		}
		return s.Run(ctx, p.At, log)
	}, jobs.KindOptions{Queue: queue, MaxAttempts: 3})
	sched.Add(s.schedules)
}

// Run takes the backup for the occurrence at, then prunes. A backup for at that already exists
// is kept, so running twice is harmless.
func (s *Scheduler) Run(ctx context.Context, at time.Time, log *slog.Logger) error {
	if late := s.clock.Now().Sub(at); late > staleAfter {
		log.Info("backup skipped: a later one is due", "occurrence", at, "late", late)
		return nil
	}
	dir := filepath.Join(s.dataDir, DirName)
	out := filepath.Join(dir, FileName(at))
	res, err := Write(ctx, s.dataDir, out, s.clock, WriteOptions{}) // in the server, which holds the data dir
	switch {
	case errors.Is(err, ErrExists):
		log.Info("backup already taken", "occurrence", at)
		// An earlier attempt may have died before recording it; recording again is a no-op if not.
		info, statErr := os.Stat(out)
		if statErr != nil {
			return statErr
		}
		if err := RecordCreated(ctx, s.db.Queries, at, audit.System, out, info.Size(), nil); err != nil {
			return err
		}
	case err != nil:
		return err // the runner logs it and retries
	default:
		if err := RecordCreated(ctx, s.db.Queries, res.CreatedAt, audit.System, res.Path, res.Size, &res.MediaFiles); err != nil {
			return err // the retry finds the archive and records it then
		}
		log.Info("backup", "path", res.Path, "bytes", res.Size, "snapshot_ms", res.Snapshot.Milliseconds(),
			"schema", res.SchemaVersion, "media_files", res.MediaFiles, "media_bytes", res.MediaBytes)
	}
	pruned, err := Prune(dir, s.cfg.Keep)
	if err != nil {
		return err
	}
	if pruned > 0 {
		log.Info("backup pruned", "deleted", pruned, "kept", s.cfg.Keep)
	}
	return nil
}

// schedules is the daily backup schedule; empty before first-run setup, when there is no
// household day end yet.
func (s *Scheduler) schedules(ctx context.Context) ([]jobs.Recurring, error) {
	h, err := household.Calendar(ctx, s.db.Queries)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	after := s.cfg.AfterDayEnd
	return []jobs.Recurring{{Name: "backup", Kind: Kind, Next: func(t time.Time) jobs.Occurrence {
		// The first close+after strictly after t is the first close strictly after t-after.
		d, closeAt := h.NextDayClose(t.Add(-after), h.DayEnd)
		at := closeAt.Add(after)
		return jobs.Occurrence{At: at, Key: d.String() + "@" + db.Timestamp(at), Payload: jobPayload{At: at}}
	}}}, nil
}

// RecordCreated writes the audit row for the archive at path, after its snapshot so the row is
// not in the archive. Only the file name is recorded, not the path. A scheduled (system) backup
// is recorded once per file name, so a retried job adds nothing. mediaFiles is nil when unknown.
func RecordCreated(ctx context.Context, q *sqlc.Queries, at time.Time, actor audit.Actor, path string, size int64, mediaFiles *int64) error {
	return audit.Record(ctx, q, at, audit.Entry{
		Actor: actor, Action: audit.ActionBackupCreated, TargetKind: audit.TargetBackup, Summary: filepath.Base(path),
		Details: audit.AuditDetails{Bytes: size, MediaFiles: mediaFiles},
	})
}

// ErrSchemaBehind means the database is not at this binary's schema, so a row cannot be written
// without migrating it.
var ErrSchemaBehind = errors.New("backup: the database is not at this binary's schema version")

// RecordCreatedFile writes RecordCreated's row through a read-write connection of its own that
// does not migrate, so the backup command never changes the schema of a database it only copied.
// The database is in WAL mode, so this is safe beside a running server. It returns
// ErrSchemaBehind, writing nothing, when the schema is not this binary's latest.
func RecordCreatedFile(ctx context.Context, dataDir string, at time.Time, actor audit.Actor, res Result) error {
	conn, err := openFile(filepath.Join(dataDir, db.FileName), "rw")
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	have, err := db.SchemaVersion(ctx, conn)
	if err != nil {
		return err
	}
	latest, err := db.LatestSchemaVersion()
	if err != nil {
		return err
	}
	if have != latest {
		return fmt.Errorf("%w: schema %d, binary %d", ErrSchemaBehind, have, latest)
	}
	return RecordCreated(ctx, sqlc.New(conn), at, actor, res.Path, res.Size, &res.MediaFiles)
}
