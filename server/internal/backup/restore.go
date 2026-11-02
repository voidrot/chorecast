package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/voidrot/chorecast/server/internal/audit"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/db"
)

var (
	// ErrInvalidArchive means the file is not a backup this binary can restore: corrupt, truncated,
	// or holding entries a backup never has.
	ErrInvalidArchive = errors.New("backup: invalid archive")
	// ErrNewerSchema means the backup comes from a newer Chorecast; upgrade before restoring it.
	ErrNewerSchema = errors.New("backup: the backup's schema is newer than this binary")
	// ErrNotEmpty means the data dir already holds a database or media; restoring needs force.
	ErrNotEmpty = errors.New("backup: the data dir already has data")
	// ErrTooLarge means the archive would extract to more than the restore may write.
	ErrTooLarge = errors.New("backup: the archive is larger than the space a restore may use")
	// ErrLinkedData means the data dir's database or media is a symlink or a mount point, which a
	// restore would replace with a plain file or directory.
	ErrLinkedData = errors.New("backup: the database or media in the data dir is a symlink or a mount point")
)

const (
	maxManifestBytes = 1 << 20
	// maxMediaEntryBytes caps one media file; photos are capped far lower at upload.
	maxMediaEntryBytes = 1 << 30
	// freeSpaceMargin is left free on the data disk when the restore budget comes from free space.
	freeSpaceMargin = 256 << 20
	copyChunk       = 1 << 20
	// maxTrailingBytes is what may follow the tar end marker (record padding) before the gzip end.
	maxTrailingBytes = 1 << 20
	stagePrefix      = ".restore-"
)

// RestoreOptions tune Restore.
type RestoreOptions struct {
	// Force replaces a data dir that already has data.
	Force bool
	// MaxBytes caps what the restore extracts in total. Zero means the free space on the data
	// dir's disk minus a 256 MiB margin.
	MaxBytes int64
	// Clock dates the restore_completed audit row.
	Clock clock.Clock
}

// Restore replaces the database and media in dataDir with the archive's.
//
// It holds the data dir lock, so it refuses while a server uses dataDir, and the restore lock, so
// it refuses while an admin or backup command runs (db.ErrDataDirLocked). It refuses while an
// interrupted restore's marker exists (db.ErrRestoreInterrupted), when the database or media is a
// symlink or mount point (ErrLinkedData), and, without opts.Force, a data dir that already has
// data (ErrNotEmpty).
//
// The archive is extracted to a staging dir inside dataDir within the size budget (ErrTooLarge),
// and checked there: only a manifest, the database and media files in the storage layout are
// accepted, every row count and media total must match the manifest, and the schema must not be
// newer than this binary's (an older one is migrated). Only then is a marker written, the
// existing data moved aside and the restored data moved in. If a move fails, the moves are
// undone; if undoing fails too, the staging dir and the marker are kept and the error names where
// the old data is. Backups in dataDir/backups are never touched.
func Restore(ctx context.Context, archive, dataDir string, opts RestoreOptions) (Manifest, error) {
	lock, err := db.LockDataDir(dataDir)
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: a server or another restore is using %s; stop it first", err, dataDir)
	}
	defer func() { _ = lock.Unlock() }()
	restoreLock, err := db.LockRestore(dataDir)
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: an admin or backup command is using %s; wait for it", err, dataDir)
	}
	defer func() { _ = restoreLock.Unlock() }()
	if err := db.CheckRestoreMarker(dataDir); err != nil {
		return Manifest{}, err
	}
	if err := checkLayout(dataDir); err != nil {
		return Manifest{}, err
	}
	if !opts.Force {
		has, err := hasData(dataDir)
		if err != nil {
			return Manifest{}, err
		}
		if has {
			return Manifest{}, fmt.Errorf("%w: %s (use --force to replace it)", ErrNotEmpty, dataDir)
		}
	}
	budget, err := restoreBudget(dataDir, opts.MaxBytes)
	if err != nil {
		return Manifest{}, err
	}

	stage, err := os.MkdirTemp(dataDir, stagePrefix+"*")
	if err != nil {
		return Manifest{}, fmt.Errorf("backup: create staging dir: %w", err)
	}
	keepStage := false
	defer func() {
		if !keepStage {
			_ = os.RemoveAll(stage)
		}
	}()
	restored := filepath.Join(stage, "new")
	m, got, err := extract(ctx, archive, restored, budget)
	if err != nil {
		return Manifest{}, err
	}
	if err := verify(ctx, restored, m, got); err != nil {
		return Manifest{}, err
	}
	// Migrate in staging: a migration failure must not leave a half-upgraded database in place.
	store, err := db.Open(ctx, restored)
	if err != nil {
		return Manifest{}, fmt.Errorf("backup: migrate restored database: %w", err)
	}
	if err := store.Close(); err != nil {
		return Manifest{}, err
	}

	old := filepath.Join(stage, "old")
	if err := db.WriteRestoreMarker(dataDir, old); err != nil {
		return Manifest{}, err
	}
	stranded, err := swap(dataDir, restored, old)
	if stranded {
		keepStage = true
		return Manifest{}, fmt.Errorf("backup: restore failed and undoing it failed too; previous data that "+
			"wasn't moved back is in %s, and %s blocks the server until you sort it out: %w",
			old, filepath.Join(dataDir, db.RestoreMarkerName), err)
	}
	if rmErr := db.RemoveRestoreMarker(dataDir); rmErr != nil {
		keepStage = true // the marker still names the old data
		return Manifest{}, errors.Join(err, rmErr)
	}
	if err != nil {
		return Manifest{}, err // undone: dataDir is as it was
	}
	// The restored database is in place and the data dir still locked: record the restore in it.
	if err := recordRestored(ctx, dataDir, archive, m, opts); err != nil {
		return m, fmt.Errorf("backup: restored, but recording it in the audit log failed: %w", err)
	}
	return m, nil
}

