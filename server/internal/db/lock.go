package db

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// ErrDataDirLocked means another chorecast process holds a lock this one needs: a running server
// or restore (LockDataDir), or a restore against an admin command (LockRestore, BlockRestore).
var ErrDataDirLocked = errors.New("the data dir is in use by another chorecast process")

// ErrRestoreInterrupted means a restore stopped while swapping data: the data dir may hold
// neither the old data nor the restored data. Nothing may open the database until an operator
// has sorted it out (CheckRestoreMarker).
var ErrRestoreInterrupted = errors.New("a restore was interrupted while swapping data")

const (
	// LockFileName is the lock a server or restore holds exclusively (LockDataDir).
	LockFileName = "chorecast.lock"
	// RestoreLockFileName is the lock a restore holds exclusively and admin commands share
	// (LockRestore, BlockRestore), so admin works beside a server but never during a restore.
	RestoreLockFileName = "chorecast-restore.lock"
	// RestoreMarkerName exists while a restore swaps data. It holds the path of the old data.
	RestoreMarkerName = "chorecast.restoring"
)

// LockDataDir takes the data dir's exclusive lock without waiting, creating the dir if needed.
// The server and restore hold it. It returns ErrDataDirLocked when another process holds it.
func LockDataDir(dataDir string) (*DataDirLock, error) {
	return LockFile(dataDir, LockFileName, true)
}

// LockRestore takes the restore lock exclusively: no admin command runs during a restore.
func LockRestore(dataDir string) (*DataDirLock, error) {
	return LockFile(dataDir, RestoreLockFileName, true)
}

// BlockRestore takes the restore lock shared: any number of admin commands at once, and no
// restore until they finish.
func BlockRestore(dataDir string) (*DataDirLock, error) {
	return LockFile(dataDir, RestoreLockFileName, false)
}

// WriteRestoreMarker durably records that a restore is about to move the data dir's data to
// oldData. RemoveRestoreMarker clears it once the swap is complete.
func WriteRestoreMarker(dataDir, oldData string) error {
	abs, err := filepath.Abs(oldData)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dataDir, ".restoring-*")
	if err != nil {
		return fmt.Errorf("write restore marker: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := io.WriteString(tmp, abs+"\n"); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write restore marker: %w", err)
	}
	if err := errors.Join(tmp.Sync(), tmp.Close()); err != nil {
		return fmt.Errorf("write restore marker: %w", err)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dataDir, RestoreMarkerName)); err != nil {
		return fmt.Errorf("write restore marker: %w", err)
	}
	return syncDir(dataDir)
}

// RemoveRestoreMarker clears the marker WriteRestoreMarker wrote.
func RemoveRestoreMarker(dataDir string) error {
	if err := os.Remove(filepath.Join(dataDir, RestoreMarkerName)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove restore marker: %w", err)
	}
	// The swap is complete either way; at worst the marker reappears after a power cut and asks
	// the operator to check, so a sync failure is only worth a warning.
	if err := syncDir(dataDir); err != nil {
		slog.Warn("restore marker removed but not synced to disk", "dir", dataDir, "err", err)
	}
	return nil
}

// CheckRestoreMarker returns ErrRestoreInterrupted, with what to do, if a restore stopped while
// swapping data in dataDir. The server, restore and admin call it before opening the database.
func CheckRestoreMarker(dataDir string) error {
	marker := filepath.Join(dataDir, RestoreMarkerName)
	f, err := os.Open(marker) //nolint:gosec // fixed name inside the configured data dir
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("read restore marker: %w", err)
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, 4096))
	if err != nil {
		return fmt.Errorf("read restore marker: %w", err)
	}
	return fmt.Errorf("%w: the previous database and media were moved to %q. Check %s: move the previous data back or "+
		"keep the restored data, then delete %s", ErrRestoreInterrupted, strings.TrimSpace(string(b)), dataDir, marker)
}

func syncDir(dir string) error {
	d, err := os.Open(dir) //nolint:gosec // the configured data dir
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", dir, err)
	}
	return nil
}
