// Package backup writes and restores Chorecast backups: a tar.gz holding a VACUUM INTO snapshot
// of the database, the local media files and a manifest (docs/features/household.md, Backups &
// export). Archives contain every secret in the database, such as the media signing key and TOTP
// secrets, so they are written readable by the owner only.
package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // the database driver

	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/media"
	"github.com/voidrot/chorecast/server/internal/version"
)

const (
	// DirName is the directory inside the data dir that scheduled backups go to.
	DirName = "backups"
	// Format is the archive layout version written to the manifest.
	Format = 1

	manifestName = "manifest.json"
	mediaDirName = "media"
	fileStamp    = "20060102T150405Z"
	workPrefix   = ".chorecast-backup-"
	workLockName = ".lock"
)

// ErrExists means the output file already exists; Write never replaces one.
var ErrExists = errors.New("backup: output file already exists")

// Manifest describes an archive. It is the archive's last entry, written after the media files,
// so its media totals are those of the files actually archived.
type Manifest struct {
	Format        int              `json:"format"`
	AppVersion    string           `json:"appVersion"`
	Commit        string           `json:"commit"`
	CreatedAt     time.Time        `json:"createdAt"`
	SchemaVersion int64            `json:"schemaVersion"` // goose version of the snapshot
	Tables        map[string]int64 `json:"tables"`        // row count per table in the snapshot
	MediaFiles    int64            `json:"mediaFiles"`    // media files archived
	MediaBytes    int64            `json:"mediaBytes"`    // their total size
}

// Result is what Write produced.
type Result struct {
	Manifest
	Path     string
	Size     int64         // archive size in bytes
	Snapshot time.Duration // how long VACUUM INTO took
}

// FileName is the name of a backup taken at t: chorecast-20261101T010000Z.tar.gz. Names sort in
// time order, which Prune relies on.
func FileName(t time.Time) string { return "chorecast-" + t.UTC().Format(fileStamp) + ".tar.gz" }

var fileNamePattern = regexp.MustCompile(`^chorecast-\d{8}T\d{6}Z\.tar\.gz$`)

// WriteOptions tune Write.
type WriteOptions struct {
	// ServerStopped means the caller holds the data dir lock (db.LockDataDir), so no server has
	// the database open. The snapshot then opens it read-write: closing that last connection
	// checkpoints and removes the -wal and -shm files, which a read-only connection would leave
	// behind owned by whoever ran the backup. With a server running they are the server's files,
	// and a read-only connection is used.
	ServerStopped bool
}

// Write archives the database and media in dataDir to out. It writes a temporary file next to out
// and hard-links it into place, so out is either absent or a complete archive. It returns
// ErrExists if out already exists, also when another writer creates it first.
//
// The snapshot is taken on a connection of its own, so the server's connection is never blocked:
// in WAL mode VACUUM INTO is a reader, and writers carry on while it runs.
func Write(ctx context.Context, dataDir, out string, clk clock.Clock, opts WriteOptions) (Result, error) {
	if _, err := os.Stat(filepath.Join(dataDir, db.FileName)); err != nil {
		return Result{}, fmt.Errorf("backup: no database in %s: %w", dataDir, err)
	}
	if _, err := os.Lstat(out); err == nil {
		return Result{}, fmt.Errorf("%w: %s", ErrExists, out)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return Result{}, err
	}
	outDir := filepath.Dir(out)
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return Result{}, fmt.Errorf("backup: create %s: %w", outDir, err)
	}
	removeStaleWork(outDir, clk.Now())
	// Everything temporary lives in one directory next to out (not /tmp, which is often RAM on a
	// Pi), removed whatever happens.
	work, err := os.MkdirTemp(outDir, workPrefix+"*")
	if err != nil {
		return Result{}, fmt.Errorf("backup: create work dir: %w", err)
	}
	// Held while the backup runs, so another backup's stale-dir cleanup leaves this dir alone
	// however long it takes.
	workLock, err := db.LockFile(work, workLockName, true)
	if err != nil {
		_ = os.RemoveAll(work)
		return Result{}, fmt.Errorf("backup: lock work dir: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(work)
		_ = workLock.Unlock()
	}()

	res := Result{Path: out}
	res.Manifest = Manifest{Format: Format, AppVersion: version.Version, Commit: version.Commit, CreatedAt: clk.Now().UTC()}
	snap := filepath.Join(work, db.FileName)
	mode := "ro"
	if opts.ServerStopped {
		mode = "rw"
	}
	start := clk.Now()
	if err := vacuumInto(ctx, filepath.Join(dataDir, db.FileName), snap, mode); err != nil {
		return Result{}, err
	}
	res.Snapshot = clk.Now().Sub(start)
	if err := describe(ctx, snap, &res.Manifest); err != nil {
		return Result{}, err
	}

	tmp, err := os.CreateTemp(work, "archive-*") // 0600: the archive holds secrets
	if err != nil {
		return Result{}, fmt.Errorf("backup: create archive: %w", err)
	}
	defer func() { _ = tmp.Close() }()
	if err := writeArchive(ctx, tmp, snap, filepath.Join(dataDir, mediaDirName), &res.Manifest); err != nil {
		return Result{}, err
	}
	if err := tmp.Sync(); err != nil {
		return Result{}, fmt.Errorf("backup: sync archive: %w", err)
	}
	info, err := tmp.Stat()
	if err != nil {
		return Result{}, err
	}
	res.Size = info.Size()
	if err := tmp.Close(); err != nil {
		return Result{}, fmt.Errorf("backup: close archive: %w", err)
	}
	if err := publish(tmp.Name(), out); err != nil {
		return Result{}, err
	}
	syncDir(outDir)
	return res, nil
}

