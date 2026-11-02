package backup_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/voidrot/chorecast/server/internal/backup"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/db"
)

// householdName reads the household name from the database in dir.
func householdName(t *testing.T, dir string) string {
	t.Helper()
	store, err := db.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	hh, err := store.GetHousehold(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return hh.Name
}

// changedDataDir is a data dir whose household is renamed after a backup was taken, plus that
// backup: restoring it must bring the old name back, and a failed restore must keep the new one.
func changedDataDir(t *testing.T) (dataDir, archive string) {
	t.Helper()
	clk := clock.NewFake(start)
	dir, store := newDataDir(t, clk)
	archive = writeBackup(t, dir, clk)
	if _, err := store.SQL.ExecContext(t.Context(), `UPDATE household SET name = 'Changed'`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	return dir, archive
}

func stagingDirs(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".restore-") {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}

// A failed move-in is undone: the previous data is back, and no marker or staging dir is left.
// The media dir moves in last, so the restored database is already in place when it fails and
// the undo has to move it back out.
func TestRestoreSwapFailureIsUndone(t *testing.T) {
	dir, archive := changedDataDir(t)
	media := mediaHashes(t, dir)
	backup.SetRename(t, func(from, to string) error {
		if to == filepath.Join(dir, "media") && strings.Contains(from, string(filepath.Separator)+"new"+string(filepath.Separator)) {
			return &os.LinkError{Op: "rename", Old: from, New: to, Err: syscall.EIO}
		}
		return os.Rename(from, to)
	})
	_, err := backup.Restore(t.Context(), archive, dir, backup.RestoreOptions{Clock: clock.NewFake(start), Force: true})
	if !errors.Is(err, syscall.EIO) {
		t.Fatalf("restore: %v, want the move-in failure", err)
	}
	if got := householdName(t, dir); got != "Changed" {
		t.Errorf("household after an undone restore: %q, want the previous data", got)
	}
	if got := mediaHashes(t, dir); !equalCounts(got, media) {
		t.Error("an undone restore changed the media")
	}
	if s := stagingDirs(t, dir); len(s) != 0 {
		t.Errorf("staging dirs left: %v", s)
	}
	if err := db.CheckRestoreMarker(dir); err != nil {
		t.Errorf("marker left after a clean undo: %v", err)
	}
}

// When undoing fails too, the staging dir with the previous data and the marker are kept, the
// error names where the data is, and a later restore refuses until an operator sorts it out.
func TestRestoreFailedUndoKeepsStage(t *testing.T) {
	dir, archive := changedDataDir(t)
	sep := string(filepath.Separator)
	backup.SetRename(t, func(from, to string) error {
		intoDB := to == filepath.Join(dir, db.FileName)
		if intoDB && (strings.Contains(from, sep+"new"+sep) || strings.Contains(from, sep+"old"+sep)) {
			return &os.LinkError{Op: "rename", Old: from, New: to, Err: syscall.EIO}
		}
		return os.Rename(from, to)
	})
	_, err := backup.Restore(t.Context(), archive, dir, backup.RestoreOptions{Clock: clock.NewFake(start), Force: true})
	stages := stagingDirs(t, dir)
	if len(stages) != 1 {
		t.Fatalf("restore: %v; staging dirs %v, want the one holding the previous data", err, stages)
	}
	old := filepath.Join(stages[0], "old")
	if err == nil || !strings.Contains(err.Error(), old) {
		t.Fatalf("restore error %v doesn't name %s", err, old)
	}
	if _, err := os.Stat(filepath.Join(old, db.FileName)); err != nil {
		t.Errorf("the previous database isn't in the kept staging dir: %v", err)
	}
	if err := db.CheckRestoreMarker(dir); !errors.Is(err, db.ErrRestoreInterrupted) || !strings.Contains(err.Error(), old) {
		t.Errorf("marker check: %v, want ErrRestoreInterrupted naming %s", err, old)
	}
	if _, err := backup.Restore(t.Context(), archive, dir, backup.RestoreOptions{Clock: clock.NewFake(start), Force: true}); !errors.Is(err, db.ErrRestoreInterrupted) {
		t.Errorf("restore after an interrupted one: %v, want ErrRestoreInterrupted", err)
	}
	if _, err := os.Stat(old); err != nil {
		t.Errorf("a refused restore removed the previous data: %v", err)
	}
}

func TestRestoreRefusesWhileMarked(t *testing.T) {
	dir, archive := changedDataDir(t)
	if err := db.WriteRestoreMarker(dir, filepath.Join(dir, ".restore-1", "old")); err != nil {
		t.Fatal(err)
	}
	if _, err := backup.Restore(t.Context(), archive, dir, backup.RestoreOptions{Clock: clock.NewFake(start), Force: true}); !errors.Is(err, db.ErrRestoreInterrupted) {
		t.Fatalf("restore: %v, want ErrRestoreInterrupted", err)
	}
	if got := householdName(t, dir); got != "Changed" {
		t.Errorf("household %q: a refused restore changed the data", got)
	}
}

func TestRestoreRefusesSymlinkedMedia(t *testing.T) {
	dir, archive := changedDataDir(t)
	elsewhere := filepath.Join(t.TempDir(), "media")
	if err := os.Rename(filepath.Join(dir, "media"), elsewhere); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(dir, "media")); err != nil {
		t.Fatal(err)
	}
	_, err := backup.Restore(t.Context(), archive, dir, backup.RestoreOptions{Clock: clock.NewFake(start), Force: true})
	if !errors.Is(err, backup.ErrLinkedData) {
		t.Fatalf("restore: %v, want ErrLinkedData", err)
	}
	if info, err := os.Lstat(filepath.Join(dir, "media")); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the media symlink was replaced (%v)", err)
	}
}