func recordRestored(ctx context.Context, dataDir, archive string, m Manifest, opts RestoreOptions) error {
	store, err := db.Open(ctx, dataDir)
	if err != nil {
		return err
	}
	forced := opts.Force
	recErr := audit.Record(ctx, store.Queries, opts.Clock.Now(), audit.Entry{
		Actor: audit.CLI, Action: audit.ActionRestoreCompleted, TargetKind: audit.TargetBackup, Summary: filepath.Base(archive),
		Details: audit.AuditDetails{AppVersion: m.AppVersion, SchemaVer: m.SchemaVersion, Forced: &forced},
	})
	return errors.Join(recErr, store.Close())
}

// dataNames are what a restore replaces in the data dir. The WAL files go with the database: a
// stale WAL next to a restored database would corrupt it.
var dataNames = []string{db.FileName, db.FileName + "-wal", db.FileName + "-shm", db.FileName + "-journal", mediaDirName}

// hasData reports whether dataDir holds a database or any media.
func hasData(dataDir string) (bool, error) {
	for _, n := range []string{db.FileName, db.FileName + "-wal"} {
		info, err := os.Stat(filepath.Join(dataDir, n))
		if err == nil && info.Size() > 0 {
			return true, nil
		} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return false, err
		}
	}
	entries, err := os.ReadDir(filepath.Join(dataDir, mediaDirName))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return len(entries) > 0, err
}

// checkLayout refuses a database or media dir that is a symlink or a mount point: the swap would
// move the link or mount aside and put plain files in the data dir instead of where they live.
func checkLayout(dataDir string) error {
	dirInfo, err := os.Stat(dataDir)
	if err != nil {
		return err
	}
	for _, n := range []string{db.FileName, mediaDirName} {
		p := filepath.Join(dataDir, n)
		info, err := os.Lstat(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%w: %s is a symlink; restore into a data dir that holds the files themselves", ErrLinkedData, p)
		}
		if otherDevice(info, dirInfo) {
			return fmt.Errorf("%w: %s is a mount point; restore into a data dir that holds the files themselves", ErrLinkedData, p)
		}
	}
	return nil
}

