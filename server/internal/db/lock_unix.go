//go:build unix

package db

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// DataDirLock is an exclusive lock on a data dir. The kernel releases it when the process exits,
// even after a crash, so a stale lock file never blocks anything.
type DataDirLock struct{ f *os.File }

// LockDataDir takes the data dir's exclusive lock without waiting, creating the dir if needed.
// It returns ErrDataDirLocked when another process holds it.
func LockDataDir(dataDir string) (*DataDirLock, error) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dataDir, LockFileName), os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // fixed name inside the configured data dir
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil { //nolint:gosec // a file descriptor fits in an int
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrDataDirLocked
		}
		return nil, fmt.Errorf("lock data dir: %w", err)
	}
	return &DataDirLock{f: f}, nil
}

// Unlock releases the lock. The lock file stays; it holds no state.
func (l *DataDirLock) Unlock() error { return l.f.Close() }
