package db

import "errors"

// ErrDataDirLocked means another process (a running server, or a restore) holds the data dir.
var ErrDataDirLocked = errors.New("the data dir is in use by another chorecast process")

// LockFileName is the lock file a server or restore holds inside the data dir (LockDataDir).
const LockFileName = "chorecast.lock"
