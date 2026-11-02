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
	"os"
	"path/filepath"
	"strings"

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
)

const maxManifestBytes = 1 << 20

// Restore replaces the database and media in dataDir with the archive's. It holds the data dir
// lock throughout, so it refuses to run while a server uses dataDir (db.ErrDataDirLocked). Without
// force it refuses a data dir that already has data (ErrNotEmpty).
//
// The archive is extracted to a staging dir inside dataDir and checked there: only a manifest,
// the database and media files in the storage layout are accepted, every row count and media
// total must match the manifest, and the schema must not be newer than this binary's (an older
// one is migrated). Only then is the existing data moved aside and the restored data moved in, so
// a refused or failed restore leaves dataDir as it was. Backups in dataDir/backups are never
// touched.
func Restore(ctx context.Context, archive, dataDir string, force bool) (Manifest, error) {
	lock, err := db.LockDataDir(dataDir)
	if err != nil {
		return Manifest{}, err
	}
	defer func() { _ = lock.Unlock() }()
	if !force {
		has, err := hasData(dataDir)
		if err != nil {
			return Manifest{}, err
		}
		if has {
			return Manifest{}, fmt.Errorf("%w: %s (use --force to replace it)", ErrNotEmpty, dataDir)
		}
	}

	stage, err := os.MkdirTemp(dataDir, ".restore-*")
	if err != nil {
		return Manifest{}, fmt.Errorf("backup: create staging dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(stage) }()
	restored := filepath.Join(stage, "new")
	m, got, err := extract(ctx, archive, restored)
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
	if err := swap(dataDir, restored, filepath.Join(stage, "old")); err != nil {
		return Manifest{}, err
	}
	syncDir(dataDir)
	return m, nil
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

// totals is what extraction found, to check against the manifest.
type totals struct{ mediaFiles, mediaBytes int64 }

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidArchive, fmt.Sprintf(format, args...))
}

// extract unpacks archive into dir, accepting only regular files at the paths a backup has.
// Anything else, such as a symlink, a hard link, an absolute path or one with "..", is refused
// before it is written.
func extract(ctx context.Context, archive, dir string) (Manifest, totals, error) {
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
		if err := writeEntry(filepath.Join(dir, filepath.FromSlash(name)), tr, hdr.Size); err != nil {
			return m, got, err
		}
		if isMedia {
			got.mediaFiles++
			got.mediaBytes += hdr.Size
		}
	}
	// Read to the end so gzip checks its checksum: a corrupt archive must not pass.
	if _, err := io.Copy(io.Discard, gz); err != nil { //nolint:gosec // drained to discard, nothing is kept
		return m, got, invalid("read: %v", err)
	}
	if !seen[manifestName] || !seen[db.FileName] {
		return m, got, invalid("missing %s or %s", manifestName, db.FileName)
	}
	if m.Format != Format {
		return m, got, invalid("unsupported format %d", m.Format)
	}
	return m, got, nil
}

// writeEntry writes exactly size bytes from r to a new file at path.
func writeEntry(path string, r io.Reader, size int64) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // path is an allowlisted entry name under the staging dir
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	if _, err := io.CopyN(out, r, size); err != nil {
		return invalid("read %s: %v", filepath.Base(path), err)
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
// If a step fails it moves everything back.
func swap(dataDir, restored, old string) (err error) {
	if err := os.Mkdir(old, 0o750); err != nil {
		return err
	}
	var movedOut, movedIn []string
	defer func() {
		if err == nil {
			return
		}
		for _, n := range movedIn {
			_ = os.RemoveAll(filepath.Join(dataDir, n))
		}
		for _, n := range movedOut {
			_ = os.Rename(filepath.Join(old, n), filepath.Join(dataDir, n))
		}
	}()
	for _, n := range dataNames {
		if err := os.Rename(filepath.Join(dataDir, n), filepath.Join(old, n)); err == nil {
			movedOut = append(movedOut, n)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("backup: move aside %s: %w", n, err)
		}
	}
	for _, n := range dataNames {
		if err := os.Rename(filepath.Join(restored, n), filepath.Join(dataDir, n)); err == nil {
			movedIn = append(movedIn, n)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("backup: move in %s: %w", n, err)
		}
	}
	return nil
}
