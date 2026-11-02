package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"

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
	res, err := backup.Write(ctx, dataDir, *path, clk)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "Wrote %s (%d bytes): schema %d, %d tables, %d media files (%d bytes). The snapshot took %s.\n",
		res.Path, res.Size, res.SchemaVersion, len(res.Tables), res.MediaFiles, res.MediaBytes, res.Snapshot)
	return err
}

// restoreCmd runs "chorecast restore" into dataDir. --force may come before or after the file.
func restoreCmd(ctx context.Context, args []string, dataDir string, out io.Writer) error {
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
	m, err := backup.Restore(ctx, file, dataDir, *force)
	if errors.Is(err, db.ErrDataDirLocked) {
		return fmt.Errorf("%w: stop the server using %s, then restore", err, dataDir)
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "Restored %s into %s: backup from %s (chorecast %s, schema %d), %d media files. Start the server to use it.\n",
		file, dataDir, m.CreatedAt.Format("2006-01-02 15:04 MST"), m.AppVersion, m.SchemaVersion, m.MediaFiles)
	return err
}
