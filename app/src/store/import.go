package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/b070nd/stoxicity/app/src/model"
)

// ImportReport says what ImportSQLite copied: one entry per schema.
type ImportReport struct {
	Schemas []ImportedSchema
}

// ImportedSchema is the copy of one SQLite file.
type ImportedSchema struct {
	Schema string // "base" or "tenant_<id>"
	Source string // the SQLite file
	Tables []ImportedTable
}

// ImportedTable is one copied table: its rows and a hash of its content that is
// the same for the SQLite source and the Postgres copy.
type ImportedTable struct {
	Name string
	Rows int64
	Hash string
}

// ImportSQLite copies the data of the SQLite files in dir (base.db and
// tenants/<id>.db) into the empty Postgres database db is open on, keeping every
// value, key and sequence number as it is. The files are opened read-only and
// their content is not changed (SQLite may create empty -wal and -shm files
// beside a database it reads; changes still in a -wal file are read, so the
// copy sees everything the program last committed). A schema is copied in one transaction, so a failure leaves it
// empty. Afterwards every table is read back from both sides and its rows hashed
// in primary key order; a difference is an error.
//
// It refuses when the files are at another schema version than this program, or
// when the target already holds rows.
func (db *DB) ImportSQLite(ctx context.Context, dir string, progress func(string)) (ImportReport, error) {
	var rep ImportReport
	if db.pgURL == "" {
		return rep, errors.New("store: import: the target is not a Postgres database")
	}
	if progress == nil {
		progress = func(string) {}
	}
	known, err := KnownSchema()
	if err != nil {
		return rep, err
	}
	type job struct {
		h      *handle
		schema string
		file   string
		want   int
	}
	var jobs []job
	basePath := filepath.Join(dir, "base.db")
	if _, err := os.Stat(basePath); err != nil {
		return rep, fmt.Errorf("store: import: %w", err)
	}
	jobs = append(jobs, job{db.base, "base", basePath, known.Base})
	ents, err := os.ReadDir(filepath.Join(dir, "tenants"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return rep, fmt.Errorf("store: import: %w", err)
	}
	for _, e := range ents {
		id, ok := strings.CutSuffix(e.Name(), ".db")
		if !ok || !e.Type().IsRegular() {
			continue
		}
		if !model.ValidID(id) {
			return rep, fmt.Errorf("store: import: tenants/%s: not a tenant ID", e.Name())
		}
		h, err := db.tenantHandle(id)
		if err != nil {
			return rep, err
		}
		jobs = append(jobs, job{h, pgTenantSchema(id), filepath.Join(dir, "tenants", e.Name()), known.Tenant})
	}
	// Check everything before copying anything.
	srcs := make([]*sql.DB, len(jobs))
	for i, j := range jobs {
		src, err := openSQLiteReadOnly(j.file)
		if err != nil {
			return rep, err
		}
		defer src.Close()
		srcs[i] = src
		var v sql.NullInt64
		if err := src.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&v); err != nil {
			return rep, fmt.Errorf("store: import: %s: %w", j.file, err)
		}
		if int(v.Int64) != j.want {
			return rep, fmt.Errorf("store: import: %s is at schema version %d and this program at %d: open it with the program once (SQLite migrates it) or use the matching version", j.file, v.Int64, j.want)
		}
		tables, err := pgTables(ctx, j.h)
		if err != nil {
			return rep, err
		}
		for _, t := range tables {
			var n int64
			if err := j.h.w.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+pgx.Identifier{t}.Sanitize()).Scan(&n); err != nil {
				return rep, err
			}
			if n > 0 {
				return rep, fmt.Errorf("store: import: the Postgres schema %s already holds rows (table %s); import only into an empty database", j.schema, t)
			}
		}
	}
	for i, j := range jobs {
		progress("copying " + j.file + " into schema " + j.schema)
		is, err := importSchema(ctx, j.h, srcs[i], j.schema, j.file, progress)
		if err != nil {
			return rep, fmt.Errorf("store: import: %s: %w", j.file, err)
		}
		rep.Schemas = append(rep.Schemas, is)
	}
	return rep, nil
}

