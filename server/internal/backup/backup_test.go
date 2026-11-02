package backup_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/voidrot/chorecast/server/internal/backup"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/config"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
	"github.com/voidrot/chorecast/server/internal/jobs"
	"github.com/voidrot/chorecast/server/internal/media"
)

var start = time.Date(2026, 11, 4, 18, 0, 0, 0, time.UTC) // Wednesday 12:00 in Chicago

// newDataDir opens a database with a household, a server key and a few jobs, plus media files
// (a photo, its thumbnail, another photo) and an upload still being written, which backups skip.
func newDataDir(t *testing.T, clk clock.Clock) (string, *db.DB) {
	t.Helper()
	dir := t.TempDir()
	store, err := db.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := t.Context()
	if err := store.CreateHousehold(ctx, sqlc.CreateHouseholdParams{
		Name: "Test", Timezone: "America/Chicago", DayEnd: "21:00", CreatedAt: db.Timestamp(clk.Now()),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertServerKey(ctx, sqlc.InsertServerKeyParams{Name: "media_url", Key: []byte("secret"), CreatedAt: db.Timestamp(clk.Now())}); err != nil {
		t.Fatal(err)
	}
	runner := jobs.NewRunner(store.Queries, clk, slog.New(slog.DiscardHandler))
	runner.Register("test.kind", func(context.Context, jobs.Job) error { return nil }, jobs.KindOptions{})
	for _, k := range []string{"a", "b", "c"} {
		if _, _, err := runner.Enqueue(ctx, "test.kind", struct{}{}, jobs.EnqueueOptions{Key: k}); err != nil {
			t.Fatal(err)
		}
	}
	files := media.NewLocal(dir, "", nil)
	for i, body := range []string{"photo one", "photo two, a bit longer"} {
		key, err := media.NewKey()
		if err != nil {
			t.Fatal(err)
		}
		if err := files.Put(ctx, key, strings.NewReader(body)); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if err := files.Put(ctx, media.ThumbKey(key), strings.NewReader("thumb")); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "media", key[0:2], key[2:4], ".upload-123"), []byte("partial"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dir, store
}

// tableCounts counts the rows of every table in the database file in dir.
func tableCounts(t *testing.T, dir string) map[string]int64 {
	t.Helper()
	conn, err := sql.Open("sqlite", "file:"+filepath.Join(dir, db.FileName)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	rows, err := conn.QueryContext(t.Context(), `SELECT name FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite\_%' ESCAPE '\'`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	_ = rows.Close()
	counts := map[string]int64{}
	for _, n := range names {
		var c int64
		if err := conn.QueryRowContext(t.Context(), `SELECT count(*) FROM "`+n+`"`).Scan(&c); err != nil {
			t.Fatal(err)
		}
		counts[n] = c
	}
	return counts
}

// mediaHashes maps each file under dir/media to its SHA-256.
func mediaHashes(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	root := filepath.Join(dir, "media")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p) //nolint:gosec // test files
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		rel, _ := filepath.Rel(root, p)
		out[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	return out
}

func writeBackup(t *testing.T, dataDir string, clk clock.Clock) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "backup.tar.gz")
	if _, err := backup.Write(t.Context(), dataDir, out, clk); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRoundTrip(t *testing.T) {
	clk := clock.NewFake(start)
	src, _ := newDataDir(t, clk)
	out := filepath.Join(src, backup.DirName, backup.FileName(clk.Now()))
	res, err := backup.Write(t.Context(), src, out, clk)
	if err != nil {
		t.Fatal(err)
	}
	if res.MediaFiles != 3 || res.MediaBytes != int64(len("photo one")+len("photo two, a bit longer")+len("thumb")) {
		t.Errorf("manifest media: %d files, %d bytes", res.MediaFiles, res.MediaBytes)
	}
	if res.Tables["household"] != 1 || res.Tables["server_keys"] != 1 || res.Tables["jobs"] != 3 {
		t.Errorf("manifest tables: %v", res.Tables)
	}
	latest, err := db.LatestSchemaVersion()
	if err != nil || res.SchemaVersion != latest || latest < 1 {
		t.Errorf("schema version %d, latest %d (%v)", res.SchemaVersion, latest, err)
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("archive mode %v, want owner-only", info.Mode().Perm())
	}
	if entries, _ := os.ReadDir(filepath.Dir(out)); len(entries) != 1 {
		t.Errorf("backups dir holds %d entries, want only the archive", len(entries))
	}

	target := filepath.Join(t.TempDir(), "data")
	m, err := backup.Restore(t.Context(), out, target, backup.RestoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if m.MediaFiles != 3 {
		t.Errorf("restored manifest: %+v", m)
	}
	entries, _ := os.ReadDir(target)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".restore-") || e.Name() == db.RestoreMarkerName {
			t.Errorf("a successful restore left %s behind", e.Name())
		}
	}
	if got, want := tableCounts(t, target), tableCounts(t, src); !equalCounts(got, want) {
		t.Errorf("row counts after restore:\n got  %v\n want %v", got, want)
	}
	want := mediaHashes(t, src)
	for rel := range want {
		if strings.Contains(rel, ".upload-") {
			delete(want, rel) // in-progress uploads are not backed up
		}
	}
	if got := mediaHashes(t, target); !equalCounts(got, want) {
		t.Errorf("media after restore:\n got  %v\n want %v", got, want)
	}
	// The restored data dir opens as a working database.
	store, err := db.Open(t.Context(), target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if hh, err := store.GetHousehold(t.Context()); err != nil || hh.Name != "Test" {
		t.Errorf("restored household %+v, %v", hh, err)
	}
}

func equalCounts[V comparable](a, b map[string]V) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

func TestWriteRefusesExistingOutput(t *testing.T) {
	clk := clock.NewFake(start)
	src, _ := newDataDir(t, clk)
	out := writeBackup(t, src, clk)
	before, _ := os.ReadFile(out) //nolint:gosec // test file
	if _, err := backup.Write(t.Context(), src, out, clk); !errors.Is(err, backup.ErrExists) {
		t.Fatalf("second write: %v, want ErrExists", err)
	}
	if after, _ := os.ReadFile(out); !bytes.Equal(before, after) { //nolint:gosec // test file
		t.Error("the existing archive changed")
	}
}

func TestPruneKeepsNewest(t *testing.T) {
	dir := t.TempDir()
	var names []string
	for i := range 9 {
		n := backup.FileName(start.AddDate(0, 0, i))
		names = append(names, n)
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "my-copy.tar.gz"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	n, err := backup.Prune(dir, 7)
	if err != nil || n != 2 {
		t.Fatalf("Prune = %d, %v; want 2 deleted", n, err)
	}
	for i, name := range names {
		_, err := os.Stat(filepath.Join(dir, name))
		if kept := err == nil; kept != (i >= 2) {
			t.Errorf("%s kept = %v", name, kept)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "my-copy.tar.gz")); err != nil {
		t.Error("an unrelated file was pruned")
	}
}

// The scheduled backup runs at the household day end + 1 h (21:00 Chicago -> 22:00 CST = 04:00
// UTC), writes chorecast-<occurrence>.tar.gz and keeps the newest 7.
func TestScheduledBackup(t *testing.T) {
	clk := clock.NewFake(start)
	dataDir, store := newDataDir(t, clk)
	log := slog.New(slog.DiscardHandler)
	runner := jobs.NewRunner(store.Queries, clk, log)
	sched := jobs.NewScheduler(store.Queries, runner, clk, log)
	svc := backup.NewScheduler(store, clk, dataDir, config.BackupConfig{Keep: 7, AfterDayEnd: time.Hour})
	svc.Register(runner, sched, log)
	backups := filepath.Join(dataDir, backup.DirName)
	step := func(to time.Time) []string {
		t.Helper()
		clk.Set(to)
		if _, err := sched.Tick(t.Context()); err != nil {
			t.Fatal(err)
		}
		if _, err := runner.RunDue(t.Context()); err != nil {
			t.Fatal(err)
		}
		entries, _ := os.ReadDir(backups)
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		return names
	}
	firstAt := time.Date(2026, 11, 5, 4, 0, 0, 0, time.UTC)
	step(start) // the first tick starts the schedule
	if got := step(firstAt.Add(-time.Second)); len(got) != 0 {
		t.Fatalf("before day end + 1 h: %v", got)
	}
	if got := step(firstAt); len(got) != 1 || got[0] != backup.FileName(firstAt) {
		t.Fatalf("at day end + 1 h: %v, want %s", got, backup.FileName(firstAt))
	}
	// A retry of the same occurrence keeps the backup it already wrote, byte for byte, even though
	// the data and the clock have moved on.
	first := filepath.Join(backups, backup.FileName(firstAt))
	before, err := os.ReadFile(first) //nolint:gosec // test file
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SQL.ExecContext(t.Context(), `UPDATE household SET name = 'Changed'`); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Minute)
	if err := svc.Run(t.Context(), firstAt, log); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(first); !bytes.Equal(before, after) { //nolint:gosec // test file
		t.Error("a retry rewrote the existing backup")
	}
	// Ten more days, one tick each: still only the newest seven.
	var got []string
	for i := 1; i <= 10; i++ {
		got = step(firstAt.AddDate(0, 0, i))
	}
	if len(got) != 7 || got[6] != backup.FileName(firstAt.AddDate(0, 0, 10)) {
		t.Fatalf("after ten more days: %v", got)
	}
	// An occurrence caught up long after it was due is skipped.
	if err := svc.Run(t.Context(), firstAt.AddDate(0, 0, -3), log); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(backups, backup.FileName(firstAt.AddDate(0, 0, -3)))); err == nil {
		t.Error("a stale occurrence wrote a backup")
	}
}

func TestRestoreRefusesWhileServerHoldsLock(t *testing.T) {
	clk := clock.NewFake(start)
	src, _ := newDataDir(t, clk)
	out := writeBackup(t, src, clk)
	target := t.TempDir()
	lock, err := db.LockDataDir(target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backup.Restore(t.Context(), out, target, backup.RestoreOptions{Force: true}); !errors.Is(err, db.ErrDataDirLocked) {
		t.Fatalf("restore while locked: %v, want ErrDataDirLocked", err)
	}
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}
	if _, err := backup.Restore(t.Context(), out, target, backup.RestoreOptions{}); err != nil {
		t.Fatalf("restore after unlock: %v", err)
	}
}

func TestRestoreRefusesDataDirWithData(t *testing.T) {
	clk := clock.NewFake(start)
	src, srcStore := newDataDir(t, clk)
	out := filepath.Join(src, backup.DirName, backup.FileName(clk.Now()))
	if _, err := backup.Write(t.Context(), src, out, clk); err != nil {
		t.Fatal(err)
	}
	if _, err := srcStore.SQL.ExecContext(t.Context(), `UPDATE household SET name = 'Changed'`); err != nil {
		t.Fatal(err)
	}
	if err := srcStore.Close(); err != nil {
		t.Fatal(err)
	}
	before := mediaHashes(t, src)
	if _, err := backup.Restore(t.Context(), out, src, backup.RestoreOptions{}); !errors.Is(err, backup.ErrNotEmpty) {
		t.Fatalf("restore over data: %v, want ErrNotEmpty", err)
	}
	if got := mediaHashes(t, src); !equalCounts(got, before) {
		t.Error("a refused restore changed the media")
	}
	if _, err := backup.Restore(t.Context(), out, src, backup.RestoreOptions{Force: true}); err != nil {
		t.Fatalf("forced restore: %v", err)
	}
	store, err := db.Open(t.Context(), src)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if hh, err := store.GetHousehold(t.Context()); err != nil || hh.Name != "Test" {
		t.Errorf("after a forced restore the household is %q (%v), want the backup's", hh.Name, err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("the restored backup in the data dir's backups dir is gone: %v", err)
	}
}

// A backup taken by a newer binary (a schema version this one doesn't have) is refused.
func TestRestoreRefusesNewerSchema(t *testing.T) {
	clk := clock.NewFake(start)
	src, store := newDataDir(t, clk)
	if _, err := store.SQL.ExecContext(t.Context(), `INSERT INTO goose_db_version (version_id, is_applied) VALUES (999, 1)`); err != nil {
		t.Fatal(err)
	}
	out := writeBackup(t, src, clk)
	target := t.TempDir()
	if _, err := backup.Restore(t.Context(), out, target, backup.RestoreOptions{}); !errors.Is(err, backup.ErrNewerSchema) {
		t.Fatalf("restore newer schema: %v, want ErrNewerSchema", err)
	}
	assertUntouched(t, target)

	// The manifest alone claiming a newer schema is refused too.
	plain, _ := newDataDir(t, clk)
	out = rewrite(t, writeBackup(t, plain, clk), func(name string, body []byte) []byte {
		if name != "manifest.json" {
			return body
		}
		return editManifest(t, body, func(m *backup.Manifest) { m.SchemaVersion = 999 })
	})
	if _, err := backup.Restore(t.Context(), out, target, backup.RestoreOptions{}); !errors.Is(err, backup.ErrNewerSchema) {
		t.Fatalf("restore with a newer manifest: %v, want ErrNewerSchema", err)
	}
}

// A backup from an older schema is migrated to this binary's after it is restored.
func TestRestoreMigratesOlderSchema(t *testing.T) {
	work := t.TempDir()
	conn, err := sql.Open("sqlite", "file:"+filepath.Join(work, db.FileName))
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE goose_db_version (id INTEGER PRIMARY KEY AUTOINCREMENT, version_id INTEGER NOT NULL, is_applied INTEGER NOT NULL, tstamp TIMESTAMP DEFAULT (datetime('now')))`,
		`INSERT INTO goose_db_version (version_id, is_applied) VALUES (0, 1)`,
	} {
		if _, err := conn.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	_ = conn.Close()
	dbBytes, err := os.ReadFile(filepath.Join(work, db.FileName)) //nolint:gosec // test file
	if err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.Marshal(backup.Manifest{Format: backup.Format, SchemaVersion: 0, Tables: map[string]int64{"goose_db_version": 1}})
	out := buildArchive(t, []entry{
		{hdr: tar.Header{Name: db.FileName, Typeflag: tar.TypeReg}, body: dbBytes},
		{hdr: tar.Header{Name: "manifest.json", Typeflag: tar.TypeReg}, body: manifest},
	})
	target := t.TempDir()
	if _, err := backup.Restore(t.Context(), out, target, backup.RestoreOptions{}); err != nil {
		t.Fatal(err)
	}
	conn, err = sql.Open("sqlite", "file:"+filepath.Join(target, db.FileName)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	v, err := db.SchemaVersion(t.Context(), conn)
	latest, _ := db.LatestSchemaVersion()
	if err != nil || v != latest {
		t.Fatalf("restored schema %d (%v), want %d", v, err, latest)
	}
	if counts := tableCounts(t, target); counts["household"] != 0 {
		t.Errorf("migrated tables: %v", counts)
	}
	if _, ok := tableCounts(t, target)["household"]; !ok {
		t.Error("the migration didn't create the schema")
	}
}

func TestRestoreRefusesUnsafeEntries(t *testing.T) {
	clk := clock.NewFake(start)
	src, _ := newDataDir(t, clk)
	good := readEntries(t, writeBackup(t, src, clk))
	key, _ := media.NewKey()
	for name, bad := range map[string]tar.Header{
		"parent dir":       {Name: "../evil", Typeflag: tar.TypeReg},
		"nested parent":    {Name: "media/../../evil", Typeflag: tar.TypeReg},
		"absolute":         {Name: "/tmp/evil", Typeflag: tar.TypeReg},
		"symlink":          {Name: "media/" + key[0:2] + "/" + key[2:4] + "/" + key, Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
		"hardlink":         {Name: "media/" + key[0:2] + "/" + key[2:4] + "/" + key, Typeflag: tar.TypeLink, Linkname: "../../evil"},
		"symlink db":       {Name: db.FileName, Typeflag: tar.TypeSymlink, Linkname: "../evil"},
		"directory":        {Name: "media/", Typeflag: tar.TypeDir},
		"unknown file":     {Name: "evil", Typeflag: tar.TypeReg},
		"wrong media path": {Name: "media/00/00/" + key, Typeflag: tar.TypeReg},
	} {
		t.Run(name, func(t *testing.T) {
			// The bad entry comes first, so nothing valid has been written when it's refused.
			entries := append([]entry{{hdr: bad, body: []byte("evil")}}, good...)
			out := buildArchive(t, entries)
			parent := t.TempDir()
			target := filepath.Join(parent, "data")
			if _, err := backup.Restore(t.Context(), out, target, backup.RestoreOptions{}); !errors.Is(err, backup.ErrInvalidArchive) {
				t.Fatalf("restore: %v, want ErrInvalidArchive", err)
			}
			for _, p := range []string{filepath.Join(parent, "evil"), "/tmp/evil"} {
				if _, err := os.Lstat(p); err == nil {
					t.Errorf("%s was written", p)
				}
			}
			assertUntouched(t, target)
		})
	}

	t.Run("duplicate entry", func(t *testing.T) {
		out := buildArchive(t, append(good, good[0]))
		target := t.TempDir()
		if _, err := backup.Restore(t.Context(), out, target, backup.RestoreOptions{}); !errors.Is(err, backup.ErrInvalidArchive) {
			t.Fatalf("restore: %v, want ErrInvalidArchive", err)
		}
	})
}

func TestRestoreRefusesCorruptArchive(t *testing.T) {
	clk := clock.NewFake(start)
	src, _ := newDataDir(t, clk)
	out := writeBackup(t, src, clk)
	raw, err := os.ReadFile(out) //nolint:gosec // test file
	if err != nil {
		t.Fatal(err)
	}
	flipped := bytes.Clone(raw)
	flipped[len(flipped)/2] ^= 0xff
	cases := map[string]string{
		"truncated":   writeFile(t, raw[:len(raw)/2]),
		"flipped":     writeFile(t, flipped),
		"not gzip":    writeFile(t, []byte("hello")),
		"empty":       writeFile(t, nil),
		"no manifest": rewrite(t, out, func(name string, body []byte) []byte { return skipIf(name == "manifest.json", body) }),
		"no database": rewrite(t, out, func(name string, body []byte) []byte { return skipIf(name == db.FileName, body) }),
		"row counts": rewrite(t, out, func(name string, body []byte) []byte {
			if name != "manifest.json" {
				return body
			}
			return editManifest(t, body, func(m *backup.Manifest) { m.Tables["household"] = 2 })
		}),
		"media totals": rewrite(t, out, func(name string, body []byte) []byte {
			if name != "manifest.json" {
				return body
			}
			return editManifest(t, body, func(m *backup.Manifest) { m.MediaFiles++ })
		}),
		"damaged database": rewrite(t, out, func(name string, body []byte) []byte {
			if name != db.FileName {
				return body
			}
			return bytes.Repeat([]byte{0x5a}, len(body))
		}),
	}
	for name, archive := range cases {
		t.Run(name, func(t *testing.T) {
			target := t.TempDir()
			if _, err := backup.Restore(t.Context(), archive, target, backup.RestoreOptions{}); !errors.Is(err, backup.ErrInvalidArchive) {
				t.Fatalf("restore: %v, want ErrInvalidArchive", err)
			}
			assertUntouched(t, target)
		})
	}
}

// assertUntouched checks a refused restore left no data (or staging dirs) in dir.
func assertUntouched(t *testing.T, dir string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != db.LockFileName && e.Name() != db.RestoreLockFileName {
			t.Errorf("a refused restore left %s in the data dir", e.Name())
		}
	}
}

type entry struct {
	hdr  tar.Header
	body []byte
}

func buildArchive(t *testing.T, entries []entry) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := e.hdr
		h.Mode = 0o600
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			if _, err := tw.Write(e.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := errors.Join(tw.Close(), gz.Close()); err != nil {
		t.Fatal(err)
	}
	return writeFile(t, buf.Bytes())
}

func readEntries(t *testing.T, archive string) []entry {
	t.Helper()
	f, err := os.Open(archive) //nolint:gosec // test file
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var out []entry
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, entry{hdr: *h, body: body})
	}
}

// rewrite rebuilds archive with each entry's body passed through edit; a nil result drops it.
func rewrite(t *testing.T, archive string, edit func(name string, body []byte) []byte) string {
	t.Helper()
	var kept []entry
	for _, e := range readEntries(t, archive) {
		if e.body = edit(e.hdr.Name, e.body); e.body != nil {
			kept = append(kept, e)
		}
	}
	return buildArchive(t, kept)
}

func skipIf(skip bool, body []byte) []byte {
	if skip {
		return nil
	}
	return body
}

func editManifest(t *testing.T, body []byte, edit func(*backup.Manifest)) []byte {
	t.Helper()
	var m backup.Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	edit(&m)
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func writeFile(t *testing.T, b []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "archive.tar.gz")
	if err := os.WriteFile(p, b, 0o600); err != nil { //nolint:gosec // a test temp file
		t.Fatal(err)
	}
	return p
}
