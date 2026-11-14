package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// cmdDB moves data between the storage backends. It is for a one-time change of
// a whole installation; the program itself runs on whichever backend the
// configuration selects (see STOXICITY_DATABASE_URL).
func cmdDB(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 || args[0] != "import-sqlite" {
		return usageErrorf("expected: db import-sqlite [-from DIR] [-json]")
	}
	fs := newFlags("db import-sqlite")
	from := fs.String("from", "", "the folder with base.db and tenants/ to copy (default: the data folder)")
	asJSON := fs.Bool("json", false, "print the report as JSON")
	rest, err := parseFlags(fs, args[1:])
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	if a.cfg.DatabaseURL == "" {
		return fmt.Errorf("db import-sqlite: no Postgres is selected: set STOXICITY_DATABASE_URL (or database_url in the configuration) to the empty database to fill")
	}
	src := *from
	if src == "" {
		src = a.dataDir()
	} else {
		src = a.relative(src)
	}
	if _, err := os.Stat(filepath.Join(src, "base.db")); err != nil {
		return fmt.Errorf("db import-sqlite: %s has no base.db: %w", src, err)
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	progress := func(s string) { fmt.Fprintln(a.stderr, s) }
	if *asJSON {
		progress = nil
	}
	rep, err := db.ImportSQLite(ctx, src, progress)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(a.stdout, rep)
	}
	var rows int64
	for _, s := range rep.Schemas {
		for _, t := range s.Tables {
			rows += t.Rows
		}
	}
	fmt.Fprintf(a.stdout, "copied %d rows of %d databases from %s; every table read back and equal; the SQLite files were not changed\n", rows, len(rep.Schemas), src)
	fmt.Fprintln(a.stdout, "to use Postgres from now on, keep STOXICITY_DATABASE_URL set; remove it to go back to the SQLite files")
	return nil
}
