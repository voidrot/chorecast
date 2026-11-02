package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/voidrot/chorecast/server/internal/backup"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
)

func TestBackupAndRestoreCommands(t *testing.T) {
	src := t.TempDir()
	store, err := db.Open(t.Context(), src)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.CreateHousehold(t.Context(), sqlc.CreateHouseholdParams{Name: "Test", Timezone: "UTC", DayEnd: "00:00", CreatedAt: "2026-11-04T18:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(time.Date(2026, 11, 4, 18, 0, 0, 0, time.UTC))
	var out bytes.Buffer

	// Without --out the backup goes to the data dir's backups dir.
	if err := backupCmd(t.Context(), nil, src, clk, &out); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(src, backup.DirName, backup.FileName(clk.Now()))
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("default backup path: %v", err)
	}
	custom := filepath.Join(t.TempDir(), "mine.tar.gz")
	if err := backupCmd(t.Context(), []string{"--out", custom}, src, clk, &out); err != nil {
		t.Fatal(err)
	}
	if err := backupCmd(t.Context(), []string{"extra"}, src, clk, &out); err == nil {
		t.Error("backup with an argument: no usage error")
	}

	target := t.TempDir()
	if err := restoreCmd(t.Context(), nil, target, &out); err == nil {
		t.Error("restore without a file: no usage error")
	}
	if err := restoreCmd(t.Context(), []string{custom}, target, &out); err != nil {
		t.Fatal(err)
	}
	if err := restoreCmd(t.Context(), []string{custom}, target, &out); !errors.Is(err, backup.ErrNotEmpty) {
		t.Fatalf("second restore: %v, want ErrNotEmpty", err)
	}
	// --force works before or after the file.
	if err := restoreCmd(t.Context(), []string{custom, "--force"}, target, &out); err != nil {
		t.Fatal(err)
	}
	if err := restoreCmd(t.Context(), []string{"--force", file}, target, &out); err != nil {
		t.Fatal(err)
	}

	lock, err := db.LockDataDir(target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Unlock() }()
	if err := restoreCmd(t.Context(), []string{"--force", file}, target, &out); !errors.Is(err, db.ErrDataDirLocked) {
		t.Fatalf("restore while the server runs: %v, want ErrDataDirLocked", err)
	}
}

// While a restore's marker exists, the server, restore, backup and admin all refuse to open the
// database, and say where the previous data is.
func TestRestoreMarkerBlocksCommands(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, ".restore-1", "old")
	if err := db.WriteRestoreMarker(dir, old); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CHORECAST_DATA_DIR", dir)
	clk := clock.NewFake(time.Date(2026, 11, 4, 18, 0, 0, 0, time.UTC))
	var out bytes.Buffer
	for name, run := range map[string]func() error{
		"serve": func() error {
			lock, store, err := openServerDataDir(t.Context(), dir)
			if err == nil {
				_ = store.Close()
				_ = lock.Unlock()
			}
			return err
		},
		"restore": func() error { return restoreCmd(t.Context(), []string{"--force", "x.tar.gz"}, dir, &out) },
		"backup":  func() error { return backupCmd(t.Context(), nil, dir, clk, &out) },
		"admin":   func() error { return admin(t.Context(), []string{"rotate-media-key"}, &out) },
	} {
		err := run()
		if !errors.Is(err, db.ErrRestoreInterrupted) || !strings.Contains(err.Error(), old) {
			t.Errorf("%s: %v, want ErrRestoreInterrupted naming %s", name, err, old)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, db.FileName)); err == nil {
		t.Error("a refused command created a database")
	}
}

// Admin works beside a running server, but not during a restore.
func TestAdminAndRestoreExcludeEachOther(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CHORECAST_DATA_DIR", dir)
	var out bytes.Buffer
	server, err := db.LockDataDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := admin(t.Context(), []string{"rotate-media-key"}, &out); err != nil {
		t.Fatalf("admin beside a server: %v", err)
	}
	_ = server.Unlock()

	restoring, err := db.LockRestore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := admin(t.Context(), []string{"rotate-media-key"}, &out); !errors.Is(err, db.ErrDataDirLocked) {
		t.Errorf("admin during a restore: %v, want ErrDataDirLocked", err)
	}
	_ = restoring.Unlock()

	adminLock, err := db.BlockRestore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adminLock.Unlock() }()
	if err := restoreCmd(t.Context(), []string{"x.tar.gz"}, dir, &out); !errors.Is(err, db.ErrDataDirLocked) {
		t.Errorf("restore during an admin command: %v, want ErrDataDirLocked", err)
	}
}
