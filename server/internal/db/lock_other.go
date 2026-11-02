//go:build !unix

package db

import (
	"fmt"
	"os"
)

// DataDirLock is a no-op outside unix.
type DataDirLock struct{}

// lockFile only creates the data dir.
// shortcut: no lock outside unix (releases ship for Linux only), add LockFileEx if Windows is supported.
func lockFile(dataDir, _ string, _ bool) (*DataDirLock, error) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	return &DataDirLock{}, nil
}

// Unlock does nothing.
func (l *DataDirLock) Unlock() error { return nil }
