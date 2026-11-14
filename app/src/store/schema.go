package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
)

// SchemaVersions are the newest migration this binary knows for each database.
type SchemaVersions struct {
	Base, Tenant int
}

// KnownSchema returns the newest embedded migration of the base and tenant
// databases. It reads nothing from disk.
func KnownSchema() (SchemaVersions, error) {
	b, err := loadMigrations(baseFS, "migrations/sqlite/base")
	if err != nil {
		return SchemaVersions{}, err
	}
	t, err := loadMigrations(tenantFS, "migrations/sqlite/tenant")
	if err != nil {
		return SchemaVersions{}, err
	}
	return SchemaVersions{Base: len(b), Tenant: len(t)}, nil
}

// SchemaOnDisk is what a database file has applied.
type SchemaOnDisk struct {
	Exists  bool
	Applied int // 0 when the file does not exist or has no migration table
}

// ReadSchema reports the applied migration version of the database file at path
// without creating, migrating or writing to it: the file is opened read-only
// and a missing file is a normal answer. Opening a WAL database read-only does
// not change its contents.
func ReadSchema(ctx context.Context, path string) (SchemaOnDisk, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return SchemaOnDisk{}, err
	}
	if _, err := os.Stat(abs); errors.Is(err, fs.ErrNotExist) {
		return SchemaOnDisk{}, nil
	} else if err != nil {
		return SchemaOnDisk{}, fmt.Errorf("store: schema: %w", err)
	}
	u := url.URL{Scheme: "file", Path: abs, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return SchemaOnDisk{}, fmt.Errorf("store: schema: %w", err)
	}
	defer db.Close()
	var n int
	err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'`).Scan(&n)
	if err != nil {
		return SchemaOnDisk{}, fmt.Errorf("store: schema: %w", err)
	}
	if n == 0 {
		return SchemaOnDisk{Exists: true}, nil
	}
	var v sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&v); err != nil {
		return SchemaOnDisk{}, fmt.Errorf("store: schema: %w", err)
	}
	return SchemaOnDisk{Exists: true, Applied: int(v.Int64)}, nil
}
