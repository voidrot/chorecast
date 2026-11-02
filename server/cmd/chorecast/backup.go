package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/voidrot/chorecast/server/internal/audit"
	"github.com/voidrot/chorecast/server/internal/backup"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/db"
)

const backupUsage = `usage: chorecast backup [--out PATH]

Writes a backup of the database and media in CHORECAST_DATA_DIR, by default to
<data dir>/backups/chorecast-<time>.tar.gz. Safe to run while the server is running. The
backup holds every secret in the database (media signing key, TOTP secrets), so keep it private`

const restoreUsage = `usage: chorecast restore [--force] <file>

Restores a backup into CHORECAST_DATA_DIR. Stop the server first. Refuses a data dir that
already has data unless --force, which replaces its database and media (never its backups)`

// backupCmd runs "chorecast backup" against dataDir.
func backupCmd(ctx context.Context, args []string, dataDir string, clk clock.Clock, out io.Writer) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	path := fs.String("out", "", "archive to write")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return errors.New(backupUsage)
	}
	if *path == "" {
		*path = filepath.Join(dataDir, backup.DirName, backup.FileName(clk.Now()))
	}
	release, err := blockRestore(dataDir)
	if err != nil {
		return err
	}
	defer release()
	// With no server running, hold its lock (a server starting meanwhile is refused) and let the
	// snapshot open the database read-write, so it leaves no -wal/-shm owned by this user behind.
	var opts backup.WriteOptions
	serverLock, err := db.LockDataDir(dataDir)
	switch {
	case err == nil:
		defer func() { _ = serverLock.Unlock() }()
		opts.ServerStopped = true
	case !errors.Is(err, db.ErrDataDirLocked):
		return err
	}
	res, err := backup.Write(ctx, dataDir, *path, clk, opts)
	if err != nil {
		return err
	}
	// After the snapshot, so the row is not in the archive. The archive is good either way, so a
	// failure here is a warning, not a failed command (cron would report a good backup as failed).
	if err := backup.RecordCreatedFile(ctx, dataDir, res.CreatedAt, audit.CLI, res); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "warning: wrote %s but did not record it in the audit log: %v\n", res.Path, err)
	}
	_, err = fmt.Fprintf(out, "Wrote %s (%d bytes): schema %d, %d tables, %d media files (%d bytes). The snapshot took %s.\n",
		res.Path, res.Size, res.SchemaVersion, len(res.Tables), res.MediaFiles, res.MediaBytes, res.Snapshot)
	return err
}

// blockRestore is what the commands that work beside a running server (admin, backup) take
// first: the restore lock shared, so no restore runs meanwhile, and a check that no restore was
// interrupted. Call the returned func to release the lock.
func blockRestore(dataDir string) (func(), error) {
	lock, err := db.BlockRestore(dataDir)
	if err != nil {
		return nil, fmt.Errorf("%w: a restore is running in %s; wait for it", err, dataDir)
	}
	if err := db.CheckRestoreMarker(dataDir); err != nil {
		_ = lock.Unlock()
		return nil, err
	}
	return func() { _ = lock.Unlock() }, nil
}

// restoreCmd runs "chorecast restore" into dataDir. --force may come before or after the file.
func restoreCmd(ctx context.Context, args []string, dataDir string, clk clock.Clock, out io.Writer) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	force := fs.Bool("force", false, "replace the data dir's existing data")
	if err := fs.Parse(args); err != nil || fs.NArg() < 1 {
		return errors.New(restoreUsage)
	}
	file := fs.Arg(0)
	if err := fs.Parse(fs.Args()[1:]); err != nil || fs.NArg() != 0 {
		return errors.New(restoreUsage)
	}
	m, err := backup.Restore(ctx, file, dataDir, backup.RestoreOptions{Force: *force, Clock: clk})
	if err != nil {
		return err
	}
	// %q: the version comes from the archive and may hold control characters.
	_, err = fmt.Fprintf(out, "Restored %s into %s: backup from %s (chorecast %q, schema %d), %d media files. Start the server to use it.\n",
		file, dataDir, m.CreatedAt.Format("2006-01-02 15:04 MST"), m.AppVersion, m.SchemaVersion, m.MediaFiles)
	return err
}