// link and rename are os.Link and os.Rename; tests replace them to inject races and failures.
var (
	link   = os.Link
	rename = os.Rename
)

// publish puts the finished archive tmp at out without ever replacing an existing file: a hard
// link fails if out exists, where a rename would overwrite it.
func publish(tmp, out string) error {
	err := link(tmp, out)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, fs.ErrExist):
		return fmt.Errorf("%w: %s", ErrExists, out)
	case errors.Is(err, errors.ErrUnsupported) || errors.Is(err, fs.ErrPermission):
		// shortcut: filesystems without hard links (FAT, some network mounts) get a checked
		// rename, which can race with another writer of the same name; fine for a USB stick.
		if _, err := os.Lstat(out); err == nil {
			return fmt.Errorf("%w: %s", ErrExists, out)
		}
		if err := rename(tmp, out); err != nil {
			return fmt.Errorf("backup: publish archive: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("backup: publish archive: %w", err)
	}
}

// staleWorkAge is how old a backup work dir must be before Write treats it as left by a crash.
// A backup of a large media dir on a Pi takes minutes, not an hour.
const staleWorkAge = time.Hour

// removeStaleWork deletes work dirs a crashed backup left in dir (each holds a database copy).
// A dir whose lock is held belongs to a backup still running, and is skipped. Failing to delete
// is not an error: it only costs disk space until the next try.
func removeStaleWork(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), workPrefix) {
			continue
		}
		info, err := e.Info()
		if err != nil || now.Sub(info.ModTime()) <= staleWorkAge {
			continue
		}
		p := filepath.Join(dir, e.Name())
		lock, err := db.LockFile(p, workLockName, true)
		if err != nil {
			continue
		}
		_ = os.RemoveAll(p)
		_ = lock.Unlock()
	}
}

// vacuumInto writes a consistent copy of the database at src to dst, which must not exist. mode
// is how src is opened (WriteOptions.ServerStopped); VACUUM INTO only reads it either way, and
// sees WAL content a running server hasn't checkpointed yet.
func vacuumInto(ctx context.Context, src, dst, mode string) error {
	conn, err := openFile(src, mode)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, "VACUUM INTO ?", dst); err != nil {
		return fmt.Errorf("backup: snapshot: %w", err)
	}
	// SQLite doesn't sync VACUUM INTO's output.
	f, err := os.Open(dst) //nolint:gosec // dst is our own work file
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := f.Sync(); err != nil {
		return fmt.Errorf("backup: sync snapshot: %w", err)
	}
	return nil
}

// openFile opens the SQLite file at path without migrating it. mode is "ro" or "rw"; neither
// creates a missing file.
func openFile(path, mode string) (*sql.DB, error) {
	conn, err := sql.Open("sqlite", "file:"+path+"?mode="+mode+"&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("backup: open %s: %w", path, err)
	}
	conn.SetMaxOpenConns(1)
	return conn, nil
}

// describe fills m's schema version and row counts from the database file at path.
func describe(ctx context.Context, path string, m *Manifest) error {
	conn, err := openFile(path, "ro")
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if m.SchemaVersion, err = db.SchemaVersion(ctx, conn); err != nil {
		return fmt.Errorf("backup: schema version: %w", err)
	}
	m.Tables, err = rowCounts(ctx, conn)
	return err
}