// restoreBudget is how many bytes a restore may extract: max if set, otherwise the free space on
// dataDir's disk minus a margin.
func restoreBudget(dataDir string, maxBytes int64) (int64, error) {
	if maxBytes > 0 {
		return maxBytes, nil
	}
	free, ok := freeBytes(dataDir)
	if !ok {
		return math.MaxInt64, nil // shortcut: no free-space check outside unix; pass MaxBytes there
	}
	if free <= freeSpaceMargin {
		return 0, fmt.Errorf("%w: only %d bytes free in %s", ErrTooLarge, free, dataDir)
	}
	return free - freeSpaceMargin, nil
}

// totals is what extraction found, to check against the manifest.
type totals struct{ mediaFiles, mediaBytes int64 }

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidArchive, fmt.Sprintf(format, args...))
}

// extract unpacks archive into dir, accepting only regular files at the paths a backup has, and
// at most budget bytes in all. Anything else, such as a symlink, a hard link, a sparse file, an
// absolute path or one with "..", is refused before it is written.
func extract(ctx context.Context, archive, dir string, budget int64) (Manifest, totals, error) {
	var m Manifest
	var got totals
	f, err := os.Open(archive) //nolint:gosec // the operator names the archive to restore
	if err != nil {
		return m, got, fmt.Errorf("backup: open archive: %w", err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return m, got, invalid("not a gzip file: %v", err)
	}
	defer func() { _ = gz.Close() }()
	if err := os.Mkdir(dir, 0o750); err != nil {
		return m, got, err
	}

	tr := tar.NewReader(gz)
	seen := map[string]bool{}
	for {
		if err := ctx.Err(); err != nil {
			return m, got, err
		}
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return m, got, invalid("read: %v", err)
		}
		name := hdr.Name
		if hdr.Typeflag != tar.TypeReg {
			return m, got, invalid("entry %q is not a regular file", name)
		}
		// The tar reader expands sparse files to their full size: a few hundred bytes of archive
		// can claim gigabytes. Backups never contain them.
		for k := range hdr.PAXRecords {
			if strings.HasPrefix(k, "GNU.sparse.") {
				return m, got, invalid("entry %q is a sparse file", name)
			}
		}
		isMedia := strings.HasPrefix(name, mediaDirName+"/") && validMediaPath(strings.TrimPrefix(name, mediaDirName+"/"))
		if (name != manifestName && name != db.FileName && !isMedia) || !filepath.IsLocal(name) {
			return m, got, invalid("unexpected entry %q", name)
		}
		if seen[name] {
			return m, got, invalid("duplicate entry %q", name)
		}
		seen[name] = true

		if name == manifestName {
			if hdr.Size > maxManifestBytes {
				return m, got, invalid("manifest too large")
			}
			if err := json.NewDecoder(io.LimitReader(tr, maxManifestBytes)).Decode(&m); err != nil {
				return m, got, invalid("manifest: %v", err)
			}
			continue
		}
		if isMedia && hdr.Size > maxMediaEntryBytes {
			return m, got, invalid("media entry %q is %d bytes", name, hdr.Size)
		}
		if hdr.Size > budget {
			return m, got, fmt.Errorf("%w: %q alone needs %d more bytes than the %d left", ErrTooLarge, name, hdr.Size-budget, budget)
		}
		budget -= hdr.Size
		if err := writeEntry(ctx, filepath.Join(dir, filepath.FromSlash(name)), tr, hdr.Size); err != nil {
			return m, got, err
		}
		if isMedia {
			got.mediaFiles++
			got.mediaBytes += hdr.Size
		}
	}
	// Read to the end so gzip checks its checksum: a corrupt archive must not pass. A backup has
	// at most tar padding after its end marker, so anything past maxTrailingBytes is refused
	// rather than decompressed.
	if err := ctx.Err(); err != nil {
		return m, got, err
	}
	trailing, err := io.Copy(io.Discard, io.LimitReader(gz, maxTrailingBytes+1))
	if err != nil {
		return m, got, invalid("read: %v", err)
	}
	if trailing > maxTrailingBytes {
		return m, got, invalid("more than %d bytes after the end of the archive", maxTrailingBytes)
	}
	if !seen[manifestName] || !seen[db.FileName] {
		return m, got, invalid("missing %s or %s", manifestName, db.FileName)
	}
	if m.Format != Format {
		return m, got, invalid("unsupported format %d", m.Format)
	}
	return m, got, nil
}

