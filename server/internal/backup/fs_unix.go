//go:build unix

package backup

import (
	"os"
	"syscall"
)

// freeBytes is the space available to this user on dir's filesystem; ok is false if unknown.
func freeBytes(dir string) (n int64, ok bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, false
	}
	return int64(st.Bavail) * int64(st.Bsize), true //nolint:gosec,unconvert // block counts and sizes fit in int64; field types differ by OS
}

// otherDevice reports whether a and b are on different filesystems, i.e. a is a mount point when
// b is its parent.
func otherDevice(a, b os.FileInfo) bool {
	sa, okA := a.Sys().(*syscall.Stat_t)
	sb, okB := b.Sys().(*syscall.Stat_t)
	return okA && okB && sa.Dev != sb.Dev
}