func openSQLiteReadOnly(path string) (*sql.DB, error) {
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&_pragma=busy_timeout(5000)"}
	src, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("store: import: %w", err)
	}
	src.SetMaxOpenConns(1)
	return src, nil
}

// pgTables lists the tables of the handle's schema except the migration table.
func pgTables(ctx context.Context, h *handle) ([]string, error) {
	rows, err := h.r.QueryContext(ctx, `SELECT table_name FROM information_schema.tables
		WHERE table_schema = current_schema() AND table_type = 'BASE TABLE' AND table_name <> 'schema_migrations' ORDER BY table_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// pgTableOrder returns the tables with the ones other tables point at first.
func pgTableOrder(ctx context.Context, h *handle) ([]string, error) {
	tables, err := pgTables(ctx, h)
	if err != nil {
		return nil, err
	}
	rows, err := h.r.QueryContext(ctx, `SELECT child.relname, parent.relname
		FROM pg_constraint c
		JOIN pg_class child ON child.oid = c.conrelid
		JOIN pg_class parent ON parent.oid = c.confrelid
		WHERE c.contype = 'f' AND c.connamespace = (SELECT oid FROM pg_namespace WHERE nspname = current_schema())`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	needs := map[string][]string{}
	for rows.Next() {
		var child, parent string
		if err := rows.Scan(&child, &parent); err != nil {
			return nil, err
		}
		if child != parent {
			needs[child] = append(needs[child], parent)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []string
	done := map[string]bool{}
	var visit func(string, int) error
	visit = func(t string, depth int) error {
		if done[t] {
			return nil
		}
		if depth > len(tables) {
			return fmt.Errorf("tables that point at each other around %s", t)
		}
		for _, p := range needs[t] {
			if err := visit(p, depth+1); err != nil {
				return err
			}
		}
		done[t] = true
		out = append(out, t)
		return nil
	}
	for _, t := range tables {
		if err := visit(t, 0); err != nil {
			return nil, err
		}
	}
	return out, nil
}

type pgColumn struct {
	Name     string
	Identity bool
}

// pgColumns lists the columns a copy writes: all but the generated ones.
func pgColumns(ctx context.Context, h *handle, table string) ([]pgColumn, error) {
	rows, err := h.r.QueryContext(ctx, `SELECT column_name, is_identity = 'YES' FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = ? AND is_generated = 'NEVER' ORDER BY ordinal_position`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []pgColumn
	for rows.Next() {
		var c pgColumn
		if err := rows.Scan(&c.Name, &c.Identity); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// sqliteOrder is ORDER BY the primary key of a SQLite table, so rows are copied
// (and hashed) in a fixed order that keeps sequence numbers rising.
func sqliteOrder(ctx context.Context, src *sql.DB, table string) (string, error) {
	rows, err := src.QueryContext(ctx, `SELECT name, pk FROM pragma_table_info(?) WHERE pk > 0 ORDER BY pk`, table)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var n string
		var pk int
		if err := rows.Scan(&n, &pk); err != nil {
			return "", err
		}
		cols = append(cols, `"`+strings.ReplaceAll(n, `"`, `""`)+`"`)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(cols) == 0 {
		return "", fmt.Errorf("table %s has no primary key", table)
	}
	return " ORDER BY " + strings.Join(cols, ", "), nil
}

func quoteCols(cols []pgColumn) string {
	q := make([]string, len(cols))
	for i, c := range cols {
		q[i] = pgx.Identifier{c.Name}.Sanitize()
	}
	return strings.Join(q, ", ")
}

