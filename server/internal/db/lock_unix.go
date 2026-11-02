//go:build unix

package db

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// DataDirLock is a lock on a file in the data dir (flock). The kernel releases it when the process
// exits, even after a crash, so a stale lock file never blocks anything.
type DataDirLock struct{ f *os.File }

// LockFile takes the lock on dataDir/name without waiting, creating the dir if needed. It returns
// ErrDataDirLocked when another holder conflicts (any holder if exclusive, an exclusive one if not).
func LockFile(dataDir, name string, exclusive bool) (*DataDirLock, error) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dataDir, name), os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // fixed name inside the configured data dir
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	how := syscall.LOCK_SH
	if exclusive {
		how = syscall.LOCK_EX
	}
	if err := syscall.Flock(int(f.Fd()), how|syscall.LOCK_NB); err != nil { //nolint:gosec // a file descriptor fits in an int
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrDataDirLocked
		}
		return nil, fmt.Errorf("lock %s: %w", name, err)
	}
	return &DataDirLock{f: f}, nil
}

// Unlock releases the lock. The lock file stays; it holds no state.
func (l *DataDirLock) Unlock() error { return l.f.Close() }
