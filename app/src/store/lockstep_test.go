package store

import (
	"context"
	"database/sql"
	"slices"
	"testing"
)

// The two backends keep their migrations in separate files, and a change to the
// schema must be made in both. These tests are the rule: the newest version is
// the same everywhere, a migration that exists on both sides has the same
// number, and (with a Postgres to look at) the tables and columns agree.

func TestMigrationVersionsAreInLockstep(t *testing.T) {
	for _, c := range []struct {
		name       string
		lite, pg   migrationFS
		liteD, pgD string
	}{
		{"base", baseFS, pgBaseFS, "migrations/sqlite/base", "migrations/postgres/base"},
		{"tenant", tenantFS, pgTenantFS, "migrations/sqlite/tenant", "migrations/postgres/tenant"},
	} {
		lite, err := loadMigrations(c.lite, c.liteD)
		if err != nil {
			t.Fatal(err)
		}
		pg, err := loadMigrationsFrom(c.pg, c.pgD, true)
		if err != nil {
			t.Fatal(err)
		}
		if lite[len(lite)-1].version != pg[len(pg)-1].version {
			t.Errorf("%s: SQLite is at migration %d and Postgres at %d: a schema change belongs in both directories, with the same number", c.name, lite[len(lite)-1].version, pg[len(pg)-1].version)
		}
		// the Postgres list starts at a baseline; every later one has a twin
		for _, m := range pg[1:] {
			found := false
			for _, l := range lite {
				if l.version == m.version && l.name == m.name {
					found = true
				}
			}
			if !found {
				t.Errorf("%s: Postgres migration %04d_%s has no SQLite migration of the same number and name", c.name, m.version, m.name)
			}
		}
		if len(pg) > 1 {
			t.Logf("%s: %d migrations after the Postgres baseline", c.name, len(pg)-1)
		}
	}
}

func TestSchemasHaveTheSameTablesAndColumns(t *testing.T) {
	if pgTestURL() == "" {
		t.Skip("needs STOXICITY_TEST_DATABASE_URL")
	}
	ctx := context.Background()
	lite, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer lite.Close()
	pg := newPGDB(t)
	for _, tenant := range []bool{false, true} {
		lh, ph := lite.base, pg.base
		name := "base"
		if tenant {
			var err error
			if lh, err = lite.tenantHandle("lock"); err != nil {
				t.Fatal(err)
			}
			if ph, err = pg.tenantHandle("lock"); err != nil {
				t.Fatal(err)
			}
			name = "tenant"
		}
		want := liteColumns(ctx, t, lh.r)
		got := pgColumns2(ctx, t, ph.r)
		for tbl, cols := range want {
			g, ok := got[tbl]
			if !ok {
				t.Errorf("%s: table %s is only in SQLite", name, tbl)
				continue
			}
			if !slices.Equal(cols, g) {
				t.Errorf("%s: table %s: SQLite has columns %v, Postgres %v", name, tbl, cols, g)
			}
		}
		for tbl := range got {
			if _, ok := want[tbl]; !ok {
				t.Errorf("%s: table %s is only in Postgres", name, tbl)
			}
		}
	}
}

// liteColumns maps each table of a SQLite database to its column names (sorted).
func liteColumns(ctx context.Context, t *testing.T, db *sql.DB) map[string][]string {
	t.Helper()
	rows, err := db.QueryContext(ctx, `SELECT m.name, p.name FROM sqlite_master m, pragma_table_info(m.name) p
		WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite\_%' ESCAPE '\' AND m.name NOT LIKE 'articles\_fts%' ESCAPE '\'`)
	if err != nil {
		t.Fatal(err)
	}
	return collect(t, rows)
}

// pgColumns2 does the same for the current schema of a Postgres database,
// leaving out generated columns (the full-text vector has no SQLite twin).
func pgColumns2(ctx context.Context, t *testing.T, db *sql.DB) map[string][]string {
	t.Helper()
	rows, err := db.QueryContext(ctx, `SELECT c.table_name, c.column_name FROM information_schema.columns c
		JOIN information_schema.tables t ON t.table_schema = c.table_schema AND t.table_name = c.table_name AND t.table_type = 'BASE TABLE'
		WHERE c.table_schema = current_schema() AND c.is_generated = 'NEVER'`)
	if err != nil {
		t.Fatal(err)
	}
	return collect(t, rows)
}

func collect(t *testing.T, rows *sql.Rows) map[string][]string {
	t.Helper()
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var tbl, col string
		if err := rows.Scan(&tbl, &col); err != nil {
			t.Fatal(err)
		}
		out[tbl] = append(out[tbl], col)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, c := range out {
		slices.Sort(c)
	}
	return out
}
