package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"hash/fnv"
	"maps"
	"runtime"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/b070nd/stoxicity/app/src/model"
)

//go:embed migrations/postgres/base/*.sql
var pgBaseFS embed.FS

//go:embed migrations/postgres/tenant/*.sql
var pgTenantFS embed.FS

// OpenPostgres opens the store in a Postgres database instead of a folder of
// SQLite files, and migrates the base schema. The database holds the base data
// in the schema "base" and each tenant's in the schema "tenant_<id>"; a handle
// only ever sees its own schema (the search path of its connections), so one
// tenant's data cannot be reached through another's. The database must sort
// text by bytes, as SQLite does, or the order of results would differ between
// the two; OpenPostgres refuses one that does not.
func OpenPostgres(ctx context.Context, url string) (*DB, error) {
	if url == "" {
		return nil, errors.New("store: empty database URL")
	}
	db := &DB{pgURL: url, now: time.Now, tenants: map[string]*handle{}}
	var err error
	db.base, err = openPGHandle(ctx, url, "base", pgBaseFS, "migrations/postgres/base")
	if err != nil {
		return nil, err
	}
	return db, nil
}

// OpenAt opens the store in the Postgres database named by url when there is
// one, and otherwise in the SQLite files of dir. It is the one place that
// chooses; a configured but unreachable Postgres is an error, never a quiet
// fall back to SQLite, which would split the history across two databases.
func OpenAt(dir, url string) (*DB, error) {
	if url != "" {
		return OpenPostgres(context.Background(), url)
	}
	return Open(dir)
}

// pgSchema is the schema of a tenant. The caller has checked model.ValidID.
func pgTenantSchema(id string) string { return "tenant_" + id }

func (db *DB) openPGTenant(ctx context.Context, id string) (*handle, error) {
	if !model.ValidID(id) {
		return nil, fmt.Errorf("store: invalid tenant id %q", id)
	}
	return openPGHandle(ctx, db.pgURL, pgTenantSchema(id), pgTenantFS, "migrations/postgres/tenant")
}

// openPGHandle creates the schema if it is missing, migrates it while holding a
// lock no other process can hold at once, and opens the write and read pools.
func openPGHandle(ctx context.Context, url, schema string, fsys migrationFS, dir string) (*handle, error) {
	migrations, err := loadMigrationsFrom(fsys, dir, true)
	if err != nil {
		return nil, err
	}
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("store: database URL: %w", err)
	}
	if err := pgPrepareSchema(ctx, cfg, schema); err != nil {
		return nil, fmt.Errorf("store: schema %s: %w", schema, err)
	}
	w, err := pgPool(cfg, schema, false)
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(4)
	w.SetMaxIdleConns(4)
	if err := migrate(ctx, w, pgDialect{}, migrations, time.Now); err != nil {
		w.Close()
		return nil, fmt.Errorf("store: schema %s: %w", schema, err)
	}
	r, err := pgPool(cfg, schema, true)
	if err != nil {
		w.Close()
		return nil, err
	}
	n := min(max(runtime.NumCPU(), 2), 8)
	r.SetMaxOpenConns(n)
	r.SetMaxIdleConns(n)
	if err := r.PingContext(ctx); err != nil {
		r.Close()
		w.Close()
		return nil, fmt.Errorf("store: %w", err)
	}
	return &handle{w: w, r: r, d: pgDialect{}, lock: pgLockKey(schema)}, nil
}

// pgPrepareSchema checks the database and creates the schema when it is
// missing, one process at a time.
func pgPrepareSchema(ctx context.Context, cfg *pgx.ConnConfig, schema string) error {
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("Postgres at %s:%d is not reachable as %s (database %s): %w", cfg.Host, cfg.Port, cfg.User, cfg.Database, err)
	}
	defer conn.Close(ctx)
	var provider, collate, locale *string
	if err := conn.QueryRow(ctx, `SELECT datlocprovider::text, datcollate, datlocale FROM pg_database WHERE datname = current_database()`).
		Scan(&provider, &collate, &locale); err != nil {
		return err
	}
	if !byteOrder(deref(provider), deref(collate), deref(locale)) {
		return fmt.Errorf("the database sorts text by locale (%s); create it with the byte-order locale: CREATE DATABASE name TEMPLATE template0 ENCODING 'UTF8' LOCALE_PROVIDER builtin BUILTIN_LOCALE 'C.UTF-8'",
			strings.Trim(deref(collate)+" "+deref(locale), " "))
	}
	// One process at a time creates the schema and migrates it.
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, int64(openLock)); err != nil {
		return err
	}
	defer conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, int64(openLock))
	var have bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)`, schema).Scan(&have); err != nil {
		return err
	}
	if !have {
		if _, err := conn.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{schema}.Sanitize()); err != nil {
			return err
		}
	}
	return nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// byteOrder reports whether a database compares text by its bytes: the builtin
// provider with a C locale, or the libc provider with collation C or POSIX.
func byteOrder(provider, collate, locale string) bool {
	switch provider {
	case "b":
		return locale == "C" || locale == "C.UTF-8"
	default:
		return collate == "C" || collate == "POSIX"
	}
}

// pgPool opens a connection pool whose connections see only schema. A read
// pool refuses writes.
func pgPool(base *pgx.ConnConfig, schema string, readOnly bool) (*sql.DB, error) {
	c := *base
	c.RuntimeParams = maps.Clone(base.RuntimeParams)
	if c.RuntimeParams == nil {
		c.RuntimeParams = map[string]string{}
	}
	c.RuntimeParams["search_path"] = pgx.Identifier{schema}.Sanitize()
	c.RuntimeParams["application_name"] = "stoxicity"
	if readOnly {
		c.RuntimeParams["default_transaction_read_only"] = "on"
	}
	return sql.OpenDB(rebindConnector{stdlib.GetConnector(c)}), nil
}

// openLock is the key of the lock that makes a process create and migrate a
// schema alone.
const openLock = 0x73746f78_6f70656e

// pgLockKey is the key of the write lock of a schema.
func pgLockKey(schema string) int64 {
	h := fnv.New64a()
	h.Write([]byte("stoxicity:write:" + schema))
	return int64(h.Sum64())
}

// pgDialect is the dialect of Postgres.
type pgDialect struct{}

func (pgDialect) jsonEach(expr, alias string) string {
	return "jsonb_array_elements_text((" + expr + ")::jsonb) AS " + alias + "(value)"
}

func (pgDialect) migrationsTable() string {
	return `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    BIGINT PRIMARY KEY,
		name       TEXT NOT NULL,
		applied_ms BIGINT NOT NULL)`
}

// afterBegin takes the schema's write lock until the transaction ends, so write
// transactions run one at a time as they do in SQLite. Readers are not blocked.
func (pgDialect) afterBegin(ctx context.Context, tx *sql.Tx, key int64) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(?)`, key)
	return err
}

// The full-text index is a generated column; there is nothing to add.
func (pgDialect) indexArticles(context.Context, *sql.Tx, int64) error { return nil }

func (pgDialect) searchFrom() string  { return ` FROM articles a` }
func (pgDialect) searchWhere() string { return ` WHERE a.fts @@ to_tsquery('simple', ?)` }

func (pgDialect) matchArg(match string) (string, error) { return ftsToTSQuery(match) }