// rowCounts counts the rows of every table, SQLite's internal ones excepted.
func rowCounts(ctx context.Context, conn *sql.DB) (map[string]int64, error) {
	rows, err := conn.QueryContext(ctx, `SELECT name FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite\_%' ESCAPE '\'`)
	if err != nil {
		return nil, fmt.Errorf("backup: list tables: %w", err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			_ = rows.Close()
			return nil, err
		}
		names = append(names, n)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, fmt.Errorf("backup: list tables: %w", err)
	}
	counts := make(map[string]int64, len(names))
	for _, n := range names {
		var c int64
		// The name comes from sqlite_schema and is quoted as an identifier.
		q := `SELECT count(*) FROM "` + strings.ReplaceAll(n, `"`, `""`) + `"` //nolint:gosec // quoted identifier, not user input
		if err := conn.QueryRowContext(ctx, q).Scan(&c); err != nil {
			return nil, fmt.Errorf("backup: count %s: %w", n, err)
		}
		counts[n] = c
	}
	return counts, nil
}

// writeArchive streams the snapshot, then every media file, then the manifest into w as a
// gzipped tar. Files are copied straight from disk, so memory use doesn't grow with media size.
func writeArchive(ctx context.Context, w io.Writer, snapshot, mediaRoot string, m *Manifest) error {
	// Photos are already compressed; the fastest level spares a Pi's CPU for little size.
	gz, err := gzip.NewWriterLevel(w, gzip.BestSpeed)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(gz)
	if _, err := addFile(tw, snapshot, db.FileName, m.CreatedAt); err != nil {
		return err
	}
	if err := addMedia(ctx, tw, mediaRoot, m); err != nil {
		return err
	}
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{
		Name: manifestName, Typeflag: tar.TypeReg, Mode: 0o600, Size: int64(len(body)), ModTime: m.CreatedAt,
	}); err != nil {
		return fmt.Errorf("backup: write manifest: %w", err)
	}
	if _, err := tw.Write(body); err != nil {
		return fmt.Errorf("backup: write manifest: %w", err)
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("backup: finish archive: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("backup: finish archive: %w", err)
	}
	return nil
}

// addMedia archives every stored media file under root (the Local storage layout aa/bb/<key>)
// and adds them to m's totals. Other files, such as uploads still being written, are skipped,
// and so is a file deleted (by retention) while the backup runs.
func addMedia(ctx context.Context, tw *tar.Writer, root string, m *Manifest) error {
	// The media dir may be a symlink to another disk; archive what it points at.
	root, err := filepath.EvalSymlinks(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("backup: media dir: %w", err)
	}
	var names []string
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel = filepath.ToSlash(rel); validMediaPath(rel) {
			names = append(names, rel)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("backup: list media: %w", err)
	}
	sort.Strings(names)
	for _, rel := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := addFile(tw, filepath.Join(root, filepath.FromSlash(rel)), mediaDirName+"/"+rel, time.Time{})
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		m.MediaFiles++
		m.MediaBytes += n
	}
	return nil
}

// addFile copies the regular file at path into tw as name and returns its size. A zero modTime
// takes the file's own.
func addFile(tw *tar.Writer, path, name string, modTime time.Time) (int64, error) {
	f, err := os.Open(path) //nolint:gosec // paths come from our snapshot or a walk of the media dir
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf("backup: %s is not a regular file", path)
	}
	if modTime.IsZero() {
		modTime = info.ModTime()
	}
	if err := tw.WriteHeader(&tar.Header{
		Name: name, Typeflag: tar.TypeReg, Mode: 0o600, Size: info.Size(), ModTime: modTime.UTC(),
	}); err != nil {
		return 0, fmt.Errorf("backup: write %s: %w", name, err)
	}
	if _, err := io.CopyN(tw, f, info.Size()); err != nil {
		return 0, fmt.Errorf("backup: write %s: %w", name, err)
	}
	return info.Size(), nil
}

// validMediaPath reports whether rel (slash-separated, relative to the media dir) is where Local
// storage keeps a file: aa/bb/<key>, aa and bb being the key's first two pairs of hex digits.
func validMediaPath(rel string) bool {
	parts := strings.Split(rel, "/")
	if len(parts) != 3 || !media.ValidKey(parts[2]) {
		return false
	}
	key := parts[2]
	return parts[0] == key[0:2] && parts[1] == key[2:4]
}

// Prune deletes the oldest backups in dir (files named by FileName) beyond the newest keep, and
// returns how many it deleted. Other files are left alone.
func Prune(dir string, keep int) (int, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	} else if err != nil {
		return 0, err
	}
	var names []string
	for _, e := range entries {
		if e.Type().IsRegular() && fileNamePattern.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	deleted := 0
	for _, n := range names[:max(len(names)-keep, 0)] {
		if err := os.Remove(filepath.Join(dir, n)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return deleted, fmt.Errorf("backup: prune %s: %w", n, err)
		}
		deleted++
	}
	return deleted, nil
}

// syncDir makes a rename in dir durable. Failing to is not an error: the file is complete either
// way, and some filesystems can't sync directories.
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil { //nolint:gosec // a directory we just wrote to
		_ = d.Sync()
		_ = d.Close()
	}
}
