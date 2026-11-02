package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/voidrot/chorecast/server/internal/audit"
	"github.com/voidrot/chorecast/server/internal/backup"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
	"github.com/voidrot/chorecast/server/internal/paging"
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
	if err := restoreCmd(t.Context(), nil, target, clk, &out); err == nil {
		t.Error("restore without a file: no usage error")
	}
	if err := restoreCmd(t.Context(), []string{custom}, target, clk, &out); err != nil {
		t.Fatal(err)
	}
	if err := restoreCmd(t.Context(), []string{custom}, target, clk, &out); !errors.Is(err, backup.ErrNotEmpty) {
		t.Fatalf("second restore: %v, want ErrNotEmpty", err)
	}
	// --force works before or after the file.
	if err := restoreCmd(t.Context(), []string{custom, "--force"}, target, clk, &out); err != nil {
		t.Fatal(err)
	}
	if err := restoreCmd(t.Context(), []string{"--force", file}, target, clk, &out); err != nil {
		t.Fatal(err)
	}

	lock, err := db.LockDataDir(target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Unlock() }()
	if err := restoreCmd(t.Context(), []string{"--force", file}, target, clk, &out); !errors.Is(err, db.ErrDataDirLocked) {
		t.Fatalf("restore while the server runs: %v, want ErrDataDirLocked", err)
	}
}

// A CLI backup of a stopped data dir leaves no -wal/-shm behind: run as another user, they would
// be that user's files, and the server couldn't write its database.
func TestBackupOfStoppedDataDirLeavesNoWAL(t *testing.T) {
	dir := t.TempDir()
	store, err := db.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateHousehold(t.Context(), sqlc.CreateHouseholdParams{Name: "Test", Timezone: "UTC", DayEnd: "00:00", CreatedAt: "2026-11-04T18:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	walFiles := func() []string {
		var found []string
		for _, n := range []string{db.FileName + "-wal", db.FileName + "-shm"} {
			if _, err := os.Stat(filepath.Join(dir, n)); err == nil {
				found = append(found, n)
			}
		}
		return found
	}
	if f := walFiles(); len(f) != 0 {
		t.Fatalf("a closed database left %v", f)
	}
	clk := clock.NewFake(time.Date(2026, 11, 4, 18, 0, 0, 0, time.UTC))
	var out bytes.Buffer
	if err := backupCmd(t.Context(), []string{"--out", filepath.Join(t.TempDir(), "b.tar.gz")}, dir, clk, &out); err != nil {
		t.Fatal(err)
	}
	if f := walFiles(); len(f) != 0 {
		t.Errorf("a backup of a stopped data dir left %v", f)
	}
	// The data dir lock was released: a server can start.
	lock, err := db.LockDataDir(dir)
	if err != nil {
		t.Fatalf("data dir lock after backup: %v", err)
	}
	_ = lock.Unlock()
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
		"restore": func() error { return restoreCmd(t.Context(), []string{"--force", "x.tar.gz"}, dir, clk, &out) },
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
	clk := clock.NewFake(time.Date(2026, 11, 4, 18, 0, 0, 0, time.UTC))
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
	if err := restoreCmd(t.Context(), []string{"x.tar.gz"}, dir, clk, &out); !errors.Is(err, db.ErrDataDirLocked) {
		t.Errorf("restore during an admin command: %v, want ErrDataDirLocked", err)
	}
}

// Each CLI backup adds one backup_created row (actor cli, file name, size, media count) to the live
// database, not to the archive it describes; a restore adds one restore_completed row to the
// restored database, after the rows that came with the archive.
func TestCLIBackupAndRestoreAreAudited(t *testing.T) {
	src := t.TempDir()
	store, err := db.Open(t.Context(), src)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	clk := clock.NewFake(time.Date(2026, 11, 4, 18, 0, 0, 0, time.UTC))
	var out bytes.Buffer
	if err := audit.Record(t.Context(), store.Queries, clk.Now(), audit.Entry{
		Actor: audit.CLI, Action: audit.ActionSettingsUpdated, TargetKind: audit.TargetHousehold,
	}); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "mine.tar.gz")
	if err := backupCmd(t.Context(), []string{"--out", archive}, src, clk, &out); err != nil {
		t.Fatal(err)
	}
	rows := auditRows(t, store)
	if len(rows) != 2 || rows[0].ActorKind != audit.ActorCLI || rows[0].Action != audit.ActionBackupCreated ||
		rows[0].Summary != "mine.tar.gz" {
		t.Fatalf("after one backup: %+v", rows)
	}
	if rows[0].Details.Bytes <= 0 || rows[0].Details.MediaFiles == nil {
		t.Errorf("details: %+v", rows[0].Details)
	}
	// A second backup, same name elsewhere, is a second row.
	if err := backupCmd(t.Context(), []string{"--out", filepath.Join(t.TempDir(), "mine.tar.gz")}, src, clk, &out); err != nil {
		t.Fatal(err)
	}
	if got := auditRows(t, store); len(got) != 3 {
		t.Fatalf("after two backups: %d rows", len(got))
	}

	// The archive holds the state before its own row: restoring it brings the settings row but no
	// backup row, and adds exactly one restore row.
	target := t.TempDir()
	if err := restoreCmd(t.Context(), []string{archive}, target, clk, &out); err != nil {
		t.Fatal(err)
	}
	restored, err := db.Open(t.Context(), target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restored.Close() })
	got := auditRows(t, restored)
	if len(got) != 2 || got[1].Action != audit.ActionSettingsUpdated || got[0].Action != audit.ActionRestoreCompleted ||
		got[0].ActorKind != audit.ActorCLI || got[0].Summary != "mine.tar.gz" ||
		got[0].Details.Forced == nil || *got[0].Details.Forced {
		t.Fatalf("restored database: %+v", got)
	}
	if got[0].Details.SchemaVer == 0 {
		t.Errorf("manifest schema version missing: %+v", got[0].Details)
	}
	// Forcing a restore over it records the flag.
	if err := restoreCmd(t.Context(), []string{"--force", archive}, target, clk, &out); err != nil {
		t.Fatal(err)
	}
	restoredAgain, err := db.Open(t.Context(), target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restoredAgain.Close() })
	got = auditRows(t, restoredAgain)
	if len(got) != 2 || got[0].Details.Forced == nil || !*got[0].Details.Forced {
		t.Fatalf("forced restore: %+v", got)
	}
}

func auditRows(t *testing.T, store *db.DB) []audit.AuditEntry {
	t.Helper()
	page, err := audit.New(store).List(t.Context(), audit.Filter{}, paging.Params{})
	if err != nil {
		t.Fatal(err)
	}
	return page.Items
}