func TestRestoreBudget(t *testing.T) {
	clk := clock.NewFake(start)
	src, _ := newDataDir(t, clk)
	archive := writeBackup(t, src, clk)

	t.Run("total", func(t *testing.T) {
		target := t.TempDir()
		if _, err := backup.Restore(t.Context(), archive, target, backup.RestoreOptions{Clock: clock.NewFake(start), MaxBytes: 1000}); !errors.Is(err, backup.ErrTooLarge) {
			t.Fatalf("restore over budget: %v, want ErrTooLarge", err)
		}
		assertUntouched(t, target)
	})

	t.Run("sparse entry", func(t *testing.T) {
		out := sparseArchive(t)
		// The tar reader really does expand it to 2 GiB.
		if size := firstEntrySize(t, out); size != 2<<30 {
			t.Fatalf("crafted sparse entry has size %d", size)
		}
		target := t.TempDir()
		// Within a 1 GiB budget, so only the sparse check (not the budget) can refuse it as invalid.
		_, err := backup.Restore(t.Context(), out, target, backup.RestoreOptions{Clock: clock.NewFake(start), MaxBytes: 1 << 30})
		if !errors.Is(err, backup.ErrInvalidArchive) {
			t.Fatalf("restore sparse: %v, want ErrInvalidArchive", err)
		}
		assertUntouched(t, target)
	})

	t.Run("cancelled", func(t *testing.T) {
		target := t.TempDir()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := backup.Restore(ctx, archive, target, backup.RestoreOptions{Clock: clock.NewFake(start)}); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled restore: %v", err)
		}
		assertUntouched(t, target)
	})
}

// sparseArchive is a few hundred bytes of PAX tar whose chorecast.db is a 2 GiB GNU sparse file.
// tar.Writer drops GNU.sparse records, so the PAX header block is patched in by hand.
func sparseArchive(t *testing.T) string {
	t.Helper()
	var records bytes.Buffer
	for _, kv := range [][2]string{
		{"GNU.sparse.major", "1"}, {"GNU.sparse.minor", "0"},
		{"GNU.sparse.name", db.FileName}, {"GNU.sparse.realsize", "2147483648"},
	} {
		rec := " " + kv[0] + "=" + kv[1] + "\n"
		n := len(rec) + 1
		for len(fmt.Sprint(n))+len(rec) != n {
			n++
		}
		fmt.Fprintf(&records, "%d%s", n, rec)
	}
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	write := func(name string, body []byte) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o600, Size: int64(len(body)), Format: tar.FormatUSTAR}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	write("PaxHeaders/"+db.FileName, records.Bytes())
	sparseMap := make([]byte, 512) // one data region: offset 2 GiB, length 0
	copy(sparseMap, "1\n2147483648\n0\n")
	write(db.FileName, sparseMap)
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	b := raw.Bytes()
	// Turn the first entry into a PAX extended header ('x') and fix its checksum.
	b[156] = 'x'
	copy(b[148:156], "        ")
	sum := 0
	for _, c := range b[:512] {
		sum += int(c)
	}
	copy(b[148:156], fmt.Sprintf("%06o\x00 ", sum))
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write(b); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return writeFile(t, gz.Bytes())
}

func firstEntrySize(t *testing.T, archive string) int64 {
	t.Helper()
	f, err := os.Open(archive) //nolint:gosec // test file
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	h, err := tar.NewReader(zr).Next()
	if err != nil {
		t.Fatal(err)
	}
	return h.Size
}

// Another writer creating the same file between the existence check and publishing wins: Write
// returns ErrExists and leaves that file alone.
func TestWritePublishRace(t *testing.T) {
	clk := clock.NewFake(start)
	src, _ := newDataDir(t, clk)
	out := filepath.Join(t.TempDir(), "race.tar.gz")
	backup.SetLink(t, func(from, to string) error {
		if err := os.WriteFile(to, []byte("theirs"), 0o600); err != nil {
			return err
		}
		return os.Link(from, to)
	})
	if _, err := backup.Write(t.Context(), src, out, clk, backup.WriteOptions{}); !errors.Is(err, backup.ErrExists) {
		t.Fatalf("write: %v, want ErrExists", err)
	}
	if b, _ := os.ReadFile(out); string(b) != "theirs" { //nolint:gosec // test file
		t.Errorf("the other writer's file now holds %d bytes", len(b))
	}
	entries, _ := os.ReadDir(filepath.Dir(out))
	if len(entries) != 1 {
		t.Errorf("work files left next to the output: %d entries", len(entries))
	}
}

// Work dirs a crashed backup left behind are removed once they are an hour old, unless a running
// backup still holds their lock.
func TestWriteRemovesStaleWorkDirs(t *testing.T) {
	clk := clock.NewFake(start)
	src, _ := newDataDir(t, clk)
	dir := t.TempDir()
	stale := filepath.Join(dir, ".chorecast-backup-111")
	fresh := filepath.Join(dir, ".chorecast-backup-222")
	running := filepath.Join(dir, ".chorecast-backup-333") // old, but its backup still holds the lock
	lock, err := db.LockFile(running, ".lock", true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Unlock() }()
	for p, age := range map[string]time.Duration{stale: 2 * time.Hour, fresh: 10 * time.Minute, running: 3 * time.Hour} {
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, start.Add(-age), start.Add(-age)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := backup.Write(t.Context(), src, filepath.Join(dir, "b.tar.gz"), clk, backup.WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Error("a stale work dir is still there")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("a recent work dir (maybe a running backup) was removed: %v", err)
	}
	if _, err := os.Stat(running); err != nil {
		t.Errorf("a locked work dir (a backup running over an hour) was removed: %v", err)
	}
}