// sqliteTables lists the tables of a SQLite file that carry the program's data.
func sqliteTables(ctx context.Context, src *sql.DB) ([]string, error) {
	rows, err := src.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table'
		AND name NOT LIKE 'sqlite\_%' ESCAPE '\' AND name NOT LIKE 'articles\_fts%' ESCAPE '\' AND name <> 'schema_migrations' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// pgxOf returns the pgx connection under a database/sql one.
func pgxOf(c *sql.Conn) (*pgx.Conn, func(), error) {
	var pc *pgx.Conn
	err := c.Raw(func(dc any) error {
		rc, ok := dc.(*rebindConn)
		if !ok {
			return fmt.Errorf("unexpected connection type %T", dc)
		}
		sc, ok := rc.Conn.(*stdlib.Conn)
		if !ok {
			return fmt.Errorf("unexpected connection type %T", rc.Conn)
		}
		pc = sc.Conn()
		return nil
	})
	return pc, func() { c.Close() }, err
}

func importSchema(ctx context.Context, h *handle, src *sql.DB, schema, file string, progress func(string)) (ImportedSchema, error) {
	is := ImportedSchema{Schema: schema, Source: file}
	pgNames, err := pgTableOrder(ctx, h)
	if err != nil {
		return is, err
	}
	liteNames, err := sqliteTables(ctx, src)
	if err != nil {
		return is, err
	}
	pgSorted := slices.Clone(pgNames)
	slices.Sort(pgSorted)
	if !slices.Equal(pgSorted, liteNames) {
		return is, fmt.Errorf("the tables differ: only in SQLite %v, only in Postgres %v", minus(liteNames, pgSorted), minus(pgSorted, liteNames))
	}
	conn, err := h.w.Conn(ctx)
	if err != nil {
		return is, err
	}
	pc, release, err := pgxOf(conn)
	if err != nil {
		return is, err
	}
	defer release()
	tx, err := pc.Begin(ctx)
	if err != nil {
		return is, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, h.lock); err != nil {
		return is, err
	}
	for _, name := range pgNames {
		cols, err := pgColumns(ctx, h, name)
		if err != nil {
			return is, err
		}
		order, err := sqliteOrder(ctx, src, name)
		if err != nil {
			return is, fmt.Errorf("table %s: %w", name, err)
		}
		rows, err := src.QueryContext(ctx, `SELECT `+sqliteCols(cols)+` FROM "`+name+`"`+order)
		if err != nil {
			return is, fmt.Errorf("table %s: %w", name, err)
		}
		cs := &copySource{rows: rows, n: len(cols)}
		n, err := tx.CopyFrom(ctx, pgx.Identifier{name}, colNames(cols), cs)
		rows.Close()
		if err != nil {
			return is, fmt.Errorf("table %s: %w", name, err)
		}
		if err := cs.Err(); err != nil {
			return is, fmt.Errorf("table %s: %w", name, err)
		}
		for _, c := range cols {
			if !c.Identity {
				continue
			}
			if _, err := tx.Exec(ctx, `SELECT setval(pg_get_serial_sequence($1, $2), COALESCE((SELECT MAX(`+pgx.Identifier{c.Name}.Sanitize()+`) FROM `+pgx.Identifier{name}.Sanitize()+`), 0) + 1, false)`,
				pgx.Identifier{name}.Sanitize(), c.Name); err != nil {
				return is, fmt.Errorf("table %s: sequence of %s: %w", name, c.Name, err)
			}
		}
		is.Tables = append(is.Tables, ImportedTable{Name: name, Rows: n})
		progress(fmt.Sprintf("  %-28s %9d rows", name, n))
	}
	if err := tx.Commit(ctx); err != nil {
		return is, err
	}
	// Read both sides back.
	for i, name := range pgNames {
		cols, err := pgColumns(ctx, h, name)
		if err != nil {
			return is, err
		}
		order, _ := sqliteOrder(ctx, src, name)
		want, wn, err := hashRowsOf(ctx, src, `SELECT `+sqliteCols(cols)+` FROM "`+name+`"`+order, len(cols))
		if err != nil {
			return is, fmt.Errorf("table %s: %w", name, err)
		}
		got, gn, err := hashRowsOf(ctx, h.r, `SELECT `+quoteCols(cols)+` FROM `+pgx.Identifier{name}.Sanitize()+pgOrder(order), len(cols))
		if err != nil {
			return is, fmt.Errorf("table %s: %w", name, err)
		}
		if got != want || gn != wn {
			return is, fmt.Errorf("table %s differs after the copy: %d rows %s in SQLite, %d rows %s in Postgres", name, wn, want[:12], gn, got[:12])
		}
		is.Tables[i].Hash = want
	}
	progress("  verified: every table read back and equal")
	return is, nil
}

func pgOrder(sqliteOrder string) string { return sqliteOrder }

func minus(a, b []string) []string {
	var out []string
	for _, x := range a {
		if !slices.Contains(b, x) {
			out = append(out, x)
		}
	}
	return out
}

func colNames(cols []pgColumn) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.Name
	}
	return out
}

