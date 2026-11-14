package store

import (
	"context"
	"database/sql"
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
}

// sqliteDialect is the dialect of the embedded database.
type sqliteDialect struct{}

func (sqliteDialect) jsonEach(expr, alias string) string {
	return "json_each(" + expr + ") " + alias
}

// beginWrite starts a transaction on the write connection. Every transaction
// that writes starts here, so what a database needs at the start of one (for
// SQLite nothing: the connection takes the write lock when the transaction
// begins, see dsn) has one home.
func (h *handle) beginWrite(ctx context.Context) (*sql.Tx, error) {
	return h.w.BeginTx(ctx, nil)
}

// inSources is the SQL condition on the article table alias a: its source is
// one of the IDs in the JSON array bound as the next argument.
func (h *handle) inSources() string {
	return ` AND a.source_id IN (SELECT value FROM ` + h.d.jsonEach("?", "j") + `)`
}
