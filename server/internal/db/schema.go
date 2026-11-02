package db

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"path"

	"github.com/pressly/goose/v3"
)

// FileName is the database file inside the data dir.
const FileName = "chorecast.db"

// LatestSchemaVersion is the newest migration embedded in this binary: the schema version Open
// migrates a database to.
func LatestSchemaVersion() (int64, error) {
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return 0, err
	}
	var latest int64
	for _, name := range names {
		v, err := goose.NumericComponent(path.Base(name))
		if err != nil {
			return 0, fmt.Errorf("migration %s: %w", name, err)
		}
		latest = max(latest, v)
	}
	return latest, nil
}

// SchemaVersion is the highest migration version recorded in sqldb's goose table. Unlike Open, it
// changes nothing, so it can inspect a database before deciding whether to migrate it.
func SchemaVersion(ctx context.Context, sqldb *sql.DB) (int64, error) {
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return 0, err
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, sqldb, sub)
	if err != nil {
		return 0, err
	}
	return p.GetDBVersion(ctx)
}