func sqliteCols(cols []pgColumn) string {
	q := make([]string, len(cols))
	for i, c := range cols {
		q[i] = `"` + strings.ReplaceAll(c.Name, `"`, `""`) + `"`
	}
	return strings.Join(q, ", ")
}

// copySource feeds the rows of a SQLite query to COPY.
type copySource struct {
	rows *sql.Rows
	n    int
	vals []any
	err  error
}

func (c *copySource) Next() bool {
	if c.err != nil || !c.rows.Next() {
		if c.err == nil {
			c.err = c.rows.Err()
		}
		return false
	}
	c.vals = make([]any, c.n)
	ptrs := make([]any, c.n)
	for i := range ptrs {
		ptrs[i] = &c.vals[i]
	}
	if err := c.rows.Scan(ptrs...); err != nil {
		c.err = err
		return false
	}
	for i, v := range c.vals {
		if b, ok := v.([]byte); ok { // TEXT that the driver gave as bytes
			c.vals[i] = string(b)
		}
	}
	return true
}

func (c *copySource) Values() ([]any, error) { return c.vals, nil }
func (c *copySource) Err() error             { return c.err }

// hashRowsOf reads a query and hashes its rows: each value by type and content,
// so an integer 1, a real 1 and a text "1" differ.
func hashRowsOf(ctx context.Context, q interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}, query string, n int) (string, int64, error) {
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return "", 0, err
	}
	defer rows.Close()
	h := sha256.New()
	vals := make([]any, n)
	ptrs := make([]any, n)
	for i := range ptrs {
		ptrs[i] = &vals[i]
	}
	var count int64
	var buf []byte
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return "", 0, err
		}
		buf = buf[:0]
		for _, v := range vals {
			switch x := v.(type) {
			case nil:
				buf = append(buf, 'n', ';')
			case int64:
				buf = append(buf, 'i')
				buf = strconv.AppendInt(buf, x, 10)
				buf = append(buf, ';')
			case float64:
				buf = append(buf, 'f')
				buf = strconv.AppendFloat(buf, x, 'g', -1, 64)
				buf = append(buf, ';')
			case string:
				buf = append(buf, 's')
				buf = strconv.AppendInt(buf, int64(len(x)), 10)
				buf = append(buf, ':')
				buf = append(buf, x...)
				buf = append(buf, ';')
			case []byte:
				buf = append(buf, 's')
				buf = strconv.AppendInt(buf, int64(len(x)), 10)
				buf = append(buf, ':')
				buf = append(buf, x...)
				buf = append(buf, ';')
			case bool:
				buf = append(buf, 'i')
				if x {
					buf = append(buf, '1')
				} else {
					buf = append(buf, '0')
				}
				buf = append(buf, ';')
			default:
				return "", 0, fmt.Errorf("a value of type %T", v)
			}
		}
		buf = append(buf, '\n')
		h.Write(buf)
		count++
	}
	if err := rows.Err(); err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), count, nil
}
