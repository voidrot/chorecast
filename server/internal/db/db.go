// Package db opens the SQLite database and applies embedded migrations.
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite" // pure-Go driver: no CGO, easy ARM cross-compiles

	"github.com/voidrot/chorecast/server/internal/db/sqlc"
)

//go:embed migrations/*.sql
var migrations embed.FS

// DB bundles the raw handle with the sqlc query set.
type DB struct {
	*sqlc.Queries
	SQL *sql.DB
}

// Open opens (creating if needed) the database in dataDir and migrates it to the latest version.
func Open(ctx context.Context, dataDir string) (*DB, error) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	dsn := "file:" + filepath.Join(dataDir, "chorecast.db") +
		"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite allows one writer; a single connection avoids SQLITE_BUSY churn at household scale.
	sqldb.SetMaxOpenConns(1)

	goose.SetBaseFS(migrations)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite3"); err != nil {
		return nil, err
	}
	if err := goose.UpContext(ctx, sqldb, "migrations"); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &DB{Queries: sqlc.New(sqldb), SQL: sqldb}, nil
}

func (d *DB) Close() error { return d.SQL.Close() }
