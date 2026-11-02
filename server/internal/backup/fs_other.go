//go:build !unix

package backup

import "os"

// freeBytes is unknown outside unix.
func freeBytes(string) (int64, bool) { return 0, false }

// otherDevice can't tell outside unix.
func otherDevice(_, _ os.FileInfo) bool { return false }
