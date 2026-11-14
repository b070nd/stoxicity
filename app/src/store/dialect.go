package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// dialect holds the few things whose SQL text differs between the databases the
// store runs on. Everything else in the store is written in the subset both
// understand: INSERT ... ON CONFLICT, IS DISTINCT FROM, RETURNING, CASE for a
// greater of two values, string_agg with ORDER BY, and no bare columns next to
// GROUP BY. A statement that needs a form this subset lacks asks the dialect.
type dialect interface {
	// jsonEach is a table expression that lists the elements of the JSON array
	// expr (a column or a bound parameter) as rows with one column, value.
	jsonEach(expr, alias string) string
	// migrationsTable is the statement that creates the table of applied
	// migrations when it is missing.
	migrationsTable() string
	// afterBegin runs right after a write transaction begins, with the key of
	// the database's write lock.
	afterBegin(ctx context.Context, tx *sql.Tx, key int64) error
	// indexArticles adds the articles with a sequence number above after to
	// the full-text index, for a database whose index is not kept by itself.
	indexArticles(ctx context.Context, tx *sql.Tx, after int64) error
	// searchFrom and searchWhere are the parts of a full-text query: the
	// tables to read, and the condition that takes the match expression as
	// its next argument. matchArg turns an expression of the filter package
	// into what the database wants as that argument.
	searchFrom() string
	searchWhere() string
	matchArg(match string) (string, error)
}

// sqliteDialect is the dialect of the embedded database.
type sqliteDialect struct{}

func (sqliteDialect) jsonEach(expr, alias string) string {
	return "json_each(" + expr + ") " + alias
}

func (sqliteDialect) migrationsTable() string {
	return `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		name       TEXT NOT NULL,
		applied_ms INTEGER NOT NULL)`
}

// SQLite takes its write lock when the transaction begins (see dsn).
func (sqliteDialect) afterBegin(context.Context, *sql.Tx, int64) error { return nil }

func (sqliteDialect) indexArticles(ctx context.Context, tx *sql.Tx, after int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO articles_fts (rowid, title, summary, tickers)
		SELECT seq, title, summary, tickers FROM articles WHERE seq > ?`, after)
	return err
}

func (sqliteDialect) searchFrom() string {
	return ` FROM articles_fts JOIN articles a ON a.seq = articles_fts.rowid`
}
func (sqliteDialect) searchWhere() string { return ` WHERE articles_fts MATCH ?` }

func (sqliteDialect) matchArg(match string) (string, error) { return match, nil }

// beginWrite starts a transaction on the write connection. Every transaction
// that writes starts here, so what a database needs at the start of one (for
// SQLite nothing: the connection takes the write lock when the transaction
// begins, see dsn) has one home.
func (h *handle) beginWrite(ctx context.Context) (*sql.Tx, error) {
	tx, err := h.w.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if err := h.d.afterBegin(ctx, tx, h.lock); err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}

// inSources is the SQL condition on the article table alias a: its source is
// one of the IDs in the JSON array bound as the next argument.
func (h *handle) inSources() string {
	return ` AND a.source_id IN (SELECT value FROM ` + h.d.jsonEach("?", "j") + `)`
}

// isUniqueViolation reports whether err says a unique constraint was broken, on
// either database.
func isUniqueViolation(err error) bool {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.Code == "23505"
	}
	return strings.Contains(err.Error(), "UNIQUE")
}
