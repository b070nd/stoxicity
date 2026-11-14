package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strconv"
	"time"
)

//go:embed migrations/sqlite/base/*.sql
var baseFS embed.FS

//go:embed migrations/sqlite/tenant/*.sql
var tenantFS embed.FS

// migrationFS is the part of fs.FS the loader needs.
type migrationFS interface {
	fs.ReadDirFS
	fs.ReadFileFS
}

// migrateLock is the key of the lock that lets one process at a time migrate a
// database that has locks (Postgres).
const migrateLock = 0x73746f78_6d696772

var migrationName = regexp.MustCompile(`^(\d{4})_[a-z0-9_]+\.sql$`)

type migration struct {
	version int
	name    string
	sql     string
}

// loadMigrations reads the NNNN_name.sql files in dir, in version order. The
// versions must start at 1 and have no gaps.
func loadMigrations(fsys migrationFS, dir string) ([]migration, error) {
	return loadMigrationsFrom(fsys, dir, false)
}

// loadMigrationsFrom is loadMigrations; with squashed the first file may have
// any version (a baseline that stands for every earlier migration of the other
// database), and the ones after it must follow without gaps.
func loadMigrationsFrom(fsys migrationFS, dir string, squashed bool) ([]migration, error) {
	entries, err := fsys.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("store: migrations: %w", err)
	}
	var list []migration
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := migrationName.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("store: migrations: bad file name %q", e.Name())
		}
		v, _ := strconv.Atoi(m[1])
		body, err := fsys.ReadFile(dir + "/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("store: migrations: %w", err)
		}
		list = append(list, migration{version: v, name: e.Name(), sql: string(body)})
	}
	slices.SortFunc(list, func(a, b migration) int { return a.version - b.version })
	first := 1
	if squashed && len(list) > 0 {
		first = list[0].version
	}
	for i, m := range list {
		if m.version != first+i {
			return nil, fmt.Errorf("store: migrations: %s: expected version %d", m.name, first+i)
		}
	}
	return list, nil
}

// migrate applies the migrations the database has not seen yet, each in its
// own transaction, and records them in schema_migrations. A database whose
// recorded version is higher than the newest known one was written by a newer
// binary and is refused untouched.
func migrate(ctx context.Context, db *sql.DB, d dialect, list []migration, now func() time.Time) error {
	_, err := db.ExecContext(ctx, d.migrationsTable())
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	var cur sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&cur); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	known := 0
	if len(list) > 0 {
		known = list[len(list)-1].version
	}
	if int(cur.Int64) > known {
		return fmt.Errorf("migrate: database is at schema version %d, newer than this binary (%d)", cur.Int64, known)
	}
	for _, m := range list {
		if err := applyMigration(ctx, db, d, m, now()); err != nil {
			return fmt.Errorf("migrate: %s: %w", m.name, err)
		}
	}
	return nil
}

func applyMigration(ctx context.Context, db *sql.DB, d dialect, m migration, at time.Time) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := d.afterBegin(ctx, tx, migrateLock); err != nil {
		return err
	}
	var done int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, m.version).Scan(&done); err != nil {
		return err
	}
	if done > 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, name, applied_ms) VALUES (?, ?, ?)`,
		m.version, m.name, ms(at)); err != nil {
		return err
	}
	return tx.Commit()
}
