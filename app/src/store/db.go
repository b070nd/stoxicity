package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite" // pure Go SQLite driver, registered as "sqlite"

	"github.com/b070nd/stoxicity/app/src/model"
)

// ErrNotFound is returned (wrapped) when a requested record does not exist.
var ErrNotFound = errors.New("store: not found")

var errClosed = errors.New("store: closed")

// DB is the SQLite implementation of Store. The shared base lives in
// dir/base.db and each tenant has its own file, dir/tenants/<id>.db, so one
// tenant's data cannot be reached through another's handle. Tenant files are
// created the first time they are used.
//
// Every database has one write connection and a separate pool of read-only
// connections, all in WAL mode. A DB is safe for concurrent use.
type DB struct {
	dir string
	now func() time.Time

	closed atomic.Bool

	mu      sync.Mutex // guards tenants and tenant creation
	base    *handle
	tenants map[string]*handle
}

var _ Store = (*DB)(nil)

// handle is one open database file.
type handle struct {
	w *sql.DB // single write connection
	r *sql.DB // read-only pool
	d dialect // what differs in the SQL of this database
}

func (h *handle) close() error {
	return errors.Join(h.r.Close(), h.w.Close())
}

// SetClock replaces the clock the store stamps recorded times with. The
// command line passes its own so that "recorded at" and "now" are one clock;
// call it right after Open, before any other use.
func (db *DB) SetClock(now func() time.Time) {
	if now != nil {
		db.now = now
	}
}

// Open opens (creating it if needed) the store rooted at dir and migrates the
// base database. It fails if the base database was written by a newer binary.
func Open(dir string) (*DB, error) {
	if dir == "" {
		return nil, errors.New("store: empty directory")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	db := &DB{dir: abs, now: time.Now, tenants: map[string]*handle{}}
	db.base, err = openHandle(context.Background(), filepath.Join(abs, "base.db"), baseFS, "migrations/base")
	if err != nil {
		return nil, err
	}
	return db, nil
}

// Close closes every database. Methods called afterwards return an error.
// Closing twice is harmless.
func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed.Swap(true) {
		return nil
	}
	err := db.base.close()
	for id, h := range db.tenants {
		err = errors.Join(err, h.close())
		delete(db.tenants, id)
	}
	return err
}

// baseHandle returns the base database, or an error after Close.
func (db *DB) baseHandle() (*handle, error) {
	if db.closed.Load() {
		return nil, errClosed
	}
	return db.base, nil
}

// tenantHandle returns the database of tenant id, opening it on first use.
// The caller must have checked model.ValidID(id).
func (db *DB) tenantHandle(id string) (*handle, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed.Load() {
		return nil, errClosed
	}
	if h, ok := db.tenants[id]; ok {
		return h, nil
	}
	if !model.ValidID(id) {
		return nil, fmt.Errorf("store: invalid tenant id %q", id)
	}
	tdir := filepath.Join(db.dir, "tenants")
	if err := os.MkdirAll(tdir, 0o755); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	h, err := openHandle(context.Background(), filepath.Join(tdir, id+".db"), tenantFS, "migrations/tenant")
	if err != nil {
		return nil, fmt.Errorf("store: tenant %q: %w", id, err)
	}
	db.tenants[id] = h
	return h, nil
}

// openHandle creates the file with mode 0600 if it is missing, opens the write
// connection, migrates, and then opens the read pool.
func openHandle(ctx context.Context, path string, fsys migrationFS, dir string) (*handle, error) {
	migrations, err := loadMigrations(fsys, dir)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	w, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	w.SetConnMaxLifetime(0)
	if err := migrate(ctx, w, migrations, time.Now); err != nil {
		w.Close()
		return nil, fmt.Errorf("store: %s: %w", filepath.Base(path), err)
	}

	r, err := sql.Open("sqlite", dsn(path, true))
	if err != nil {
		w.Close()
		return nil, fmt.Errorf("store: %w", err)
	}
	n := min(max(runtime.NumCPU(), 2), 8)
	r.SetMaxOpenConns(n)
	r.SetMaxIdleConns(n)
	r.SetConnMaxLifetime(0)
	if err := r.PingContext(ctx); err != nil {
		r.Close()
		w.Close()
		return nil, fmt.Errorf("store: %w", err)
	}
	return &handle{w: w, r: r, d: sqliteDialect{}}, nil
}

// dsn builds the connection string. Every connection gets the same pragmas;
// read connections are also query_only, write connections take the write lock
// when a transaction begins so it cannot fail halfway with a busy error.
func dsn(path string, readOnly bool) string {
	q := []string{
		"_pragma=busy_timeout(5000)",
		"_pragma=journal_mode(WAL)",
		"_pragma=synchronous(NORMAL)",
		"_pragma=foreign_keys(1)",
	}
	if readOnly {
		q = append(q, "_pragma=query_only(1)")
	} else {
		q = append(q, "_txlock=immediate")
	}
	u := url.URL{Scheme: "file", Path: path}
	return u.String() + "?" + strings.Join(q, "&")
}

// ms converts a time to unix milliseconds, the stored form.
func ms(t time.Time) int64 { return t.UnixMilli() }

// fromMS converts stored unix milliseconds back to a UTC time.
func fromMS(v int64) time.Time { return time.UnixMilli(v).UTC() }