// writeEntry writes exactly size bytes from r to a new file at path, in chunks so a cancelled
// ctx stops a large entry.
func writeEntry(ctx context.Context, path string, r io.Reader, size int64) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // path is an allowlisted entry name under the staging dir
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	for left := size; left > 0; {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := io.CopyN(out, r, min(left, copyChunk))
		if err != nil {
			return invalid("read %s: %v", filepath.Base(path), err)
		}
		left -= n
	}
	if err := out.Sync(); err != nil {
		return err
	}
	return out.Close()
}

// verify checks the extracted database and media in dir against the manifest, before anything
// migrates the database.
func verify(ctx context.Context, dir string, m Manifest, got totals) error {
	latest, err := db.LatestSchemaVersion()
	if err != nil {
		return err
	}
	if m.SchemaVersion > latest {
		return fmt.Errorf("%w: backup has schema %d, this binary %d; restore it with a newer chorecast", ErrNewerSchema, m.SchemaVersion, latest)
	}
	conn, err := openFile(filepath.Join(dir, db.FileName), "ro")
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	var check string
	if err := conn.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&check); err != nil || check != "ok" {
		return invalid("database fails its integrity check: %v %s", err, check)
	}
	v, err := db.SchemaVersion(ctx, conn)
	if err != nil {
		return invalid("database schema version: %v", err)
	}
	if v > latest {
		return fmt.Errorf("%w: backup has schema %d, this binary %d; restore it with a newer chorecast", ErrNewerSchema, v, latest)
	}
	if v != m.SchemaVersion {
		return invalid("database has schema %d, manifest says %d", v, m.SchemaVersion)
	}
	counts, err := rowCounts(ctx, conn)
	if err != nil {
		return err
	}
	if !maps.Equal(counts, m.Tables) {
		return invalid("row counts don't match the manifest")
	}
	if got.mediaFiles != m.MediaFiles || got.mediaBytes != m.MediaBytes {
		return invalid("media: %d files, %d bytes; manifest says %d files, %d bytes",
			got.mediaFiles, got.mediaBytes, m.MediaFiles, m.MediaBytes)
	}
	return nil
}

// swap moves dataDir's current data into old, then the restored data from restored into dataDir.
// If a step fails it undoes the moves made so far and returns the error. stranded reports that
// undoing failed, so the data dir is a mix and some previous data may still be in old; the error
// then includes why.
func swap(dataDir, restored, old string) (stranded bool, err error) {
	if err := os.Mkdir(old, 0o750); err != nil {
		return false, err
	}
	var movedOut, movedIn []string
	undo := func(cause error) (bool, error) {
		errs := []error{cause}
		for _, n := range movedIn {
			if err := rename(filepath.Join(dataDir, n), filepath.Join(restored, n)); err != nil {
				errs = append(errs, fmt.Errorf("undo: move restored %s back out: %w", n, err))
			}
		}
		for _, n := range movedOut {
			if err := rename(filepath.Join(old, n), filepath.Join(dataDir, n)); err != nil {
				errs = append(errs, fmt.Errorf("undo: move previous %s back: %w", n, err))
			}
		}
		// Any failed undo step leaves the data dir mixed, so the marker must stay.
		return len(errs) > 1, errors.Join(errs...)
	}
	for _, n := range dataNames {
		if err := rename(filepath.Join(dataDir, n), filepath.Join(old, n)); err == nil {
			movedOut = append(movedOut, n)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return undo(fmt.Errorf("backup: move aside %s: %w", n, err))
		}
	}
	for _, n := range dataNames {
		if err := rename(filepath.Join(restored, n), filepath.Join(dataDir, n)); err == nil {
			movedIn = append(movedIn, n)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return undo(fmt.Errorf("backup: move in %s: %w", n, err))
		}
	}
	syncDir(dataDir)
	return false, nil
}
