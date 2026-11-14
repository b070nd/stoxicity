package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// PGState is what a Postgres database holds, read without creating, migrating
// or writing anything.
type PGState struct {
	// Schemas maps each schema the program owns ("base" and "tenant_<id>") to
	// the newest migration applied there; 0 when it has no migration table.
	Schemas map[string]int
	// Tables maps "schema.table" to its rows. Only filled when asked for.
	Tables map[string]int64
}

// InspectPostgres reads the state of the database at url. It opens one
// connection and only reads; it is what health, doctor and the backup tools use
// where SQLite's ReadSchema looks at a file. A database that cannot be reached
// is an error that names the host and database but not the password.
func InspectPostgres(ctx context.Context, url string, counts bool) (PGState, error) {
	st := PGState{Schemas: map[string]int{}}
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		return st, fmt.Errorf("store: database URL: %w", err)
	}
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return st, fmt.Errorf("Postgres at %s:%d is not reachable as %s (database %s): %w", cfg.Host, cfg.Port, cfg.User, cfg.Database, err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, `SET default_transaction_read_only = on`); err != nil {
		return st, err
	}
	rows, err := conn.Query(ctx, `SELECT nspname FROM pg_namespace WHERE nspname = 'base' OR nspname LIKE 'tenant\_%' ORDER BY nspname`)
	if err != nil {
		return st, err
	}
	var schemas []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			rows.Close()
			return st, err
		}
		schemas = append(schemas, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return st, err
	}
	for _, s := range schemas {
		var have bool
		if err := conn.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, pgx.Identifier{s, "schema_migrations"}.Sanitize()).Scan(&have); err != nil {
			return st, err
		}
		st.Schemas[s] = 0
		if !have {
			continue
		}
		var v *int64
		if err := conn.QueryRow(ctx, `SELECT MAX(version) FROM `+pgx.Identifier{s, "schema_migrations"}.Sanitize()).Scan(&v); err != nil {
			return st, err
		}
		if v != nil {
			st.Schemas[s] = int(*v)
		}
	}
	if !counts {
		return st, nil
	}
	st.Tables = map[string]int64{}
	for _, s := range schemas {
		trows, err := conn.Query(ctx, `SELECT table_name FROM information_schema.tables WHERE table_schema = $1 AND table_type = 'BASE TABLE' ORDER BY table_name`, s)
		if err != nil {
			return st, err
		}
		var names []string
		for trows.Next() {
			var n string
			if err := trows.Scan(&n); err != nil {
				trows.Close()
				return st, err
			}
			names = append(names, n)
		}
		trows.Close()
		if err := trows.Err(); err != nil {
			return st, err
		}
		for _, n := range names {
			var c int64
			if err := conn.QueryRow(ctx, `SELECT COUNT(*) FROM `+pgx.Identifier{s, n}.Sanitize()).Scan(&c); err != nil {
				return st, err
			}
			st.Tables[s+"."+n] = c
		}
	}
	return st, nil
}

// KnownFor says which newest migration this program knows for a schema of the
// database: base for "base", tenant for any "tenant_<id>".
func (v SchemaVersions) KnownFor(schema string) int {
	if schema == "base" {
		return v.Base
	}
	return v.Tenant
}
